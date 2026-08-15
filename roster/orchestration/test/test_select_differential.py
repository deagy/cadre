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
import shutil
import subprocess
import tempfile
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


class SelectDiscoveryParityTest(unittest.TestCase):
    """Parity for the inputs the golden corpus deliberately cannot cover.

    Every corpus case pins `--files` and `--source` so its golden does not
    encode the generating machine's working tree or git origin. That is the
    right call for a golden -- and it leaves the discovery paths that run when
    those flags are *absent* completely unexercised by the gate, which is how
    a real invocation is almost always made.

    So this class builds throwaway checkouts and runs both implementations
    against them with neither flag. Nothing is stored: the comparison is
    Python against Go, here, on this machine, which is exactly the comparison
    that stays valid on a fork, a clone, or a contributor's laptop.
    """

    @classmethod
    def setUpClass(cls) -> None:
        if shutil.which("git") is None:
            raise unittest.SkipTest("git is not available")

        probe = load_corpus()[0]
        code, _, stderr = run_select(probe, implementation="go")
        if code == GO_NOT_IMPLEMENTED_EXIT:
            raise unittest.SkipTest(
                "no Go select implementation yet: CADRE_SELECT_IMPL=go returns "
                f"exit {GO_NOT_IMPLEMENTED_EXIT}"
            )
        if code != 0:
            raise AssertionError(f"CADRE_SELECT_IMPL=go failed (exit {code}):\n{stderr}")

    def _git(self, root: Path, *args: str) -> None:
        subprocess.run(
            ["git", *args], cwd=root, check=True, capture_output=True,
            env={**os.environ, "GIT_CONFIG_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0"},
        )

    def _checkout(self, parent: Path, name: str, origin: str | None) -> Path:
        root = parent / name
        root.mkdir(parents=True)
        self._git(root, "init", "-q", "-b", "main")
        self._git(root, "config", "user.email", "test@example.invalid")
        self._git(root, "config", "user.name", "Test")
        (root / "seed.txt").write_text("seed\n", encoding="utf-8")
        self._git(root, "add", "-A")
        self._git(root, "commit", "-q", "-m", "seed", "--no-gpg-sign")
        if origin:
            self._git(root, "remote", "add", "origin", origin)
        return root

    def _plan(self, root: Path, *, base: str | None, implementation: str) -> dict:
        environment = dict(os.environ)
        if implementation == "go":
            environment["CADRE_SELECT_IMPL"] = "go"
        else:
            environment.pop("CADRE_SELECT_IMPL", None)
        arguments = [
            str(CADRE), "select",
            "--task", "add a login handler and update the deployment manifest",
            "--task-id", "DISCOVERY-1",
            "--classification", "internal",
            "--root", str(root),
        ]
        if base:
            arguments += ["--base", base]
        completed = subprocess.run(
            arguments, capture_output=True, text=True, timeout=180,
            cwd=REPO_ROOT, env=environment,
        )
        if completed.returncode != 0:
            raise AssertionError(
                f"`cadre select` failed ({implementation}, exit "
                f"{completed.returncode}):\n{completed.stderr}"
            )
        return json.loads(completed.stdout)

    def _assert_parity(self, root: Path, why: str, *, base: str | None = None) -> dict:
        python_plan = self._plan(root, base=base, implementation="python")
        go_plan = self._plan(root, base=base, implementation="go")
        self.assertEqual(
            canonical_form(python_plan), canonical_form(go_plan),
            f"the Go plan differs from the Python one. This case exists because: {why}",
        )
        self.assertEqual(
            python_plan["dispatch_fingerprint"], go_plan["dispatch_fingerprint"],
            f"dispatch_fingerprint differs; the Go plan is a different plan. Case: {why}",
        )
        return python_plan

    def test_changed_files_discovered_from_the_working_tree_agree(self) -> None:
        with tempfile.TemporaryDirectory() as workspace:
            root = self._checkout(Path(workspace), "worktree", None)
            # A modification, a deletion, a rename and two untracked files,
            # because the rename is what appends an extra NUL-separated field
            # and an untracked file is exactly the change routing most needs.
            (root / "seed.txt").write_text("modified\n", encoding="utf-8")
            (root / "to-rename.txt").write_text("body\n", encoding="utf-8")
            self._git(root, "add", "-A")
            self._git(root, "commit", "-q", "-m", "second", "--no-gpg-sign")
            self._git(root, "mv", "to-rename.txt", "renamed.txt")
            (root / "handler.go").write_text("package main\n", encoding="utf-8")
            (root / "café.tsx").write_text("export const x = 1\n", encoding="utf-8")

            plan = self._assert_parity(root, "git-status discovery, including a rename and a quotable path")
            self.assertEqual(plan["inputs"]["changed_file_source"], "git-status")
            discovered = plan["inputs"]["changed_files"]
            self.assertIn("handler.go", discovered)
            self.assertIn("café.tsx", discovered)
            self.assertNotIn("to-rename.txt", discovered, "the rename's original path is not a changed file")

    def test_changed_files_discovered_from_a_base_ref_agree(self) -> None:
        with tempfile.TemporaryDirectory() as workspace:
            root = self._checkout(Path(workspace), "branch", None)
            self._git(root, "checkout", "-q", "-b", "feature")
            (root / "handler.go").write_text("package main\n", encoding="utf-8")
            self._git(root, "add", "-A")
            self._git(root, "commit", "-q", "-m", "feature", "--no-gpg-sign")
            (root / "uncommitted.txt").write_text("x\n", encoding="utf-8")

            plan = self._assert_parity(root, "base-ref diff discovery", base="main")
            self.assertEqual(plan["inputs"]["changed_file_source"], "git-diff:main...HEAD")
            self.assertEqual(plan["inputs"]["changed_files"], ["handler.go"])

    def test_knowledge_sources_derived_from_the_origin_remote_agree(self) -> None:
        with tempfile.TemporaryDirectory() as workspace:
            root = self._checkout(Path(workspace), "with-origin", "git@github.com:example/demo.git")
            (root / "handler.go").write_text("package main\n", encoding="utf-8")

            plan = self._assert_parity(root, "origin-derived knowledge source")
            self.assertEqual(plan["knowledge_context"]["source_filter"], ["example/demo"])

    def test_knowledge_sources_without_an_origin_agree(self) -> None:
        with tempfile.TemporaryDirectory() as workspace:
            root = self._checkout(Path(workspace), "no-origin", None)
            (root / "handler.go").write_text("package main\n", encoding="utf-8")

            plan = self._assert_parity(root, "local-<name>-<digest> fallback with no origin remote")
            sources = plan["knowledge_context"]["source_filter"]
            self.assertEqual(len(sources), 1)
            self.assertTrue(sources[0].startswith("local-no-origin-"), sources)

    def test_the_staged_source_appears_only_with_a_project_local_store(self) -> None:
        """The store refuses to read `proposed-knowledge` from the shared
        global-fallback store, and refuses per call rather than per source --
        so naming it without a project-local partition would return the agent
        nothing at all. Both implementations have to draw that line together.
        """
        with tempfile.TemporaryDirectory() as workspace:
            root = self._checkout(Path(workspace), "local-store", "git@github.com:example/demo.git")
            (root / "handler.go").write_text("package main\n", encoding="utf-8")

            before = self._assert_parity(root, "no project-local store: staged source omitted")
            self.assertEqual(before["knowledge_context"]["source_filter"], ["example/demo"])

            store = root / ".agents" / "knowledge-store"
            store.mkdir(parents=True)
            (store / "config.json").write_text("{}\n", encoding="utf-8")

            after = self._assert_parity(root, "project-local store makes the staged source legal")
            self.assertEqual(
                after["knowledge_context"]["source_filter"],
                ["example/demo", "proposed-knowledge"],
            )


