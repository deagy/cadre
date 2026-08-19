package generators

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// What the generated shim actually does when run, as opposed to what it says.
//
// The shim resolves a per-platform compiled binary, verifies it, caches it, and
// executes it. Every property below is one whose failure is silent: a cache
// that is never hit, an unverified binary that runs anyway, an archive member
// that escapes into the cache directory.
//
// Ported from plugin/tools/test_binary_shim_sidecar.py,
// test_binary_shim_hardening.py and test_binary_shim_behavioral.py.
//
// Two of those hardcoded the CLI version as "0.5.0", which happens to be
// current. On the next bump the warm-cache tests would have failed loudly, but
// the two refusal tests would have passed vacuously -- a cache the shim never
// looks at produces the same refusal as no cache at all. The version is read
// from the shim itself here.

// pluginManifestVersion is deliberately different from the CLI version, so a
// test can tell which one the shim used. indent matters: the shim's --version
// path reads the manifest with sed and needs "version" at the start of a line.
const pluginManifestVersion = "0.23.3"

const refusalMessage = "could not obtain the cadre binary"

type shimHarness struct {
	t          *testing.T
	root       string
	pluginRoot string
	shim       string
	cacheHome  string
	cacheDir   string
	stubBin    string
	offlineBin string
	version    string
	goos       string
	goarch     string
	binaryName string
}

func newShimHarness(t *testing.T) *shimHarness {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("this POSIX sh shim does not run natively on %s", runtime.GOOS)
	}
	repoRoot := repositoryRoot(t)
	generated := filepath.Join(repoRoot, "plugin", "bin", "cadre")
	source, err := os.ReadFile(generated)
	if err != nil {
		t.Skipf("no generated shim to exercise: %v", err)
	}

	harness := &shimHarness{t: t, root: t.TempDir()}
	harness.pluginRoot = filepath.Join(harness.root, "plug")
	binDir := filepath.Join(harness.pluginRoot, "bin")
	manifestDir := filepath.Join(harness.pluginRoot, ".claude-plugin")
	harness.cacheHome = filepath.Join(harness.root, "cache")
	harness.cacheDir = filepath.Join(harness.cacheHome, "cadre")
	harness.stubBin = filepath.Join(harness.root, "stubbin")
	harness.offlineBin = filepath.Join(harness.root, "offlinebin")
	for _, directory := range []string{binDir, manifestDir, harness.cacheDir, harness.stubBin, harness.offlineBin} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", directory, err)
		}
	}
	// Downloaders that always fail, ahead of the real ones on PATH.
	for _, tool := range []string{"curl", "wget"} {
		stub := filepath.Join(harness.offlineBin, tool)
		if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
			t.Fatalf("writing the offline %s stub: %v", tool, err)
		}
	}
	manifest := "{\n  \"version\": \"" + pluginManifestVersion + "\",\n  \"name\": \"cadre\"\n}\n"
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("writing the manifest: %v", err)
	}
	harness.shim = filepath.Join(binDir, "cadre")
	if err := os.WriteFile(harness.shim, source, 0o755); err != nil {
		t.Fatalf("copying the shim: %v", err)
	}

	// Read from the shim, not from a literal: the shim is what decides which
	// cache entry is looked for, so anything else can drift out from under
	// these tests without failing them.
	match := cliVersionAssignment.FindSubmatch(source)
	if match == nil {
		t.Fatal("the shim pins no CADRE_CLI_VERSION, so no cache path can be derived")
	}
	harness.version = string(match[1])
	if harness.version == pluginManifestVersion {
		t.Fatalf("the CLI version and the plugin manifest version are both %q, so no "+
			"test here can tell which one the shim used", harness.version)
	}
	harness.goos = runtime.GOOS
	harness.goarch = runtime.GOARCH
	harness.binaryName = fmt.Sprintf("cadre-v%s-%s-%s", harness.version, harness.goos, harness.goarch)
	return harness
}

// stubBinaryBody is an identifiable stand-in for the compiled binary. Exit 42
// is the signal that the shim reached the point of executing it.
const stubBinaryBody = "#!/bin/sh\necho \"BINARY-EXECUTED\" >&2\nexit 42\n"

