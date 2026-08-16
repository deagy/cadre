package kernel

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Descriptor-confined I/O for the mutable surface of `repair`.
//
// Every other command in this kernel resolves a path, proves it lands inside
// the project, and then opens it. That leaves a gap: between the check and the
// open, a directory or file can be exchanged for a symlink, and the write
// lands wherever the link points. Repair is the command where that matters
// most -- it is the one that writes into a project it did not just create,
// against a filesystem somebody else may be touching.
//
// So repair does not resolve paths. It pins the root with a descriptor and
// walks one component at a time, refusing a symlink at every step, and it
// installs files through a temporary file in the pinned directory rather than
// writing a path.
//
// The two installation modes are different on purpose:
//
//   - Overwriting uses rename, which replaces whatever is at the name --
//     including a symlink raced in behind us -- and never follows it.
//   - Creating uses link, which is atomic and fails if anything already exists
//     at the name. That is what makes "create only what is missing" true even
//     when a decision appears between planning and writing: repair loses the
//     race rather than overwriting the decision.
//
// Where the platform cannot refuse a symlink at open time, repair refuses to
// run at all rather than falling back to the ordinary path I/O it exists to
// avoid.

// RepairFilesystem is a project root pinned by descriptor.
type RepairFilesystem struct {
	root *os.Root
	name string
}

// OpenRepairFilesystem pins root without resolving it.
//
// Deliberately not resolved: resolving would silently accept a final-component
// symlink an operator handed in as --root. If the root itself is a link, the
// operator should be told, not quietly followed.
func OpenRepairFilesystem(root string) (*RepairFilesystem, error) {
	if !NoFollowSupported {
		return nil, fmt.Errorf("secure repair I/O requires O_NOFOLLOW support")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if info, err := os.Lstat(absolute); err != nil {
		return nil, fmt.Errorf("cannot securely open project root: %w", err)
	} else if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("cannot securely open project root: %s is a symlink", absolute)
	}
	opened, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, fmt.Errorf("cannot securely open project root: %w", err)
	}
	return &RepairFilesystem{root: opened, name: absolute}, nil
}

// Close releases the pinned descriptors.
func (f *RepairFilesystem) Close() error { return f.root.Close() }

// Root is the absolute path the filesystem was opened at, unresolved.
func (f *RepairFilesystem) Root() string { return f.name }

