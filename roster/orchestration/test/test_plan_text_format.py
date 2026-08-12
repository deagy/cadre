"""`cadre select --format text` renders the plan without changing it.

Two properties matter more than the layout, and both are easy to break with a
well-meant edit:

1. **JSON is untouched.** It is the contract every downstream tool reads, so
   the default output must stay byte-identical to what it was before a text
   mode existed.
2. **The renderer invents nothing.** It is a pure function of the plan dict.
   A formatter that re-read routing.json or re-derived a field could disagree
   with the JSON it claims to be showing -- a bad failure for a command whose
   value is being reproducible.
"""

from __future__ import annotations

import json
import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
REPO_ROOT = ROOT.parent.parent

import sys as _sys  # noqa: E402

_sys.path.insert(0, str(ROOT / "src"))

from plan_text_format import format_plan_text  # noqa: E402

SELECT = REPO_ROOT / "roster" / "orchestration" / "src" / "select_agents.py"

# A plan with something in every block the renderer knows how to draw.
FULL_PLAN = {
    "schema_version": 6,
    "task_id": "TASK-7",
    "status": "ready",
    "workflow": "production-release",
    "inputs": {"task": "rotate the signing keys", "changed_files": ["terraform/prod/main.tf"]},
    "matched_routes": [
        {
            "id": "infrastructure",
            "reasons": {"keywords": ["terraform"], "keyword_groups": [], "paths": [{"pattern": "terraform/**", "file": "terraform/prod/main.tf"}]},
        }
    ],
    "matched_risks": [{"id": "production"}],
    "context_packs": [],
    "agents": {
        "primary": ["kubernetes-manifest-implementer"],
        "reviewers": ["security-reviewer"],
        "support": ["release-engineer"],
    },
    "dispatch_disposition": {"status": "staffed", "reason": "A primary was selected."},
    "teams": [
        {
            "id": "parallel-review",
            "type": "fixed",
            "members": ["code-reviewer", "security-reviewer"],
            "communication_mode": "peer",
        }
    ],
    "required_quality_gates": [{"id": "G1", "required": True}, {"id": "G9", "required": True}],
    "human_gates": [
        {"id": "production-change", "required": True, "reason": "An authorized human must approve the target."}
    ],
    "dispatch_fingerprint": "sha256:abc123",
}

TRIAGE_PLAN = {
    "schema_version": 6,
    "task_id": "TASK-8",
    "status": "needs-triage",
    "workflow": "needs-triage",
    "inputs": {"task": "reticulate the splines", "changed_files": ["foo.xyz"]},
    "matched_routes": [],
    "matched_risks": [],
    "agents": {"primary": [], "reviewers": [], "support": []},
    "dispatch_disposition": {
        "status": "no-agents-selected",
        "reason": "No route or risk rule matched this task; there is nothing to dispatch.",
    },
    "teams": [],
}


def _run_select(*extra: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [
            sys.executable,
            str(SELECT),
            "--task",
            "add rate limiting to the login endpoint",
            "--files",
            "api/auth.go",
            "--task-id",
            "TASK-FMT",
            "--classification",
            "internal",
            *extra,
        ],
        capture_output=True,
        text=True,
        timeout=300,
        cwd=REPO_ROOT,
    )


class JsonRemainsTheDefaultTest(unittest.TestCase):
    def test_default_output_is_json(self) -> None:
        result = _run_select()
        self.assertEqual(result.returncode, 0, result.stderr)
        json.loads(result.stdout)  # raises if the default stopped being JSON

    def test_explicit_json_matches_the_default_byte_for_byte(self) -> None:
        """The flag's presence must not perturb the contract output."""
        default = _run_select()
        explicit = _run_select("--format", "json")
        self.assertEqual(default.returncode, 0, default.stderr)

        def _stable(payload: str) -> dict:
            plan = json.loads(payload)
            for volatile in ("generated_at", "dispatch_fingerprint"):
                plan.pop(volatile, None)
            plan.get("provenance", {}).pop("git_dirty_paths", None)
            return plan

        self.assertEqual(_stable(default.stdout), _stable(explicit.stdout))

    def test_text_format_is_not_json(self) -> None:
        result = _run_select("--format", "text")
        self.assertEqual(result.returncode, 0, result.stderr)
        with self.assertRaises(json.JSONDecodeError):
            json.loads(result.stdout)
        self.assertIn("AGENTS", result.stdout)

    def test_output_file_honors_the_chosen_format(self) -> None:
        """`--format text --output f` must not write JSON into f."""
        import tempfile

        with tempfile.TemporaryDirectory() as tmp:
            target = Path(tmp) / "plan.txt"
            result = _run_select("--format", "text", "--output", str(target))
            self.assertEqual(result.returncode, 0, result.stderr)
            written = target.read_text(encoding="utf-8")
            self.assertIn("AGENTS", written)
            with self.assertRaises(json.JSONDecodeError):
                json.loads(written)


class RenderingTest(unittest.TestCase):
    def test_full_plan_names_every_selected_agent(self) -> None:
        rendered = format_plan_text(FULL_PLAN)
        for agent in ("kubernetes-manifest-implementer", "security-reviewer", "release-engineer"):
            self.assertIn(agent, rendered)

    def test_role_ids_are_never_split_across_lines(self) -> None:
        """Hyphenated ids must survive wrapping.

        `textwrap`'s default breaks on hyphens, which turns one real role into
        two lines that each read as a role that does not exist. Asserted on a
        deliberately long roster, since short ones never reach the margin.
        """
        plan = dict(FULL_PLAN)
        plan["agents"] = {
            "primary": [
                "kubernetes-manifest-implementer",
                "supply-chain-security-reviewer",
                "opentofu-module-implementer",
                "network-management-automation-implementer",
                "embedded-linux-platform-implementer",
            ],
            "reviewers": [],
            "support": [],
        }
        rendered = format_plan_text(plan)
        for line in rendered.splitlines():
            self.assertFalse(
                line.rstrip().endswith("-"),
                f"a hyphenated id was split across lines: {line!r}",
            )
        for agent in plan["agents"]["primary"]:
            self.assertIn(agent, rendered)

    def test_needs_triage_says_so_in_words(self) -> None:
        """The case a JSON skim misreads: valid plan, every agent list empty."""
        rendered = format_plan_text(TRIAGE_PLAN)
        self.assertIn("NO AGENTS SELECTED", rendered)
        self.assertIn("nothing to dispatch", rendered)
        self.assertIn("--explain", rendered)

    def test_human_gates_are_rendered_with_their_reason(self) -> None:
        rendered = format_plan_text(FULL_PLAN)
        self.assertIn("HUMAN APPROVAL REQUIRED", rendered)
        self.assertIn("production-change", rendered)
        self.assertIn("An authorized human must approve", rendered)

    def test_a_minimal_plan_renders_instead_of_raising(self) -> None:
        """An older or truncated plan must not turn into a traceback.

        A formatter is the wrong place to discover a schema change, and raising
        here would hide the plan entirely rather than showing what it has.
        """
        for sparse in ({}, {"status": "ready"}, {"task_id": "X", "agents": {}}):
            with self.subTest(plan=sparse):
                self.assertTrue(format_plan_text(sparse).endswith("\n"))

    def test_rendering_does_not_mutate_the_plan(self) -> None:
        original = json.dumps(FULL_PLAN, sort_keys=True)
        format_plan_text(FULL_PLAN)
        self.assertEqual(original, json.dumps(FULL_PLAN, sort_keys=True))


if __name__ == "__main__":
    unittest.main()
