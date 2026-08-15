"""PP-FR-6: platform code must not name a specific roster's contents.

The mirror of `test_kernel_boundary.py`. That one guards roster → kernel; until
PP-FR-1 nothing could violate the other direction, because there was only ever
one roster.

Modelled on `test_context_boundary.py:157-229`, which already enforces a
two-way don't-name-each-other rule between the knowledge and context stores,
including its string-literal check. Two things are carried over deliberately:
its **self-vacuity guard** (`:150-155`), so a rename cannot make every check
pass over an empty set; and its non-docstring literal scan.

**One thing is done differently, and it is the reason this file is longer than
that one.** That boundary needs no exemptions: neither store has any legitimate
use of the other's name. This one does — a diagnostic that tells a user which
file to edit is not a resolution path. `requirements.md` PP-FR-6 demands the
exemption be expressed as a *rule* rather than a file allowlist, "which is how a
guard stops meaning anything", so the categories below are decided by the
enclosing call target, not by which file a literal lives in.

That precision was earned expensively. PP-FR-6's own example sets were
hand-classified in prose and were wrong in *both* directions at once:
`routing.py`'s five cited lines were listed as forbidden and are all
`raise ValueError(...)` diagnostics, while `config.py`'s three were offered as
the evidence for the exemption and do not demonstrate it. The example sets here
are derived from the rule rather than restated from that table.
"""

from __future__ import annotations

import ast
import os
import re
import sys
import unittest
from pathlib import Path
from unittest import mock

ORCHESTRATION_ROOT = Path(__file__).resolve().parent.parent
SRC_DIR = ORCHESTRATION_ROOT / "src"
ROSTER_DIR = ORCHESTRATION_ROOT.parent
REPO_ROOT = ROSTER_DIR.parent
KNOWLEDGE_SRC = ROSTER_DIR / "knowledge-store" / "src"

for candidate in (SRC_DIR, ROSTER_DIR / "shared" / "src"):
    if str(candidate) not in sys.path:
        sys.path.insert(0, str(candidate))

# Platform modules, for this purpose. mcp/* was absent from five consecutive
# revisions of the requirements baseline -- the module list is the part of a
# guard most likely to be quietly incomplete, which is why the self-vacuity
# check below asserts membership rather than merely non-emptiness.
#
# The two mcp/ entries are gone because the files are: dispatch moved to Go,
# and roster/orchestration/mcp/ was deleted. The surface they covered is
# guarded by internal/orchestration/platform_role_ids_test.go, which carries
# this list's lesson with it -- including the membership check, because the
# failure this boundary actually suffered was an incomplete list rather than
# an empty one.
PLATFORM_MODULES = (
    SRC_DIR / "select_agents.py",
    SRC_DIR / "build_dispatch_plan.py",
    SRC_DIR / "risk_classifier.py",
    SRC_DIR / "routing.py",
    SRC_DIR / "routing_overlay.py",
    SRC_DIR / "roster_manifest.py",
)

# Filenames a roster package owns. Naming one in a resolution path means the
# platform assumed a layout instead of reading the manifest.
#
# `roster.json` is split out because it is the BOOTSTRAP name and cannot itself
# be indirected: something has to know what the manifest is called in order to
# open it. The exemption is expressed as a rule rather than a file allowlist
# (PP-FR-6) -- the exempt module is the one that *defines* MANIFEST_FILENAME,
# derived from the source rather than hand-listed, so moving the loader moves
# the exemption with it and adding a second module that hardcodes the name
# fails.
DECLARED_FILENAMES = ("catalog.yaml", "routing.json")
BOOTSTRAP_FILENAME = "roster.json"


def _defines_manifest_filename(path: Path) -> bool:
    tree = ast.parse(path.read_text(encoding="utf-8"))
    for node in tree.body:
        if isinstance(node, ast.Assign):
            for bound in node.targets:
                if isinstance(bound, ast.Name) and bound.id == "MANIFEST_FILENAME":
                    return True
    return False

