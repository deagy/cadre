"""Tests for `roster/orchestration/src/doctor.py` (`cadre doctor`).

Covers the DX trap Proposal 06 names: a bare `cadre` on PATH silently
resolving to a different implementation than the checkout a developer is
standing in. Uses temporary directories to build minimal fake checkout /
plugin-cache / site-packages trees rather than touching this repository's
own checkout or any real installed copy, so these cases stay deterministic
and independent of what happens to be installed in the test environment.

`gather_report(cwd=..., running_file=...)` takes both inputs explicitly for
exactly this reason -- it lets a test simulate "the code that ran lives
somewhere other than the cwd's own checkout" without spawning a second real
process against a second real repository tree.
"""

from __future__ import annotations

import sys
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "src"))

import doctor  # noqa: E402


def _make_checkout(root: Path) -> None:
    """Populate `root` with the minimal marker files
    `doctor._repo_markers_present` checks for."""
    (root / ".git").mkdir()
    (root / "roster").mkdir(parents=True, exist_ok=True)
    (root / "roster" / "catalog.yaml").write_text("roles: []\n", encoding="utf-8")
    (root / "bin").mkdir(parents=True, exist_ok=True)
    (root / "bin" / "cadre.py").write_text("# fake\n", encoding="utf-8")


class RepoMarkersTests(unittest.TestCase):
    def test_recognizes_a_real_checkout_shape(self) -> None:
        with TemporaryDirectory() as tmp:
            root = Path(tmp)
            _make_checkout(root)
            self.assertTrue(doctor._repo_markers_present(root))

    def test_rejects_a_directory_missing_markers(self) -> None:
        with TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "roster").mkdir()
            # No catalog.yaml, no .git, no bin/cadre.py.
            self.assertFalse(doctor._repo_markers_present(root))

    def test_find_checkout_root_walks_upward(self) -> None:
        with TemporaryDirectory() as tmp:
            root = Path(tmp)
            _make_checkout(root)
            nested = root / "roster" / "orchestration" / "src"
            nested.mkdir(parents=True)
            self.assertEqual(doctor.find_checkout_root(nested), root)

    def test_find_checkout_root_none_outside_any_checkout(self) -> None:
        with TemporaryDirectory() as tmp:
            # A bare temp dir with no checkout markers anywhere above it.
            self.assertIsNone(doctor.find_checkout_root(Path(tmp)))


class ClassifyRunningBinaryTests(unittest.TestCase):
    def test_classifies_a_checkout(self) -> None:
        with TemporaryDirectory() as tmp:
            root = Path(tmp)
            _make_checkout(root)
            running_file = root / "roster" / "orchestration" / "src" / "doctor.py"
            running_file.parent.mkdir(parents=True)
            running_file.write_text("# fake\n", encoding="utf-8")
            kind, install_root, _detail = doctor.classify_running_binary(running_file)
            self.assertEqual(kind, "checkout")
            self.assertEqual(install_root, root)

    def test_classifies_a_plugin_cache_copy(self) -> None:
        with TemporaryDirectory() as tmp:
            plugin_root = (
                Path(tmp) / "plugins" / "cache" / "test-market" / "cadre" / "9.9.9"
            )
            running_file = plugin_root / "roster" / "orchestration" / "src" / "doctor.py"
            running_file.parent.mkdir(parents=True)
            running_file.write_text("# fake\n", encoding="utf-8")
            kind, install_root, detail = doctor.classify_running_binary(running_file)
            self.assertEqual(kind, "plugin-cache")
            self.assertEqual(install_root, plugin_root)
            self.assertIn("heuristic", detail)

    def test_classifies_a_site_packages_install(self) -> None:
        with TemporaryDirectory() as tmp:
            site_packages = Path(tmp) / "lib" / "python3.12" / "site-packages"
            running_file = (
                site_packages / "cadre_cli" / "_vendor" / "roster" / "orchestration" / "src" / "doctor.py"
            )
            running_file.parent.mkdir(parents=True)
            running_file.write_text("# fake\n", encoding="utf-8")
            kind, install_root, _detail = doctor.classify_running_binary(running_file)
            self.assertEqual(kind, "pip-install")
            self.assertEqual(install_root, site_packages)

    def test_unknown_when_nothing_matches(self) -> None:
        with TemporaryDirectory() as tmp:
            # No .git/catalog.yaml/bin above it, and no plugin-cache or
            # site-packages path shape either.
            running_file = Path(tmp) / "somewhere" / "doctor.py"
            running_file.parent.mkdir(parents=True)
            running_file.write_text("# fake\n", encoding="utf-8")
            kind, _root, detail = doctor.classify_running_binary(running_file)
            self.assertEqual(kind, "unknown")
            self.assertIn("could not classify", detail)


