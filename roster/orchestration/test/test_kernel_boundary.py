#!/usr/bin/env python3
"""The kernel-ownership boundary, enforced structurally.

`kernel/` owns the G1-G10 gate schemas, run-record validation, and
gate-authority semantics. `roster/` owns the role catalog, routing, policy,
and the knowledge store. That split is permanent and load-bearing: it is
what stops role-selection code from becoming authoritative about whether
another project's gate may be approved.

Until the monorepo merge, the boundary was enforced *by construction* --
the two lived in different repositories, released on different cadences,
and could only reach each other across a versioned provider manifest. One
repository cannot import another's internals.

Merging removed that guarantee. Nothing about a single tree stops
`roster/orchestration/src/` from doing `from agentic_sdlc import
validate_repository` and quietly taking over gate evaluation, and the
resulting change would look small and reasonable in review. This test is
the replacement guarantee, and it exists precisely because the structural
one is gone.

The permitted couplings, and only these:

  1. Shell out to the kernel CLI. `roster/orchestration/src/
     agentic_sdlc_contracts.py` is the single implementation -- it resolves
     an executable and runs `agentic-sdlc show-contract`, parsing JSON.
  2. Read `kernel/contracts/*.json` as data.

Both keep the kernel authoritative: roster asks, the kernel answers.
Importing kernel code would let roster *re-implement* the answer, which is
the failure mode this guards.

    python3 -m unittest discover -s roster/orchestration/test -p "test_*.py"
"""

from __future__ import annotations

import ast
import unittest
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
ROSTER_ROOT = REPOSITORY_ROOT / "roster"
KERNEL_ROOT = REPOSITORY_ROOT / "kernel"

# The kernel's own importable package, plus the engine's. Note the near-miss:
# roster/orchestration/src/agentic_sdlc_contracts.py is *roster's* module --
# the sanctioned CLI wrapper -- and its name differs from the kernel package
# `agentic_sdlc` by a suffix. Match on the exact top-level module name so the
# wrapper is never mistaken for the thing it wraps.
FORBIDDEN_TOP_LEVEL_MODULES = frozenset({"agentic_sdlc", "agentic_sdlc_langgraph"})


def _roster_python_files() -> list[Path]:
    return [
        path
        for path in sorted(ROSTER_ROOT.rglob("*.py"))
        if "__pycache__" not in path.parts
    ]


def _imported_top_level_modules(tree: ast.AST) -> set[str]:
    found: set[str] = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                found.add(alias.name.split(".")[0])
        elif isinstance(node, ast.ImportFrom):
            # Relative imports (level > 0) can never reach the kernel.
            if node.level == 0 and node.module:
                found.add(node.module.split(".")[0])
    return found


class TestRosterNeverImportsTheKernel(unittest.TestCase):
    def test_no_roster_module_imports_kernel_code(self) -> None:
        offenders: list[str] = []
        for path in _roster_python_files():
            try:
                tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
            except SyntaxError:  # pragma: no cover - a syntax error is another test's problem
                continue
            for module in sorted(_imported_top_level_modules(tree) & FORBIDDEN_TOP_LEVEL_MODULES):
                offenders.append(f"{path.relative_to(REPOSITORY_ROOT)}: imports {module}")

        self.assertEqual(
            offenders,
            [],
            "roster/ must not import kernel or engine code. The kernel owns gate "
            "schemas, run-record validation, and gate-authority semantics; roster "
            "may only ask it questions, never re-implement its answers. Use "
            "roster/orchestration/src/agentic_sdlc_contracts.py, which shells out "
            "to the kernel CLI, or read kernel/contracts/*.json as data.\n"
            + "\n".join(offenders),
        )

    def test_kernel_executable_resolution_stays_in_one_sanctioned_place(self) -> None:
        """Exactly one Python module may resolve a kernel executable.

        `settings.py` owns the `agentic_sdlc.bin_path` field itself (env var
        > config > computed default). Spreading resolution beyond it makes
        the boundary unauditable and easy to widen by accident.

        This named two modules until `agentic_sdlc_contracts.py` -- the
        single caller that turned that path into a subprocess -- was deleted
        with the rest of the dead orchestration Python. The subprocess side
        is Go now, and internal/kernel/kernel_boundary_test.go guards it
        there. Narrowing the set was a decision rather than a quiet edit
        because this assertion refused to pass without one.
        """
        sanctioned = {
            ROSTER_ROOT / "shared" / "src" / "settings.py",
        }
        for path in sanctioned:
            self.assertTrue(path.is_file(), str(path))

        offenders: list[str] = []
        for path in _roster_python_files():
            if path in sanctioned or "test" in path.relative_to(ROSTER_ROOT).parts:
                continue
            text = path.read_text(encoding="utf-8")
            if 'which("agentic-sdlc")' in text or "which('agentic-sdlc')" in text:
                offenders.append(str(path.relative_to(REPOSITORY_ROOT)))

        self.assertEqual(
            offenders,
            [],
            "Only settings.py (the field) and agentic_sdlc_contracts.py (the caller) "
            "may resolve the kernel executable; everything else must go through them.\n"
            + "\n".join(offenders),
        )

    def test_kernel_bin_path_cannot_be_set_by_a_project_local_file(self) -> None:
        """`agentic_sdlc.bin_path` selects an executable to run.

        A project-local `.agents/cadre.yaml` is untrusted input -- checking
        out a repository must never be able to redirect which binary runs.
        The field is therefore global-scope-only, and that is a security
        property of the boundary, not a preference.
        """
        text = (ROSTER_ROOT / "shared" / "src" / "settings.py").read_text(encoding="utf-8")
        field_start = text.index('"agentic_sdlc.bin_path": FieldSpec(')
        field_block = text[field_start : text.index("),", field_start)]
        self.assertIn("SCOPE_GLOBAL_ONLY", field_block)


class TestKernelStaysIndependentlyReleasable(unittest.TestCase):
    def test_kernel_carries_its_own_version(self) -> None:
        """Merging the repositories must not merge the version lines.

        The kernel is no longer a separately *publishable* distribution --
        the Python package was deleted once the Go port replaced it -- but it
        is still separately *versioned*, and that is the half that carries the
        contract: provider.json's `kernel_compatibility` window is only
        meaningful if the kernel can move independently of the role catalog.
        """
        source = (REPOSITORY_ROOT / "internal" / "kernel" / "provider.go").read_text(
            encoding="utf-8"
        )
        self.assertRegex(source, r'(?m)^const Version = "\d+\.\d+\.\d+"')

    def test_kernel_owns_the_lifecycle_contracts(self) -> None:
        for contract in ("lifecycle-gates.json", "mutation-gates.json", "run-record.schema.json"):
            with self.subTest(contract=contract):
                self.assertTrue((KERNEL_ROOT / "contracts" / contract).is_file())
        # ...and roster ships no copy of them to drift against.
        strays = [
            str(path.relative_to(REPOSITORY_ROOT))
            for path in ROSTER_ROOT.rglob("lifecycle-gates.json")
        ]
        self.assertEqual(strays, [], "roster/ must not carry its own copy of a kernel contract")


if __name__ == "__main__":
    unittest.main()
