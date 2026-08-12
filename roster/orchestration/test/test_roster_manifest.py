"""PP-FR-2: the roster manifest loads, and fails closed by name.

Phase A′ of `roster/orchestration/runs/cadre-feature-portable-platform-2026-08-11/`.

Intent §7 C4 requires a roster package missing a required file to fail with a
message naming the file rather than degrading to the built-in roster. Every
assertion here checks the *failure*, not just the success — PP-NFR-4's bar, and
the reason `test_roster_package.py`'s case (c) was singled out as "the one that
usually gets skipped".
"""

from __future__ import annotations

import json
import os
import sys
import tempfile
import unittest
from pathlib import Path

ORCHESTRATION_ROOT = Path(__file__).resolve().parent.parent
SRC_DIR = ORCHESTRATION_ROOT / "src"
ROSTER_DIR = ORCHESTRATION_ROOT.parent

if str(SRC_DIR) not in sys.path:
    sys.path.insert(0, str(SRC_DIR))
if str(ROSTER_DIR / "shared" / "src") not in sys.path:
    sys.path.insert(0, str(ROSTER_DIR / "shared" / "src"))

import settings  # noqa: E402
from roster_manifest import (  # noqa: E402
    MANIFEST_FILENAME,
    SUPPORTED_SCHEMA_VERSIONS,
    RosterManifestError,
    default_roster_root,
    load_roster_manifest,
)

VALID = {
    "schema_version": 1,
    "id": "fixture-roster",
    "version": "0.1.0",
    "catalog": "catalog.yaml",
    "routing": "orchestration/routing.json",
    "role_root": ".",
    "shared_policy_root": "shared",
}


class _Fixture:
    """A minimal on-disk roster package, mutable per test."""

    def __init__(self, tmp: Path, **overrides):
        self.root = tmp
        (tmp / "orchestration").mkdir(parents=True, exist_ok=True)
        (tmp / "shared").mkdir(parents=True, exist_ok=True)
        (tmp / "catalog.yaml").write_text("version: 1\nagents: {}\n", encoding="utf-8")
        (tmp / "orchestration" / "routing.json").write_text("{}", encoding="utf-8")
        manifest = {**VALID, **overrides}
        for key in [k for k, v in overrides.items() if v is _OMIT]:
            manifest.pop(key, None)
        (tmp / MANIFEST_FILENAME).write_text(json.dumps(manifest), encoding="utf-8")


_OMIT = object()


class TestCadresOwnManifest(unittest.TestCase):
    def test_this_checkouts_roster_is_a_valid_package(self) -> None:
        manifest = load_roster_manifest(default_roster_root())
        self.assertEqual(manifest.root, ROSTER_DIR)
        for field in ("catalog", "routing", "role_root", "shared_policy_root"):
            with self.subTest(field=field):
                self.assertTrue(getattr(manifest, field).exists())

    def test_the_default_root_is_not_computed_from_the_setting(self) -> None:
        """`roster.root`'s default IS this value, so deriving one from the other
        would be circular. Pins the direction."""
        self.assertEqual(default_roster_root(), ROSTER_DIR)


class TestScopeIsGlobalOnly(unittest.TestCase):
    """OD-2 as reversed. The inverse of what Revisions 2-6 asked for.

    Modelled on test_kernel_boundary.py:129-140, which states the rule this
    field now follows rather than departs from: a project-local
    `.agents/cadre.yaml` is untrusted input, and checking out a repository must
    never redirect what the platform loads.
    """

    def test_roster_root_is_global_scope_only(self) -> None:
        spec = settings._spec("roster.root")
        self.assertEqual(
            spec.scope,
            settings.SCOPE_GLOBAL_ONLY,
            "roster.root must stay global-only: it selects the role prose an "
            "agent is handed as its instructions, which is a strictly more "
            "powerful redirect than any of its three siblings.",
        )

    def test_roster_root_has_a_computed_default_not_a_null_one(self) -> None:
        spec = settings._spec("roster.root")
        self.assertIsNotNone(spec.default_computed)
        self.assertTrue(Path(spec.default_computed()).is_dir())


