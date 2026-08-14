"""Differential harness for a Go port of `cadre select`.

`cadre select` has a published, byte-level output contract: the plan is
`selection.schema.json`, it carries a `dispatch_fingerprint` that is a
SHA-256 over the plan's own canonical form, and consumers compare plans
across invocation paths. `internal/cli/select_agents.go`'s header records
that a from-scratch Go reimplementation already existed once in this
repository and diverged from that contract before being replaced by
dispatch-through to Python.

So the port is gated on this file, not the other way round: a Go
implementation is only allowed to replace the Python one when it produces
the same plan for every case in `select_corpus.json`.

## What "the same plan" means here

`build_dispatch_plan.py` computes the fingerprint over the plan with three
keys removed -- `generated_at`, `dispatch_fingerprint` itself, and
`provenance` -- serialised with `sort_keys=True` and
`separators=(",", ":")`. That canonical form is exactly the right basis for
comparison, and not a convenience:

- `generated_at` is a wall-clock timestamp; two runs of the *same*
  implementation differ on it.
- `provenance` carries `git_commit_sha` and `git_dirty_paths`, so a golden
  including it would break on the next commit and teach everyone to
  regenerate goldens without reading them -- the failure mode that makes a
  golden suite worthless.

One further property, discovered by this harness failing in CI and worth
stating because it is not obvious from the contract: **the plan embeds
absolute paths**, in `inputs.repository_root` and in the knowledge
invocation's argv. Those are inside the fingerprint's canonical form, so
`dispatch_fingerprint` is *checkout-location dependent* -- the same task
against the same tree fingerprints differently at `/home/me/cadre` and
`/home/runner/work/cadre/cadre`. Verified directly against two clones.

So goldens store the canonical form with the repository root replaced by
`<REPO_ROOT>`, which is portable, and cross-implementation fingerprint
equality is asserted **within a single machine and run** rather than against
a stored value. Storing a fingerprint in the golden file would have pinned
one developer's directory layout and failed for everyone else.

Everything else is compared. A matching fingerprint is therefore a claim
about every semantic field in the plan, which is why it is asserted
separately from the field-by-field comparison rather than instead of it.

## What this file is worth today

Two of its three tests have teeth immediately, against the *Python*
implementation: they pin the selector's output for 25 input shapes, so an
unintended change to routing, gate derivation, team recipes or plan encoding
fails here. That is useful independently of any port.

The third activates the moment a Go implementation exists behind
`CADRE_SELECT_IMPL=go`, and skips -- loudly, naming what is missing -- until
then.

Regenerate goldens deliberately, never reflexively:

    CADRE_SELECT_GOLDEN_REGENERATE=1 python3 -m unittest discover -b \\
        -s roster/orchestration/test -p test_select_differential.py

and read the diff before committing it.
"""

from __future__ import annotations

import hashlib
import json
import os
import subprocess
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[3]
CORPUS_PATH = Path(__file__).parent / "select_corpus.json"
GOLDEN_PATH = Path(__file__).parent / "select_golden.json"
CADRE = REPO_ROOT / "bin" / "cadre"

# Removed before comparison. Mirrors build_dispatch_plan.py's own exclusion
# set for the fingerprint -- if that set ever changes, this must change with
# it, and test_canonical_form_matches_the_shipped_fingerprint is what catches
# the drift.
VOLATILE_KEYS = frozenset({"generated_at", "dispatch_fingerprint", "provenance"})

REGENERATE = os.environ.get("CADRE_SELECT_GOLDEN_REGENERATE") == "1"

# Exit code the Go path returns while unimplemented. Distinct from 1 (a real
# selection error) and 2 (a usage error) so this harness can tell "not built
# yet" from "built and broken" -- conflating them is how a parity gate ends
# up green against a port that does not run.
GO_NOT_IMPLEMENTED_EXIT = 3


def load_corpus() -> list[dict]:
    return json.loads(CORPUS_PATH.read_text(encoding="utf-8"))["cases"]


def canonical_form(plan: dict) -> dict:
    """The plan as the fingerprint sees it."""
    return {key: value for key, value in plan.items() if key not in VOLATILE_KEYS}


REPO_ROOT_PLACEHOLDER = "<REPO_ROOT>"


def portable_form(plan: dict) -> dict:
    """canonical_form with this checkout's absolute path abstracted away.

    See the module docstring: the plan embeds absolute paths, so a golden
    recorded verbatim pins whoever generated it to their own directory
    layout.
    """
    encoded = json.dumps(canonical_form(plan), sort_keys=True, separators=(",", ":"))
    encoded = encoded.replace(str(REPO_ROOT), REPO_ROOT_PLACEHOLDER)
    return json.loads(encoded)


def canonical_bytes(plan: dict) -> bytes:
    return json.dumps(canonical_form(plan), sort_keys=True, separators=(",", ":")).encode("utf-8")


def run_select(case: dict, *, implementation: str) -> tuple[int, str, str]:
    environment = dict(os.environ)
    if implementation == "go":
        environment["CADRE_SELECT_IMPL"] = "go"
    else:
        environment.pop("CADRE_SELECT_IMPL", None)
    completed = subprocess.run(
        [
            str(CADRE), "select",
            "--task", case["task"],
            "--files", case["files"],
            "--classification", case["classification"],
            "--task-id", case["id"].upper(),
            # Pinned, not defaulted. Left alone, source_filter is derived from
            # the checkout's git origin slug, so this suite would pass on
            # deagy/cadre and fail on every fork and every local clone -- the
            # same class of environmental dependency that passing --files
            # removes for changed-file discovery. The origin-derivation path
            # itself is selector behaviour covered by test_selector.py; what
            # is pinned here is everything downstream of it.
            "--source", "deagy/cadre",
            "--source", "proposed-knowledge",
        ],
        capture_output=True, text=True, timeout=180,
        cwd=REPO_ROOT, env=environment,
    )
    return completed.returncode, completed.stdout, completed.stderr


