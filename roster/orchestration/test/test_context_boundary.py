#!/usr/bin/env python3
"""The two-store boundary, enforced structurally.

`roster/knowledge-store/` holds a curated corpus: content is steward-
dispositioned, retention-governed, and deleted only with a reason, an
authorized human, and retained evidence. `roster/context-store/` holds agent
working material: written freely, never dispositioned, and destroyed on a
timer.

The whole safety argument for letting agents write freely into the second store
is that content cannot cross into the first without passing
`cadre knowledge propose` and a steward's disposition. Once cross-run
durability and semantic retrieval are both in scope -- and they are -- write
authority is the *only* property distinguishing the two stores. A boundary that
rests on one property has to be enforced by construction, not by convention,
because the convention is one plausible-looking import away from gone.

The permitted couplings, and only these:

  1. Both stores import shared utilities from `roster/shared/src/`
     (`settings.py`, `content_protection.py`). Neither store owns that
     directory, so a utility living there is not a coupling *between* them.
  2. `cadre context promote` (phase 4) emits a document in the shape
     `cadre knowledge propose --from-finding -` accepts, and an operator pipes
     one into the other. That is an out-of-process, one-directional, shell-
     visible coupling -- not an import.

Anything else -- a shared database file, an import in either direction, a
config module that can resolve the other store's path -- collapses the
distinction that makes the design safe.

**What this guard is, and is not.** It is a set of AST and string-literal
checks over the current source; it is *not a runtime sandbox*. It reliably
catches the accidental crossing -- a copy-pasted `sys.path.append`, a plain
`from database import ...`, a hardcoded sibling path, or the subtler move of
resolving the other store's home through the sanctioned `settings` import. It
does not stop a determined one: a literal split across two constants, an
f-string assembled at runtime, or reading the other database's bytes with
`open()` instead of `sqlite3` would all pass. The honest claim is that it
raises an accidental crossing from one plausible-looking line to a deliberate
workaround someone would have to explain in review. Overstating it would be
worse than the gap, because a reader who believes the boundary is
unbreakable stops checking whether it broke.

    python3 -m unittest discover -s roster/orchestration/test -p "test_*.py"
"""

from __future__ import annotations

import ast
import json
import sys
import tempfile
import unittest
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
ROSTER_ROOT = REPOSITORY_ROOT / "roster"
KNOWLEDGE_SRC = ROSTER_ROOT / "knowledge-store" / "src"
CONTEXT_SRC = ROSTER_ROOT / "context-store" / "src"
SHARED_SRC = ROSTER_ROOT / "shared" / "src"

# Both stores deliberately use the same flat module names for the same roles
# (`config`, `database`, `service`, `cli`), so a name alone cannot say which
# store a module belongs to. These are the names unique to one store, and
# therefore the ones whose appearance in the other store is unambiguous
# evidence of a crossing.
KNOWLEDGE_ONLY_MODULES = frozenset({
    "staged_store", "staged_records", "ingested_deletion", "normalize",
    "embeddings", "finding_record", "content",
})
CONTEXT_ONLY_MODULES = frozenset({"handles"})

# The shared directory neither store owns. Importing from here is coupling to
# a common utility, not to the other store.
#
# Each entry was added when a second store needed the routine, and each one is
# a deliberate widening rather than a convenience:
#   settings           -- operator settings resolution, predates both stores
#   content_protection -- secret redaction + injection indicators (context phase 1)
#   text_chunking      -- chunk boundaries (context phase 2)
#   text_embedding     -- the *offline* embedding half only; see below
SHARED_MODULES = frozenset({"settings", "content_protection", "text_chunking", "text_embedding"})

# The knowledge-store module holding the remote (`openai-compatible`) embedding
# path. It is deliberately absent from SHARED_MODULES: the context store holds
# unreviewed agent working material, and whether that may be transmitted to a
# third-party endpoint is an open security decision (OD-5, currently "refused").
REMOTE_EMBEDDING_MODULE = "embeddings"


def _python_files(root: Path) -> list[Path]:
    return [path for path in sorted(root.rglob("*.py")) if "__pycache__" not in path.parts]


def _imported_top_level_modules(tree: ast.AST) -> set[str]:
    found: set[str] = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                found.add(alias.name.split(".")[0])
        elif isinstance(node, ast.ImportFrom):
            if node.level == 0 and node.module:
                found.add(node.module.split(".")[0])
    return found


