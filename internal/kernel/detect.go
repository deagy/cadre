package kernel

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

// `agentic-sdlc detect` -- report what a repository looks like, changing
// nothing.
//
// A port of detect_repository. Detection deliberately stops at description:
// it proposes a profile and lists candidate commands, and says so in its own
// output ("Detection never assigns human authority or compliance
// applicability"). Nothing here decides anything.

// stackSignature maps a stack name to the filenames that imply it. Ordered,
// because the output lists detected stacks in this order rather than
// alphabetically -- Python iterates a dict literal, and matching that is
// what makes the two outputs comparable.
type stackSignature struct {
	name     string
	patterns []string
}

var stackSignatures = []stackSignature{
	{"python", []string{"pyproject.toml", "requirements.txt", "setup.py"}},
	{"node", []string{"package.json", "pnpm-lock.yaml", "yarn.lock"}},
	{"go", []string{"go.mod"}},
	{"rust", []string{"Cargo.toml"}},
	{"java", []string{"pom.xml", "build.gradle", "build.gradle.kts"}},
	{"dotnet", []string{"*.sln", "*.csproj"}},
	{"terraform", []string{"*.tf"}},
	{"containers", []string{"Dockerfile", "compose.yaml", "docker-compose.yml"}},
}

// reportedDirectories are the directories `detect` mentions if present.
var reportedDirectories = []string{"src", "app", "api", "cmd", "internal", "infra", "deploy"}

// serviceLayoutDirectories, with a web marker present, imply a service rather
// than a script.
var serviceLayoutDirectories = []string{"src", "app", "api", "cmd", "internal"}

// webMarkers are the manifests that suggest something is served rather than
// run once.
var webMarkers = []string{"package.json", "go.mod", "requirements.txt", "pyproject.toml"}

// commandCandidates preserves insertion order.
//
// Python builds this as a dict and prints it with json.dumps, which emits
// keys in insertion order -- test, then test_candidate, then static_analysis,
// depending on which stacks matched. A Go map marshals sorted, so a direct
// translation would reorder the object and diverge from the kernel it is
// replacing. Order is not cosmetic in an output another program parses.
type commandCandidates struct {
	keys   []string
	values map[string][]string
}

func (c *commandCandidates) set(key string, value []string) {
	if c.values == nil {
		c.values = map[string][]string{}
	}
	if _, present := c.values[key]; !present {
		c.keys = append(c.keys, key)
	}
	c.values[key] = value
}

