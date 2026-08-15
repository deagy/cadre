package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// The final-handoff channel: a private file a dispatched CLI child may write
// one structured result to, separate from its stdout.
//
// A port of dispatch_core.py's _prepare_cli_final_handoff_channel /
// _read_cli_final_handoff / _cleanup_cli_final_handoff_channel. Go declared
// MaxFinalHandoffResultBytes and FinalHandoffResultEnvVar and used neither.
//
// Why a file rather than stdout: stdout is a transcript. It carries whatever
// the child chose to print -- reasoning, tool output, test logs, sometimes
// secrets -- and parsing a structured result out of it means deciding which
// part of an untrusted stream to believe. A separate channel makes the
// handoff something the child states deliberately, and lets stdout stay what
// it is: text to report or summarize, never to act on.
//
// The child is not trusted with this channel. It is handed a path, and a
// hostile child can do anything to that path: replace the file, substitute a
// FIFO that blocks the reader forever, swap in a symlink pointing somewhere
// this process can write, or nest content to defeat cleanup. Every defence
// below exists for one of those.

// finalHandoffChannel is one dispatch's private result file.
type finalHandoffChannel struct {
	directory string
	path      string

	// file is held open from creation until cleanup, and every read goes
	// through it rather than through the path. This is the central defence:
	// a descriptor keeps referring to the file it was opened on, so whatever
	// the child later puts at the path is simply not what gets read.
	file *os.File

	resultIdentity    fileIdentity
	directoryIdentity fileIdentity

	cleanupOnce sync.Once
}

// finalHandoffProtocol is appended to the prompt so the child knows the
// channel exists and what may go in it.
//
// The prohibitions are explicit because the failure they prevent is silent:
// a child that writes its transcript here turns a narrow structured channel
// into a second, unbounded copy of everything, stored automatically.
func finalHandoffProtocol() string {
	return fmt.Sprintf(
		"\n\nFinal-handoff result channel: after completing the task, optionally write one "+
			"JSON object (max %d bytes) to the path in $%s. It must be the versioned "+
			"cadre-final-handoff envelope. Write only the final structured handoff and "+
			"identifier-only artifact manifest; never write conversation text, prompts, "+
			"command/tool results, test logs, raw diffs, secrets, or credentials. This file "+
			"is the only automatic-capture channel; stdout is not used for capture.\n",
		MaxFinalHandoffResultBytes, FinalHandoffResultEnvVar)
}

// prepareFinalHandoffChannel creates the channel and returns the environment
// and prompt the child should be run with.
//
// On any failure the caller proceeds without a channel rather than failing
// the dispatch: the handoff is an optional enrichment, and refusing to
// dispatch because a temp directory could not be made would trade a missing
// convenience for a missing result.
func prepareFinalHandoffChannel(
	env map[string]string, prompt string,
) (map[string]string, string, *finalHandoffChannel) {
	directory, err := os.MkdirTemp("", "cadre-final-handoff-")
	if err != nil {
		return env, prompt, nil
	}
	// 0700 explicitly: MkdirTemp already uses it, but the channel's privacy
	// is a property worth stating where it is relied on rather than
	// inherited from another function's default.
	if err := os.Chmod(directory, 0o700); err != nil {
		_ = os.RemoveAll(directory)
		return env, prompt, nil
	}

	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		_ = os.RemoveAll(directory)
		return env, prompt, nil
	}

	path := filepath.Join(directory, "handoff.json")
	// O_EXCL so this is the file this process created, not one that was
	// already waiting at the path; O_NOFOLLOW so it is not a symlink.
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL|noFollowFlag, 0o600)
	if err != nil {
		_ = os.RemoveAll(directory)
		return env, prompt, nil
	}
	resultInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		_ = os.RemoveAll(directory)
		return env, prompt, nil
	}

	channel := &finalHandoffChannel{
		directory:         directory,
		path:              path,
		file:              file,
		resultIdentity:    identityOf(resultInfo),
		directoryIdentity: identityOf(directoryInfo),
	}

	childEnv := make(map[string]string, len(env)+1)
	for key, value := range env {
		childEnv[key] = value
	}
	childEnv[FinalHandoffResultEnvVar] = path

	return childEnv, prompt + finalHandoffProtocol(), channel
}