# Call targets whose string arguments are user-facing text, never paths that get
# resolved. This is the category-C rule PP-FR-6 asked for: a property of how the
# literal is *used*, closed and enumerable, not a list of exempt files.
DIAGNOSTIC_SINKS = {
    "ValueError", "RuntimeError", "TypeError", "KeyError", "OSError",
    "RosterManifestError", "RoutingOverlayError", "SettingsError",
    "SystemExit", "print", "warn", "warning", "error", "debug", "info",
    "format", "join", "write",
    # argparse surfaces: a --help string naming the file a user must supply is
    # user-facing text by construction, exactly like a raise message. Both the
    # parser's own description and each argument's help qualify.
    "ArgumentParser", "add_argument", "add_parser",
}


def _parents(tree: ast.AST) -> dict[int, ast.AST]:
    mapping: dict[int, ast.AST] = {}
    for node in ast.walk(tree):
        for child in ast.iter_child_nodes(node):
            mapping[id(child)] = node
    return mapping


def _docstring_nodes(tree: ast.AST) -> set[int]:
    found: set[int] = set()
    for node in ast.walk(tree):
        if isinstance(node, (ast.Module, ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef)):
            body = getattr(node, "body", [])
            if body and isinstance(body[0], ast.Expr) and isinstance(body[0].value, ast.Constant):
                if isinstance(body[0].value.value, str):
                    found.add(id(body[0].value))
    return found


def _call_target_name(node: ast.AST) -> str | None:
    func = getattr(node, "func", None)
    if isinstance(func, ast.Name):
        return func.id
    if isinstance(func, ast.Attribute):
        return func.attr
    return None


def _is_diagnostic(node: ast.AST, parents: dict[int, ast.AST]) -> bool:
    """True when the literal's nearest enclosing call is user-facing text.

    Walks outward rather than checking only the immediate parent, so an
    f-string or a `", ".join(...)` inside a `raise ValueError(...)` still reads
    as a diagnostic.
    """
    current: ast.AST | None = node
    depth = 0
    while current is not None and depth < 12:
        parent = parents.get(id(current))
        if parent is None:
            return False
        if isinstance(parent, ast.Raise):
            return True
        if isinstance(parent, ast.Call):
            name = _call_target_name(parent)
            if name in DIAGNOSTIC_SINKS:
                return True
        current = parent
        depth += 1
    return False


def _offending_literals(
    path: Path, needles: tuple[str, ...], *, whole_token: bool = False
) -> list[tuple[int, str]]:
    """Scan non-docstring string literals for `needles`, skipping diagnostics.

    `whole_token` matters for role ids and not for filenames. Role ids are
    kebab-case and freely appear as *prefixes* of unrelated identifiers -- the
    gate id `halt-authority-determination` contains the role id
    `halt-authority` and has nothing to do with it. Substring matching flagged
    that on the first run, which is a false positive that would have taught
    whoever hit it to loosen the guard rather than fix the code.
    """
    tree = ast.parse(path.read_text(encoding="utf-8"))
    parents = _parents(tree)
    docstrings = _docstring_nodes(tree)
    hits: list[tuple[int, str]] = []
    for node in ast.walk(tree):
        if not isinstance(node, ast.Constant) or not isinstance(node.value, str):
            continue
        if id(node) in docstrings:
            continue
        if whole_token:
            tokens = set(re.findall(r"[a-z0-9]+(?:-[a-z0-9]+)*", node.value))
            if not tokens.intersection(needles):
                continue
        elif not any(needle in node.value for needle in needles):
            continue
        if _is_diagnostic(node, parents):
            continue
        hits.append((node.lineno, node.value))
    return hits


