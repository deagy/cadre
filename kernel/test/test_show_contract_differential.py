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
