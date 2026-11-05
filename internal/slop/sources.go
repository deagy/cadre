package slop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxFileBytes bounds what one scan will read from a single file. A larger
// file is skipped and named in the report; it is never read partially.
const maxFileBytes = 8 << 20

// StdinName is the unit name used for text read from standard input.
const StdinName = "stdin"

// CommitMessageName is the file name reported for commit message hits.
const CommitMessageName = "commit-message"

var proseExtensions = map[string]bool{
	".md": true, ".markdown": true, ".mdx": true, ".txt": true, ".rst": true,
}

var skippedDirectories = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
}

// Skipped names an input the scan did not read, with the reason.
type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// ReadUnits turns command-line inputs into units. "-" reads stdin. A
// directory is walked for prose files; an explicit file is read whatever its
// extension. Binary and oversized files are skipped and reported.
func ReadUnits(inputs []string, surface Surface, stdin io.Reader) ([]Unit, []Skipped, error) {
	var units []Unit
	var skipped []Skipped
	for _, input := range inputs {
		if input == "-" {
			data, err := io.ReadAll(io.LimitReader(stdin, maxFileBytes+1))
			if err != nil {
				return nil, nil, fmt.Errorf("reading stdin: %w", err)
			}
			if len(data) > maxFileBytes {
				return nil, nil, fmt.Errorf("stdin is larger than %d bytes", maxFileBytes)
			}
			units = append(units, Unit{Name: StdinName, Surface: surface, Text: string(data)})
			continue
		}
		paths, err := expand(input)
		if err != nil {
			return nil, nil, err
		}
		for _, path := range paths {
			unit, skip, err := readFile(path, surface)
			if err != nil {
				return nil, nil, err
			}
			if skip != nil {
				skipped = append(skipped, *skip)
				continue
			}
			units = append(units, unit)
		}
	}
	return units, skipped, nil
}

func expand(input string) ([]string, error) {
	if strings.ContainsAny(input, "*?[") {
		matches, err := filepath.Glob(input)
		if err != nil {
			return nil, fmt.Errorf("bad glob %q: %w", input, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("glob %q matched no files", input)
		}
		sort.Strings(matches)
		var all []string
		for _, match := range matches {
			more, err := expandPath(match)
			if err != nil {
				return nil, err
			}
			all = append(all, more...)
		}
		return all, nil
	}
	return expandPath(input)
}

func expandPath(input string) ([]string, error) {
	info, err := os.Stat(input)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{input}, nil
	}
	var paths []string
	walkErr := filepath.WalkDir(input, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != input && skippedDirectories[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() && proseExtensions[strings.ToLower(filepath.Ext(path))] {
			paths = append(paths, path)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return paths, nil
}

func readFile(path string, surface Surface) (Unit, *Skipped, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Unit{}, nil, err
	}
	if info.Size() > maxFileBytes {
		return Unit{}, &Skipped{Path: path, Reason: fmt.Sprintf("larger than %d bytes", maxFileBytes)}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Unit{}, nil, err
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return Unit{}, &Skipped{Path: path, Reason: "binary file"}, nil
	}
	return Unit{Name: path, Surface: surface, Text: string(data)}, nil, nil
}

// CommitUnits returns one unit per non-merge commit in a git range such as
// origin/main..HEAD, oldest first, run in dir. The range is validated before
// it reaches git, so it cannot be read as an option.
func CommitUnits(ctx context.Context, dir, rangeSpec string) ([]Unit, error) {
	if err := validateRange(rangeSpec); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "log", "--no-merges", "--reverse",
		"--format=%H%x00%B%x01", rangeSpec, "--")
	command.Dir = dir
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("git log %s: %s", rangeSpec, detail)
	}
	return parseCommitLog(string(out))
}

func validateRange(rangeSpec string) error {
	switch {
	case rangeSpec == "":
		return errors.New("--git-range is empty")
	case strings.HasPrefix(rangeSpec, "-"):
		return fmt.Errorf("--git-range %q must not start with a dash", rangeSpec)
	case strings.ContainsAny(rangeSpec, " \t\r\n\x00"):
		return fmt.Errorf("--git-range %q must not contain whitespace", rangeSpec)
	case !strings.Contains(rangeSpec, ".."):
		return fmt.Errorf("--git-range %q must be a range such as A..B", rangeSpec)
	}
	return nil
}

func parseCommitLog(raw string) ([]Unit, error) {
	var units []Unit
	for _, record := range strings.Split(raw, "\x01") {
		record = strings.TrimLeft(record, "\n")
		if strings.TrimSpace(record) == "" {
			continue
		}
		sha, message, found := strings.Cut(record, "\x00")
		if !found || sha == "" {
			return nil, fmt.Errorf("unexpected git log record %q", truncate(record, 40))
		}
		units = append(units, Unit{
			Name: CommitMessageName, Commit: sha, Surface: SurfaceCommit,
			Text: strings.TrimRight(message, "\n"),
		})
	}
	return units, nil
}