class TestGuardIsNotVacuous(unittest.TestCase):
    """Carried over from test_context_boundary.py:150-155, and extended.

    That guard asserts the directories exist and contain modules. This one also
    asserts specific known-load-bearing members are present, because the failure
    this boundary actually suffered was an *incomplete* list, not an empty one:
    mcp/dispatch_core.py and mcp/dispatch_server.py were missing from the
    requirements baseline through five revisions of review.
    """

    def test_every_declared_platform_module_exists(self) -> None:
        self.assertTrue(PLATFORM_MODULES, "platform module list is empty")
        for module in PLATFORM_MODULES:
            with self.subTest(module=module.name):
                self.assertTrue(module.is_file(), f"{module} does not exist")

    def test_the_selection_surface_is_in_scope(self) -> None:
        names = {module.name for module in PLATFORM_MODULES}
        for required in ("select_agents.py", "build_dispatch_plan.py"):
            self.assertIn(
                required,
                names,
                f"{required} dropped out of the platform module list. A guard that "
                "omits an entry point covers only the surface everyone already "
                "remembers -- which is how mcp/dispatch_core.py went unchecked "
                "through five revisions of the requirements baseline.",
            )


class TestNoRosterPackagePathsInResolution(unittest.TestCase):
    """Category B: roster-package filenames used to resolve, not to explain."""

    def test_platform_modules_do_not_name_declared_roster_files(self) -> None:
        for module in PLATFORM_MODULES:
            with self.subTest(module=module.name):
                hits = _offending_literals(module, DECLARED_FILENAMES)
                self.assertEqual(
                    [],
                    hits,
                    f"{module.name} resolves a roster-package filename from a literal: "
                    f"{hits}. These paths come from roster.json (PP-FR-2); a literal "
                    "here means the platform assumed a roster's directory layout.",
                )

    def test_only_the_manifest_loader_names_the_bootstrap_file(self) -> None:
        """`roster.json` is the one name that cannot be indirected.

        Exempting the loader by *rule* rather than by filename: the exempt
        module is whichever one defines MANIFEST_FILENAME. A second module that
        hardcodes the bootstrap name therefore fails, and moving the loader does
        not silently move the hole with it.
        """
        exempt = [m for m in PLATFORM_MODULES if _defines_manifest_filename(m)]
        self.assertEqual(
            1, len(exempt),
            f"expected exactly one module to define MANIFEST_FILENAME, found {exempt}",
        )
        for module in PLATFORM_MODULES:
            if module in exempt:
                continue
            with self.subTest(module=module.name):
                hits = _offending_literals(module, (BOOTSTRAP_FILENAME,))
                self.assertEqual(
                    [], hits,
                    f"{module.name} hardcodes the manifest filename: {hits}. Only "
                    f"{exempt[0].name} may -- import MANIFEST_FILENAME from it.",
                )


class TestNoCadreRoleIdsInPlatformCode(unittest.TestCase):
    """Category A, across every platform module rather than one of them.

    **This check was added after fault injection found the guard passing with a
    hole in it.** The first draft asserted category A only against
    `build_dispatch_plan.py`, where the known defect lived. Planting
    `FALLBACK_REVIEWER = "code-reviewer"` in `mcp/dispatch_core.py` -- the module
    five revisions of the requirements baseline forgot existed -- passed 12 of 12
    tests. A guard scoped to the violation you already know about is the
    "non-vacuous but incomplete" failure this file's own self-vacuity section
    warns about, committed in the file that warns about it.

    Role ids are read from the catalog rather than hand-listed, per PP-FR-6's
    preference against ad-hoc allowlists: adding a role to Cadre extends this
    check automatically, and no one has to remember to.
    """

    @classmethod
    def setUpClass(cls) -> None:
        import routing
        import roster_manifest

        manifest = roster_manifest.load_roster_manifest(roster_manifest.default_roster_root())
        cls.role_ids = tuple(routing.load_catalog(manifest.catalog))

    def test_the_catalog_actually_loaded(self) -> None:
        # Self-vacuity: an empty role set would make every assertion below pass.
        self.assertGreater(len(self.role_ids), 100, "catalog role ids did not load")
        self.assertIn("code-reviewer", self.role_ids)

    def test_no_platform_module_hardcodes_a_cadre_role_id(self) -> None:
        for module in PLATFORM_MODULES:
            with self.subTest(module=module.name):
                hits = _offending_literals(module, self.role_ids, whole_token=True)
                self.assertEqual(
                    [], hits,
                    f"{module.name} hardcodes a Cadre role id: {hits}. Platform code "
                    "must not name a specific roster's roles -- a foreign roster has "
                    "no such role, and _validate_agents raises "
                    "'Routing selected an unknown agent' rather than degrading "
                    "(OD-9). Move it into roster-declared data.",
                )


