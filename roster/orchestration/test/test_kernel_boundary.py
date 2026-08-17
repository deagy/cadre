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

    # Two assertions stood here and are gone with roster/shared/src/settings.py,
    # the only sanctioned Python module either of them was about.
    #
    #   - `agentic_sdlc.bin_path` is global-scope-only, so a project-local
    #     .agents/cadre.yaml cannot redirect which binary runs. Now asserted by
    #     internal/config/trust_scope_test.go, which pins the trust tier of
    #     every field and refuses each one from a project file behaviourally.
    #   - Exactly one Python module may resolve a kernel executable. Zero do
    #     now: no file under roster/ calls which("agentic-sdlc"), and the
    #     subprocess side is internal/kernel's, guarded by
    #     internal/kernel/kernel_boundary_test.go.
    #
    # Re-adding either here means re-adding Python that resolves a kernel path.

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
