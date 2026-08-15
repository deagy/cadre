#!/usr/bin/env python3
"""Behavioural tests for the binary shim's cache-hardening controls.

These cover three properties that a supply-chain review found were either
broken or unprotected, and that no committed test exercised:

1. A pre-existing *directory* at the cache path must not defeat the Python
   fallback. `[ -f ]` alone was not enough: when the warm-cache gate rejected
   the directory, control fell through to the download path, whose final
   `mv "$tmp" "$BINARY_CACHE"` moved the file *into* that directory and
   returned success. `BINARY_CACHE` still pointed at a directory, and
   `exec`ing it exited 126 without ever reaching Python.

2. A cached binary that any other user could rewrite must be refused. The
   check has to key on *writability*, not on the mere presence of group or
   other bits, or it rejects a perfectly safe 0750.

3. Extraction must be constrained to the member the shim actually wants. An
   earlier guard inspected `"$TEMP_DIR/$EXECUTABLE"` for `..` -- a path built
   from a hardcoded literal, so it could never match whatever an archive
   contained, while `tar`/`unzip` still extracted every member unconstrained.

All three assert on observed behaviour of the generated shim rather than on
strings in its text. A structural predecessor of this file asserted that the
shim contained the literal glob `w???w*|*w??w*`; it broke the moment that
positional match was correctly replaced with field extraction, having never
tested the behaviour it was named for.
"""

import re
import hashlib
import json
import os
import shutil
import stat
import subprocess
import tarfile
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
GENERATED_SHIM = REPO_ROOT / "plugin" / "bin" / "cadre"
# A plain text file since Phase 2 of PYTHON_ELIMINATION_PLAN.md.
CLI_VERSION_SOURCE = REPO_ROOT / "VERSION"


def _cli_version() -> str:
    """The CLI version the shim is pinned to at generation time.

    Accepts both shapes: the plain `x.y.z` the VERSION file now holds, and the
    `VERSION = "x.y.z"` assignment cadre_cli/_version.py used to, so this
    keeps working against an older generated shim.
    """
    for line in CLI_VERSION_SOURCE.read_text(encoding="utf-8").splitlines():
        stripped = line.strip()
        if stripped.startswith("VERSION = "):
            return stripped.split('"')[1]
        if re.fullmatch(r"\d+\.\d+\.\d+", stripped):
            return stripped
    raise AssertionError(f"no version found in {CLI_VERSION_SOURCE}")


