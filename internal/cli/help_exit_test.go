package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// `--help` is a satisfied request, so it exits 0.
//
// Every subcommand here is built on flag.ContinueOnError, which reports an
// explicit -h/--help as flag.ErrHelp *after* printing usage. Call sites
// collapsed that into `return 2` alongside genuine parse failures, so five
// subcommands printed perfectly good help and then reported a usage error:
// select, knowledge, config, init and profile, while cadre --help, doctor
// and sdlc exited 0. Nothing noticed, because reading the output tells you
// nothing about the status.
//
// It broke the release. The workflow's smoke test runs
//
//	cadre --version
//	cadre knowledge --help
//
// under `set -eu`, and the first release that ever reached that step failed
// on the second line with correct help on stdout -- which reads like the
// binary is broken, and it is not.
//
// The table is bin/subcommands.tsv rather than a list here, so a subcommand
// added later is covered without anyone remembering to add it.
func TestEverySubcommandExitsZeroOnHelp(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	tablePath := filepath.Join(root, SubcommandsTableRelativePath)

	subcommands, err := LoadSubcommands(tablePath)
	if err != nil {
		t.Fatalf("loading %s: %v", tablePath, err)
	}
	if len(subcommands) == 0 {
		t.Fatal("subcommands.tsv is empty; this guard checked nothing")
	}

	// Servers block once started. They must still answer --help without
	// serving, which is the thing worth asserting, but a regression there
	// would hang the suite rather than fail it -- so they are called with a
	// cancelled context.
	servers := map[string]bool{
		"mcp-dispatch-server": true,
		"mcp-gitlab-server":   true,
	}

	var findings []string
	for _, sub := range subcommands {
		for _, spelling := range []string{"--help", "-h"} {
			ctx := context.Background()
			if servers[sub.Name] {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}

			var stdout, stderr bytes.Buffer
			code := Run(ctx, []string{sub.Name, spelling}, Deps{
				Stdout:          &stdout,
				Stderr:          &stderr,
				RepoRoot:        root,
				SubcommandsPath: tablePath,
			})
			if code != 0 {
				findings = append(findings, describeHelpFailure(sub.Name, spelling, code, stdout.String()+stderr.String()))
			}
		}
	}

	if len(findings) > 0 {
		sort.Strings(findings)
		t.Fatalf("subcommands that do not exit 0 on an explicit help request:\n  %s",
			strings.Join(findings, "\n  "))
	}
	t.Logf("checked %d subcommands x 2 help spellings", len(subcommands))
}

func describeHelpFailure(name, spelling string, code int, output string) string {
	summary := strings.TrimSpace(output)
	if i := strings.IndexByte(summary, '\n'); i >= 0 {
		summary = summary[:i]
	}
	if len(summary) > 70 {
		summary = summary[:70] + "..."
	}
	if summary == "" {
		summary = "(nothing on the dispatcher's streams; these commands write to os.Stderr)"
	}
	return "cadre " + name + " " + spelling + ": exit " + strconv.Itoa(code) + "; first line: " + summary
}

// A usage error is still a usage error. The fix above must not turn every
// bad invocation into success -- that would make the guard above pass by
// making the CLI useless to script against.
func TestGenuineUsageErrorsStillExitNonZero(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	tablePath := filepath.Join(root, SubcommandsTableRelativePath)

	cases := [][]string{
		{"knowledge"},                    // no subcommand
		{"select", "--definitely-bogus"}, // unknown flag
		{"config"},                       // no verb
		{"config", "bogus"},              // unknown verb
		{"profile"},                      // no verb
		{"generate-plugin", "--nope"},    // unknown flag
	}

	for _, argv := range cases {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), argv, Deps{
			Stdout:          &stdout,
			Stderr:          &stderr,
			RepoRoot:        root,
			SubcommandsPath: tablePath,
		})
		if code == 0 {
			t.Errorf("cadre %s: exit 0, want nonzero (it is a usage error)", strings.Join(argv, " "))
		}
	}
}
