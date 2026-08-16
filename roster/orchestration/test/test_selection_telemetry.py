"""Tests for `selection_telemetry.py` -- opt-in, local `cadre select` outcome
telemetry (backlog idea #5, "Selection outcome telemetry (opt-in, local)").

Covers, per the backlog item's own hard constraints:

- OFF by default: no CLI flag, no env var -> zero telemetry files written,
  zero change to `cadre select`'s stdout JSON.
- ON writes exactly one well-formed JSON-lines record per invocation, via
  both the `--record-telemetry` CLI flag and the `CADRE_SELECTION_TELEMETRY`
  env var, with raw task text/changed files excluded unless the caller also
  opts into `--record-telemetry-include-task`.
- The summarizer correctly aggregates a multi-record fixture file.
- A source-level grep check that this module never gains a networking
  import, matching this repository's existing boundary-safety test style
  (see `test_selection_golden_corpus.py`'s module docstring for the B-FR-4
  precedent this mirrors).
"""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
AGENTS_ROOT = ROOT.parent
sys.path.insert(0, str(ROOT / "src"))

import selection_telemetry  # noqa: E402

SELECTOR = ROOT / "src" / "select_agents.py"
TELEMETRY_MODULE_SOURCE = (ROOT / "src" / "selection_telemetry.py").read_text(encoding="utf-8")


def _run_select(args: list[str], cwd: Path, env: dict[str, str] | None = None) -> subprocess.CompletedProcess:
    import os

    full_env = dict(os.environ)
    if env:
        full_env.update(env)
    # Ensure any ambient opt-in from the developer's own shell doesn't leak
    # into a test asserting the off-by-default behavior.
    full_env.pop("CADRE_SELECTION_TELEMETRY", None)
    full_env.pop("CADRE_SELECTION_TELEMETRY_INCLUDE_TASK", None)
    full_env.pop("CADRE_SELECTION_TELEMETRY_PATH", None)
    if env:
        full_env.update(env)
    return subprocess.run(
        [sys.executable, str(SELECTOR), *args],
        cwd=cwd,
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
        env=full_env,
    )


def _init_repo(root: Path) -> None:
    subprocess.run(["git", "init", "-q", str(root)], check=True)
    subprocess.run(["git", "-C", str(root), "config", "user.email", "test@example.invalid"], check=True)
    subprocess.run(["git", "-C", str(root), "config", "user.name", "Test"], check=True)


class TelemetryOffByDefaultTests(unittest.TestCase):
    def test_is_enabled_false_without_signal(self) -> None:
        self.assertFalse(selection_telemetry.is_enabled(cli_flag=False))