@unittest.skipUnless(GENERATED_SHIM.exists(), "generated shim not present")
class BinaryShimHardeningTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmpdir = Path(tempfile.mkdtemp(prefix="cadre-shim-hardening-"))
        self.addCleanup(lambda: shutil.rmtree(self.tmpdir, ignore_errors=True))

        self.plugin_root = self.tmpdir / "plug"
        self.bin_dir = self.plugin_root / "bin"
        self.bin_dir.mkdir(parents=True)
        manifest_dir = self.plugin_root / ".claude-plugin"
        manifest_dir.mkdir()
        # indent=2 matters: the shim's `--version` sed requires "version" at
        # the start of a line. A single-line json.dumps yields an empty
        # version and quietly changes which code paths a test exercises.
        (manifest_dir / "plugin.json").write_text(
            json.dumps({"version": "0.23.3", "name": "cadre"}, indent=2),
            encoding="utf-8",
        )

        self.shim = self.bin_dir / "cadre"
        shutil.copy(GENERATED_SHIM, self.shim)
        self.shim.chmod(self.shim.stat().st_mode | stat.S_IEXEC)

        self.cache_home = self.tmpdir / "cache"
        self.cache_dir = self.cache_home / "cadre"
        self.cache_dir.mkdir(parents=True)

        self.stub_bin = self.tmpdir / "stubbin"
        self.stub_bin.mkdir()

        self.version = _cli_version()
        self.goos, self.goarch = self._platform()
        self.binary_name = f"cadre-v{self.version}-{self.goos}-{self.goarch}"

    def _platform(self):
        uname = subprocess.run(["uname", "-s", "-m"], capture_output=True, text=True).stdout
        if "Linux" in uname:
            goos = "linux"
        elif "Darwin" in uname:
            goos = "darwin"
        else:
            self.skipTest(f"unsupported OS for this shim: {uname.strip()}")
        goarch = "arm64" if ("aarch64" in uname or "arm64" in uname) else "amd64"
        return goos, goarch

    def _payload_archive(self) -> Path:
        """A release-shaped archive holding an identifiable `cadre` stub."""
        staging = self.tmpdir / "payload"
        staging.mkdir(exist_ok=True)
        binary = staging / "cadre"
        binary.write_text('#!/bin/sh\necho "BINARY-EXECUTED" >&2\nexit 42\n', encoding="utf-8")
        binary.chmod(0o755)
        # A second member that must never be extracted once extraction is
        # constrained to the named executable.
        bystander = staging / "UNWANTED-MEMBER"
        bystander.write_text("should not be extracted\n", encoding="utf-8")
        archive = self.tmpdir / f"{self.binary_name}.tar.gz"
        with tarfile.open(archive, "w:gz") as tar:
            tar.add(binary, arcname="cadre")
            tar.add(bystander, arcname="UNWANTED-MEMBER")
        return archive

    def _install_curl_stub(
        self, archive: Path, *, checksum: str | None = None, log: Path | None = None
    ) -> None:
        """A `curl` that serves the archive and a SHA256SUMS naming it."""
        digest = checksum or hashlib.sha256(archive.read_bytes()).hexdigest()
        stub = self.stub_bin / "curl"
        record = f'printf "%s\\n" "$*" >> "{log}"\n' if log else ""
        stub.write_text(
            "#!/bin/sh\n"
            + record
            + 'out=""; url=""\n'
            'while [ $# -gt 0 ]; do case "$1" in -o) out="$2"; shift 2;; -*) shift;; '
            '*) url="$1"; shift;; esac; done\n'
            'case "$url" in\n'
            f'  *SHA256SUMS) printf "%s  {archive.name}\\n" "{digest}" > "$out" ;;\n'
            f'  *) cp "{archive}" "$out" ;;\n'
            "esac\n"
            "exit 0\n",
            encoding="utf-8",
        )
        stub.chmod(0o755)

    def _run(self, *args: str, extra_path: bool = True):
        env = os.environ.copy()
        env["XDG_CACHE_HOME"] = str(self.cache_home)
        env.pop("CADRE_REPO_ROOT", None)
        if extra_path:
            env["PATH"] = f"{self.stub_bin}:{env.get('PATH', '')}"
        return subprocess.run(
            [str(self.shim), *args],
            env=env,
            capture_output=True,
            text=True,
            timeout=60,
            cwd=self.tmpdir,
        )

    def _cache_binary(self, mode: int = 0o700):
        """A valid cached binary plus its sidecar, at the given mode."""
        binary = self.cache_dir / self.binary_name
        binary.write_text('#!/bin/sh\necho "BINARY-EXECUTED" >&2\nexit 42\n', encoding="utf-8")
        binary.chmod(mode)
        sidecar = Path(f"{binary}.sha256")
        sidecar.write_text(hashlib.sha256(binary.read_bytes()).hexdigest(), encoding="utf-8")
        return binary

    # -- 1. directory at the cache path ----------------------------------

    def test_directory_at_cache_path_does_not_defeat_the_python_fallback(self) -> None:
        """A directory at the cache path must fall back, not exit 126.

        The download must succeed for this to be meaningful: an offline run
        stops at the warm-cache gate and never reaches the `mv` where the
        original defect lived. That is precisely why an earlier version of
        this scenario passed while the defect was live.
        """
        occupied = self.cache_dir / self.binary_name
        (occupied / "occupied").mkdir(parents=True)
        archive = self._payload_archive()
        curl_log = self.tmpdir / "curl-calls.log"
        self._install_curl_stub(archive, log=curl_log)

        result = self._run("definitely-not-a-subcommand")

        # Assert the download actually ran. Without this the test proves far
        # less than it appears to: any unrelated failure to resolve a binary
        # (a curl stub not on PATH, a platform mismatch) produces exactly the
        # same observable outcome, so the assertions below would pass while
        # the mv-into-directory path was never reached at all.
        self.assertTrue(
            curl_log.exists() and curl_log.read_text(encoding="utf-8").strip(),
            "the download path was never exercised, so this test did not reach "
            f"the defect it covers; stderr={result.stderr}",
        )

        self.assertNotEqual(
            126, result.returncode,
            "exec'ing a directory bypassed the Python fallback (exit 126); "
            f"stderr={result.stderr}",
        )
        self.assertIn(
            "unknown subcommand", result.stderr,
            f"expected the Python dispatch to be reached; stderr={result.stderr}",
        )
        moved_in = [p.name for p in occupied.iterdir() if p.name != "occupied"]
        self.assertEqual(
            [], moved_in,
            f"mv absorbed the download into the pre-existing directory: {moved_in}",
        )

        # The refusal path returns before the mv, but BINARY_TEMP has already
        # been created and chmod +x'd by then. Leaving it behind drops an
        # executable .tmp.$$ into the cache on every collision.
        strays = [p.name for p in self.cache_dir.iterdir() if ".tmp." in p.name]
        self.assertEqual(
            [], strays,
            f"the refusal path leaked a temporary executable into the cache: {strays}",
        )

    def test_windows_shells_resolve_the_windows_binary(self) -> None:
        """Git Bash / MSYS2 / Cygwin must reach the published windows-amd64 asset.

        This POSIX sh shim only runs on Windows under those environments, so
        their `uname -s` strings are the only way the windows branch is ever
        reachable. Without them the zip/cadre.exe code below was dead, and the
        windows-amd64 binary the release pipeline publishes could not be used.
        """
        archive = self._payload_archive()
        curl_log = self.tmpdir / "curl-calls.log"
        self._install_curl_stub(archive, log=curl_log)

        for uname_s in ("MINGW64_NT-10.0-19045", "MSYS_NT-10.0-19045", "CYGWIN_NT-10.0"):
            with self.subTest(uname=uname_s):
                curl_log.unlink(missing_ok=True)
                stub = self.stub_bin / "uname"
                stub.write_text(
                    "#!/bin/sh\n"
                    'case "$1" in\n'
                    f'  -s) echo "{uname_s}" ;;\n'
                    "  -m) echo x86_64 ;;\n"
                    f'  *) echo "{uname_s} x86_64" ;;\n'
                    "esac\n",
                    encoding="utf-8",
                )
                stub.chmod(0o755)

                self._run("definitely-not-a-subcommand")

                self.assertTrue(curl_log.exists(), f"no download attempted under {uname_s}")
                requested = curl_log.read_text(encoding="utf-8")
                self.assertIn(
                    f"cadre-v{self.version}-windows-amd64.zip", requested,
                    f"{uname_s} must resolve the windows-amd64 zip asset; got {requested!r}",
                )

    # -- 2. permission matrix --------------------------------------------

    def test_cached_binary_permissions_gate_execution_on_writability(self) -> None:
        """Group/other *writable* is refused; merely readable is allowed."""
        # 0o706 isolates *other*-write with no group-write. Without it the
        # matrix cannot distinguish a correct implementation from one that
        # checks GROUP_WRITE and ignores OTHER_WRITE: 0o770 and 0o777 both
        # carry group-write, so either would still be refused. This is the
        # shape of the bug the positional-glob check originally missed.
        matrix = (
            (0o700, True),
            (0o750, True),
            (0o706, False),
            (0o770, False),
            (0o777, False),
        )
        for mode, should_execute in matrix:
            with self.subTest(mode=oct(mode)):
                for stale in self.cache_dir.iterdir():
                    shutil.rmtree(stale, ignore_errors=True) if stale.is_dir() else stale.unlink()
                self._cache_binary(mode=mode)
                # No network: a refusal must fall back rather than re-download,
                # which keeps this test about the permission gate alone.
                result = self._run("definitely-not-a-subcommand", extra_path=False)
                if should_execute:
                    self.assertEqual(
                        42, result.returncode,
                        f"mode {oct(mode)} is not group/other-writable and must execute; "
                        f"rc={result.returncode} stderr={result.stderr}",
                    )
                else:
                    self.assertNotEqual(
                        42, result.returncode,
                        f"mode {oct(mode)} is group/other-writable and must be refused",
                    )

    # -- 3. constrained extraction ---------------------------------------

    def test_extraction_is_constrained_to_the_named_member(self) -> None:
        """`tar` must be asked for the executable, not the whole archive.

        Observed through a logging wrapper around the real `tar`, because the
        temporary extraction directory is removed before the shim exits and
        cannot be inspected afterwards.
        """
        archive = self._payload_archive()
        self._install_curl_stub(archive)
        tar_log = self.tmpdir / "tar-args.log"
        real_tar = shutil.which("tar")
        if real_tar is None:
            self.skipTest("tar not available")
        wrapper = self.stub_bin / "tar"
        wrapper.write_text(
            f'#!/bin/sh\nprintf "%s\\n" "$*" >> "{tar_log}"\nexec "{real_tar}" "$@"\n',
            encoding="utf-8",
        )
        wrapper.chmod(0o755)

        result = self._run("definitely-not-a-subcommand")

        self.assertTrue(tar_log.exists(), f"tar was never invoked; stderr={result.stderr}")
        invocation = tar_log.read_text(encoding="utf-8").strip()
        self.assertRegex(
            invocation, r"(^|\s)cadre(\s|$)",
            f"extraction was not constrained to the named member: {invocation!r}",
        )
        cached = self.cache_dir / self.binary_name
        self.assertTrue(cached.exists(), "the verified binary should have been cached")
        self.assertFalse(
            (self.cache_dir / "UNWANTED-MEMBER").exists(),
            "a non-target archive member reached the cache directory",
        )


if __name__ == "__main__":
    unittest.main()