// read attaches the child's structured handoff to the result, or an
// explanation of why there is none.
//
// Never silently absent: a capture that failed and a child that wrote nothing
// are different facts, and only one of them is worth investigating.
func (channel *finalHandoffChannel) read(result map[string]any) {
	if channel == nil || result == nil {
		return
	}

	info, err := channel.file.Stat()
	if err != nil {
		result["final_handoff_capture_error"] = fmt.Sprintf(
			"final_handoff result file could not be inspected: %v", err)
		return
	}
	if !info.Mode().IsRegular() {
		// The retained descriptor was opened on a regular file, so this
		// cannot normally happen -- but a platform without O_NOFOLLOW, or a
		// future change that re-opens by path, could reach it. Refusing here
		// is what keeps a substituted FIFO from blocking the read forever.
		result["final_handoff_capture_error"] = "final_handoff result file was not a regular file"
		return
	}
	// A replacement check that cannot fire on the current code path, kept
	// deliberately.
	//
	// fstat on the retained descriptor always describes the file that was
	// opened, so a child that unlinks the path and creates a new file there
	// cannot make this differ -- and does not need to be caught, because the
	// read goes to the original file anyway. The check exists so that a
	// future change which re-opens by path fails here instead of silently
	// reading whatever the child substituted. Python carries the same check
	// with the same property.
	if identity := identityOf(info); identity.known && !sameFile(identity, channel.resultIdentity) {
		result["final_handoff_capture_error"] = "final_handoff result file was replaced"
		return
	}
	if info.Size() == 0 {
		// Nothing written. Not an error: the channel is optional.
		return
	}
	if info.Size() > MaxFinalHandoffResultBytes {
		result["final_handoff_capture_error"] = "final_handoff result file exceeds the 64KiB cap"
		return
	}

	if _, err := channel.file.Seek(0, 0); err != nil {
		result["final_handoff_capture_error"] = fmt.Sprintf(
			"final_handoff result file could not be read: %v", err)
		return
	}
	// One byte past the cap, so a file that grew between the stat and the
	// read is caught rather than silently truncated.
	payload := make([]byte, MaxFinalHandoffResultBytes+1)
	read, err := channel.file.Read(payload)
	if err != nil && read == 0 {
		result["final_handoff_capture_error"] = fmt.Sprintf(
			"final_handoff result file could not be read: %v", err)
		return
	}
	if read > MaxFinalHandoffResultBytes {
		result["final_handoff_capture_error"] = "final_handoff result file exceeds the 64KiB cap"
		return
	}

	var handoff any
	if err := json.Unmarshal(payload[:read], &handoff); err != nil {
		// Reported, not guessed at. A malformed envelope is the child
		// failing to hold up its end, and storing a fragment of it would put
		// unparsed text where a structured record is expected.
		result["final_handoff_capture_error"] = fmt.Sprintf(
			"final_handoff result file was invalid: %v", err)
		return
	}
	result["final_handoff"] = handoff
}

// cleanup tears the channel down. Best-effort and idempotent: it runs on
// every dispatch outcome, including ones that already failed, and a cleanup
// failure must never become the reported result.
func (channel *finalHandoffChannel) cleanup() {
	if channel == nil {
		return
	}
	channel.cleanupOnce.Do(func() {
		_ = channel.file.Close()

		// Only if the directory at that path is still the one that was
		// created. A hostile child that replaced it with a symlink to
		// somewhere else would otherwise have this process delete that
		// instead.
		info, err := os.Lstat(channel.directory)
		if err != nil {
			return
		}
		if !info.IsDir() || !sameFile(identityOf(info), channel.directoryIdentity) {
			return
		}
		// RemoveAll unlinks symlinks rather than following them and opens
		// each subdirectory relative to its parent, so nested content a child
		// created cannot redirect the removal outside this tree.
		_ = os.RemoveAll(channel.directory)
	})
}
