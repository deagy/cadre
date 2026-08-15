package cli

import "testing"

// DoctorCmd's exit code is the part a script reads. Nothing tested it.
//
// Ported from roster/orchestration/test/test_doctor.py's MainExitCodeTests.

func TestDoctorHelpExitsZero(t *testing.T) {
	// Asking for help is not an error. A non-zero exit here fails any script
	// that probes for the subcommand's existence.
	if code := DoctorCmd([]string{"--help"}); code != 0 {
		t.Errorf("doctor --help exited %d, want 0", code)
	}
	if code := DoctorCmd([]string{"-h"}); code != 0 {
		t.Errorf("doctor -h exited %d, want 0", code)
	}
}

func TestDoctorRejectsUnknownFlags(t *testing.T) {
	// Ignoring an unrecognised flag would let `cadre doctor --jsonn` print a
	// human report while the caller parsed it as JSON -- a usage error that
	// looks like a data problem.
	//
	// 2, not 1: exit 1 is reserved for a real mismatch finding, so a script
	// that treats "doctor failed" as "installation is broken" is not
	// triggered by its own typo.
	for _, arg := range []string{"--jsonn", "--verbose", "extra-positional"} {
		if code := DoctorCmd([]string{arg}); code != 2 {
			t.Errorf("doctor %s exited %d, want 2", arg, code)
		}
	}
}

func TestDoctorAgainstTheRealInstallIsSelfConsistent(t *testing.T) {
	// Whatever this test run is executing from, doctor must reach a verdict
	// rather than crash, and that verdict must be one of the two it defines:
	// 0 when the picture is consistent, 1 when the cwd's checkout is not what
	// answered.
	code := DoctorCmd(nil)
	if code != 0 && code != 1 {
		t.Errorf("doctor exited %d, want 0 or 1", code)
	}
	if code := DoctorCmd([]string{"--json"}); code != 0 && code != 1 {
		t.Errorf("doctor --json exited %d, want 0 or 1", code)
	}
}