def plan_for(case: dict, *, implementation: str = "python") -> dict:
    code, stdout, stderr = run_select(case, implementation=implementation)
    if code != 0:
        raise AssertionError(
            f"`cadre select` failed for case {case['id']!r} "
            f"({implementation}, exit {code}):\n{stderr}"
        )
    return json.loads(stdout)


class SelectGoldenTest(unittest.TestCase):
    """Pins the Python selector's output for every corpus case."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.corpus = load_corpus()
        if REGENERATE:
            goldens = {}
            for case in cls.corpus:
                plan = plan_for(case)
                goldens[case["id"]] = {"canonical": portable_form(plan)}
            GOLDEN_PATH.write_text(
                json.dumps(goldens, indent=2, sort_keys=True, ensure_ascii=False) + "\n",
                encoding="utf-8",
            )
        if not GOLDEN_PATH.exists():
            raise unittest.SkipTest(
                f"{GOLDEN_PATH.name} missing; regenerate with "
                "CADRE_SELECT_GOLDEN_REGENERATE=1"
            )
        cls.goldens = json.loads(GOLDEN_PATH.read_text(encoding="utf-8"))

    def test_corpus_and_goldens_cover_the_same_cases(self) -> None:
        """Guard the guard: a case added to the corpus without a golden would
        otherwise be silently untested, and a golden left behind after its
        case was deleted would never be compared against anything."""
        self.assertEqual(
            sorted(case["id"] for case in self.corpus),
            sorted(self.goldens),
            "corpus and goldens disagree; regenerate with "
            "CADRE_SELECT_GOLDEN_REGENERATE=1 and read the diff",
        )

    def test_python_selector_still_produces_the_recorded_plan(self) -> None:
        for case in self.corpus:
            with self.subTest(case=case["id"]):
                plan = plan_for(case)
                golden = self.goldens[case["id"]]
                self.assertEqual(
                    golden["canonical"], portable_form(plan),
                    f"the plan for {case['id']!r} changed. This case exists because: "
                    f"{case['why']}",
                )

    def test_canonical_form_matches_the_shipped_fingerprint(self) -> None:
        """This harness recomputes the fingerprint the way
        build_dispatch_plan.py does. If that computation ever changes --
        different exclusions, different separators, different sort -- every
        comparison above would still pass while measuring the wrong thing.
        Recomputing and checking against the plan's own value is what stops
        this file drifting away from the contract it exists to protect."""
        case = self.corpus[0]
        plan = plan_for(case)
        recomputed = "sha256:" + hashlib.sha256(canonical_bytes(plan)).hexdigest()
        self.assertEqual(
            plan["dispatch_fingerprint"], recomputed,
            "this harness's canonicalisation no longer matches "
            "build_dispatch_plan.py's; comparisons here are measuring the "
            "wrong bytes",
        )


class SelectGoParityTest(unittest.TestCase):
    """The gate a Go port has to pass before it can replace the Python one."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.corpus = load_corpus()
        if not GOLDEN_PATH.exists():
            raise unittest.SkipTest(f"{GOLDEN_PATH.name} missing")
        cls.goldens = json.loads(GOLDEN_PATH.read_text(encoding="utf-8"))

        probe = load_corpus()[0]
        code, _, stderr = run_select(probe, implementation="go")
        if code == GO_NOT_IMPLEMENTED_EXIT:
            raise unittest.SkipTest(
                "no Go select implementation yet: CADRE_SELECT_IMPL=go returns "
                f"exit {GO_NOT_IMPLEMENTED_EXIT}. This test activates by itself "
                "the moment one exists -- nothing here needs enabling."
            )
        if code != 0:
            raise AssertionError(
                "CADRE_SELECT_IMPL=go failed for a reason other than being "
                f"unimplemented (exit {code}):\n{stderr}"
            )

    def test_go_selector_matches_the_python_plan_byte_for_byte(self) -> None:
        """Both implementations are run here, in this process, on this
        machine -- so the fingerprints are directly comparable and no stored
        value has to encode anyone's directory layout."""
        for case in self.corpus:
            with self.subTest(case=case["id"]):
                python_plan = plan_for(case, implementation="python")
                go_plan = plan_for(case, implementation="go")

                self.assertEqual(
                    canonical_form(python_plan), canonical_form(go_plan),
                    f"the Go plan for {case['id']!r} differs from the Python one. "
                    f"This case exists because: {case['why']}",
                )
                self.assertEqual(
                    python_plan["dispatch_fingerprint"], go_plan["dispatch_fingerprint"],
                    f"dispatch_fingerprint differs for {case['id']!r}; the Go plan "
                    "is a different plan, whatever else matches",
                )
                # And still the recorded shape, so a change that moved both
                # implementations together is not silently accepted.
                self.assertEqual(
                    self.goldens[case["id"]]["canonical"], portable_form(go_plan),
                    f"both implementations agree but differ from the recorded plan "
                    f"for {case['id']!r}",
                )


if __name__ == "__main__":
    unittest.main()
