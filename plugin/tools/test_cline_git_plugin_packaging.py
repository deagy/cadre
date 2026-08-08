#!/usr/bin/env python3
"""Guard the dependency closure Cline needs for a Git-source install.

Cline discovers the three TypeScript entrypoints from the repository root
when a user runs ``cline plugin install https://github.com/deagy/cadre``.
Unlike this repository's development workspace, that installation resolves
bare runtime imports from a root ``node_modules``.  Keep the manifest and
lockfile that create it small, explicit, and independent from the Claude
Code/Codex marketplace package under ``plugin/``.
"""

from __future__ import annotations

import json
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
ENTRYPOINTS = (
    "./cline-plugins/cline-agents/index.ts",
    "./cline-plugins/cline-lifecycle/index.ts",
    "./cline-plugins/cline/index.ts",
)
ROOT_RUNTIME_DEPENDENCIES = {"yaml": "2.9.0", "zod": "4.4.3"}


class TestClineGitPluginPackaging(unittest.TestCase):
    def test_root_manifest_explicitly_declares_all_discovered_entrypoints(self) -> None:
        manifest = json.loads((REPO_ROOT / "package.json").read_text(encoding="utf-8"))
        self.assertTrue(manifest["private"])
        self.assertEqual(manifest["cline"]["plugins"], [{"paths": list(ENTRYPOINTS)}])
        self.assertEqual(manifest["dependencies"], ROOT_RUNTIME_DEPENDENCIES)
        self.assertNotIn("workspaces", manifest)
        self.assertNotIn("devDependencies", manifest)
        for entrypoint in ENTRYPOINTS:
            self.assertTrue((REPO_ROOT / entrypoint.removeprefix("./")).is_file())

    def test_root_lockfile_pins_the_runtime_dependency_closure(self) -> None:
        lockfile = json.loads((REPO_ROOT / "package-lock.json").read_text(encoding="utf-8"))
        self.assertEqual(lockfile["lockfileVersion"], 3)
        self.assertEqual(lockfile["packages"][""]["dependencies"], ROOT_RUNTIME_DEPENDENCIES)
        for dependency, version in ROOT_RUNTIME_DEPENDENCIES.items():
            self.assertEqual(lockfile["packages"][f"node_modules/{dependency}"]["version"], version)

    def test_claude_and_codex_marketplace_package_remains_npm_free(self) -> None:
        manifests = [
            path.relative_to(REPO_ROOT)
            for path in (REPO_ROOT / "plugin").rglob("package.json")
            if "node_modules" not in path.parts
        ]
        self.assertEqual(manifests, [])


if __name__ == "__main__":
    unittest.main()
