package orchestration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The channel hands a hostile child a writable path and then reads from it.
// Everything below is about the fact that the child controls that path
// between the two.

func TestAHandoffTheChildWritesIsCaptured(t *testing.T) {
	env, prompt, channel := prepareFinalHandoffChannel(map[string]string{}, "task")
	if channel == nil {
		t.Fatal("no channel was created")
	}
	defer channel.cleanup()

	// The child learns the path from the environment, and the protocol from
	// the prompt -- a path with no explanation is a file nobody writes.
	path := env[FinalHandoffResultEnvVar]
	if path == "" {
		t.Fatal("the child was given no result path")
	}
	if !strings.Contains(prompt, FinalHandoffResultEnvVar) {
		t.Error("the prompt does not tell the child the channel exists")
	}

	if err := os.WriteFile(path, []byte(`{"version":1,"summary":"done"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	result := map[string]any{}
	channel.read(result)

	handoff, ok := result["final_handoff"].(map[string]any)
	if !ok {
		t.Fatalf("no handoff captured: %v", result)
	}
	if handoff["summary"] != "done" {
		t.Errorf("handoff = %v, want the child's own object", handoff)
	}
}

func TestAnUnwrittenChannelIsNotAnError(t *testing.T) {
	// The channel is optional. A child that writes nothing has not failed,
	// and reporting an error would make every ordinary dispatch look broken.
	_, _, channel := prepareFinalHandoffChannel(map[string]string{}, "task")
	defer channel.cleanup()

	result := map[string]any{}
	channel.read(result)

	if _, present := result["final_handoff"]; present {
		t.Error("a handoff was reported for a file nobody wrote")
	}
	if _, present := result["final_handoff_capture_error"]; present {
		t.Errorf("an empty channel was reported as an error: %v", result)
	}
}

func TestAReplacedResultFileIsNotRead(t *testing.T) {
	// The central defence. The child is handed a path and can unlink what is
	// there and put anything in its place -- so the read goes through the
	// descriptor opened at creation, which still refers to the original file
	// no matter what now sits at the name.
	env, _, channel := prepareFinalHandoffChannel(map[string]string{}, "task")
	defer channel.cleanup()
	path := env[FinalHandoffResultEnvVar]

	// The child writes a legitimate handoff...
	if err := os.WriteFile(path, []byte(`{"summary":"real"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// ...then replaces the file entirely with different content.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"summary":"substituted"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	result := map[string]any{}
	channel.read(result)

	// The substituted content is never what is captured.
	if handoff, ok := result["final_handoff"].(map[string]any); ok {
		if handoff["summary"] == "substituted" {
			t.Fatal("the replacement was read instead of the original file")
		}
		// Reading the retained descriptor gives the original bytes, which is
		// the correct outcome on a platform that cannot compare identity.
		if handoff["summary"] != "real" {
			t.Errorf("handoff = %v, want the original file's content", handoff)
		}
		return
	}
	// No handoff at all is also correct: the original file was unlinked
	// before anything was written to the descriptor's copy, so it is empty.
	// What must never happen is reading the substituted content, which the
	// check above covers.
	if reason, present := result["final_handoff_capture_error"]; present {
		if !strings.Contains(reason.(string), "replaced") {
			t.Errorf("unexpected capture error: %v", reason)
		}
	}
}

func TestAFifoAtTheResultPathDoesNotBlockTheRead(t *testing.T) {
	// A FIFO with no writer blocks a reader forever. Substituting one at the
	// path is how a child would hang the dispatch that is waiting on it --
	// and the retained descriptor is why it cannot: the read never touches
	// the path again.
	if runtime.GOOS == "windows" {
		t.Skip("no FIFOs on Windows")
	}
	env, _, channel := prepareFinalHandoffChannel(map[string]string{}, "task")
	defer channel.cleanup()
	path := env[FinalHandoffResultEnvVar]

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}

	// This must return. A test that hangs here is the bug.
	done := make(chan map[string]any, 1)
	go func() {
		result := map[string]any{}
		channel.read(result)
		done <- result
	}()

	select {
	case result := <-done:
		if handoff, ok := result["final_handoff"]; ok {
			t.Errorf("a FIFO substitution produced a handoff: %v", handoff)
		}
	case <-timeoutAfterSeconds(5):
		t.Fatal("the read blocked on a substituted FIFO")
	}
}

func TestASymlinkedResultPathIsNotFollowedOnCleanup(t *testing.T) {
	// Cleanup deletes a directory the child could write into. A child that
	// replaces the directory with a symlink would otherwise have this process
	// delete whatever it points at.
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	_, _, channel := prepareFinalHandoffChannel(map[string]string{}, "task")

	// Something valuable elsewhere, and a symlink standing where the
	// channel's directory was.
	elsewhere := t.TempDir()
	keep := filepath.Join(elsewhere, "important.txt")
	if err := os.WriteFile(keep, []byte("do not delete"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(channel.directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, channel.directory); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	channel.cleanup()

	if _, err := os.Stat(keep); err != nil {
		t.Errorf("cleanup followed the symlink and deleted the target: %v", err)
	}
	// The symlink itself is left rather than followed. Removing it would be
	// fine; following it would not.
	_ = os.Remove(channel.directory)
}

func TestCleanupDoesNotDeleteADirectoryItDidNotCreate(t *testing.T) {
	// The symlink case above passes even without the identity check, because
	// RemoveAll unlinks a symlink rather than following it. This is the case
	// the identity check is actually for: the child replaces the channel
	// directory with a *real* directory of its own, and cleanup must not
	// delete that.
	_, _, channel := prepareFinalHandoffChannel(map[string]string{}, "task")
	if !channel.directoryIdentity.known {
		t.Skip("this platform cannot compare file identity")
	}

	if err := os.RemoveAll(channel.directory); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(channel.directory, 0o700); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(channel.directory, "not-ours.txt")
	if err := os.WriteFile(planted, []byte("someone else's"), 0o600); err != nil {
		t.Fatal(err)
	}

	channel.cleanup()

	if _, err := os.Stat(planted); err != nil {
		t.Errorf("cleanup deleted a directory it did not create: %v", err)
	}
	_ = os.RemoveAll(channel.directory)
}

func TestNestedContentTheChildCreatedIsCleanedUp(t *testing.T) {
	// A child can write more than the one file it was told about. Leaving
	// that behind means every dispatch leaks a temp directory.
	_, _, channel := prepareFinalHandoffChannel(map[string]string{}, "task")
	nested := filepath.Join(channel.directory, "nested", "deeper")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "junk"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	channel.cleanup()

	if _, err := os.Stat(channel.directory); !os.IsNotExist(err) {
		t.Errorf("the channel directory survived cleanup: %v", err)
	}
}

func TestCleanupIsIdempotent(t *testing.T) {
	// It runs on every dispatch outcome, including ones that already failed,
	// so a second call must be harmless rather than a double close.
	_, _, channel := prepareFinalHandoffChannel(map[string]string{}, "task")
	channel.cleanup()
	channel.cleanup()
}

func TestAnOversizedHandoffIsRefusedNotTruncated(t *testing.T) {
	// A truncated JSON object does not parse, so truncation would surface as
	// "invalid" and hide the real reason. The cap is reported as itself.
	env, _, channel := prepareFinalHandoffChannel(map[string]string{}, "task")
	defer channel.cleanup()

	oversized := append([]byte(`{"padding":"`), make([]byte, MaxFinalHandoffResultBytes)...)
	if err := os.WriteFile(env[FinalHandoffResultEnvVar], oversized, 0o600); err != nil {
		t.Fatal(err)
	}

	result := map[string]any{}
	channel.read(result)

	if _, present := result["final_handoff"]; present {
		t.Error("an oversized handoff was captured")
	}
	reason, _ := result["final_handoff_capture_error"].(string)
	if !strings.Contains(reason, "cap") {
		t.Errorf("error = %q, want the size cap named", reason)
	}
}

func TestAMalformedHandoffIsReportedRatherThanStored(t *testing.T) {
	// Storing a fragment of unparsed text where a structured record is
	// expected would put the child's raw output into the capture channel by
	// the back door -- the one thing the channel exists to avoid.
	env, _, channel := prepareFinalHandoffChannel(map[string]string{}, "task")
	defer channel.cleanup()

	if err := os.WriteFile(env[FinalHandoffResultEnvVar], []byte("not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}

	result := map[string]any{}
	channel.read(result)

	if _, present := result["final_handoff"]; present {
		t.Error("malformed content was captured as a handoff")
	}
	if _, present := result["final_handoff_capture_error"]; !present {
		t.Error("a malformed handoff was silently ignored")
	}
}

func TestTheProtocolForbidsTheThingsThatWouldMakeItATranscript(t *testing.T) {
	// The channel is narrow on purpose. A child that writes its transcript
	// here turns automatic capture into a second, unbounded copy of
	// everything -- so the prohibition is stated to the child, not merely
	// assumed.
	protocol := finalHandoffProtocol()
	for _, forbidden := range []string{"conversation text", "test logs", "secrets", "credentials"} {
		if !strings.Contains(protocol, forbidden) {
			t.Errorf("the protocol does not forbid %q", forbidden)
		}
	}
	if !strings.Contains(protocol, "stdout is not used for capture") {
		t.Error("the protocol does not say stdout is not the capture channel")
	}
}

func TestTheChannelDirectoryIsPrivate(t *testing.T) {
	// It holds whatever the child reports, on a shared temp directory.
	_, _, channel := prepareFinalHandoffChannel(map[string]string{}, "task")
	defer channel.cleanup()

	info, err := os.Stat(channel.directory)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("channel directory mode = %04o, want no group or other access", mode)
	}
}

// timeoutAfterSeconds is a small helper so the FIFO test states its own
// deadline rather than relying on the package-level test timeout, which would
// fail the whole run with no indication of which test hung.
func timeoutAfterSeconds(seconds int) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		time.Sleep(time.Duration(seconds) * time.Second)
		close(done)
	}()
	return done
}