class SelectOverlayParityTest(unittest.TestCase):
    """Parity for a project-local routing overlay, end to end through the plan.

    The merge rules are compared exhaustively by
    `probe_overlay_parity.py` (69 documents). What this adds is the part a
    merge comparison cannot show: that an overlay actually reaches the plan,
    changes it, and changes it the same way in both implementations -- and
    that an illegal overlay stops the run in both rather than being ignored
    by one.

    Ignoring an overlay is the failure that matters here. It produces a
    perfectly ordinary-looking plan built from rules the project did not
    declare, and no amount of comparing plans against each other would catch
    it if both implementations ignored overlays equally.
    """

    @classmethod
    def setUpClass(cls) -> None:
        if shutil.which("git") is None:
            raise unittest.SkipTest("git is not available")

        probe = load_corpus()[0]
        code, _, stderr = run_select(probe, implementation="go")
        if code == GO_NOT_IMPLEMENTED_EXIT:
            raise unittest.SkipTest(
                "no Go select implementation yet: CADRE_SELECT_IMPL=go returns "
                f"exit {GO_NOT_IMPLEMENTED_EXIT}"
            )
        if code != 0:
            raise AssertionError(f"CADRE_SELECT_IMPL=go failed (exit {code}):\n{stderr}")

        cls.base_routing = json.loads(
            (REPO_ROOT / "roster" / "orchestration" / "routing.json").read_text(encoding="utf-8")
        )

    def _project(self, workspace: str, overlay: dict | str | None) -> Path:
        root = Path(workspace) / "project"
        root.mkdir(parents=True)
        subprocess.run(["git", "init", "-q", "-b", "main"], cwd=root, check=True, capture_output=True)
        (root / "handler.go").write_text("package main\n", encoding="utf-8")
        if overlay is not None:
            destination = root / ".agents" / "orchestration" / "routing-overlay.json"
            destination.parent.mkdir(parents=True)
            destination.write_text(
                overlay if isinstance(overlay, str) else json.dumps(overlay, indent=2),
                encoding="utf-8",
            )
        return root

    def _run(self, root: Path, *, implementation: str) -> tuple[int, str, str]:
        environment = dict(os.environ)
        if implementation == "go":
            environment["CADRE_SELECT_IMPL"] = "go"
        else:
            environment.pop("CADRE_SELECT_IMPL", None)
        completed = subprocess.run(
            [
                str(CADRE), "select",
                "--task", "probe the overlay path with an unmistakable keyword: zzprobekeyword",
                "--files", "handler.go",
                "--task-id", "OVERLAY-1",
                "--classification", "internal",
                "--root", str(root),
                "--source", "deagy/cadre",
            ],
            capture_output=True, text=True, timeout=180, cwd=REPO_ROOT, env=environment,
        )
        return completed.returncode, completed.stdout, completed.stderr

    def _both(self, root: Path) -> tuple[dict, dict]:
        results = {}
        for implementation in ("python", "go"):
            code, stdout, stderr = self._run(root, implementation=implementation)
            if code != 0:
                raise AssertionError(
                    f"`cadre select` failed ({implementation}, exit {code}):\n{stderr}"
                )
            results[implementation] = json.loads(stdout)
        return results["python"], results["go"]

    def test_a_legal_overlay_reaches_the_plan_and_agrees(self) -> None:
        overlay = {
            "routes": [{
                "id": "probe-overlay-route",
                "keywords": ["zzprobekeyword"],
                # primary/reviewers are lists, as every base route declares
                # them; a bare string would be iterated character by character
                # by both implementations alike.
                "primary": ["backend-engineer"],
                "reviewers": ["code-reviewer"],
            }],
        }
        with tempfile.TemporaryDirectory() as workspace:
            root = self._project(workspace, overlay)
            python_plan, go_plan = self._both(root)

            matched = [route["id"] for route in python_plan["matched_routes"]]
            self.assertIn(
                "probe-overlay-route", matched,
                "the overlay's route did not reach the plan at all, so this test "
                "would pass even if both implementations ignored overlays entirely",
            )
            self.assertEqual(canonical_form(python_plan), canonical_form(go_plan))
            self.assertEqual(python_plan["dispatch_fingerprint"], go_plan["dispatch_fingerprint"])

    def test_the_same_run_without_the_overlay_selects_differently(self) -> None:
        """Guard the guard: if the overlay changed nothing, the test above
        would be comparing two implementations of doing nothing."""
        with tempfile.TemporaryDirectory() as workspace:
            root = self._project(workspace, None)
            python_plan, go_plan = self._both(root)

            matched = [route["id"] for route in python_plan["matched_routes"]]
            self.assertNotIn("probe-overlay-route", matched)
            self.assertEqual(python_plan["dispatch_fingerprint"], go_plan["dispatch_fingerprint"])

    def test_a_widened_base_route_agrees(self) -> None:
        route = next(r for r in self.base_routing["routes"] if r.get("keywords"))
        overlay = {"routes": [{"id": route["id"], "keywords": [*route["keywords"], "zzprobekeyword"]}]}
        with tempfile.TemporaryDirectory() as workspace:
            root = self._project(workspace, overlay)
            python_plan, go_plan = self._both(root)

            matched = [entry["id"] for entry in python_plan["matched_routes"]]
            self.assertIn(route["id"], matched, "the widened route did not fire on the probe keyword")
            self.assertEqual(canonical_form(python_plan), canonical_form(go_plan))
            self.assertEqual(python_plan["dispatch_fingerprint"], go_plan["dispatch_fingerprint"])

    def test_an_illegal_overlay_stops_both_implementations(self) -> None:
        """An overlay that weakens a base route's reviewers is the case the
        whole mechanism exists to refuse. Neither implementation may proceed
        with a plan -- proceeding is worse than any disagreement about the
        wording of the refusal."""
        route = next(r for r in self.base_routing["routes"] if r.get("reviewers"))
        for why, overlay in [
            ("weakening reviewers", {"routes": [{"id": route["id"], "reviewers": []}]}),
            ("narrowing keywords", {"routes": [{"id": route["id"], "keywords": []}]}),
            ("adding a gate suppression", {"ignored_gates": [*self.base_routing.get("ignored_gates", []), "G9"]}),
            ("an unknown top-level field", {"nonsense": True}),
            ("malformed JSON", "{not json"),
        ]:
            with self.subTest(why=why), tempfile.TemporaryDirectory() as workspace:
                root = self._project(workspace, overlay)
                for implementation in ("python", "go"):
                    code, stdout, _ = self._run(root, implementation=implementation)
                    self.assertNotEqual(
                        code, 0,
                        f"{implementation} accepted an overlay that {why}; "
                        f"stdout was:\n{stdout[:400]}",
                    )