// MarshalJSON emits the keys in insertion order.
func (c commandCandidates) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, key := range c.keys {
		if index > 0 {
			buffer.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buffer.Write(encodedKey)
		buffer.WriteByte(':')
		encodedValue, err := json.Marshal(c.values[key])
		if err != nil {
			return nil, err
		}
		buffer.Write(encodedValue)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

// DetectionReport is what `detect` prints. Field order matches the Python
// dict's insertion order, because json.MarshalIndent follows struct order and
// the two outputs are compared.
type DetectionReport struct {
	Root              string            `json:"root"`
	DetectedStacks    []string          `json:"detected_stacks"`
	Directories       []string          `json:"directories"`
	ProposedProfile   string            `json:"proposed_profile"`
	CommandCandidates commandCandidates `json:"command_candidates"`
	Warnings          []string          `json:"warnings"`
}

// pythonInterpreter resolves the interpreter `detect` names in its Python
// test-command candidate.
//
// Python emits sys.executable: the absolute path of the interpreter that
// happens to be running the kernel. A Go binary has no such thing, and the
// value is machine-specific either way -- a report captured on one machine
// names a path that may not exist on another.
//
// Resolving python3 on PATH is the closest honest equivalent, and it can
// still differ from what the Python kernel would have said (a kernel invoked
// by an interpreter that is not first on PATH, most obviously). The
// differential harness normalises this field and asserts a property of it
// rather than pretending the two can be made byte-identical.
func pythonInterpreter() string {
	for _, name := range []string{"python3", "python"} {
		if resolved, err := exec.LookPath(name); err == nil {
			if absolute, err := filepath.Abs(resolved); err == nil {
				return absolute
			}
			return resolved
		}
	}
	// Named rather than omitted: a candidate command with no interpreter is
	// still a description of what would run, and dropping the key would
	// change the object's shape.
	return "python3"
}

// DetectRepository inspects root and reports what it looks like.
func DetectRepository(root string) DetectionReport {
	entries, err := os.ReadDir(root)
	names := make([]string, 0, len(entries))
	directories := map[string]bool{}
	if err == nil {
		for _, entry := range entries {
			names = append(names, entry.Name())
			if entry.IsDir() {
				directories[entry.Name()] = true
			}
		}
	}

	var stacks []string
	for _, signature := range stackSignatures {
		if anyNameMatches(names, signature.patterns) {
			stacks = append(stacks, signature.name)
		}
	}
	if stacks == nil {
		stacks = []string{}
	}

	present := map[string]bool{}
	for _, name := range names {
		present[name] = true
	}

	serviceLayout := containsAnyKey(present, webMarkers) && containsAnyKey(directories, serviceLayoutDirectories)
	multiTier := present["package.json"] &&
		(present["go.mod"] || present["requirements.txt"] || present["pyproject.toml"])

	profile := "quick"
	if serviceLayout || multiTier {
		profile = "web-service"
	}

	var reported []string
	for _, name := range reportedDirectories {
		if directories[name] {
			reported = append(reported, name)
		}
	}
	sort.Strings(reported)
	if reported == nil {
		reported = []string{}
	}

	// Insertion order matters here: python first, then node, then go -- and
	// go overwrites python's "test" in place rather than appending it again.
	var commands commandCandidates
	stackSet := map[string]bool{}
	for _, stack := range stacks {
		stackSet[stack] = true
	}
	if stackSet["python"] {
		commands.set("test", []string{pythonInterpreter(), "-m", "unittest", "discover"})
	}
	if stackSet["node"] {
		commands.set("test_candidate", []string{"use-project-pinned-package-manager", "test"})
	}
	if stackSet["go"] {
		commands.set("test", []string{"go", "test", "./..."})
		commands.set("static_analysis", []string{"go", "vet", "./..."})
	}

	resolvedRoot := root
	if absolute, err := filepath.Abs(root); err == nil {
		if evaluated, err := filepath.EvalSymlinks(absolute); err == nil {
			resolvedRoot = evaluated
		} else {
			resolvedRoot = absolute
		}
	}

	return DetectionReport{
		Root:              resolvedRoot,
		DetectedStacks:    stacks,
		Directories:       reported,
		ProposedProfile:   profile,
		CommandCandidates: commands,
		Warnings: []string{
			"Detection never assigns human authority or compliance applicability.",
		},
	}
}

func anyNameMatches(names []string, patterns []string) bool {
	for _, pattern := range patterns {
		for _, name := range names {
			if matched, err := filepath.Match(pattern, name); err == nil && matched {
				return true
			}
		}
	}
	return false
}

func containsAnyKey(set map[string]bool, keys []string) bool {
	for _, key := range keys {
		if set[key] {
			return true
		}
	}
	return false
}

// RenderDetection prints the report the way the Python kernel does:
// json.dumps(..., indent=2) followed by a newline.
func RenderDetection(report DetectionReport) (string, error) {
	// SetEscapeHTML(false) is the point of using an Encoder here rather than
	// MarshalIndent. Go escapes <, > and & to \u003c, \u003e and \u0026 by
	// default; Python's json.dumps does not. A repository path or filename
	// containing any of them would come out rewritten, and this output is
	// compared byte-for-byte against the kernel it replaces.
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return "", err
	}
	// Encode already appends the newline json.dumps + print produces.
	return buffer.String(), nil
}
