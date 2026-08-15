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

import contextlib
import io
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[3]
SUBCOMMANDS_PATH = REPO_ROOT / "bin" / "subcommands.tsv"
VERSION_SOURCE_PATH = REPO_ROOT / "cadre_cli" / "_version.py"
PLUGIN_ROOT = REPO_ROOT / "plugin"
PLUGIN_VERSION_MANIFEST_PATH = PLUGIN_ROOT / ".claude-plugin" / "plugin.json"

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
    """Ask the real dispatcher, whichever one that currently is.

    This used to run `python bin/cadre.py <name> --help`. The Python-to-Go
    migration deleted `bin/cadre.py` and made `bin/cadre` the dispatcher, so
    that invocation stopped reaching a CLI at all -- every subTest below saw
    an interpreter error instead of help text. Going through `bin/cadre`
    keeps this asking the question it was written to ask ("does the command
    the user typed name itself?") of whatever implementation is shipping,
    Python or Go, rather than pinning a file path that a migration can
    delete out from under it.
    """
    env = dict(os.environ, NO_COLOR="1")
    return subprocess.run(
        [str(REPO_ROOT / "bin" / "cadre"), name, "--help"],
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


class GlobalVersionTest(unittest.TestCase):
    def test_version_reports_the_pip_distribution_marker_without_mutating(self) -> None:
        """The global flag must be safe before any subcommand is selected."""
        version_match = re.search(
            r'^VERSION = "(?P<version>[^"]+)"$',
            VERSION_SOURCE_PATH.read_text(encoding="utf-8"),
            flags=re.MULTILINE,
        )
        self.assertIsNotNone(version_match, f"VERSION assignment missing from {VERSION_SOURCE_PATH}")

        before = subprocess.run(
            ["git", "status", "--porcelain"],
            capture_output=True,
            text=True,
            cwd=REPO_ROOT,
            timeout=120,
        ).stdout
        result = subprocess.run(
            [str(REPO_ROOT / "bin" / "cadre"), "--version"],
            capture_output=True,
            text=True,
            stdin=subprocess.DEVNULL,
            cwd=REPO_ROOT,
            timeout=120,
        )
        after = subprocess.run(
            ["git", "status", "--porcelain"],
            capture_output=True,
            text=True,
            cwd=REPO_ROOT,
            timeout=120,
        ).stdout

        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(f"cadre {version_match.group('version')}\n", result.stdout)
        self.assertEqual("", result.stderr)
        self.assertEqual(before, after, "`cadre --version` must not mutate the working tree")

    @unittest.skipUnless(sys.platform != "win32", "plugin/bin/cadre is a POSIX sh script")
    def test_plugin_version_reports_its_manifest_without_python_or_mutation(self) -> None:
        """The generated launcher must not confuse plugin and pip versions."""
        plugin_version = json.loads(PLUGIN_VERSION_MANIFEST_PATH.read_text(encoding="utf-8"))["version"]

        before = subprocess.run(
            ["git", "status", "--porcelain"],
            capture_output=True,
            text=True,
            cwd=REPO_ROOT,
            timeout=120,
        ).stdout
        # Only the two POSIX utilities the wrapper needs before its version
        # branch are present. In particular, Python is unavailable, proving
        # the branch executes before the generic-subcommand Python check.
        with tempfile.TemporaryDirectory(prefix="cadre-plugin-version-") as utility_directory:
            utility_path = Path(utility_directory)
            for utility in ("dirname", "sed"):
                executable = shutil.which(utility)
                self.assertIsNotNone(executable, f"{utility} is required for this POSIX wrapper test")
                (utility_path / utility).symlink_to(executable)
            result = subprocess.run(
                [str(PLUGIN_ROOT / "bin" / "cadre"), "--version"],
                capture_output=True,
                text=True,
                stdin=subprocess.DEVNULL,
                cwd=REPO_ROOT,
                timeout=120,
                env={"PATH": str(utility_path)},
            )
        after = subprocess.run(
            ["git", "status", "--porcelain"],
            capture_output=True,
            text=True,
            cwd=REPO_ROOT,
            timeout=120,
        ).stdout

        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(f"cadre {plugin_version}\n", result.stdout)
        self.assertEqual("", result.stderr)
        self.assertEqual(before, after, "plugin `cadre --version` must not mutate the working tree")


@unittest.skipIf(sys.platform == "win32", "bin/cadre is a POSIX sh script; bin/cadre.ps1 covers Windows")
class WrapperCgoBuildTest(unittest.TestCase):
    """`bin/cadre` must build a knowledge-capable binary, and must not become
    unbuildable when it cannot.

    The wrapper used to run a bare `go build`, which inherits `go env
    CGO_ENABLED` -- 0 on plenty of machines. mattn/go-sqlite3 ships a cgo-less
    stub, so that binary links cleanly and then fails *every* `cadre knowledge`
    call at runtime with "Binary was compiled with 'CGO_ENABLED=0'". Nothing
    warned at build time, so a checkout could sit in that state indefinitely.

    Forcing CGO_ENABLED=1 unconditionally trades one failure for a worse one:
    with no C toolchain the build fails outright and the whole CLI stops
    working, not just `knowledge`. Hence prefer-then-fall-back, which is what
    these two tests pin from both directions.

    Each test uses its own CADRE_BUILD_CACHE so it neither reads nor clobbers
    the developer's real .cadre-build-cache/.
    """

    def _run(self, args, *, cache, extra_env=None):
        env = dict(os.environ, CADRE_BUILD_CACHE=str(cache))
        env.pop("CGO_ENABLED", None)
        if extra_env:
            env.update(extra_env)
        return subprocess.run(
            [str(REPO_ROOT / "bin" / "cadre"), *args],
            capture_output=True,
            text=True,
            stdin=subprocess.DEVNULL,
            cwd=REPO_ROOT,
            timeout=600,
            env=env,
        )

    @unittest.skipUnless(shutil.which("cc") or shutil.which("gcc"), "needs a C toolchain to build with cgo")
    def test_wrapper_builds_a_cgo_binary_when_a_c_toolchain_exists(self) -> None:
        """Fails if the wrapper stops preferring cgo -- the original defect."""
        with tempfile.TemporaryDirectory(prefix="cadre-cgo-cache-") as cache:
            result = self._run(["doctor"], cache=cache)
            # doctor exits 1 on a cwd/binary mismatch, which is expected here
            # because the binary lives in a temp cache; the knowledge-store
            # line is what this test is about.
            self.assertIn(
                "knowledge store:    available",
                result.stdout,
                "bin/cadre must build with CGO_ENABLED=1 where a C compiler exists, "
                f"otherwise `cadre knowledge` is dead on arrival.\nstdout:\n{result.stdout}",
            )

    def test_wrapper_falls_back_and_stays_usable_without_a_c_toolchain(self) -> None:
        """Fails if the cgo-less fallback is removed, which would make a
        machine with no C compiler unable to run `cadre` at all."""
        with tempfile.TemporaryDirectory(prefix="cadre-nocgo-cache-") as cache:
            result = self._run(
                ["--version"], cache=cache, extra_env={"CC": "/nonexistent/cc"}
            )
            self.assertEqual(
                0,
                result.returncode,
                f"the wrapper must fall back to a cgo-less build rather than failing.\n"
                f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}",
            )
            self.assertRegex(result.stdout, r"^cadre \S+\n$")
            # The buffering contract still holds on the fallback path: a
            # cold-cache build writes "go: downloading ..." to stderr, and
            # anything that leaks breaks `--version` for every stderr-sensitive
            # caller (this file's GlobalVersionTest, the Cline vitest suites).
            self.assertEqual("", result.stderr)


