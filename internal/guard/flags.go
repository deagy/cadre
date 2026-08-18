package guard

import (
	"regexp"
	"strings"
)

// Argument-shape helpers modelling git's own parse-options behaviour for short
// flags, which several handlers have to agree on: `-B name`, `-Bname`, and the
// combined group forms.

// shortFlagGroup returns the letters of a combined short-flag group
// (`-fB` -> "fB"), or false.
//
// Only all-alphabetic groups qualify, so `-Bexisting-1` and `-B=x` fall
// through to the attached-value handling instead.
func shortFlagGroup(token string) (string, bool) {
	if len(token) > 1 && token[0] == '-' && token[1] != '-' && isAlpha(token[1:]) {
		return token[1:], true
	}
	return "", false
}

func isAlpha(text string) bool {
	if text == "" {
		return false
	}
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') {
			return false
		}
	}
	return true
}

// flagValue returns the value of `<flag> <value>`, `--long=<value>`, or -- for
// a short flag -- git's attached and combined spellings.
//
// Git's parse-options consumes a short flag's value from the REST of its group
// when characters follow it, and from the next token when it is the group's
// last character:
//
//   - `git checkout -Bexisting` resets `existing` exactly as the detached
//     spelling does, so missing the attached form would leave the same
//     destructive operation unguarded behind a one-space difference;
//   - `git checkout -fB existing` resets `existing` (B is last in the group);
//   - `git checkout -Bf existing` creates a branch literally named `f` and
//     treats `existing` as the START POINT (B is not last, so the remainder of
//     the group is its value).
//
// `-B=name` is NOT a git spelling despite reading like one: `git checkout
// -B=weird` creates a branch named `=weird`, which is why the attached branch
// returns "=weird" and the `<flag>=<value>` form is reserved for LONG flags.
func flagValue(args []string, flag string) (string, bool) {
	isShort := len(flag) == 2 && strings.HasPrefix(flag, "-") && !strings.HasPrefix(flag, "--")
	var letter byte
	if isShort {
		letter = flag[1]
	}
	for i, argument := range args {
		if argument == flag {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", false
		}
		if isShort {
			if group, ok := shortFlagGroup(argument); ok {
				if position := strings.IndexByte(group, letter); position >= 0 {
					if position == len(group)-1 {
						if i+1 < len(args) {
							return args[i+1], true
						}
						return "", false
					}
					return group[position+1:], true
				}
			}
			if strings.HasPrefix(argument, flag) && len(argument) > 2 {
				return argument[2:], true
			}
		} else if strings.HasPrefix(argument, flag+"=") {
			return strings.SplitN(argument, "=", 2)[1], true
		}
	}
	return "", false
}

// flagPresent reports whether flag appears at all, in any spelling flagValue
// understands. Distinct from flagValue succeeding, which cannot tell "absent"
// from "present with no value left on the line".
func flagPresent(args []string, flag string) bool {
	isShort := len(flag) == 2 && strings.HasPrefix(flag, "-") && !strings.HasPrefix(flag, "--")
	var letter byte
	if isShort {
		letter = flag[1]
	}
	for _, argument := range args {
		if argument == flag {
			return true
		}
		if isShort {
			if group, ok := shortFlagGroup(argument); ok && strings.IndexByte(group, letter) >= 0 {
				return true
			}
			if strings.HasPrefix(argument, flag) && len(argument) > 2 {
				return true
			}
		} else if strings.HasPrefix(argument, flag+"=") {
			return true
		}
	}
	return false
}

// consumesNextToken reports whether token takes the FOLLOWING token as its
// value. A combined group only does so when the value-taking flag is its last
// character; otherwise the rest of the group is the value.
func consumesNextToken(token string, flagsWithValue map[string]bool) bool {
	if flagsWithValue[token] {
		return true
	}
	if group, ok := shortFlagGroup(token); ok && len(group) > 1 {
		for position := 0; position < len(group); position++ {
			if flagsWithValue["-"+string(group[position])] {
				return position == len(group)-1
			}
		}
	}
	return false
}

// positionalArgs returns positional arguments, skipping flags and their values.
//
// Conservative, not exhaustive -- an unrecognised flag falls through to the
// generic "-" skip without consuming a value. Getting a flags-with-value set
// wrong mis-resolves a start point, which git then fails to resolve, which
// fails open.
func positionalArgs(args []string, flagsWithValue map[string]bool) []string {
	var found []string
	for i := 0; i < len(args); {
		argument := args[i]
		if argument == "--" {
			found = append(found, args[i+1:]...)
			break
		}
		if consumesNextToken(argument, flagsWithValue) {
			i += 2
			continue
		}
		if strings.HasPrefix(argument, "-") && argument != "-" {
			i++
			continue
		}
		found = append(found, argument)
		i++
	}
	return found
}

// shellUnsafe matches anything that forces quoting, exactly as Python's
// shlex.quote decides it.
var shellUnsafe = regexp.MustCompile(`[^\w@%+=:,./-]`)

// shellQuoteWord is Python's shlex.quote. Used when a `!shell` alias's
// definition is recombined with the arguments git would append to it.
func shellQuoteWord(word string) string {
	if word == "" {
		return "''"
	}
	if !shellUnsafe.MatchString(word) {
		return word
	}
	return "'" + strings.ReplaceAll(word, "'", `'"'"'`) + "'"
}