type shimResult struct {
	exitCode int
	stderr   string
}

func (h *shimHarness) run(extraPath bool, args ...string) shimResult {
	h.t.Helper()
	command := exec.Command(h.shim, args...)
	command.Dir = h.root
	// "Offline" has to be enforced, not assumed.
	//
	// This used to be PATH=/usr/bin:/bin, which is where curl and wget live --
	// so the shim could and did reach the network. The refusal tests passed
	// only because the download failed for an unrelated reason: releases before
	// cli-v0.6.4 packed the binary under its build name, so the shim's
	// `tar -xzf ... cadre` never found what it asked for. Fixing the archives
	// made the download work and those tests started executing a real,
	// freshly-downloaded binary instead of refusing.
	//
	// offlineBin puts failing curl and wget ahead of the real ones, so the
	// refusal under test is the one the shim reaches with no network at all.
	path := h.offlineBin + ":/usr/bin:/bin"
	if extraPath {
		path = h.stubBin + ":" + os.Getenv("PATH")
	}
	command.Env = append(os.Environ(),
		"XDG_CACHE_HOME="+h.cacheHome,
		"PATH="+path)
	// CADRE_REPO_ROOT would let the shim resolve a checkout instead of the
	// cache, which is a different code path from the one under test.
	filtered := command.Env[:0]
	for _, entry := range command.Env {
		if !strings.HasPrefix(entry, "CADRE_REPO_ROOT=") {
			filtered = append(filtered, entry)
		}
	}
	command.Env = filtered
	var stderr strings.Builder
	command.Stderr = &stderr
	command.Stdout = nil
	err := command.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		h.t.Fatalf("running the shim: %v", err)
	}
	return shimResult{exitCode: code, stderr: stderr.String()}
}

// cacheBinary writes a valid cached binary and its sidecar at the given mode.
func (h *shimHarness) cacheBinary(mode os.FileMode, withSidecar bool) string {
	h.t.Helper()
	binary := filepath.Join(h.cacheDir, h.binaryName)
	if err := os.WriteFile(binary, []byte(stubBinaryBody), mode); err != nil {
		h.t.Fatalf("writing the cached binary: %v", err)
	}
	if err := os.Chmod(binary, mode); err != nil {
		h.t.Fatalf("chmod: %v", err)
	}
	if withSidecar {
		sum := sha256.Sum256([]byte(stubBinaryBody))
		if err := os.WriteFile(binary+".sha256", []byte(hex.EncodeToString(sum[:])), 0o644); err != nil {
			h.t.Fatalf("writing the sidecar: %v", err)
		}
	}
	return binary
}