class TestDiagnosticsAreExemptByRule(unittest.TestCase):
    """Category C, and proof the rule is not over-broad in either direction."""

    def test_a_diagnostic_naming_a_roster_file_is_permitted(self) -> None:
        source = 'raise ValueError("routing.json must contain version 1 routes")\n'
        tree = ast.parse(source)
        parents = _parents(tree)
        literal = next(
            n for n in ast.walk(tree)
            if isinstance(n, ast.Constant) and isinstance(n.value, str)
        )
        self.assertTrue(
            _is_diagnostic(literal, parents),
            "a raise ValueError(...) naming the file a user must edit is not a "
            "resolution path, and routing.py's five such lines were wrongly "
            "classified as violations in PP-FR-6's original table",
        )

    def test_a_resolution_path_is_not_exempt(self) -> None:
        source = 'catalog = root / "catalog.yaml"\n'
        tree = ast.parse(source)
        parents = _parents(tree)
        literal = next(
            n for n in ast.walk(tree)
            if isinstance(n, ast.Constant) and isinstance(n.value, str)
        )
        self.assertFalse(
            _is_diagnostic(literal, parents),
            "a path join is not a diagnostic; the exemption must not swallow it",
        )

    def test_routing_py_diagnostics_are_still_permitted_in_the_real_module(self) -> None:
        """The concrete case the original table got backwards."""
        self.assertEqual([], _offending_literals(SRC_DIR / "routing.py", DECLARED_FILENAMES))


class TestPlatformAnchorsDoNotFollowTheRoster(unittest.TestCase):
    """PP-FR-1's exceptions, asserted structurally.

    The single most dangerous line in the baseline: `REPOSITORY_ROOT` and
    `_SHARED_SRC_DIR` were derived from `ROSTER_ROOT`, so making that
    resolver-driven while touching neither would have silently redirected both.
    `_SHARED_SRC_DIR` is the sys.path bootstrap for the platform's own settings
    resolver.
    """

    def setUp(self) -> None:
        self.tree = ast.parse((SRC_DIR / "select_agents.py").read_text(encoding="utf-8"))

    def _rhs_names(self, target: str) -> set[str]:
        for node in self.tree.body:
            if isinstance(node, ast.Assign):
                for bound in node.targets:
                    if isinstance(bound, ast.Name) and bound.id == target:
                        return {
                            child.id for child in ast.walk(node.value)
                            if isinstance(child, ast.Name)
                        }
        raise AssertionError(f"{target} is not a module-level constant any more")

    def test_shared_src_dir_does_not_resolve_through_the_roster(self) -> None:
        names = self._rhs_names("_SHARED_SRC_DIR")
        self.assertNotIn("ROSTER_ROOT", names)
        self.assertIn(
            "_PLATFORM_ROSTER_ROOT",
            names,
            "_SHARED_SRC_DIR must stay platform-anchored: it is the sys.path "
            "bootstrap through which settings, routing_overlay, text_embedding "
            "and content_protection are imported, so a roster-driven value "
            "would let a resolved roster supply the platform's own resolver.",
        )

    def test_repository_root_does_not_resolve_through_the_roster(self) -> None:
        names = self._rhs_names("REPOSITORY_ROOT")
        self.assertNotIn("ROSTER_ROOT", names)
        self.assertIn("_PLATFORM_ROSTER_ROOT", names)

    def test_orchestration_root_stays_platform_anchored(self) -> None:
        names = self._rhs_names("ORCHESTRATION_ROOT")
        self.assertNotIn("ROSTER_ROOT", names)