class SelectPresentationParityTest(unittest.TestCase):
    """Parity for `--format text`, `--explain` and `--output`.

    None of these touch the JSON plan or the fingerprint, which is exactly
    why they need their own gate: the corpus comparison would stay green
    while every line break moved.

    `--format text` is compared byte for byte across the whole corpus. That
    is a stronger claim than it looks -- the rendering is produced by a port
    of `textwrap.fill`, since Go has no equivalent, so every wrapped line is
    a reimplementation agreeing with the original rather than a translation
    of it.
    """

    @classmethod
    def setUpClass(cls) -> None:
        cls.corpus = load_corpus()
        probe = load_corpus()[0]
        code, _, stderr = run_select(probe, implementation="go")
        if code == GO_NOT_IMPLEMENTED_EXIT:
            raise unittest.SkipTest(
                "no Go select implementation yet: CADRE_SELECT_IMPL=go returns "
                f"exit {GO_NOT_IMPLEMENTED_EXIT}"
            )
        if code != 0:
            raise AssertionError(f"CADRE_SELECT_IMPL=go failed (exit {code}):\n{stderr}")

    def _run(self, case: dict, *extra: str, implementation: str) -> tuple[int, str, str]:
        environment = dict(os.environ)
        if implementation == "go":
            environment["CADRE_SELECT_IMPL"] = "go"
        else:
            environment.pop("CADRE_SELECT_IMPL", None)
        completed = subprocess.run(
            [
                str(CADRE), "select",
                "--task", case["task"], "--files", case["files"],
                "--classification", case["classification"], "--task-id", case["id"].upper(),
                "--source", "deagy/cadre", "--source", "proposed-knowledge",
                *extra,
            ],
            capture_output=True, text=True, timeout=180, cwd=REPO_ROOT, env=environment,
        )
        return completed.returncode, completed.stdout, completed.stderr

    def test_text_rendering_is_identical_for_every_corpus_case(self) -> None:
        for case in self.corpus:
            with self.subTest(case=case["id"]):
                python_code, python_out, python_err = self._run(case, "--format", "text", implementation="python")
                go_code, go_out, go_err = self._run(case, "--format", "text", implementation="go")

                self.assertEqual(python_code, 0, python_err)
                self.assertEqual(go_code, 0, go_err)
                self.assertEqual(
                    python_out, go_out,
                    f"the text rendering differs for {case['id']!r}. This case exists "
                    f"because: {case['why']}",
                )

    def test_text_rendering_is_a_view_of_the_same_plan(self) -> None:
        """Guard the guard: two implementations could render identical text
        from different plans if the renderer dropped the fields that differed.
        The fingerprint is in the rendering precisely so this is checkable."""
        case = self.corpus[0]
        _, text_out, _ = self._run(case, "--format", "text", implementation="go")
        _, json_out, _ = self._run(case, implementation="go")

        fingerprint = json.loads(json_out)["dispatch_fingerprint"]
        self.assertIn(
            fingerprint, text_out,
            "the text rendering must carry the same fingerprint as the JSON plan",
        )

    def test_explain_writes_to_stderr_and_leaves_the_plan_untouched(self) -> None:
        """--explain is diagnostic. It must never alter the plan on stdout --
        otherwise a diagnostic flag would change the artifact being
        diagnosed, and the fingerprint with it."""
        case = self.corpus[0]
        for implementation in ("python", "go"):
            with self.subTest(implementation=implementation):
                _, plain_out, _ = self._run(case, implementation=implementation)
                _, explain_out, explain_err = self._run(case, "--explain", implementation=implementation)

                # canonical_form, not raw bytes: two invocations always
                # differ on generated_at and provenance, so comparing stdout
                # verbatim would fail for one implementation against itself.
                self.assertEqual(
                    canonical_form(json.loads(plain_out)),
                    canonical_form(json.loads(explain_out)),
                    "--explain changed the JSON plan on stdout",
                )
                self.assertTrue(explain_err.strip(), "--explain produced no reasoning on stderr")

        _, _, python_err = self._run(case, "--explain", implementation="python")
        _, _, go_err = self._run(case, "--explain", implementation="go")
        self.assertEqual(python_err, go_err, "--explain reasoning differs between implementations")

    def test_output_writes_the_same_bytes_in_both_formats(self) -> None:
        case = self.corpus[0]
        for arguments in ([], ["--format", "text"]):
            with self.subTest(format=arguments or ["json"]):
                written = {}
                for implementation in ("python", "go"):
                    with tempfile.TemporaryDirectory() as workspace:
                        # A nested path, so the parent-directory creation is
                        # exercised rather than assumed.
                        destination = Path(workspace) / "nested" / "deeper" / "plan.out"
                        code, stdout, stderr = self._run(
                            case, "--output", str(destination), *arguments,
                            implementation=implementation,
                        )
                        self.assertEqual(code, 0, stderr)
                        self.assertEqual(
                            stdout, "",
                            f"{implementation} wrote to stdout as well as --output",
                        )
                        self.assertTrue(destination.is_file(), f"{implementation} wrote no file")
                        written[implementation] = destination.read_bytes()

                self.assertEqual(
                    canonicalize_written(written["python"]),
                    canonicalize_written(written["go"]),
                    "--output produced different bytes",
                )

    def test_an_invalid_format_is_refused_by_both(self) -> None:
        case = self.corpus[0]
        for implementation in ("python", "go"):
            code, stdout, _ = self._run(case, "--format", "yaml", implementation=implementation)
            self.assertNotEqual(code, 0, f"{implementation} accepted --format yaml")
            self.assertEqual(stdout, "", f"{implementation} emitted a plan for an invalid --format")


def canonicalize_written(payload: bytes) -> object:
    """A written plan compared the way the corpus comparison does.

    JSON output carries `generated_at` and `provenance`, which differ between
    two runs of the *same* implementation, so comparing raw bytes would fail
    for reasons that have nothing to do with the port. Text output has no
    such fields and is compared verbatim.
    """
    try:
        return canonical_form(json.loads(payload.decode("utf-8")))
    except json.JSONDecodeError:
        return payload


if __name__ == "__main__":
    unittest.main()