// payloadArchive builds a release-shaped archive holding the stub plus a
// bystander member that must never be extracted.
func (h *shimHarness) payloadArchive() string {
	h.t.Helper()
	path := filepath.Join(h.root, h.binaryName+".tar.gz")
	file, err := os.Create(path)
	if err != nil {
		h.t.Fatalf("creating the archive: %v", err)
	}
	defer file.Close()
	zipped := gzip.NewWriter(file)
	writer := tar.NewWriter(zipped)
	for _, member := range []struct {
		name string
		body string
		mode int64
	}{
		{"cadre", stubBinaryBody, 0o755},
		{"UNWANTED-MEMBER", "should not be extracted\n", 0o644},
	} {
		if err := writer.WriteHeader(&tar.Header{
			Name: member.name, Mode: member.mode, Size: int64(len(member.body)),
		}); err != nil {
			h.t.Fatalf("tar header: %v", err)
		}
		if _, err := writer.Write([]byte(member.body)); err != nil {
			h.t.Fatalf("tar body: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		h.t.Fatalf("closing tar: %v", err)
	}
	if err := zipped.Close(); err != nil {
		h.t.Fatalf("closing gzip: %v", err)
	}
	return path
}

// installCurlStub serves the archive and a SHA256SUMS naming it.
func (h *shimHarness) installCurlStub(archive, logPath string) {
	h.t.Helper()
	content, err := os.ReadFile(archive)
	if err != nil {
		h.t.Fatalf("reading the archive: %v", err)
	}
	sum := sha256.Sum256(content)
	record := ""
	if logPath != "" {
		record = fmt.Sprintf("printf \"%%s\\n\" \"$*\" >> %q\n", logPath)
	}
	stub := "#!/bin/sh\n" + record +
		"out=\"\"; url=\"\"\n" +
		"while [ $# -gt 0 ]; do case \"$1\" in -o) out=\"$2\"; shift 2;; -*) shift;; " +
		"*) url=\"$1\"; shift;; esac; done\n" +
		"case \"$url\" in\n" +
		fmt.Sprintf("  *SHA256SUMS) printf \"%%s  %s\\n\" %q > \"$out\" ;;\n",
			filepath.Base(archive), hex.EncodeToString(sum[:])) +
		fmt.Sprintf("  *) cp %q \"$out\" ;;\n", archive) +
		"esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(h.stubBin, "curl"), []byte(stub), 0o755); err != nil {
		h.t.Fatalf("installing the curl stub: %v", err)
	}
}

func TestAWarmCacheWithAValidSidecarRunsWithoutTheNetwork(t *testing.T) {
	// The fast path has to work with no network at all: the sidecar is read
	// and the hash checked locally, and nothing is fetched.
	harness := newShimHarness(t)
	harness.cacheBinary(0o700, true)
	networkLog := filepath.Join(harness.root, "network-calls.log")
	stub := fmt.Sprintf("#!/bin/sh\nprintf \"FETCH: %%s\\n\" \"$*\" >> %q\nexit 1\n", networkLog)
	if err := os.WriteFile(filepath.Join(harness.stubBin, "curl"), []byte(stub), 0o755); err != nil {
		t.Fatalf("installing the curl stub: %v", err)
	}

	result := harness.run(true, "echo", "test")
	if result.exitCode != 42 {
		t.Fatalf("a warm cache did not execute the binary: exit %d\n%s", result.exitCode, result.stderr)
	}
	if !strings.Contains(result.stderr, "BINARY-EXECUTED") {
		t.Errorf("the cached binary did not run:\n%s", result.stderr)
	}
	if fetched, err := os.ReadFile(networkLog); err == nil && len(strings.TrimSpace(string(fetched))) > 0 {
		t.Errorf("a warm cache still went to the network:\n%s", fetched)
	}
}

func TestAWarmCacheWorksWithNoFetcherOnPath(t *testing.T) {
	// The same property from the other direction: not "did not fetch" but
	// "could not have fetched". A minimal PATH with no curl or wget.
	harness := newShimHarness(t)
	harness.cacheBinary(0o700, true)
	result := harness.run(false, "echo", "test")
	if result.exitCode != 42 {
		t.Fatalf("an offline warm cache did not execute the binary: exit %d\n%s",
			result.exitCode, result.stderr)
	}
	if !strings.Contains(result.stderr, "BINARY-EXECUTED") {
		t.Errorf("the cached binary did not run:\n%s", result.stderr)
	}
}

func TestATamperedCachedBinaryIsRefusedRatherThanExecuted(t *testing.T) {
	harness := newShimHarness(t)
	binary := harness.cacheBinary(0o700, true)
	// Truncate a byte: the sidecar now describes something else.
	if err := os.WriteFile(binary, []byte(stubBinaryBody[:len(stubBinaryBody)-1]), 0o700); err != nil {
		t.Fatalf("tampering: %v", err)
	}
	result := harness.run(false, "echo", "test")
	if result.exitCode == 42 {
		t.Fatal("a tampered cached binary was executed")
	}
	if !strings.Contains(result.stderr, refusalMessage) {
		t.Errorf("the refusal does not say what happened:\n%s", result.stderr)
	}
}

func TestACachedBinaryWithNoSidecarIsRefused(t *testing.T) {
	// An unverifiable binary is not the same as a verified one. Without the
	// sidecar there is nothing to check it against, so it must not run.
	harness := newShimHarness(t)
	harness.cacheBinary(0o700, false)
	result := harness.run(false, "echo", "test")
	if result.exitCode == 42 {
		t.Fatal("a cached binary with no sidecar was executed unverified")
	}
	if !strings.Contains(result.stderr, refusalMessage) {
		t.Errorf("the refusal does not say what happened:\n%s", result.stderr)
	}
}

func TestTheCacheLookupUsesTheCliVersionNotThePluginVersion(t *testing.T) {
	// The shim once looked the cache up under the plugin version, which no
	// release publishes -- every lookup missed and every run went to the
	// network for an asset that does not exist.
	//
	// Made discriminating, which the Python was not: it placed an *unverifiable*
	// stub at the CLI path and asserted a refusal, but a lookup at the wrong
	// path refuses with the same message, so both readings passed. Here the
	// stub carries a valid sidecar, so a CLI-version lookup executes it (42)
	// and a plugin-version lookup cannot.
	harness := newShimHarness(t)
	harness.cacheBinary(0o700, true)

	wrongPath := filepath.Join(harness.cacheDir,
		fmt.Sprintf("cadre-v%s-%s-%s", pluginManifestVersion, harness.goos, harness.goarch))
	if _, err := os.Stat(wrongPath); err == nil {
		t.Fatalf("the fixture also placed a binary at the plugin-version path %s, so "+
			"this test could not tell the two lookups apart", wrongPath)
	}

	result := harness.run(false, "echo", "test")
	if result.exitCode != 42 {
		t.Errorf("the shim did not execute the binary cached at the CLI-versioned path "+
			"(%s): exit %d. The lookup is using some other version -- the plugin "+
			"version is the one this has been before.\n%s",
			harness.binaryName, result.exitCode, result.stderr)
	}
}

func TestADirectoryAtTheCachePathDoesNotDefeatVerification(t *testing.T) {
	// A directory where the binary belongs must fall back, not be exec'd (126)
	// and not silently absorb the download.
	harness := newShimHarness(t)
	occupied := filepath.Join(harness.cacheDir, harness.binaryName)
	if err := os.MkdirAll(filepath.Join(occupied, "occupied"), 0o755); err != nil {
		t.Fatalf("occupying the cache path: %v", err)
	}
	curlLog := filepath.Join(harness.root, "curl-calls.log")
	harness.installCurlStub(harness.payloadArchive(), curlLog)

	result := harness.run(true, "definitely-not-a-subcommand")

	// The download must actually have run. Without this the test proves far
	// less than it appears to: any unrelated failure to resolve a binary
	// produces the same observable outcome, so everything below would pass
	// while the path that held the defect was never reached.
	fetched, err := os.ReadFile(curlLog)
	if err != nil || len(strings.TrimSpace(string(fetched))) == 0 {
		t.Fatalf("the download path was never exercised, so this test did not reach "+
			"the case it covers:\n%s", result.stderr)
	}
	if result.exitCode == 126 {
		t.Errorf("the shim tried to exec a directory:\n%s", result.stderr)
	}
	if !strings.Contains(result.stderr, refusalMessage) {
		t.Errorf("the refusal was never reached:\n%s", result.stderr)
	}
	entries, _ := os.ReadDir(occupied)
	for _, entry := range entries {
		if entry.Name() != "occupied" {
			t.Errorf("the download was moved inside the pre-existing directory: %s", entry.Name())
		}
	}
	// The refusal returns before the move, but the temporary file has been
	// created and made executable by then. Leaving it behind drops an
	// executable into the cache on every collision.
	cached, _ := os.ReadDir(harness.cacheDir)
	for _, entry := range cached {
		if strings.Contains(entry.Name(), ".tmp.") {
			t.Errorf("the refusal leaked a temporary executable into the cache: %s", entry.Name())
		}
	}
}

func TestWindowsShellsResolveTheWindowsBinary(t *testing.T) {
	// This POSIX sh shim only runs on Windows under Git Bash, MSYS2 or Cygwin,
	// so their uname strings are the only way the windows branch is ever
	// reached. Without them the zip/cadre.exe handling is dead code and the
	// published windows-amd64 asset is unreachable.
	harness := newShimHarness(t)
	curlLog := filepath.Join(harness.root, "curl-calls.log")
	harness.installCurlStub(harness.payloadArchive(), curlLog)

	for _, unameS := range []string{"MINGW64_NT-10.0-19045", "MSYS_NT-10.0-19045", "CYGWIN_NT-10.0"} {
		_ = os.Remove(curlLog)
		stub := "#!/bin/sh\ncase \"$1\" in\n" +
			"  -s) echo \"" + unameS + "\" ;;\n" +
			"  -m) echo x86_64 ;;\n" +
			"  *) echo \"" + unameS + " x86_64\" ;;\n" +
			"esac\n"
		if err := os.WriteFile(filepath.Join(harness.stubBin, "uname"), []byte(stub), 0o755); err != nil {
			t.Fatalf("installing the uname stub: %v", err)
		}
		harness.run(true, "definitely-not-a-subcommand")

		requested, err := os.ReadFile(curlLog)
		if err != nil {
			t.Errorf("%s: no download was attempted at all", unameS)
			continue
		}
		want := fmt.Sprintf("cadre-v%s-windows-amd64.zip", harness.version)
		if !strings.Contains(string(requested), want) {
			t.Errorf("%s must resolve %s; it asked for:\n%s", unameS, want, requested)
		}
	}
}

func TestCachedBinaryPermissionsGateExecutionOnWritability(t *testing.T) {
	// Group- or other-*writable* is refused; merely readable is fine.
	//
	// 0o706 is the one that matters: it isolates other-write with no
	// group-write. Without it the matrix cannot tell a correct implementation
	// from one that checks group-write and ignores other-write, since 0o770 and
	// 0o777 both carry group-write and would be refused either way.
	for _, permission := range []struct {
		mode      os.FileMode
		shouldRun bool
	}{
		{0o700, true},
		{0o750, true},
		{0o706, false},
		{0o770, false},
		{0o777, false},
	} {
		harness := newShimHarness(t)
		harness.cacheBinary(permission.mode, true)
		// No network: a refusal must fall back rather than re-download, which
		// keeps this about the permission gate alone.
		result := harness.run(false, "definitely-not-a-subcommand")
		if permission.shouldRun && result.exitCode != 42 {
			t.Errorf("mode %#o is not group/other-writable and must execute; got exit %d\n%s",
				permission.mode, result.exitCode, result.stderr)
		}
		if !permission.shouldRun && result.exitCode == 42 {
			t.Errorf("mode %#o is group/other-writable and must be refused", permission.mode)
		}
	}
}

var tarNamedMember = regexp.MustCompile(`(^|\s)cadre(\s|$)`)

func TestExtractionIsConstrainedToTheNamedMember(t *testing.T) {
	// tar must be asked for the executable, not handed the whole archive.
	// Observed through a logging wrapper around the real tar, because the
	// extraction directory is removed before the shim exits.
	realTar, err := exec.LookPath("tar")
	if err != nil {
		t.Skip("tar is not available")
	}
	harness := newShimHarness(t)
	harness.installCurlStub(harness.payloadArchive(), "")
	tarLog := filepath.Join(harness.root, "tar-args.log")
	wrapper := fmt.Sprintf("#!/bin/sh\nprintf \"%%s\\n\" \"$*\" >> %q\nexec %q \"$@\"\n", tarLog, realTar)
	if err := os.WriteFile(filepath.Join(harness.stubBin, "tar"), []byte(wrapper), 0o755); err != nil {
		t.Fatalf("installing the tar wrapper: %v", err)
	}

	result := harness.run(true, "definitely-not-a-subcommand")

	invocation, err := os.ReadFile(tarLog)
	if err != nil {
		t.Fatalf("tar was never invoked, so this test reached nothing:\n%s", result.stderr)
	}
	if !tarNamedMember.MatchString(strings.TrimSpace(string(invocation))) {
		t.Errorf("extraction was not constrained to the named member: %q", invocation)
	}
	if _, err := os.Stat(filepath.Join(harness.cacheDir, harness.binaryName)); err != nil {
		t.Errorf("the verified binary was not cached: %v", err)
	}
	if _, err := os.Stat(filepath.Join(harness.cacheDir, "UNWANTED-MEMBER")); err == nil {
		t.Error("a non-target archive member reached the cache directory")
	}
}
