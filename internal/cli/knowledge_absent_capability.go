package cli

// A retention or erasure request must be refused where it is reached for.
//
// Retention and deletion of ingested content do not exist here. Both were
// real, tested commands in the Python CLI and were removed in b418031e when
// the Go rewrite landed; neither was rebuilt. Whether they are restored or
// declared permanently out of scope is an open decision, recorded in
// roster/knowledge-store/SECURITY.md § Storage rules.
//
// The verbs already say so: `retention-report`, `delete-ingested` and
// `deletion-evidence` each answer by name. The flags did not. A steward who
// knows retention used to exist reaches for it the way it used to work --
// `cadre knowledge ingest --retention-days 30` became
// `cadre knowledge search --retention-days 30` in the muscle memory -- and
// got `flag provided but not defined: -retention-days`.
//
// That message is true and useless. It says the parser did not recognise a
// flag, at the exact moment someone is trying to honour a retention or
// erasure obligation, and it says nothing about the obligation having no
// tool behind it here. A document they would have to already suspect is not
// a refusal; this is.
//
// Namespaced to `cadre knowledge` deliberately. `--scope` and `--as-of` are
// live flags on the *context* store, which has real expiry -- refusing them
// globally would break working commands to improve a message.

import (
	"fmt"
	"os"
	"strings"
)

// absentKnowledgeCapabilityFlags maps a flag the Python knowledge CLI
// accepted, and whose capability went with it, to what happened.
//
// Recovered from `git show b418031e~1:roster/knowledge-store/src/cli.py`
// rather than from memory: a refusal that fires on a flag nobody would type
// is a refusal nobody sees, and the point is to catch the word a steward
// actually reaches for.
var absentKnowledgeCapabilityFlags = map[string]string{
	"retention-days": "recorded a per-message retention window at ingest. No retention window " +
		"is recorded for any content now, and nothing ages out on its own",
	"trigger": "named what compelled a deletion of ingested content, for the evidence row. " +
		"There is no such deletion and no such evidence",
	"as-of": "read deletion evidence for ingested content as of a moment. There is no such " +
		"evidence to read",
}

// booleanKnowledgeFlags are the flags in this namespace that take no value.
//
// Read from the `fs.Bool` declarations across knowledge*.go. Kept as a list
// because the refusal runs before any flag set is built -- knowing this from
// the parser would mean parsing, and parsing is what this precedes.
var booleanKnowledgeFlags = map[string]bool{
	"all-sources": true, "diverged-from-proposal": true, "dry-run": true,
	"json": true, "reclaim": true, "render-only": true,
}

// refuseAbsentKnowledgeCapability reports a retention or erasure request that
// reaches a live `cadre knowledge` command, and returns true when it did.
//
// Checked before flag parsing, because the parser's own error is what this
// exists to replace.
func refuseAbsentKnowledgeCapability(args []string) bool {
	// A token is only a flag if it is not the *value* of the flag before it.
	//
	// Scanning argv blind refused `--reason "--retention-days"` -- a
	// perfectly good deletion reason, on a command that works, mentioning
	// the capability the reason is about. The refusal fired instead of the
	// deletion. That is worse than the parser error this replaced: it broke
	// a working command to improve a message.
	//
	// The heuristic is `--flag value`: a token following a dashed token with
	// no `=` is that flag's value and is not examined -- unless that flag is
	// boolean, where the next token is the following flag rather than a
	// value.
	//
	// booleanKnowledgeFlags exists for that case, and its failure mode is
	// deliberately one-directional: a boolean flag missing from the list
	// makes the refusal skip the token after it, so a real request degrades
	// to the parser error -- exactly the pre-refusal behaviour. A list that
	// is merely out of date can therefore never refuse work someone was
	// entitled to do, which is the failure that matters.
	skipNext := false
	for _, arg := range args {
		if arg == "--" {
			break // everything after is positional
		}
		if skipNext {
			skipNext = false
			continue
		}
		name := strings.TrimLeft(arg, "-")
		if index := strings.IndexByte(name, '='); index >= 0 {
			name = name[:index]
		} else if strings.HasPrefix(arg, "-") && !booleanKnowledgeFlags[name] {
			skipNext = true
		}
		if !strings.HasPrefix(arg, "-") || name == "" {
			continue
		}
		detail, absent := absentKnowledgeCapabilityFlags[name]
		if !absent {
			continue
		}
		fmt.Fprintf(os.Stderr,
			"cadre knowledge: --%s belonged to a capability this binary does not have.\n"+
				"  It %s.\n"+
				"  Retention and deletion of ingested content shipped in the Python CLI and "+
				"were removed in\n"+
				"  b418031e when the Go rewrite landed. Neither was rebuilt. Ingested content "+
				"lives in a recall\n"+
				"  store, whose CLI exposes no delete command either, so there is no "+
				"steward-facing way to\n"+
				"  remove it and nothing that reports what has expired.\n"+
				"  Whether that is rebuilt or declared out of scope is an open decision -- see\n"+
				"  roster/knowledge-store/SECURITY.md § Storage rules. Deleting the store file "+
				"is not the same\n"+
				"  act: it is unscoped, unrecorded, and removes everything else too.\n",
			name, detail)
		return true
	}
	return false
}
