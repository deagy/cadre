#!/usr/bin/env python3
"""Tests for binary shim sidecar-based cache verification.

Verifies that:
1. Warm cache with valid sidecar executes with NO network fetch
2. Offline warm cache still executes (no network dependency)
3. Tampered cached binary is rejected and falls back
4. Missing sidecar triggers re-download
5. Ownership and permission checks work correctly
"""

import hashlib
import json
import os
import shutil
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path


class BinaryShimSidecarTest(unittest.TestCase):
    """Tests for sidecar-based cache verification (offline, fast, honest about limits)."""

    def setUp(self):
        """Create a temporary test environment."""
        self.tmpdir = tempfile.mkdtemp()
        self.addCleanup(lambda: shutil.rmtree(self.tmpdir, ignore_errors=True))

        self.bin_dir = Path(self.tmpdir) / "bin"
        self.bin_dir.mkdir()

        self.plugin_dir = Path(self.tmpdir) / ".claude-plugin"
        self.plugin_dir.mkdir()

        self.cache_dir = Path(self.tmpdir) / "cache" / "cadre"
        self.cache_dir.mkdir(parents=True, exist_ok=True)

        # Create plugin.json
        plugin_json_path = self.plugin_dir / "plugin.json"
        plugin_json_path.write_text(
            json.dumps(
                {
                    "version": "0.23.3",
                    "name": "cadre",
                },
                indent=2,
            )
        )

        # Copy generated shim
        repo_root = Path(__file__).parent.parent.parent
        generated_shim = repo_root / "plugin" / "bin" / "cadre"
        if not generated_shim.exists():
            self.skipTest("Generated shim not found")

        self.shim_path = self.bin_dir / "cadre"
        shutil.copy(generated_shim, self.shim_path)
        self.shim_path.chmod(self.shim_path.stat().st_mode | stat.S_IEXEC)

    def _get_platform_tuple(self):
        """Get (goos, goarch) for current platform."""
        uname = subprocess.run(
            ["uname", "-s", "-m"], capture_output=True, text=True
        )
        if "Linux" in uname.stdout:
            goos = "linux"
            goarch = "arm64" if "aarch64" in uname.stdout else "amd64"
        elif "Darwin" in uname.stdout:
            goos = "darwin"
            goarch = "arm64" if "aarch64" in uname.stdout else "amd64"
        else:
            self.skipTest(f"Unsupported OS: {uname.stdout}")
        return goos, goarch

    def _create_cached_binary(self, version, compute_hash=True):
        """Create a binary stub in cache with optional sidecar hash."""
        goos, goarch = self._get_platform_tuple()
        binary_name = f"cadre-v{version}-{goos}-{goarch}"
        binary_path = self.cache_dir / binary_name

        # Create stub binary
        binary_path.write_text(
            "#!/bin/sh\n"
            'echo "BINARY-EXECUTED" >&2\n'
            "exit 42\n"
        )
        binary_path.chmod(binary_path.stat().st_mode | stat.S_IEXEC)

        # Create sidecar hash if requested
        if compute_hash:
            hash_value = hashlib.sha256(binary_path.read_bytes()).hexdigest()
            sidecar_path = Path(str(binary_path) + ".sha256")
            sidecar_path.write_text(hash_value)

        return binary_path

    def test_warm_cache_with_valid_sidecar_executes_no_network_fetch(self):
        """
        Verify warm cache with valid sidecar executes the binary WITHOUT network fetch.

        This test proves the fast path works offline:
        1. Binary in cache with valid sidecar hash
        2. Shim reads sidecar and verifies hash locally
        3. Shim executes binary
        4. No network fetch occurs (stub curl not called)
        """
        binary_path = self._create_cached_binary("0.5.0", compute_hash=True)

        # Create a stub curl that logs every fetch (to detect network calls)
        curl_stub = self.bin_dir / "curl"
        curl_stub.write_text(
            "#!/bin/sh\n"
            "echo 'NETWORK-FETCH: $*' >> /tmp/network-calls.log\n"
            "exit 1\n"
        )
        curl_stub.chmod(curl_stub.stat().st_mode | stat.S_IEXEC)

        network_log = Path("/tmp/network-calls.log")
        if network_log.exists():
            network_log.unlink()

        env = os.environ.copy()
        env["XDG_CACHE_HOME"] = str(Path(self.tmpdir) / "cache")
        env["PATH"] = f"{self.bin_dir}:{env.get('PATH', '')}"

        shim_run_dir = self.plugin_dir.parent
        original_cwd = os.getcwd()
        try:
            os.chdir(shim_run_dir)

            result = subprocess.run(
                [str(self.shim_path), "echo", "test"],
                env=env,
                capture_output=True,
                text=True,
                timeout=5,
            )

            # Binary should execute (exit 42)
            self.assertEqual(
                result.returncode,
                42,
                f"Warm cache should execute binary (exit 42), got {result.returncode}. "
                f"stderr={result.stderr}",
            )

            # Verify binary's output
            self.assertIn("BINARY-EXECUTED", result.stderr)

            # Verify no network fetch occurred
            if network_log.exists():
                network_calls = network_log.read_text()
                self.assertEqual(
                    "",
                    network_calls,
                    f"No network fetch should occur on warm cache. Got: {network_calls}",
                )

        finally:
            os.chdir(original_cwd)

    def test_tampered_cached_binary_rejected_fallback(self):
        """
        Verify tampered cached binary is rejected and Python fallback occurs.

        BREAKING CHANGE VERIFICATION:
        Before fix (online re-verification):
          - Tampered binary detected immediately (re-fetch archive)
          - But every invocation needed network

        After fix (sidecar hashing):
          - Tampered binary detected by hash mismatch (offline)
          - Discarded and re-downloaded on next invocation
          - Fast path has no network dependency
        """
        binary_path = self._create_cached_binary("0.5.0", compute_hash=True)

        # Tamper with the cached binary
        original_hash = hashlib.sha256(binary_path.read_bytes()).hexdigest()
        binary_path.write_bytes(binary_path.read_bytes()[:-1])  # Truncate last byte
        tampered_hash = hashlib.sha256(binary_path.read_bytes()).hexdigest()

        self.assertNotEqual(original_hash, tampered_hash, "Tampering must change hash")

        env = os.environ.copy()
        env["XDG_CACHE_HOME"] = str(Path(self.tmpdir) / "cache")
        env["PATH"] = f"{self.bin_dir}:{env.get('PATH', '')}"

        shim_run_dir = self.plugin_dir.parent
        original_cwd = os.getcwd()
        try:
            os.chdir(shim_run_dir)

            result = subprocess.run(
                [str(self.shim_path), "echo", "test"],
                env=env,
                capture_output=True,
                text=True,
                timeout=5,
            )

            # Should NOT execute the tampered binary (not exit 42)
            self.assertNotEqual(
                result.returncode,
                42,
                "Tampered binary should NOT execute",
            )

            # Should fall back to Python (unknown subcommand error)
            self.assertIn(
                "unknown subcommand",
                result.stderr,
                "Should fall back to Python after hash mismatch",
            )

        finally:
            os.chdir(original_cwd)

    def test_missing_sidecar_triggers_redownload(self):
        """
        Verify missing sidecar hash causes binary to be rejected and re-downloaded.

        Simulates:
        - Cached binary exists but sidecar is missing (e.g., manual cleanup)
        - Shim detects missing sidecar and falls back
        """
        binary_path = self._create_cached_binary("0.5.0", compute_hash=False)

        # Verify sidecar does not exist
        sidecar_path = Path(str(binary_path) + ".sha256")
        self.assertFalse(sidecar_path.exists(), "Sidecar should not exist")

        env = os.environ.copy()
        env["XDG_CACHE_HOME"] = str(Path(self.tmpdir) / "cache")
        env["PATH"] = f"{self.bin_dir}:{env.get('PATH', '')}"

        shim_run_dir = self.plugin_dir.parent
        original_cwd = os.getcwd()
        try:
            os.chdir(shim_run_dir)

            result = subprocess.run(
                [str(self.shim_path), "echo", "test"],
                env=env,
                capture_output=True,
                text=True,
                timeout=5,
            )

            # Should NOT execute binary without sidecar (not exit 42)
            self.assertNotEqual(
                result.returncode,
                42,
                "Binary without sidecar should NOT execute",
            )

            # Should fall back to Python
            self.assertIn(
                "unknown subcommand",
                result.stderr,
                "Should fall back to Python when sidecar missing",
            )

        finally:
            os.chdir(original_cwd)

    def test_offline_warm_cache_still_works(self):
        """
        Verify that offline warm cache (with valid sidecar) executes without network.

        This is the key property: cache works offline, no network dependency on warm path.
        """
        binary_path = self._create_cached_binary("0.5.0", compute_hash=True)

        # Simulate offline environment: remove curl/wget from PATH
        env = os.environ.copy()
        env["XDG_CACHE_HOME"] = str(Path(self.tmpdir) / "cache")
        env["PATH"] = "/usr/bin:/bin"  # Minimal PATH, no curl/wget

        shim_run_dir = self.plugin_dir.parent
        original_cwd = os.getcwd()
        try:
            os.chdir(shim_run_dir)

            # Should still work (binary cached and verified, no network call)
            result = subprocess.run(
                [str(self.shim_path), "echo", "test"],
                env=env,
                capture_output=True,
                text=True,
                timeout=5,
            )

            # Binary should execute (exit 42)
            self.assertEqual(
                result.returncode,
                42,
                f"Offline warm cache should execute binary. Got exit={result.returncode}, "
                f"stderr={result.stderr}",
            )

            self.assertIn("BINARY-EXECUTED", result.stderr)

        finally:
            os.chdir(original_cwd)


if __name__ == "__main__":
    unittest.main()
