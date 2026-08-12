"""Every subcommand identifies itself by its public `cadre` name.

Each row in `bin/subcommands.tsv` dispatches to a script that argparse names
after its own filename by default. Five subcommands shipped that default and
three more set it explicitly to the filename, so `cadre select --task ...`
answered a usage error as `select_agents.py`, and `knowledge` and `context`
both answered as `cli.py` -- indistinguishable from each other in the one
message meant to tell a user what they had just mistyped.

The fix is one `prog=` per parser, which is easy to get right once and easy to
omit on the next subcommand added. This test is the part that lasts: it walks
`subcommands.tsv` itself, so a new row is covered without anyone remembering
to extend a list here.

`--help` is also asserted to be side-effect free. That is not decoration:
`generate_authority_aides.py` used to scan argv for `--check` and treat
everything else -- including `--help`, and including a typo -- as "no flags
given", which is the *write* path. A CI step running `--chek` would have
regenerated the tree and exited 0, reporting success while masking the drift
the check exists to catch.
"""

from __future__ import annotations

import os
import re
import subprocess
import sys
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[3]
SUBCOMMANDS_PATH = REPO_ROOT / "bin" / "subcommands.tsv"

# argparse colorizes its output on Python 3.14+, which puts ANSI escapes
# between the start of the line and "usage:". NO_COLOR is the documented way
# to turn that off; the stripping regex below is belt-and-braces for any
# interpreter that ignores it.
_ANSI = re.compile(r"\x1b\[[0-9;]*m")

# Subcommands whose --help legitimately does not produce an argparse usage
# block. Each must still name itself correctly -- asserted below, not waived.
#
# generate-plugin refuses without --output and prints its own pointer, which
# is more useful than a usage block because the required value is a specific
# path in this repository. It is listed here because that message replaces
# the usage line, not because it is exempt from naming itself.
_NO_USAGE_BLOCK = {"generate-plugin"}


def _load_subcommands() -> list[tuple[str, str, str]]:
    rows = []
    for line in SUBCOMMANDS_PATH.read_text(encoding="utf-8").splitlines():
        if not line:
            continue
        name, script, description = line.split("\t")
        rows.append((name, script, description))
    return rows


SUBCOMMANDS = _load_subcommands()


def _run_help(name: str) -> subprocess.CompletedProcess[str]:
    env = dict(os.environ, NO_COLOR="1")
    return subprocess.run(
        [sys.executable, str(REPO_ROOT / "bin" / "cadre.py"), name, "--help"],
        capture_output=True,
        text=True,
        # stdin closed deliberately: mcp-dispatch-server serves MCP over stdio,
        # and before it parsed argv at all, `--help` started the server and sat
        # reading stdin. An inherited stdin makes that hang instead of fail.
        stdin=subprocess.DEVNULL,
        timeout=120,
        env=env,
        cwd=REPO_ROOT,
    )


class SubcommandNamingTest(unittest.TestCase):
    def test_subcommands_tsv_is_non_empty(self) -> None:
        """Guard the guard: a parse failure here would silently vacate every
        subTest below, leaving this file green while checking nothing."""
        self.assertGreaterEqual(len(SUBCOMMANDS), 10, SUBCOMMANDS_PATH)

    def test_every_subcommand_names_itself_by_its_public_name(self) -> None:
        for name, script, _description in SUBCOMMANDS:
            with self.subTest(subcommand=name):
                result = _run_help(name)
                output = _ANSI.sub("", result.stdout + result.stderr)

                script_basename = Path(script).name
                self.assertNotIn(
                    script_basename,
                    output,
                    f"`cadre {name} --help` exposes its implementation filename "
                    f"({script_basename}). Set prog='cadre {name}' on its "
                    "ArgumentParser so errors name the command the user typed.",
                )
                self.assertIn(
                    f"cadre {name}",
                    output,
                    f"`cadre {name} --help` never names itself. Set "
                    f"prog='cadre {name}' on its ArgumentParser.",
                )

                if name not in _NO_USAGE_BLOCK:
                    usage_lines = [
                        line for line in output.splitlines() if line.lower().startswith("usage:")
                    ]
                    self.assertTrue(
                        usage_lines,
                        f"`cadre {name} --help` printed no usage block. If that is "
                        "deliberate, add it to _NO_USAGE_BLOCK with the reason.",
                    )
                    self.assertTrue(
                        usage_lines[0].startswith(f"usage: cadre {name}"),
                        f"`cadre {name} --help` opens with {usage_lines[0]!r}, which "
                        f"does not name it as `cadre {name}`.",
                    )

    def test_help_never_mutates_the_working_tree(self) -> None:
        """`--help` is a question, not a command.

        Runs the whole surface and compares `git status --porcelain` around it.
        This is what catches an argv scan that treats an unrecognised flag as
        the write path -- the shape of the generate-authority-aides defect,
        which regenerated eight files when asked for help.
        """
        def tree_state() -> str:
            return subprocess.run(
                ["git", "status", "--porcelain"],
                capture_output=True,
                text=True,
                cwd=REPO_ROOT,
                timeout=120,
            ).stdout

        before = tree_state()
        for name, _script, _description in SUBCOMMANDS:
            _run_help(name)
        after = tree_state()

        self.assertEqual(
            before,
            after,
            "`--help` changed the working tree. A subcommand is treating an "
            "unrecognised flag as its default action instead of rejecting it; "
            "parse argv with argparse rather than scanning it.",
        )


if __name__ == "__main__":
    unittest.main()
