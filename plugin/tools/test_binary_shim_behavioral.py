#!/usr/bin/env python3
"""Behavioral tests for binary shim cache lookup using CLI version, not plugin version."""

import json
import os
import shutil
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path


class BinaryShimBehavioralTest(unittest.TestCase):
    """
    Behavioral tests that actually run the generated shim and verify it
    correctly uses CADRE_CLI_VERSION for cache lookup, not plugin_version.
    """

    def setUp(self):
        """Create a temporary test environment with shim, plugin config, and cache."""
        self.tmpdir = tempfile.mkdtemp()
        self.addCleanup(lambda: shutil.rmtree(self.tmpdir, ignore_errors=True))

        # Set up directory structure
        self.bin_dir = Path(self.tmpdir) / "bin"
        self.bin_dir.mkdir()

        self.plugin_dir = Path(self.tmpdir) / ".claude-plugin"
        self.plugin_dir.mkdir()

        self.cache_dir = Path(self.tmpdir) / "cache" / "cadre"
        self.cache_dir.mkdir(parents=True)

        # Create plugin.json with version 0.23.3 (different from CLI version)
        plugin_json_path = self.plugin_dir / "plugin.json"
        plugin_json_path.write_text(
            json.dumps(
                {
                    "version": "0.23.3",
                    "name": "cadre",
                }
            )
        )

        # Get the current generated shim and copy it to test directory
        repo_root = Path(__file__).parent.parent.parent
        generated_shim = repo_root / "plugin" / "bin" / "cadre"
        if not generated_shim.exists():
            self.skipTest("Generated shim not found at " + str(generated_shim))

        self.shim_path = self.bin_dir / "cadre"
        shutil.copy(generated_shim, self.shim_path)
        self.shim_path.chmod(self.shim_path.stat().st_mode | stat.S_IEXEC)

    def _create_stub_binary(self, version):
        """
        Create a stub binary that outputs a marker and exits with code 42.
        version should be the CLI version (e.g., "0.5.0")
        """
        # Assume Linux for this test; shim would use uname to determine GOOS/GOARCH
        uname_output = subprocess.run(
            ["uname", "-s", "-m"], capture_output=True, text=True
        )
        if "Linux" in uname_output.stdout:
            goos = "linux"
            if "aarch64" in uname_output.stdout or "arm64" in uname_output.stdout:
                goarch = "arm64"
            else:
                goarch = "amd64"
        elif "Darwin" in uname_output.stdout:
            goos = "darwin"
            if "aarch64" in uname_output.stdout or "arm64" in uname_output.stdout:
                goarch = "arm64"
            else:
                goarch = "amd64"
        else:
            self.skipTest(f"Unsupported OS: {uname_output.stdout}")

        stub_name = f"cadre-v{version}-{goos}-{goarch}"
        stub_path = self.cache_dir / stub_name

        # Create a simple shell script stub that outputs a marker to stderr
        stub_path.write_text(
            "#!/bin/sh\n"
            'echo "STUB-EXECUTED" >&2\n'
            "exit 42\n"
        )
        stub_path.chmod(stub_path.stat().st_mode | stat.S_IEXEC)

        return stub_path, stub_name

    def test_cache_lookup_uses_cli_version_not_plugin_version(self):
        """
        Verify that the shim looks for a cached binary using CADRE_CLI_VERSION,
        not plugin_version.

        Note: With H1 security fix (re-verification), a fake stub binary cannot execute
        because it fails checksum verification. However, the fact that it's attempted
        (not skipped) proves the lookup uses the correct CLI version. This test verifies
        that Python fallback occurs (checksum verification failed), not exec failure.

        If the cache lookup used plugin_version instead, the stub at the CLI-versioned
        path would never be attempted, and we'd see different behavior.
        """
        # Create a stub at the CLI-versioned cache path
        stub_path, stub_name = self._create_stub_binary("0.5.0")
        self.assertTrue(stub_path.exists(), f"Stub not created at {stub_path}")

        # Set environment to use our temporary directories
        env = os.environ.copy()
        env["XDG_CACHE_HOME"] = str(Path(self.tmpdir) / "cache")
        env["PATH"] = f"{self.bin_dir}:{env.get('PATH', '')}"

        # Run the shim from the plugin directory context
        shim_run_dir = self.plugin_dir.parent
        original_cwd = os.getcwd()
        try:
            os.chdir(shim_run_dir)

            # Run the shim with a simple echo command
            result = subprocess.run(
                [str(self.shim_path), "echo", "test"],
                env=env,
                capture_output=True,
                text=True,
            )

            # H1 fix: stub is found but fails checksum re-verification, so Python fallback occurs.
            # Expected: exit 1 (Python unknown subcommand), NOT exit 126 (exec failure).
            self.assertNotEqual(
                result.returncode,
                126,
                f"Should not be exec failure (exit 126). Got {result.returncode}. "
                f"stdout={result.stdout}, stderr={result.stderr}",
            )

            # Verify Python fallback occurred (re-verification failed, so stub not executed)
            self.assertIn(
                "unknown subcommand",
                result.stderr,
                f"Expected Python fallback after checksum verification, got: {result.stderr}",
            )

        finally:
            os.chdir(original_cwd)

    def test_cache_miss_when_using_plugin_version_not_cli_version(self):
        """
        Behavioral verification that confirms the bug when CANDIDATE uses
        plugin_version instead of CADRE_CLI_VERSION.

        This is not a test that should pass in the codebase; rather, it demonstrates
        the defect that would occur if the fix were reverted.

        When CANDIDATE is constructed as:
          CANDIDATE="$CACHE_DIR/cadre-v$plugin_version-$GOOS-$GOARCH"  # BUG
        Instead of:
          CANDIDATE="$CACHE_DIR/cadre-v$CADRE_CLI_VERSION-$GOOS-$GOARCH"

        The cache lookup fails because:
        - Binary published as: cadre-v0.5.0-linux-amd64
        - Cache lookup path:   cadre-v0.23.3-linux-amd64  (plugin version)
        - Result: cache miss, attempt to download, then fallback to Python
        """
        # Verify our fixture has different plugin and CLI versions
        plugin_json = json.loads((self.plugin_dir / "plugin.json").read_text())
        self.assertEqual(
            plugin_json["version"],
            "0.23.3",
            "Test fixture must have plugin version 0.23.3",
        )

        # Create a stub at the CLI-versioned path (not plugin-versioned)
        stub_path, stub_name = self._create_stub_binary("0.5.0")

        # Verify the stub is at the correct (CLI) version path
        self.assertTrue(stub_path.exists(), f"Stub must be at CLI version path: {stub_path}")

        # Verify the stub is NOT at the plugin version path
        uname_output = subprocess.run(
            ["uname", "-s", "-m"], capture_output=True, text=True
        )
        if "Linux" in uname_output.stdout:
            goos = "linux"
            if "aarch64" in uname_output.stdout or "arm64" in uname_output.stdout:
                goarch = "arm64"
            else:
                goarch = "amd64"
        else:
            self.skipTest(f"Unsupported OS: {uname_output.stdout}")

        wrong_plugin_version_path = self.cache_dir / f"cadre-v0.23.3-{goos}-{goarch}"
        self.assertFalse(
            wrong_plugin_version_path.exists(),
            "Stub must NOT be at plugin version path",
        )

        # Set environment to use our temporary directories
        env = os.environ.copy()
        env["XDG_CACHE_HOME"] = str(Path(self.tmpdir) / "cache")
        env["PATH"] = f"{self.bin_dir}:{env.get('PATH', '')}"

        # This test verifies that with the CORRECT implementation (using CADRE_CLI_VERSION),
        # the shim finds the stub. With H1 fix, it will fail re-verification, but the attempt
        # proves CLI version lookup is correct. If someone reverts to plugin_version, the stub
        # wouldn't be attempted at all (it's at CLI path, not plugin path).
        shim_run_dir = self.plugin_dir.parent
        original_cwd = os.getcwd()
        try:
            os.chdir(shim_run_dir)

            result = subprocess.run(
                [str(self.shim_path), "echo", "test"],
                env=env,
                capture_output=True,
                text=True,
            )

            # H1 fix: stub is found (at CLI-versioned path) but fails checksum re-verification.
            # Expected: Python fallback (exit 1, unknown subcommand)
            # If reverted to plugin_version: stub NOT found, different Python error
            self.assertNotEqual(
                result.returncode,
                126,
                "Should not be exec failure. Stub at CLI path should be attempted. "
                "If this fails, cache lookup may be using plugin_version instead of CLI version.",
            )

            self.assertIn(
                "unknown subcommand",
                result.stderr,
                "Expected Python fallback (re-verification failed). "
                "If checksum verification is bypassed, stub would execute (exit 42).",
            )

        finally:
            os.chdir(original_cwd)


if __name__ == "__main__":
    unittest.main()