// component refuses anything that is not a single, ordinary name.
//
// "..", a separator, or an empty string would each turn a component walk into
// a path traversal, which is the thing this type exists to prevent.
func component(value string) (string, error) {
	if value == "" || value == "." || value == ".." ||
		strings.ContainsAny(value, `/\`) {
		return "", fmt.Errorf("unsafe repair path component: %q", value)
	}
	return value, nil
}

// directory walks to the directory holding parts, opening each component as
// its own pinned root and refusing any symlink on the way.
//
// os.Root confines by itself, but it follows symlinks that stay inside the
// root. That is a weaker rule than this one: a link inside the project still
// redirects a write to a file the planner never looked at. Each component is
// therefore Lstat-ed and refused if it is a link.
func (f *RepairFilesystem) directory(parts []string, create bool) (*os.Root, error) {
	// Validated before the walk starts, not as each component is reached. A
	// missing prefix would otherwise short-circuit the walk and leave a ".."
	// further along unexamined -- reported as "missing" rather than as the
	// traversal attempt it is.
	for _, part := range parts {
		if _, err := component(part); err != nil {
			return nil, err
		}
	}

	current := f.root
	opened := false
	closeCurrent := func() {
		if opened {
			_ = current.Close()
		}
	}

	for _, part := range parts {
		name, err := component(part)
		if err != nil {
			closeCurrent()
			return nil, err
		}
		info, statErr := current.Lstat(name)
		switch {
		case statErr == nil && info.Mode()&fs.ModeSymlink != 0:
			closeCurrent()
			return nil, fmt.Errorf("unsafe repair symlink: %s", strings.Join(parts, "/"))
		case statErr == nil && !info.IsDir():
			closeCurrent()
			return nil, fmt.Errorf("repair path is not a directory: %s", strings.Join(parts, "/"))
		case statErr != nil && errors.Is(statErr, fs.ErrNotExist):
			if !create {
				closeCurrent()
				return nil, statErr
			}
			if err := current.Mkdir(name, 0o755); err != nil {
				closeCurrent()
				return nil, err
			}
		case statErr != nil:
			closeCurrent()
			return nil, fmt.Errorf("unsafe repair directory component %s: %w",
				strings.Join(parts, "/"), statErr)
		}

		next, err := current.OpenRoot(name)
		if err != nil {
			closeCurrent()
			return nil, fmt.Errorf("unsafe repair directory component %s: %w",
				strings.Join(parts, "/"), err)
		}
		closeCurrent()
		current, opened = next, true
	}
	if !opened {
		// The caller closes what it is given, so hand back a distinct handle
		// rather than the one this type holds for its own lifetime.
		return f.root.OpenRoot(".")
	}
	return current, nil
}

// FileState reports "regular", "missing", or an error for anything else.
//
// A symlink is an error rather than a state: repair's whole contract is that
// it writes where it looked, and a link means those are two different places.
func (f *RepairFilesystem) FileState(parts ...string) (string, error) {
	if len(parts) == 0 {
		return "", fmt.Errorf("repair file path is empty")
	}
	parent, err := f.directory(parts[:len(parts)-1], false)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "missing", nil
		}
		return "", err
	}
	defer func() { _ = parent.Close() }()

	name, err := component(parts[len(parts)-1])
	if err != nil {
		return "", err
	}
	info, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("unsafe repair symlink: %s", strings.Join(parts, "/"))
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("repair path is not a regular file: %s", strings.Join(parts, "/"))
	}
	return "regular", nil
}

// ReadText reads a file that must already be a regular file.
func (f *RepairFilesystem) ReadText(parts ...string) (string, error) {
	state, err := f.FileState(parts...)
	if err != nil {
		return "", err
	}
	if state != "regular" {
		return "", fmt.Errorf("missing repair file: %s", strings.Join(parts, "/"))
	}
	parent, err := f.directory(parts[:len(parts)-1], false)
	if err != nil {
		return "", err
	}
	defer func() { _ = parent.Close() }()

	name, err := component(parts[len(parts)-1])
	if err != nil {
		return "", err
	}
	// O_NOFOLLOW closes the gap between the check above and this open at the
	// kernel level rather than in this process.
	file, err := parent.OpenFile(name, os.O_RDONLY|noFollowFlag, 0)
	if err != nil {
		return "", fmt.Errorf("cannot securely read %s: %w", strings.Join(parts, "/"), err)
	}
	defer func() { _ = file.Close() }()

	// Checked through the descriptor, not the name: the descriptor is the
	// thing that was opened, and re-consulting the name would reintroduce
	// exactly the gap O_NOFOLLOW just closed.
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("repair path is not a regular file: %s", strings.Join(parts, "/"))
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteText installs content at parts, atomically.
//
// Returns false when the file exists and overwrite is not set -- that is the
// "somebody made a decision here" case, and it is a normal outcome rather than
// an error.
func (f *RepairFilesystem) WriteText(parts []string, content string, overwrite bool) (bool, error) {
	if len(parts) == 0 {
		return false, fmt.Errorf("repair file path is empty")
	}
	parent, err := f.directory(parts[:len(parts)-1], true)
	if err != nil {
		return false, err
	}
	defer func() { _ = parent.Close() }()

	name, err := component(parts[len(parts)-1])
	if err != nil {
		return false, err
	}
	state, err := f.FileState(parts...)
	if err != nil {
		return false, err
	}
	if state == "regular" && !overwrite {
		return false, nil
	}

	temporary, err := writeTemporary(parent, content)
	if err != nil {
		return false, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = parent.Remove(temporary)
		}
	}()

	if overwrite {
		// rename replaces whatever is at the name, including a symlink raced
		// in behind us, and never follows it.
		if err := parent.Rename(temporary, name); err != nil {
			return false, err
		}
		cleanup = false
		return true, nil
	}
	// link is an atomic no-clobber install. Unlike rename it fails if a
	// decision appeared after planning, so repair loses that race rather than
	// overwriting the decision.
	if err := parent.Link(temporary, name); err != nil {
		return false, err
	}
	_ = parent.Remove(temporary)
	cleanup = false
	return true, nil
}

// writeTemporary creates a uniquely named file in the pinned directory and
// fsyncs it, returning the name to install from.
func writeTemporary(parent *os.Root, content string) (string, error) {
	for attempt := 0; attempt < 20; attempt++ {
		candidate := ".agentic-sdlc-repair-" + temporaryToken()
		file, err := parent.OpenFile(candidate,
			os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollowFlag, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := file.WriteString(content); err != nil {
			_ = file.Close()
			_ = parent.Remove(candidate)
			return "", err
		}
		// Fsynced before the install, so a crash cannot leave a file that
		// exists with no content in it -- which repair would then treat as a
		// decision it must not overwrite.
		if err := file.Sync(); err != nil {
			_ = file.Close()
			_ = parent.Remove(candidate)
			return "", err
		}
		if err := file.Close(); err != nil {
			_ = parent.Remove(candidate)
			return "", err
		}
		return candidate, nil
	}
	return "", fmt.Errorf("could not create a secure temporary repair file")
}

// temporaryToken names a temporary file unpredictably.
//
// Random rather than sequential: the name lives in a directory somebody else
// may be able to write to, and a guessable one lets them pre-create the file
// this write is about to install from.
func temporaryToken() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		panic(fmt.Sprintf("kernel: cannot draw a temporary file name: %v", err))
	}
	return hex.EncodeToString(buffer)
}