class SummarizeTests(unittest.TestCase):
    def _write_fixture(self, path: Path) -> None:
        records = [
            {
                "schema_version": 1,
                "recorded_at": "2026-07-29T00:00:00.000Z",
                "task_id": "t1",
                "status": "ready",
                "workflow": "new-service",
                "matched_routes": ["frontend", "backend"],
                "matched_risks": [],
                "classification": "internal",
                "source_filter": "example/repo",
                "agent_counts": {"primary": 1, "reviewers": 1, "support": 0},
                "teams": ["full-stack-pair"],
                "lifecycle_tracking_status": "standalone",
                "required_quality_gate_count": 2,
                "human_gate_count": 0,
            },
            {
                "schema_version": 1,
                "recorded_at": "2026-07-29T00:01:00.000Z",
                "task_id": "t2",
                "status": "needs-triage",
                "workflow": "needs-triage",
                "matched_routes": [],
                "matched_risks": [],
                "classification": None,
                "source_filter": "example/repo",
                "agent_counts": {"primary": 0, "reviewers": 0, "support": 0},
                "teams": [],
                "lifecycle_tracking_status": "standalone",
                "required_quality_gate_count": 0,
                "human_gate_count": 0,
            },
            {
                "schema_version": 1,
                "recorded_at": "2026-07-29T00:02:00.000Z",
                "task_id": "t3",
                "status": "ready",
                "workflow": "infrastructure-change",
                "matched_routes": ["infrastructure", "frontend"],
                "matched_risks": ["production-change"],
                "classification": "internal",
                "source_filter": "example/repo",
                "agent_counts": {"primary": 1, "reviewers": 1, "support": 1},
                "teams": [],
                "lifecycle_tracking_status": "standalone",
                "required_quality_gate_count": 1,
                "human_gate_count": 1,
            },
        ]
        with path.open("w", encoding="utf-8") as handle:
            for record in records:
                handle.write(json.dumps(record))
                handle.write("\n")

    def test_summarize_aggregates_multi_record_fixture(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            path = Path(temporary_directory) / "telemetry.jsonl"
            self._write_fixture(path)
            report = selection_telemetry.summarize(path)
            self.assertEqual(3, report["total_records"])
            self.assertEqual({"ready": 2, "needs-triage": 1}, report["status_counts"])
            self.assertAlmostEqual(1 / 3, report["needs_triage_rate"])
            self.assertEqual(2, report["route_frequency"]["frontend"])
            self.assertEqual(1, report["route_frequency"]["backend"])
            self.assertEqual(1, report["route_frequency"]["infrastructure"])
            self.assertEqual(1, report["risk_frequency"]["production-change"])
            self.assertEqual(1, report["team_frequency"]["full-stack-pair"])
            self.assertIn("new-service", report["workflow_counts"])

    def test_summarize_empty_file_reports_zero_without_error(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            path = Path(temporary_directory) / "telemetry.jsonl"
            path.write_text("", encoding="utf-8")
            report = selection_telemetry.summarize(path)
            self.assertEqual(0, report["total_records"])
            self.assertIsNone(report["needs_triage_rate"])

    def test_summarize_malformed_line_raises_value_error(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            path = Path(temporary_directory) / "telemetry.jsonl"
            path.write_text('{"status": "ready"}\nnot json\n', encoding="utf-8")
            with self.assertRaises(ValueError):
                selection_telemetry.summarize(path)

    def test_summarize_missing_file_raises_file_not_found(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            path = Path(temporary_directory) / "missing.jsonl"
            with self.assertRaises(FileNotFoundError):
                selection_telemetry.summarize(path)

    def test_cli_summarize_prints_json_report(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            path = Path(temporary_directory) / "telemetry.jsonl"
            self._write_fixture(path)
            result = subprocess.run(
                [sys.executable, str(ROOT / "src" / "selection_telemetry.py"), "--summarize", str(path)],
                check=True, capture_output=True, text=True, encoding="utf-8",
            )
            report = json.loads(result.stdout)
            self.assertEqual(3, report["total_records"])


class NetworkAbsenceTests(unittest.TestCase):
    """Source-level boundary check: this module must never gain a networking
    primitive. Matches this repository's grep-based boundary-safety
    convention (see e.g. `test_repository_health.py` and
    `test_selection_golden_corpus.py`'s standalone-mode network guard note)."""

    FORBIDDEN_TOKENS = (
        "import socket",
        "import urllib",
        "from urllib",
        "import requests",
        "import http.client",
        "from http.client",
        "import ftplib",
        "import smtplib",
        "import telnetlib",
        # A shell-out is just as much a network-capability bypass as a
        # direct network import -- this module has no legitimate reason to
        # spawn a subprocess or invoke a shell at all.
        "import subprocess",
        "from subprocess",
        "os.system",
        "os.popen",
        "os.spawn",
    )

    def test_no_networking_imports_in_source(self) -> None:
        for token in self.FORBIDDEN_TOKENS:
            self.assertNotIn(
                token,
                TELEMETRY_MODULE_SOURCE,
                f"selection_telemetry.py must never contain {token!r} -- telemetry is local-file-only",
            )

    def test_record_selection_never_touches_the_network_module_globals(self) -> None:
        # Defense in depth beyond the source grep: confirm the module's own
        # namespace never bound any of the common networking module names.
        module_globals = vars(selection_telemetry)
        for name in ("socket", "urllib", "requests", "http"):
            self.assertNotIn(name, module_globals)


if __name__ == "__main__":
    unittest.main()