class WheelShipsNoPythonTest(unittest.TestCase):
    """The pip/pipx wheel carries the Go binary and data, and no Python.

    This replaces a pair of tests that guarded `cadre_cli`'s refusal to run
    `generate-plugin`. Two generators could produce `plugin/bin/cadre`: the Go
    one emits the hardened launcher (binary resolution, mandatory checksum
    verification, sidecar-verified cache, permission-gated exec), while
    `generate_global_plugin.py`'s `generate_bin_wrapper()` still emits the
    pre-hardening one. `cadre_cli` dispatched that subcommand to the Python
    script, so an editable install could silently regenerate a plugin whose
    launcher had none of those controls.

    Phase 2 of PYTHON_ELIMINATION_PLAN.md removed the channel rather than the
    symptom: `cadre_cli` is deleted and the wheel ships no Python for
    anything to dispatch to. Guarding the absence is strictly stronger than
    guarding the refusal, because a refusal can be relaxed by editing one
    branch while this fails the moment any `.py` re-enters the distribution.
    """

    # Text assertions rather than tomllib: this repository supports Python
    # 3.10+, and tomllib is 3.11+. The questions here are all about whether a
    # specific line is present or absent, which text answers exactly as well.

    def _pyproject(self) -> str:
        return (REPO_ROOT / "pyproject.toml").read_text(encoding="utf-8")

    def test_the_wheel_declares_no_python_packages(self) -> None:
        pyproject = self._pyproject()
        self.assertNotIn(
            'packages = ["cadre_cli"]',
            pyproject,
            "the wheel must not package any Python; the binary is the "
            "distribution (PYTHON_ELIMINATION_PLAN.md Phase 2)",
        )
        self.assertIn(
            "bypass-selection = true",
            pyproject,
            "hatchling needs bypass-selection to accept a wheel with no "
            "Python packages -- without it the build fails rather than "
            "silently shipping nothing",
        )

    def test_the_wheel_vendors_no_data_through_hatchling(self) -> None:
        """hatchling packs only the binary; `make wheel` adds the data tree.

        This is not a stylistic split. hatchling's shared-data maps *files
        only* -- a directory source produces no files and **no error**, so a
        wheel built that way succeeds and is missing all 159 role
        definitions. An entry re-added here would either be silently ignored
        (a directory) or duplicate what the Makefile already copies.
        """
        pyproject = self._pyproject()
        for table in ("shared-data", "force-include"):
            self.assertIsNone(
                re.search(
                    rf"^\[tool\.hatch\.build\.targets\.wheel\.{re.escape(table)}\]",
                    pyproject,
                    re.MULTILINE,
                ),
                f"{table} does not carry directories reliably; `make wheel` "
                "copies the data tree instead",
            )

    def test_no_python_tree_is_vendored_into_the_wheel(self) -> None:
        """The four Python trees the old wheel carried, named so a re-added
        one is a visible diff rather than a silent regression."""
        pyproject = self._pyproject()
        for tree in (
            "roster/orchestration/src",
            "roster/orchestration/mcp",
            "roster/shared/src",
            "roster/context-store/src",
        ):
            self.assertNotIn(
                f'"{tree}"',
                pyproject,
                f"{tree} is Python and must not be vendored into the wheel",
            )

    def test_the_binary_is_the_console_command(self) -> None:
        """A [project.scripts] entry would generate a Python launcher into
        <prefix>/bin/cadre and silently overwrite the binary the wheel
        installs there. That is not hypothetical -- it is what happened the
        first time this wheel was built, and the symptom was a 208-byte
        `cadre` that raised ModuleNotFoundError."""
        pyproject = self._pyproject()
        # A real table header at column 0, not the string wherever it appears
        # -- pyproject.toml's own comment explains why the table is absent,
        # and a substring check matches that explanation and passes forever.
        self.assertIsNone(
            re.search(r"^\[project\.scripts\]", pyproject, re.MULTILINE),
            "[project.scripts] would shadow the binary installed from the "
            "wheel's .data/scripts/ directory",
        )
        self.assertIn(
            '[tool.hatch.build.targets.wheel.shared-scripts]',
            pyproject,
            "the wheel must install the staged Go binary as the `cadre` command",
        )
        self.assertIn('"dist-staging/cadre" = "cadre"', pyproject)

    def test_no_python_remains_in_the_distribution_package(self) -> None:
        """cadre_cli/__init__.py -- the 482-line subcommand dispatcher -- is
        gone. Only the version marker may remain, and only because both
        channels read it as text (internal/cli/version.go parses it without
        executing Python)."""
        package = REPO_ROOT / "cadre_cli"
        if not package.exists():
            return
        remaining = sorted(path.name for path in package.glob("*.py"))
        self.assertEqual(
            ["_version.py"],
            remaining,
            "only the shared version marker may remain under cadre_cli/",
        )


if __name__ == "__main__":
    unittest.main()