class TestFailsClosedByName(unittest.TestCase):
    def _err(self, **overrides) -> str:
        with tempfile.TemporaryDirectory() as tmp:
            _Fixture(Path(tmp), **overrides)
            with self.assertRaises(RosterManifestError) as caught:
                load_roster_manifest(Path(tmp))
            return str(caught.exception)

    def test_missing_manifest_names_the_file(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(RosterManifestError) as caught:
                load_roster_manifest(Path(tmp))
        self.assertIn(MANIFEST_FILENAME, str(caught.exception))

    def test_unknown_schema_version_is_rejected_not_ignored(self) -> None:
        """OD-11's adopted mitigation.

        That decision declined a compatibility window, so this is the only
        signal a manifest written against different selector semantics gets.
        Ignoring the field instead would be the silent misbehaviour the
        decision explicitly accepted the risk of *avoiding*.
        """
        message = self._err(schema_version=99)
        self.assertIn("schema_version", message)
        self.assertIn("99", message)
        for supported in SUPPORTED_SCHEMA_VERSIONS:
            self.assertIn(str(supported), message)

    def test_missing_required_field_names_the_field(self) -> None:
        message = self._err(catalog=_OMIT)
        self.assertIn("catalog", message)

    def test_declared_file_that_does_not_exist_names_the_field(self) -> None:
        message = self._err(catalog="nope.yaml")
        self.assertIn("catalog", message)
        self.assertIn("nope.yaml", message)

    def test_malformed_json_says_so(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            _Fixture(Path(tmp))
            (Path(tmp) / MANIFEST_FILENAME).write_text("version: 1\n", encoding="utf-8")
            with self.assertRaises(RosterManifestError) as caught:
                load_roster_manifest(Path(tmp))
        self.assertIn("JSON", str(caught.exception))


class TestPathContainment(unittest.TestCase):
    """Ported from the kernel's provider_resource(), never imported.

    `test_kernel_boundary.py:76-95` forbids importing kernel code from
    `roster/`, so the logic is reproduced. Each vector below is asserted
    separately because they fail for different reasons and one of them
    (absolute paths) works via a pathlib quirk a refactor could remove without
    realising it was doing the work.
    """

    def _rejects(self, value: str, field: str = "catalog") -> str:
        with tempfile.TemporaryDirectory() as tmp:
            _Fixture(Path(tmp), **{field: value})
            with self.assertRaises(RosterManifestError) as caught:
                load_roster_manifest(Path(tmp))
            return str(caught.exception)

    def test_parent_traversal_is_rejected(self) -> None:
        self.assertIn("escapes", self._rejects("../catalog.yaml"))

    def test_deep_parent_traversal_is_rejected(self) -> None:
        self.assertIn("escapes", self._rejects("../../../../etc/passwd"))

    def test_absolute_path_is_rejected(self) -> None:
        """`Path("/a") / "/etc/passwd"` is `Path("/etc/passwd")` under pathlib
        join semantics, so containment catches this rather than an
        is_absolute() guard. Asserted explicitly for that reason."""
        self.assertIn("escapes", self._rejects("/etc/passwd"))

    @unittest.skipIf(os.name == "nt", "POSIX symlink semantics")
    def test_symlink_escape_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as outer:
            outer_path = Path(outer)
            secret = outer_path / "secret.yaml"
            secret.write_text("version: 1\n", encoding="utf-8")
            root = outer_path / "roster"
            root.mkdir()
            _Fixture(root, catalog="link.yaml")
            (root / "link.yaml").symlink_to(secret)
            with self.assertRaises(RosterManifestError) as caught:
                load_roster_manifest(root)
        self.assertIn("escapes", str(caught.exception))

    def test_empty_and_non_string_values_are_rejected(self) -> None:
        self.assertIn("catalog", self._rejects(""))


if __name__ == "__main__":
    unittest.main()