IN_TREE_KERNEL = REPO_ROOT / "bin" / "agentic-sdlc"


@unittest.skipUnless(IN_TREE_KERNEL.is_file(), "in-tree kernel wrapper missing")
class TestLifecycleAwareSelection(unittest.TestCase):
    """PP-NFR-1's second detector — the one the golden corpus cannot be.

    `test_selection_golden_corpus.py:135` patches `try_lifecycle_contract` to
    `None` so its 175 cases are deterministic across hosts, which means
    `_gate_agents()` never runs there and no case can observe the gate-reviewer
    default at all.

    **Forced, not skipped.** The existing lifecycle assertions in
    `test_selector.py` gate on `AGENTIC_SDLC_BIN or shutil.which(...)`, so they
    silently vanish on a bare checkout — reintroducing exactly the
    host-dependence the corpus was made deterministic to remove, in the tests
    added to cover the corpus's blind spot. `bin/agentic-sdlc` runs the in-tree
    kernel with no install, so pointing at it explicitly is both deterministic
    and checkout-only.
    """

    def _plan(self, task: str, files: list[str]) -> dict:
        import importlib

        with mock.patch.dict(os.environ, {"AGENTIC_SDLC_BIN": str(IN_TREE_KERNEL)}, clear=False):
            select_agents = importlib.import_module("select_agents")
            build_dispatch_plan = importlib.import_module("build_dispatch_plan")
            importlib.reload(build_dispatch_plan)
            manifest = __import__("roster_manifest").load_roster_manifest(
                __import__("roster_manifest").default_roster_root()
            )
            catalog = __import__("routing").load_catalog(manifest.catalog)
            config, _ = select_agents.resolve_effective_routing(manifest.routing, start=REPO_ROOT)
            select_agents.validate_routing_config(config)
            try:
                return build_dispatch_plan.build_dispatch_plan(
                    config, catalog,
                    {
                        "task": task, "task_id": "BOUNDARY-1",
                        "repository_root": str(REPO_ROOT), "base": None,
                        "classification": "internal", "changed_files": files,
                        "changed_file_source": "explicit", "sources": ["deagy/cadre"], "top": 5,
                    },
                    require_sdlc=True,
                )
            finally:
                importlib.reload(build_dispatch_plan)

    def test_a_gate_bearing_task_still_carries_the_roster_declared_reviewer(self) -> None:
        """OD-9 option 1's whole promise: Cadre's output does not move."""
        plan = self._plan("Update the OpenTofu module for the VPC", ["infra/vpc/main.tf"])
        self.assertEqual("integrated", plan["lifecycle_tracking"]["status"])
        self.assertIn(
            "code-reviewer",
            plan["agents"]["support"],
            "Cadre's lifecycle-aware plans lost their gate reviewer. OD-9 chose "
            "the option under which they do not: routing.json declares "
            "default_gate_review_agents, so removing it from roster data is a "
            "behaviour change, not a refactor.",
        )

    def test_the_default_is_roster_declared_not_hardcoded(self) -> None:
        """The category-A assertion. Fails if the literal returns to the source."""
        source = (SRC_DIR / "build_dispatch_plan.py").read_text(encoding="utf-8")
        hits = _offending_literals(SRC_DIR / "build_dispatch_plan.py", ("code-reviewer",))
        self.assertEqual(
            [], hits,
            f"a Cadre role id is hardcoded in platform resolution logic: {hits}. "
            "It belongs in routing.json's default_gate_review_agents (OD-9).",
        )
        self.assertIn("default_gate_review_agents", source)


if __name__ == "__main__":
    unittest.main()
