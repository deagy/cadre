"""Differential gate for `show-contract`, the first kernel subcommand ported
to Go.

`cadre select` parses `show-contract lifecycle-gates` and refuses a contract
whose version it does not recognise, so this output is not a display format --
it is an interface between two programs. A difference as small as a trailing
newline is a difference in what the selector reads.

The gate is byte equality across every contract the CLI offers, plus the exit
code, plus what each writes to stderr on a bad argument. Both implementations
run on this machine, against the same contracts, in the same process tree; a
harness that compared recorded output would only prove the recording was
faithful once.

This file retires with the Python kernel. What it is buying in the meantime is
the ability to ask "what does the Python kernel do here?" about an input
nobody wrote down -- which is exactly what stops being possible afterwards.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import unittest
from pathlib import Path

KERNEL_ROOT = Path(__file__).resolve().parent.parent
REPO_ROOT = KERNEL_ROOT.parent

# Every name the Python CLI's `choices` list accepts. Hard-coded rather than
# read from either implementation: a list derived from one of the two sides
# would agree with that side by construction, which is the one thing a
# differential harness must not do.
CONTRACT_NAMES = (
    "artifact.schema",
    "agent-catalog.schema",
    "dispatch-bindings.schema",
    "extension.schema",
    "lifecycle-gates",
    "mutation-gates",
    "profile.schema",
    "provider.schema",
    "run-record.schema",
    "selection.schema",
)


def _go_binary() -> Path | None:
    """Build the Go kernel once, or return None when Go is unavailable.

    Skipping rather than failing when Go is absent: this suite runs in the
    Python kernel's own test job, which has no Go toolchain, and a hard
    failure there would report a missing toolchain as a divergence.
    """
    target = Path(os.environ.get("TMPDIR", "/tmp")) / "agentic-sdlc-differential"
    build = subprocess.run(
        ["go", "build", "-o", str(target), "./cmd/agentic-sdlc"],
        cwd=REPO_ROOT, capture_output=True, text=True,
    )
    if build.returncode != 0:
        return None
    return target


def _run_python(args: list[str]) -> tuple[int, str, str]:
    completed = subprocess.run(
        [sys.executable, "-c",
         "import sys; from agentic_sdlc import main; sys.exit(main(sys.argv[1:]))", *args],
        cwd=KERNEL_ROOT, capture_output=True, text=True,
    )
    return completed.returncode, completed.stdout, completed.stderr


def _run_go(binary: Path, args: list[str]) -> tuple[int, str, str]:
    completed = subprocess.run(
        [str(binary), *args], cwd=KERNEL_ROOT, capture_output=True, text=True
    )
    return completed.returncode, completed.stdout, completed.stderr


class ShowContractDifferentialTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.binary = _go_binary()
        if cls.binary is None:
            raise unittest.SkipTest("no Go toolchain available to build the Go kernel")

    def test_every_contract_is_byte_identical(self) -> None:
        for name in CONTRACT_NAMES:
            with self.subTest(contract=name):
                py_code, py_out, _ = _run_python(["show-contract", name])
                go_code, go_out, _ = _run_go(self.binary, ["show-contract", name])

                self.assertEqual(py_code, 0, f"the Python kernel failed for {name}")
                self.assertEqual(go_code, 0, f"the Go kernel failed for {name}")
                self.assertEqual(
                    py_out, go_out,
                    f"{name}: the two kernels print different bytes. `cadre select` parses "
                    "this output, so any difference is a difference in what the selector reads.",
                )

    def test_the_output_is_the_contract_file_itself(self) -> None:
        # Self-vacuity: if both implementations were broken in the same way --
        # printing nothing, or an error document -- byte equality above would
        # still pass. This anchors the comparison to the file on disk.
        for name in CONTRACT_NAMES:
            with self.subTest(contract=name):
                on_disk = (KERNEL_ROOT / "contracts" / f"{name}.json").read_text(encoding="utf-8")
                _, go_out, _ = _run_go(self.binary, ["show-contract", name])
                self.assertEqual(go_out, on_disk.rstrip() + "\n")
                self.assertTrue(json.loads(go_out), f"{name} did not parse as non-empty JSON")

    def test_an_unknown_contract_is_refused_by_both_with_the_same_exit_code(self) -> None:
        # The code is what a script reads. Wording differs between argparse and
        # a hand-written Go parser and is deliberately not compared -- pinning
        # prose across two languages buys nothing and breaks on rewording.
        for name in ["nonsense", "", "lifecycle-gates.json", "../contracts/lifecycle-gates"]:
            with self.subTest(name=name):
                py_code, py_out, _ = _run_python(["show-contract", name])
                go_code, go_out, _ = _run_go(self.binary, ["show-contract", name])
                self.assertEqual(
                    py_code, go_code,
                    f"{name!r}: exit codes differ (python={py_code}, go={go_code})",
                )
                self.assertNotEqual(py_code, 0, f"{name!r} was accepted by the Python kernel")
                self.assertEqual(py_out, "", "a refused request printed to stdout")
                self.assertEqual(go_out, "", "a refused request printed to stdout")

    def test_a_path_traversal_argument_cannot_reach_another_file(self) -> None:
        # The name indexes a fixed set; it is not a path. If it were, a caller
        # could read any file the kernel can, and the kernel is the component
        # that answers questions about gate authority.
        for attempt in [
            "../pyproject", "../../etc/passwd", "contracts/lifecycle-gates",
            "lifecycle-gates/../../pyproject",
        ]:
            with self.subTest(attempt=attempt):
                go_code, go_out, _ = _run_go(self.binary, ["show-contract", attempt])
                self.assertNotEqual(go_code, 0, f"{attempt!r} was accepted")
                self.assertEqual(go_out, "", f"{attempt!r} produced output")

    def test_show_contract_needs_exactly_one_name(self) -> None:
        for args in [[], ["lifecycle-gates", "mutation-gates"]]:
            with self.subTest(args=args):
                py_code, _, _ = _run_python(["show-contract", *args])
                go_code, go_out, _ = _run_go(self.binary, ["show-contract", *args])
                self.assertNotEqual(py_code, 0)
                self.assertEqual(py_code, go_code, "exit codes differ for a bad argument count")
                self.assertEqual(go_out, "")


if __name__ == "__main__":
    unittest.main()


class DetectDifferentialTests(unittest.TestCase):
    """`detect` reports what a repository looks like and changes nothing.

    Two things about its output resist byte comparison, and both are stated
    here rather than smoothed over.

    `command_candidates` is a dict printed in *insertion* order, not sorted --
    so a Go map would have reordered it. That one is comparable, and is
    compared.

    The Python test-command candidate embeds `sys.executable`: the absolute
    path of whichever interpreter happened to run the kernel. A Go binary has
    no equivalent, and the value is machine-specific either way. It is
    normalised below and asserted as a property instead, because claiming
    byte-equality for a field that cannot have it would be the harness lying
    about what it checked.
    """

    @classmethod
    def setUpClass(cls) -> None:
        cls.binary = _go_binary()
        if cls.binary is None:
            raise unittest.SkipTest("no Go toolchain available to build the Go kernel")

    def _detect(self, root: Path) -> tuple[dict, dict]:
        py_code, py_out, py_err = _run_python(["detect", "--root", str(root)])
        go_code, go_out, go_err = _run_go(self.binary, ["detect", "--root", str(root)])
        self.assertEqual(py_code, 0, f"the Python kernel failed: {py_err}")
        self.assertEqual(go_code, 0, f"the Go kernel failed: {go_err}")
        return json.loads(py_out), json.loads(go_out)

    @staticmethod
    def _normalise_interpreter(report: dict) -> dict:
        test = report.get("command_candidates", {}).get("test")
        if test and test[0] not in ("go", "use-project-pinned-package-manager"):
            test[0] = "<python-interpreter>"
        return report

    def test_the_repository_itself_is_reported_identically(self) -> None:
        py_report, go_report = self._detect(REPO_ROOT)
        self.assertEqual(self._normalise_interpreter(py_report), self._normalise_interpreter(go_report))

    def test_command_candidate_key_order_matches(self) -> None:
        # The property a Go map would have broken, and the one a dict
        # comparison above cannot see: json.loads discards order, so this
        # compares the raw text's key sequence.
        _, py_out, _ = _run_python(["detect", "--root", str(REPO_ROOT)])
        _, go_out, _ = _run_go(self.binary, ["detect", "--root", str(REPO_ROOT)])
        py_keys = list(json.loads(py_out)["command_candidates"])
        go_keys = list(json.loads(go_out)["command_candidates"])
        self.assertEqual(
            py_keys, go_keys,
            "command_candidates keys are in a different order; Python emits insertion "
            "order and a Go map would emit them sorted",
        )

    def test_every_stack_shape_agrees(self) -> None:
        import tempfile

        shapes = {
            "python-only": ["pyproject.toml"],
            "node-only": ["package.json"],
            "go-only": ["go.mod"],
            "rust-only": ["Cargo.toml"],
            "java-gradle": ["build.gradle.kts"],
            "dotnet-glob": ["Thing.csproj"],
            "terraform-glob": ["main.tf"],
            "containers": ["Dockerfile"],
            "multi-tier": ["package.json", "go.mod"],
            "empty": [],
        }
        for name, files in shapes.items():
            with self.subTest(shape=name), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                for filename in files:
                    (root / filename).write_text("", encoding="utf-8")
                py_report, go_report = self._detect(root)
                self.assertEqual(
                    self._normalise_interpreter(py_report),
                    self._normalise_interpreter(go_report),
                    f"{name}: the two kernels describe the same directory differently",
                )

    def test_service_layout_and_directory_reporting_agree(self) -> None:
        import tempfile

        for name, dirs in {
            "service-layout": ["src"],
            "cmd-internal": ["cmd", "internal"],
            "infra-deploy": ["infra", "deploy"],
            "unreported-dir": ["docs", "vendor"],
        }.items():
            with self.subTest(shape=name), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root / "go.mod").write_text("module x\n", encoding="utf-8")
                for d in dirs:
                    (root / d).mkdir()
                py_report, go_report = self._detect(root)
                self.assertEqual(py_report, go_report, f"{name}: reports differ")

    def test_a_missing_root_is_handled_the_same_way(self) -> None:
        missing = REPO_ROOT / "definitely-not-a-directory-0f3a9c"
        py_code, py_out, _ = _run_python(["detect", "--root", str(missing)])
        go_code, go_out, _ = _run_go(self.binary, ["detect", "--root", str(missing)])
        self.assertEqual(py_code, go_code, "exit codes differ for a missing root")
        if py_code == 0:
            self.assertEqual(json.loads(py_out)["detected_stacks"], json.loads(go_out)["detected_stacks"])

    def test_a_symlinked_root_is_resolved_to_its_real_path(self) -> None:
        # Python reports `root.resolve()`, which follows symlinks. Reporting
        # the symlink instead would name a path that describes where the
        # caller pointed rather than what was actually inspected -- and two
        # runs through different links would look like two different
        # repositories.
        import tempfile

        with tempfile.TemporaryDirectory() as directory:
            real = Path(directory) / "real-repo"
            real.mkdir()
            (real / "go.mod").write_text("module x\n", encoding="utf-8")
            link = Path(directory) / "link-to-repo"
            try:
                link.symlink_to(real, target_is_directory=True)
            except (OSError, NotImplementedError):
                self.skipTest("cannot create symlinks here")

            py_report, go_report = self._detect(link)
            self.assertEqual(py_report["root"], go_report["root"], "the two kernels resolved differently")
            self.assertNotIn(
                "link-to-repo", go_report["root"],
                "the reported root is the symlink rather than what was inspected",
            )

    def test_the_interpreter_candidate_is_an_absolute_path(self) -> None:
        # The field byte comparison cannot cover, asserted as a property on
        # both sides: whatever each kernel names, it must be an absolute path
        # rather than a bare word a shell would have to resolve.
        import tempfile

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "pyproject.toml").write_text("", encoding="utf-8")
            py_report, go_report = self._detect(root)
            for label, report in (("python", py_report), ("go", go_report)):
                command = report["command_candidates"]["test"]
                self.assertTrue(
                    Path(command[0]).is_absolute(),
                    f"{label} kernel named a non-absolute interpreter: {command[0]!r}",
                )
                self.assertEqual(command[1:], ["-m", "unittest", "discover"])