class GatherReportMismatchTests(unittest.TestCase):
    """The proposal's headline scenario: cwd is inside a real checkout, but
    the code that ran is demonstrably a *different* location."""

    def test_no_mismatch_when_running_file_is_the_cwd_checkouts_own(self) -> None:
        with TemporaryDirectory() as tmp:
            root = Path(tmp)
            _make_checkout(root)
            running_file = root / "roster" / "orchestration" / "src" / "doctor.py"
            running_file.parent.mkdir(parents=True)
            running_file.write_text("# fake\n", encoding="utf-8")

            report = doctor.gather_report(cwd=root, running_file=running_file)
            self.assertFalse(report["mismatch"])
            self.assertIsNone(report["mismatch_detail"])

    def test_mismatch_when_running_file_is_a_plugin_cache_copy(self) -> None:
        with TemporaryDirectory() as tmp:
            root = Path(tmp) / "checkout"
            root.mkdir()
            _make_checkout(root)

            plugin_root = (
                Path(tmp) / "plugins" / "cache" / "test-market" / "cadre" / "9.9.9"
            )
            running_file = plugin_root / "roster" / "orchestration" / "src" / "doctor.py"
            running_file.parent.mkdir(parents=True)
            running_file.write_text("# fake\n", encoding="utf-8")

            report = doctor.gather_report(cwd=root, running_file=running_file)
            self.assertTrue(report["mismatch"])
            self.assertIsNotNone(report["mismatch_detail"])
            self.assertIn(str(root), report["mismatch_detail"])
            self.assertIn("plugin cache", report["mismatch_detail"])

    def test_mismatch_when_running_file_is_a_different_checkout(self) -> None:
        """Same install *kind* (checkout) but a different root entirely --
        e.g. two clones on disk and PATH picked the wrong one."""
        with TemporaryDirectory() as tmp:
            cwd_root = Path(tmp) / "checkout-a"
            other_root = Path(tmp) / "checkout-b"
            cwd_root.mkdir()
            other_root.mkdir()
            _make_checkout(cwd_root)
            _make_checkout(other_root)

            running_file = other_root / "roster" / "orchestration" / "src" / "doctor.py"
            running_file.parent.mkdir(parents=True)
            running_file.write_text("# fake\n", encoding="utf-8")

            report = doctor.gather_report(cwd=cwd_root, running_file=running_file)
            self.assertTrue(report["mismatch"])
            self.assertEqual(report["cwd_checkout_root"], str(cwd_root))
            self.assertEqual(report["install_root"], str(other_root))

    def test_no_mismatch_report_when_cwd_is_not_in_any_checkout(self) -> None:
        with TemporaryDirectory() as tmp:
            cwd = Path(tmp) / "not-a-checkout"
            cwd.mkdir()
            plugin_root = (
                Path(tmp) / "plugins" / "cache" / "test-market" / "cadre" / "9.9.9"
            )
            running_file = plugin_root / "roster" / "orchestration" / "src" / "doctor.py"
            running_file.parent.mkdir(parents=True)
            running_file.write_text("# fake\n", encoding="utf-8")

            report = doctor.gather_report(cwd=cwd, running_file=running_file)
            # Nothing to compare against -- cwd isn't inside a checkout at all.
            self.assertFalse(report["mismatch"])
            self.assertIsNone(report["cwd_checkout_root"])


class MainExitCodeTests(unittest.TestCase):
    def test_main_help_exits_zero(self) -> None:
        self.assertEqual(doctor.main(["--help"]), 0)

    def test_main_rejects_unknown_flags(self) -> None:
        self.assertEqual(doctor.main(["--nope"]), 2)

    def test_main_against_the_real_checkout_is_consistent(self) -> None:
        # This repository's own checkout, running via its own bin/cadre.py
        # dispatch path -- should never itself report a mismatch.
        self.assertEqual(doctor.main([]), 0)
        self.assertEqual(doctor.main(["--json"]), 0)


if __name__ == "__main__":
    unittest.main()