def _non_docstring_string_literals(path: Path) -> list[ast.Constant]:
    """Every string constant in the file except module/class/function docstrings.

    A docstring naming the sibling store is documentation -- explaining why the
    boundary exists is exactly what these modules should do. A string literal
    anywhere else naming it is a path being constructed, which is the thing
    worth refusing.
    """
    try:
        tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
    except SyntaxError:  # pragma: no cover
        return []
    docstrings: set[int] = set()
    for node in ast.walk(tree):
        if isinstance(node, (ast.Module, ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef)):
            body = getattr(node, "body", [])
            if (
                body
                and isinstance(body[0], ast.Expr)
                and isinstance(body[0].value, ast.Constant)
                and isinstance(body[0].value.value, str)
            ):
                docstrings.add(id(body[0].value))
    return [
        node
        for node in ast.walk(tree)
        if isinstance(node, ast.Constant)
        and isinstance(node.value, str)
        and id(node) not in docstrings
    ]


def _imports_of(path: Path) -> set[str]:
    try:
        tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
    except SyntaxError:  # pragma: no cover - a syntax error is another test's problem
        return set()
    return _imported_top_level_modules(tree)


class TestNeitherStoreImportsTheOther(unittest.TestCase):
    def test_both_store_directories_exist(self) -> None:
        # Guards against this whole file silently passing because a rename
        # made every rglob below return nothing.
        for root in (KNOWLEDGE_SRC, CONTEXT_SRC, SHARED_SRC):
            self.assertTrue(root.is_dir(), f"expected {root.relative_to(REPOSITORY_ROOT)} to exist")
        self.assertTrue(_python_files(CONTEXT_SRC), "context-store/src has no modules to check")
        self.assertTrue(_python_files(KNOWLEDGE_SRC), "knowledge-store/src has no modules to check")

    def test_context_store_never_imports_knowledge_store_code(self) -> None:
        offenders = [
            f"{path.relative_to(REPOSITORY_ROOT)}: imports {module}"
            for path in _python_files(CONTEXT_SRC)
            for module in sorted(_imports_of(path) & KNOWLEDGE_ONLY_MODULES)
        ]
        self.assertEqual(
            offenders,
            [],
            "roster/context-store/ must not import knowledge-store code. Working "
            "context reaches the curated corpus only through `cadre knowledge "
            "propose` and a steward disposition; an import is a path around that "
            "gate. A utility both stores need belongs in roster/shared/src/.\n"
            + "\n".join(offenders),
        )

    def test_knowledge_store_never_imports_context_store_code(self) -> None:
        offenders = [
            f"{path.relative_to(REPOSITORY_ROOT)}: imports {module}"
            for path in _python_files(KNOWLEDGE_SRC)
            for module in sorted(_imports_of(path) & CONTEXT_ONLY_MODULES)
        ]
        self.assertEqual(
            offenders,
            [],
            "roster/knowledge-store/ must not import context-store code. The "
            "boundary is symmetric: the curated store must not grow a dependency "
            "on unreviewed working material either.\n" + "\n".join(offenders),
        )

    def test_neither_store_puts_the_other_on_sys_path(self) -> None:
        """The subtlest crossing available, because it needs no import name.

        Both stores use flat module names, so `import config` resolves by
        `sys.path` order. A module that appends the *other* store's `src` to
        `sys.path` could make `config`, `database`, or `service` silently
        resolve to the wrong store -- an import-graph check would see nothing.
        """
        # Both the hyphenated directory name and the underscored settings key
        # that resolves to the same location. The key form is the subtler of
        # the two: `settings` is a sanctioned shared import, and
        # `settings.resolve_optional("knowledge_store.home")` would hand a
        # context-store module the sibling's database directory without any
        # forbidden import and without the string "knowledge-store" appearing
        # anywhere -- enough to `ATTACH` that database and write exactly the
        # cross-store JOIN this boundary exists to make unwritable.
        pairs = (
            (CONTEXT_SRC, ("knowledge-store", "knowledge_store", "knowledge.db")),
            (KNOWLEDGE_SRC, ("context-store", "context_store", "context.db")),
        )
        offenders: list[str] = []
        for root, forbidden_tokens in pairs:
            for path in _python_files(root):
                for literal in _non_docstring_string_literals(path):
                    for token in forbidden_tokens:
                        if token in literal.value:
                            offenders.append(
                                f"{path.relative_to(REPOSITORY_ROOT)}:{literal.lineno}: "
                                f"{literal.value!r} (contains {token!r})"
                            )
        self.assertEqual(
            offenders,
            [],
            "Neither store may name the other's directory, settings key, or database "
            "file in a string literal used by code. Both use flat module names, so "
            "putting the other store's src on sys.path would silently redirect "
            "`config`/`database`/`service` with nothing for an import-graph check to "
            "see; and resolving the other store's home through `settings` would hand "
            "over a database path without any forbidden import at all. Prose in "
            "docstrings and comments is exempt -- cross-referencing the sibling store "
            "while explaining the boundary is the point.\n" + "\n".join(offenders),
        )

    def test_the_guard_states_its_own_limits(self) -> None:
        """This file's docstring must not oversell what it verifies.

        These are pattern checks over current source, not a runtime sandbox. A
        determined crossing can still be written -- a literal split across two
        constants, an f-string, a path assembled at runtime, or reading the
        other database's bytes with `open()` rather than `sqlite3`. The guard
        raises the cost of an accidental or careless crossing from one line to
        a deliberate workaround, which is worth having and is not the same as
        making it impossible. Claiming otherwise in the docstring would be the
        more dangerous error, because a reader would stop looking.
        """
        docstring = (Path(__file__).read_text(encoding="utf-8")).split('"""')[1]
        self.assertIn("not a runtime sandbox", docstring)

    def test_the_only_cross_directory_imports_are_shared_utilities(self) -> None:
        """Positive statement of the permitted coupling, not just the forbidden one.

        A negative-only guard drifts: someone adds a third shared module, and
        nothing records whether that was intended. Listing what *is* allowed
        makes widening the surface a visible edit to this test.
        """
        context_module_names = {path.stem for path in _python_files(CONTEXT_SRC)}
        external: set[str] = set()
        for path in _python_files(CONTEXT_SRC):
            external |= _imports_of(path) - context_module_names - set(sys.stdlib_module_names)
        self.assertEqual(
            external - SHARED_MODULES,
            set(),
            "context-store imported a module that is neither its own, standard "
            "library, nor one of the sanctioned shared utilities "
            f"({', '.join(sorted(SHARED_MODULES))}). Add it here deliberately if "
            "the widening is intended.",
        )


