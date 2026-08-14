#!/usr/bin/env python3
"""Test that CLI version and plugin version are correctly separated at generation time.

This ensures that the two independent version streams do not get confused,
catching the defect where the shim was using plugin-v<version> instead of
cli-v<version> for binary downloads.

    python3 -m unittest discover -s plugin/tools -p "test_version_separation.py"
"""

from __future__ import annotations

import re
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
PLUGIN = REPO_ROOT / "plugin"
SHIM = PLUGIN / "bin" / "cadre"


class VersionSeparationTests(unittest.TestCase):
    """Test that CLI and plugin versions are properly separated in the generated shim."""

    def setUp(self) -> None:
        self.shim_content = SHIM.read_text(encoding="utf-8")

    def test_cli_version_embedded_separately_from_plugin_version(self) -> None:
        """Test that CADRE_CLI_VERSION is defined separately from plugin_version.

        BROKEN behavior (before fix):
          Uses plugin_version (9.9.9) for both --version AND downloads
          RELEASE_URL="https://github.com/deagy/cadre/releases/download/plugin-v9.9.9/..."

        FIXED behavior:
          plugin_version=9.9.9 (read at runtime from .claude-plugin/plugin.json)
          CADRE_CLI_VERSION=0.5.0 (embedded at generation time from cadre_cli/_version.py)
          RELEASE_URL="https://github.com/deagy/cadre/releases/download/cli-v0.5.0/..."
        """
        # Should have both variables defined
        self.assertIn("plugin_version=", self.shim_content,
                      "Must read plugin_version at runtime from .claude-plugin/plugin.json")
        self.assertIn("CADRE_CLI_VERSION=", self.shim_content,
                      "Must embed CADRE_CLI_VERSION at generation time")

        # Should use cli-v tag for downloads, not plugin-v
        self.assertIn('cli-v$VERSION', self.shim_content,
                      "Must use cli-v tag for release downloads")
        self.assertNotIn('plugin-v$VERSION', self.shim_content,
                         "Must NOT use plugin-v tag for CLI binary downloads")

    def test_cli_version_value_from_cadre_cli_version_py(self) -> None:
        """Test that CADRE_CLI_VERSION contains the value from cadre_cli/_version.py.

        This verifies the version is read at generation time, not at runtime.
        """
        # Extract the CADRE_CLI_VERSION value
        match = re.search(r'CADRE_CLI_VERSION="([^"]+)"', self.shim_content)
        self.assertIsNotNone(match, "CADRE_CLI_VERSION must be assigned a quoted value")

        cli_version = match.group(1)
        # The value should match x.y.z pattern from cadre_cli/_version.py
        self.assertRegex(cli_version, r'^\d+\.\d+\.\d+$',
                         f"CADRE_CLI_VERSION value '{cli_version}' must match x.y.z version format")

    def test_comment_explains_generation_time_pin(self) -> None:
        """Test that a comment explains the version is pinned at generation time.

        This helps the next person maintain the shim without accidentally 'fixing' it
        to a runtime lookup.
        """
        # Should have a comment explaining the pin
        self.assertIn("generation time", self.shim_content,
                      "Must comment that CADRE_CLI_VERSION is pinned at generation time")
        self.assertIn("Regenerating", self.shim_content,
                      "Must explain that regenerating updates the version")

    def test_plugin_version_stays_runtime_for_version_flag(self) -> None:
        """Test that plugin_version continues to be read at runtime for --version.

        The --version fast path must remain Python-free and network-free.
        """
        # Find --version handling
        version_start = self.shim_content.find('if [ "$command_name" = "--version"')
        version_end = self.shim_content.find("fi", version_start) + 2
        version_section = self.shim_content[version_start:version_end]

        # Should use sed to read plugin_version at runtime from .claude-plugin/plugin.json
        self.assertIn("sed -n", version_section,
                      "--version must use sed to read plugin.json at runtime")
        self.assertIn(".claude-plugin/plugin.json", version_section,
                      "--version must read from .claude-plugin/plugin.json")


if __name__ == "__main__":
    unittest.main()
