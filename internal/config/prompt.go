// prompt.go ports settings.py's interactive-prompt path: the gate that
// decides whether prompting may happen at all, and the prompt loop itself.
package config

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
)

func interactiveGateOpen(env map[string]string) bool {
	if interactiveIsDisabled() {
		return false
	}
	if env[interactiveEnvVar] != "1" {
		return false
	}
	if !isTerminal(os.Stdin) {
		return false
	}
	stdoutOK := stdoutTTYOverride()
	if stdoutOK == nil {
		if !isTerminal(os.Stdout) {
			return false
		}
	} else if !*stdoutOK {
		return false
	}
	return true
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// stdoutTTYOverride mirrors settings.py's _STDOUT_TTY_OVERRIDE: set only by
// the `cadre config resolve` command path when it has independently
// confirmed a controlling terminal exists and rebound the prompt's own
// input/output to it -- never by any other caller.
var stdoutTTYOverrideValue *bool

func stdoutTTYOverride() *bool { return stdoutTTYOverrideValue }

// WithStdoutTTYOverride runs fn with the stdout-tty gate forced to value,
// restoring the previous override afterward.
func WithStdoutTTYOverride(value bool, fn func() error) error {
	previous := stdoutTTYOverrideValue
	stdoutTTYOverrideValue = &value
	defer func() { stdoutTTYOverrideValue = previous }()
	return fn()
}

func promptTierChoice(s FieldSpec, input func(string) (string, error), output func(string)) (string, error) {
	allowProject := s.Scope == ScopeProjectOrGlobal
	choices := "global/skip"
	if allowProject {
		choices = "project/global/skip"
	}
	output(fmt.Sprintf("Save %s to which tier? (%s, default: global)", s.Key, choices))
	raw, err := input("> ")
	if err != nil {
		return "", err
	}
	raw = trimLower(raw)
	if raw == "" || raw == "global" {
		return "global", nil
	}
	if raw == "skip" {
		return "", nil
	}
	if raw == "project" && allowProject {
		return "project", nil
	}
	output(fmt.Sprintf("  unrecognized choice %q; defaulting to global", raw))
	return "global", nil
}

func trimLower(s string) string {
	start, end := 0, len(s)
	for start < end && isWhitespace(s[start]) {
		start++
	}
	for end > start && isWhitespace(s[end-1]) {
		end--
	}
	out := s[start:end]
	b := []byte(out)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func isWhitespace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

func promptFor(s FieldSpec, opts resolveOptions) (*Resolved, error) {
	if s.Secret {
		return nil, nil
	}
	input := opts.inputFunc
	if input == nil {
		input = defaultInputFunc
	}
	output := opts.outputFunc
	if output == nil {
		output = func(text string) { fmt.Println(text) }
	}

	defaultValue := displayDefault(s)
	output(s.Key + " is not configured.")
	if s.EnvVar != "" {
		output("  env var: " + s.EnvVar)
	}
	if defaultValue != nil {
		output("  default: " + stringifyForDisplay(defaultValue))
	} else {
		output("  default: (none)")
	}

	for attempt := 0; attempt < 3; attempt++ {
		raw, err := input(fmt.Sprintf("Enter value for %s (blank = default): ", s.Key))
		if err != nil {
			return nil, settingsErrorf("%s: input stream closed before a value was entered (prompt cancelled); "+
				"set it via the environment or a config file instead", s.Key)
		}
		var value any
		if isBlank(raw) {
			if defaultValue != nil {
				value = defaultValue
			} else {
				output("  no default available; please enter a value")
				continue
			}
		} else {
			validated, err := validate(s, raw)
			if err != nil {
				output("  invalid: " + err.Error())
				continue
			}
			value = validated
		}
		tier, err := promptTierChoice(s, input, output)
		if err != nil {
			return nil, err
		}
		var originPath string
		if tier != "" {
			rawStr, ok := value.(string)
			if !ok {
				rawStr = stringifyForDisplay(value)
			}
			written, err := WriteSetting(s.Key, rawStr, tier, opts.start)
			if err != nil {
				return nil, err
			}
			originPath = written
			ResetCache()
		}
		return &Resolved{Key: s.Key, Value: value, Origin: OriginPrompt, OriginPath: originPath}, nil
	}
	return nil, settingsErrorf("%s: no valid value entered after 3 attempts", s.Key)
}

func defaultInputFunc(prompt string) (string, error) {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("EOF")
	}
	return trimNewline(line), nil
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// OpenTTYIO best-effort opens the controlling terminal for prompt I/O, used
// only by the `cadre config resolve` CLI command. /dev/tty (POSIX) refers
// to whatever terminal is actually controlling this process, independent
// of this process's own stdin/stdout redirection -- exactly the property
// needed when stdout is a shell command-substitution pipe capturing a
// resolved value, not a place prompt text can go. Returns (nil, nil, false)
// if there is no controlling terminal, so the caller falls through to the
// ordinary non-prompting path.
func OpenTTYIO() (input func(string) (string, error), output func(string), ok bool) {
	if runtime.GOOS == "windows" {
		return nil, nil, false
	}
	reader, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return nil, nil, false
	}
	writer, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		reader.Close()
		return nil, nil, false
	}
	bufReader := bufio.NewReader(reader)

	input = func(prompt string) (string, error) {
		writer.WriteString(prompt)
		line, err := bufReader.ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("controlling terminal closed during prompt")
		}
		return trimNewline(line), nil
	}
	output = func(text string) {
		writer.WriteString(text + "\n")
	}
	return input, output, true
}