class TestTheContextStoreCannotEmbedRemotely(unittest.TestCase):
    """OD-5 ("remote embeddings refused") as a structural fact, not a config check.

    A config check leaves the remote code one edit away from reachable. The
    module split means the context store has no import path to any code that
    opens a socket or reads an embedding credential, so refusing remote
    embeddings does not depend on a validator continuing to exist.
    """

    def test_the_offline_and_remote_halves_are_in_different_modules(self) -> None:
        shared = (SHARED_SRC / "text_embedding.py").read_text(encoding="utf-8")
        knowledge = (KNOWLEDGE_SRC / "embeddings.py").read_text(encoding="utf-8")
        self.assertIn("def hashing_embedding", shared)
        self.assertIn("def cosine_similarity", shared)
        # The shared half must stay free of anything that could reach a network.
        for forbidden in ("urllib", "http", "socket", "api_key", "base_url"):
            self.assertNotIn(
                forbidden,
                shared.split('"""', 2)[-1],
                f"roster/shared/src/text_embedding.py must stay offline; found {forbidden!r} "
                "outside its docstring",
            )
        self.assertIn("_remote_embeddings", knowledge)

    def test_context_store_cannot_import_the_remote_embedding_module(self) -> None:
        offenders = [
            str(path.relative_to(REPOSITORY_ROOT))
            for path in _python_files(CONTEXT_SRC)
            if REMOTE_EMBEDDING_MODULE in _imports_of(path)
        ]
        self.assertEqual(
            offenders,
            [],
            "The context store must not import the knowledge store's `embeddings` "
            "module: it holds the remote (`openai-compatible`) path, and transmitting "
            "unreviewed agent working material to a third-party endpoint is an open "
            "security decision that is currently refused (OD-5).\n" + "\n".join(offenders),
        )

    # The config-level refusal of a remote provider is deliberately NOT asserted
    # here. It is a runtime property of one subsystem, and
    # `roster/context-store/test/test_search.py::ProviderTests::
    # test_a_remote_provider_is_refused_and_says_why` already exercises it
    # against the real `load_config`, asserting the same three things.
    #
    # A copy in this file was removed rather than kept: it added no coverage,
    # and reproducing it here meant putting the context store's `src` on
    # `sys.path` inside the one file whose whole subject is that flat module
    # names make `sys.path` order dangerous -- `TestTheStoresCannotShareADatabase`
    # below scrubs exactly those entries for exactly that reason.
    #
    # What belongs in this file is the *structural* half, which is above: the
    # import graph cannot reach the remote provider, and the shared module has
    # no networking code in it. Those hold whether or not any validator runs.


