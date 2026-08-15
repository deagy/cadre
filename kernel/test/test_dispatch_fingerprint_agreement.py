"""The kernel and the selector must compute the same dispatch fingerprint.

`validate` recomputes `dispatch_fingerprint(dispatch)` and compares it to the
value the selector stored in the plan. That comparison is only meaningful if
both sides hash the same payload -- and they did not.

The selector excludes `provenance`; the kernel excluded only `generated_at`
and `dispatch_fingerprint`. Every plan `cadre select` produces carries a
`provenance` key, so the kernel recomputed a different value for every one of
them and `validate` would have reported:

    stored dispatch fingerprint does not match current dispatch content

on a plan that was correct. Two Python components, disagreeing since
`provenance` was added -- found while porting the kernel to Go, because a port
has to ask what a function actually hashes.

These tests pin the agreement rather than the exclusion list, so they still
hold if the set changes on purpose: what must not happen is the two sides
changing independently.
"""

from __future__ import annotations

import json
import subprocess
import sys
import unittest
from pathlib import Path

KERNEL_ROOT = Path(__file__).resolve().parent.parent
REPO_ROOT = KERNEL_ROOT.parent

sys.path.insert(0, str(KERNEL_ROOT))
sys.path.insert(0, str(REPO_ROOT / "roster" / "orchestration" / "src"))
sys.path.insert(0, str(REPO_ROOT / "roster" / "shared" / "src"))

from agentic_sdlc import FINGERPRINT_EXCLUDED_KEYS, dispatch_fingerprint  # noqa: E402


class FingerprintAgreementTests(unittest.TestCase):
    def _real_plan(self) -> dict:
        """A plan from the selector that actually ships, not a hand-built one.

        Hand-building the payload would let this test agree with whichever
        exclusion set it was written against; the point is to check a plan the
        selector really emits, including whatever keys it really carries.
        """
        completed = subprocess.run(
            [str(REPO_ROOT / "bin" / "cadre"), "select",
             "--task", "add a backend endpoint",
             "--files", "internal/kernel/overlay.go",
             "--task-id", "T-FINGERPRINT", "--classification", "internal"],
            cwd=REPO_ROOT, capture_output=True, text=True,
        )
        if completed.returncode != 0:
            self.skipTest(f"cadre select unavailable: {completed.stderr[:200]}")
        return json.loads(completed.stdout)

    def test_the_kernel_recomputes_the_fingerprint_the_selector_stored(self) -> None:
        plan = self._real_plan()
        self.assertIn("dispatch_fingerprint", plan)
        self.assertEqual(
            dispatch_fingerprint(plan), plan["dispatch_fingerprint"],
            "the kernel recomputes a different fingerprint than the selector stored, so "
            "`validate` reports a correct plan as having a mismatched fingerprint",
        )

    def test_the_plan_actually_carries_the_key_that_caused_the_divergence(self) -> None:
        # Self-vacuity. If plans stopped carrying `provenance`, the test above
        # would pass for both exclusion sets and stop being evidence of
        # anything.
        plan = self._real_plan()
        self.assertIn(
            "provenance", plan,
            "plans no longer carry provenance, so the agreement test above no longer "
            "distinguishes the two exclusion sets",
        )

    def test_both_sides_declare_the_same_exclusions(self) -> None:
        # The structural half: read the selector's own set rather than
        # restating it here, so this compares the two implementations instead
        # of comparing each to a copy in the test.
        source = (REPO_ROOT / "roster" / "orchestration" / "src" / "build_dispatch_plan.py").read_text(
            encoding="utf-8"
        )
        marker = 'if key not in {"generated_at", "dispatch_fingerprint", "provenance"}'
        self.assertIn(
            marker, source,
            "the selector's exclusion set changed; the kernel's FINGERPRINT_EXCLUDED_KEYS "
            "must change with it or validate will reject correct plans",
        )
        self.assertEqual(
            FINGERPRINT_EXCLUDED_KEYS,
            {"generated_at", "dispatch_fingerprint", "provenance"},
        )


if __name__ == "__main__":
    unittest.main()
