package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/deagy/cadre/cli/internal/orchestration"
)

// DoctorCmd is the `cadre doctor` command. Reports which cadre binary is
// running, what kind of install it is, and warns on a cwd/checkout
// mismatch. Exit code: 0 when the picture is internally consistent (or
// nothing could be determined either way); 1 when cwd sits inside a Cadre
// checkout but the binary that actually ran is demonstrably a different
// location than that checkout's own build.
func DoctorCmd(args []string) int {
	asJSON := false
	var unexpected []string
	help := false
	for _, arg := range args {
		switch arg {
		case "--json":
			asJSON = true
		case "-h", "--help":
			help = true
		default:
			unexpected = append(unexpected, arg)
		}
	}

	if help {
		fmt.Fprintln(os.Stderr, "usage: cadre doctor [--json]")
		return 0
	}
	if len(unexpected) > 0 {
		fmt.Fprintf(os.Stderr, "cadre doctor: unknown argument(s): %s\n", joinArgs(unexpected))
		fmt.Fprintln(os.Stderr, "usage: cadre doctor [--json]")
		return 2
	}

	report := orchestration.GatherDoctorReport("", "")

	if asJSON {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre doctor: %v\n", err)
			return 1
		}
		fmt.Println(string(data))
	} else {
		fmt.Print(orchestration.RenderDoctorReport(report))
	}

	if report.Mismatch {
		return 1
	}
	return 0
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