class TestTheStoresCannotShareADatabase(unittest.TestCase):
    """Separation asserted functionally, not only by reading imports.

    Two physically distinct files are what turn "no path from working context
    into the curated corpus" into something a cross-store `JOIN` cannot express,
    rather than something a reviewer has to notice.
    """

    def setUp(self) -> None:
        for source in (KNOWLEDGE_SRC, CONTEXT_SRC, SHARED_SRC):
            if str(source) in sys.path:
                sys.path.remove(str(source))
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)

    def _load(self, source: Path, config_payload: dict) -> dict:
        """Load one store's config module in isolation.

        Both define a module named `config`, so they cannot be imported into
        one interpreter by name. Loading each from its explicit file path under
        a distinct module name is the only way to compare them in one test --
        and the fact that this workaround is necessary is itself the property
        being asserted.
        """
        import importlib.util

        config_path = self.root / f"{source.parent.name}.json"
        config_path.write_text(json.dumps(config_payload), encoding="utf-8")

        if str(SHARED_SRC) not in sys.path:
            sys.path.append(str(SHARED_SRC))
        unique_name = f"_boundary_{source.parent.name.replace('-', '_')}_config"
        spec = importlib.util.spec_from_file_location(unique_name, source / "config.py")
        assert spec and spec.loader
        module = importlib.util.module_from_spec(spec)
        sys.modules[unique_name] = module
        self.addCleanup(sys.modules.pop, unique_name, None)
        spec.loader.exec_module(module)
        return module.load_config(str(config_path))

    def test_default_database_paths_differ(self) -> None:
        knowledge = self._load(KNOWLEDGE_SRC, {})
        context = self._load(CONTEXT_SRC, {})
        self.assertNotEqual(knowledge["database"], context["database"])
        self.assertNotIn("context", Path(knowledge["database"]).name)
        self.assertNotIn("knowledge", Path(context["database"]).name)

    def test_project_local_config_locations_differ(self) -> None:
        import importlib.util

        located: dict[str, str] = {}
        for source in (KNOWLEDGE_SRC, CONTEXT_SRC):
            if str(SHARED_SRC) not in sys.path:
                sys.path.append(str(SHARED_SRC))
            name = f"_boundary_paths_{source.parent.name.replace('-', '_')}"
            spec = importlib.util.spec_from_file_location(name, source / "config.py")
            assert spec and spec.loader
            module = importlib.util.module_from_spec(spec)
            sys.modules[name] = module
            self.addCleanup(sys.modules.pop, name, None)
            spec.loader.exec_module(module)
            located[source.parent.name] = str(module.PROJECT_LOCAL_RELATIVE_PATH)
        self.assertNotEqual(located["knowledge-store"], located["context-store"])

    def test_each_store_declares_its_own_settings_key(self) -> None:
        if str(SHARED_SRC) not in sys.path:
            sys.path.append(str(SHARED_SRC))
        import settings

        self.assertIn("knowledge_store.home", settings.known_keys())
        self.assertIn("context_store.home", settings.known_keys())
        for key in ("knowledge_store.home", "context_store.home"):
            # Both pick where a database is read and written, so a cloned,
            # project-local file must not be able to redirect either.
            self.assertEqual(settings.FIELDS[key].scope, settings.SCOPE_GLOBAL_ONLY)


class TestTheContextStoreSchemaCannotHoldIndefiniteEntries(unittest.TestCase):
    """S-6, asserted from outside the subsystem.

    An entry that never expires is durable, unreviewed, agent-written content
    accumulating outside the steward gate -- the knowledge store with the gate
    removed. The guarantee is a `NOT NULL` column, and this asserts it from the
    orchestration suite so that removing it fails a second, differently-owned
    test rather than only the subsystem's own.
    """

    def test_expires_at_is_declared_not_null(self) -> None:
        schema = (CONTEXT_SRC / "database.py").read_text(encoding="utf-8")
        self.assertIn("expires_at TEXT NOT NULL", schema)

    def test_the_context_store_has_no_indefinite_retention_default(self) -> None:
        config = (CONTEXT_SRC / "config.py").read_text(encoding="utf-8")
        self.assertIn("default_ttl_days_by_scope", config)
        self.assertNotIn('"default_days_by_classification"', config)


if __name__ == "__main__":
    unittest.main()
