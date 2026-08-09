"""CLI tests for the staged-record verbs: propose, list-staged, show-staged.

These cover the two properties the proposal turns on. First, staging is *per
project* — the shared global-fallback store refuses records structurally rather
than relying on `--source` discipline. Second, `show-staged` exists at all: a
database row cannot be read in a diff the way a committed file could, so
losing pull-request review means discoverability has to be built or the corpus
becomes invisible.
"""

from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path

SRC = Path(__file__).resolve().parents[1] / "src"
RECORDS = Path(__file__).resolve().parents[1] / "proposed-knowledge"
if str(SRC) not in sys.path:
    sys.path.insert(0, str(SRC))

import cli  # noqa: E402
import staged_records  # noqa: E402


def _a_record() -> tuple[Path, dict]:
    path = sorted(RECORDS.glob("*.md"))[0]
    frontmatter, _ = staged_records.parse_record(path.read_text(encoding="utf-8"))
    return path, frontmatter


class StagedCliTests(unittest.TestCase):
    def setUp(self) -> None:
        self.workspace = tempfile.TemporaryDirectory()
        self.addCleanup(self.workspace.cleanup)
        root = Path(self.workspace.name)
        self.config_path = root / "config.json"
        # An explicit --config is the TIER_EXPLICIT_CONFIG path: a real
        # partition this test owns, never the operator's own store.
        self.config_path.write_text(
            json.dumps({"database": str(root / "knowledge.db")}), encoding="utf-8"
        )

    def _run(self, *arguments: str) -> dict:
        return cli.run([*arguments, "--config", str(self.config_path)])

    def test_propose_stages_a_record_and_reports_its_digest(self) -> None:
        path, frontmatter = _a_record()
        result = self._run("propose", "--input", str(path))
        self.assertEqual(result["status"], "staged")
        self.assertEqual(result["id"], frontmatter["id"])
        self.assertEqual(result["content_digest"], frontmatter["content_digest"])
        # The response must not let a caller read "staged" as "available".
        self.assertIn("not ingestion", result["note"])

    def test_propose_rejects_a_malformed_record_without_staging_it(self) -> None:
        path, _ = _a_record()
        broken = path.read_text(encoding="utf-8").replace("recommended_action: ingest", "recommended_action: delete")
        target = Path(self.workspace.name) / "broken.md"
        target.write_text(broken, encoding="utf-8")
        with self.assertRaises(Exception) as caught:
            self._run("propose", "--input", str(target))
        self.assertIn("delete", str(caught.exception))
        self.assertEqual(self._run("list-staged")["records"], [])

    def test_propose_reads_stdin(self) -> None:
        path, frontmatter = _a_record()
        original = sys.stdin
        sys.stdin = __import__("io").StringIO(path.read_text(encoding="utf-8"))
        self.addCleanup(lambda: setattr(sys, "stdin", original))
        self.assertEqual(self._run("propose", "--input", "-")["id"], frontmatter["id"])

    def test_list_staged_filters_by_status(self) -> None:
        for path in sorted(RECORDS.glob("*.md")):
            self._run("propose", "--input", str(path))
        everything = self._run("list-staged")["records"]
        proposed = self._run("list-staged", "--status", "proposed")["records"]
        accepted = self._run("list-staged", "--status", "accepted")["records"]
        self.assertEqual(len(everything), len(proposed) + len(accepted))
        self.assertTrue(accepted, "the committed corpus includes dispositioned records")
        self.assertTrue(all(record["status"] == "accepted" for record in accepted))

    def test_show_staged_returns_the_full_record_not_just_a_summary(self) -> None:
        path, frontmatter = _a_record()
        self._run("propose", "--input", str(path))
        shown = self._run("show-staged", "--id", frontmatter["id"])
        self.assertEqual(shown["frontmatter"], frontmatter)
        self.assertIn("body", shown)
        # The rendered text must be re-proposable: what you read is exactly
        # what the contract would accept back.
        self.assertEqual(staged_records.validate_record(shown["text"]), [])

    def test_show_staged_names_an_unknown_id_rather_than_returning_empty(self) -> None:
        with self.assertRaises(ValueError) as caught:
            self._run("show-staged", "--id", "KS-20260101-not-here")
        self.assertIn("KS-20260101-not-here", str(caught.exception))

    def test_an_invalid_status_filter_is_rejected_by_the_parser(self) -> None:
        with self.assertRaises(SystemExit):
            self._run("list-staged", "--status", "archived")


class StagingScopeTests(unittest.TestCase):
    """Decision 4: records are staged per project, enforced not conventional."""

    def test_the_shared_global_store_refuses_staging(self) -> None:
        original = cli.load_config

        def fallback_config(config_path, return_tier=False):
            configuration, _ = original(config_path, return_tier=True)
            return (configuration, cli.TIER_GLOBAL_FALLBACK) if return_tier else configuration

        cli.load_config = fallback_config
        self.addCleanup(lambda: setattr(cli, "load_config", original))

        workspace = tempfile.TemporaryDirectory()
        self.addCleanup(workspace.cleanup)
        config_path = Path(workspace.name) / "config.json"
        config_path.write_text(
            json.dumps({"database": str(Path(workspace.name) / "knowledge.db")}), encoding="utf-8"
        )
        path, _ = _a_record()
        for command in (
            ("propose", "--input", str(path)),
            ("list-staged",),
            ("show-staged", "--id", "KS-20260101-anything"),
        ):
            with self.subTest(command=command[0]):
                with self.assertRaises(ValueError) as caught:
                    cli.run([*command, "--config", str(config_path)])
                message = str(caught.exception)
                self.assertIn("per project", message)
                # The message has to say what to do, not only what failed.
                self.assertIn(".agents/knowledge-store/config.json", message)


if __name__ == "__main__":
    unittest.main()
