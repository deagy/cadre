package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Refuse to publish an SBOM that inventoried the wrong thing.
//
// syft reports success on a path it found nothing useful in, so a scan aimed
// at the wrong directory produces a well-formed, nearly empty SPDX document
// and the release attaches it. The signature and the attestation are then
// perfectly valid statements about an inventory that describes nothing.
//
// Two assertions, because they fail differently: a floor on the package count
// catches the wrong-path scan, and named packages catch a scan that found
// *something* but not the dependency set that matters -- the wrong cataloger,
// or a lockfile that resolved to nothing.
//
// Ported from two `python3 -c` blocks in release.yml.

// spdxDocument is the part of an SPDX document this reads.
type spdxDocument struct {
	Packages []struct {
		Name string `json:"name"`
	} `json:"packages"`
}

// sbomPackageNames returns the distinct non-empty package names in an SBOM.
func sbomPackageNames(contents []byte) (map[string]bool, error) {
	var document spdxDocument
	if err := json.Unmarshal(contents, &document); err != nil {
		return nil, fmt.Errorf("does not parse as SPDX JSON: %w", err)
	}
	names := map[string]bool{}
	for _, pkg := range document.Packages {
		if pkg.Name != "" {
			names[pkg.Name] = true
		}
	}
	return names, nil
}

// checkSBOM reports every reason the inventory is not publishable.
func checkSBOM(contents []byte, minimum int, required []string) ([]string, int, error) {
	names, err := sbomPackageNames(contents)
	if err != nil {
		return nil, 0, err
	}
	var problems []string
	if len(names) < minimum {
		problems = append(problems, fmt.Sprintf(
			"lists %d distinct packages, fewer than the %d expected -- this is what "+
				"scanning the wrong path produces", len(names), minimum))
	}
	var missing []string
	for _, name := range required {
		if name != "" && !names[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		problems = append(problems, "does not list "+strings.Join(missing, ", ")+
			" -- the scan ran, but not over the dependency set it was aimed at")
	}
	return problems, len(names), nil
}

const usageSBOMCheck = `Refuse an SBOM that inventoried the wrong thing.

  cadre sbom-check dist/cadre-cli-sbom.spdx.json --min 5 --require github.com/mattn/go-sqlite3

syft succeeds on a path it found nothing in, so a misaimed scan produces a
well-formed, nearly empty document that signs and attests perfectly well.`

// SBOMCheckCmd implements `cadre sbom-check`.
func SBOMCheckCmd(args []string) int {
	fs := flag.NewFlagSet("cadre sbom-check", flag.ContinueOnError)
	setUsage(fs, "sbom-check", usageSBOMCheck)
	minimum := fs.Int("min", 1, "the fewest distinct packages a real inventory has")
	required := fs.String("require", "", "comma-separated package names that must appear")
	if err := fs.Parse(args); err != nil {
		return parseExitCode(err)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: cadre sbom-check <sbom.spdx.json> [--min N] [--require a,b]")
		return 2
	}
	path := fs.Arg(0)
	contents, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sbom-check: %v\n", err)
		return 1
	}
	var names []string
	for _, name := range strings.Split(*required, ",") {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	problems, counted, err := checkSBOM(contents, *minimum, names)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sbom-check: %s %v\n", path, err)
		return 1
	}
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintf(os.Stderr, "sbom-check: %s %s\n", path, problem)
		}
		return 1
	}
	fmt.Printf("%s lists %d distinct packages\n", path, counted)
	return 0
}
