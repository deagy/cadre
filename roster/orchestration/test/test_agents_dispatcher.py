"""Direct unit coverage for bin/cadre.py's subcommand dispatcher.

bin/cadre (sh) and bin/cadre.ps1 exercise this only indirectly, through
slow subprocess-based wrapper tests (see test_repository_health.py); this
module tests load_subcommands/usage/main/dispatch_sdlc directly instead.
Loaded via importlib rather than a sys.path + `import roster`, since a bare
`roster` import would collide with the top-level roster/ package this whole
test suite already runs under.
"""

from __future__ import annotations

import contextlib
import importlib.util
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
REPOSITORY_ROOT = ROOT.parent
DISPATCHER_PATH = REPOSITORY_ROOT / "bin" / "cadre.py"

_SHARED_TEST_DIR = ROOT / "shared" / "test"
if str(_SHARED_TEST_DIR) not in sys.path:
    sys.path.append(str(_SHARED_TEST_DIR))
from settings_test_helpers import isolate_settings  # noqa: E402  (sys.path set above)

_spec = importlib.util.spec_from_file_location("agents_cli_dispatcher", DISPATCHER_PATH)
agents_cli = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(agents_cli)


def _run_capturing(func, *args, **kwargs):
    stdout, stderr = io.StringIO(), io.StringIO()
    with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
        result = func(*args, **kwargs)
    return result, stdout.getvalue(), stderr.getvalue()


class LoadSubcommandsTests(unittest.TestCase):
    def test_parses_tab_separated_rows(self) -> None:
        with tempfile.TemporaryDirectory(prefix="agents-dispatcher-") as directory:
            table = Path(directory) / "subcommands.tsv"
            table.write_text("select\troster/orchestration/src/select_agents.py\tDo the thing\n", encoding="utf-8")
            rows = agents_cli.load_subcommands(table)
            self.assertEqual(rows, [("select", "roster/orchestration/src/select_agents.py", "Do the thing")])

    def test_skips_blank_lines(self) -> None:
        with tempfile.TemporaryDirectory(prefix="agents-dispatcher-") as directory:
            table = Path(directory) / "subcommands.tsv"
            table.write_text("a\tx.py\tdesc\n\nb\ty.py\tdesc\n", encoding="utf-8")
            rows = agents_cli.load_subcommands(table)
            self.assertEqual([row[0] for row in rows], ["a", "b"])

    def test_reads_the_real_repository_table(self) -> None:
        rows = agents_cli.load_subcommands()
        names = [row[0] for row in rows]
        self.assertIn("select", names)
        self.assertIn("resolve-shared", names)
        self.assertNotIn("sdlc", names)  # sdlc is a special case, not table-driven


class UsageTests(unittest.TestCase):
    def test_renders_subcommands_sdlc_and_help_footer(self) -> None:
        text = agents_cli.usage([("select", "path/to/select.py", "Pick an agent")])
        self.assertIn("Usage: cadre <subcommand> [args...]", text)
        self.assertIn("select", text)
        self.assertIn("Pick an agent", text)
        self.assertIn("sdlc", text)
        self.assertIn(agents_cli.SDLC_DESCRIPTION, text)
        self.assertIn("help", text)
        self.assertIn("--help", text)


class MainTests(unittest.TestCase):
    def test_no_arguments_defaults_to_help(self) -> None:
        code, out, _err = _run_capturing(agents_cli.main, [])
        self.assertEqual(code, 0)
        self.assertIn("Usage: cadre <subcommand> [args...]", out)

    def test_help_flag_variants(self) -> None:
        for flag in ("help", "-h", "--help"):
            with self.subTest(flag=flag):
                code, out, _err = _run_capturing(agents_cli.main, [flag])
                self.assertEqual(code, 0)
                self.assertIn("Usage: cadre <subcommand> [args...]", out)

    def test_unknown_subcommand_fails_closed(self) -> None:
        code, _out, err = _run_capturing(agents_cli.main, ["not-a-real-subcommand"])
        self.assertEqual(code, 1)
        self.assertIn("cadre: unknown subcommand 'not-a-real-subcommand'", err)

    def test_leading_interactive_flag_is_stripped_and_forwarded_as_env(self) -> None:
        captured: dict[str, Any] = {}

        def fake_run(args, **kwargs):
            captured["args"] = args
            captured["env"] = kwargs.get("env")
            return subprocess.CompletedProcess(args, 0)

        with mock.patch.dict(os.environ, {}, clear=False):
            os.environ.pop("CADRE_INTERACTIVE", None)
            with mock.patch.object(agents_cli.subprocess, "run", side_effect=fake_run):
                code = agents_cli.main(["--interactive", "select", "--help"])
        self.assertEqual(code, 0)
        # The subcommand name itself, not "--interactive", reaches the
        # dispatched child's argv.
        self.assertNotIn("--interactive", captured["args"])
        self.assertEqual(captured["env"]["CADRE_INTERACTIVE"], "1")
        self.assertNotIn("CADRE_INTERACTIVE", os.environ)

    def test_interactive_flag_only_honored_when_leading(self) -> None:
        # A bare "select --interactive" (flag after the subcommand name) is
        # forwarded to the subcommand unchanged, not consumed here.
        captured: dict[str, Any] = {}

        def fake_run(args, **kwargs):
            captured["args"] = args
            captured["env"] = kwargs.get("env")
            return subprocess.CompletedProcess(args, 0)

        with mock.patch.object(agents_cli.subprocess, "run", side_effect=fake_run):
            code = agents_cli.main(["select", "--interactive"])
        self.assertEqual(code, 0)
        self.assertIn("--interactive", captured["args"])
        self.assertIsNone(captured["env"])

    def test_dispatches_to_the_matching_script_and_relays_its_exit_code(self) -> None:
        # main()'s dispatch path shells out via subprocess.run, which writes
        # to the real stdout file descriptor rather than sys.stdout, so
        # contextlib.redirect_stdout can't observe it from within this
        # process — invoke the dispatcher as a subprocess instead. A real,
        # fast, harmless invocation: forwarding --help to an actual
        # registered subcommand exercises the REPO_ROOT/script join and the
        # exit-code relay without depending on that subcommand's own logic.
        result = subprocess.run(
            [sys.executable, str(DISPATCHER_PATH), "select", "--help"],
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
            env={**os.environ, "NO_COLOR": "1"},
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("usage: select_agents.py", result.stdout)


class SdlcDispatchTests(unittest.TestCase):
    def setUp(self) -> None:
        # dispatch_sdlc now resolves AGENTIC_SDLC_BIN through
        # roster/shared/src/settings.py (agentic_sdlc.bin_path) instead of a
        # direct os.environ.get/shutil.which pair, so isolate the user-global
        # settings tier to avoid reading a real developer machine's
        # ~/.config/cadre/config.yaml.
        isolate_settings(self)

    def test_resolves_the_in_tree_kernel_without_any_configuration(self) -> None:
        """A checkout needs no install and no AGENTIC_SDLC_BIN.

        The kernel lives in this repository since the monorepo merge, so
        bin/agentic-sdlc is the last-resort fallback after the env var,
        configured bin_path, and PATH -- all of which represent a human's
        explicit choice of which kernel to run and still win.
        """
        captured: dict[str, list[str]] = {}

        def fake_run(command, **_kwargs):
            captured["command"] = command
            return subprocess.CompletedProcess(command, 0)

        with mock.patch.dict(os.environ, {}, clear=False):
            os.environ.pop("AGENTIC_SDLC_BIN", None)
            with mock.patch.object(agents_cli.settings.shutil, "which", return_value=None):
                with mock.patch.object(agents_cli.subprocess, "run", fake_run):
                    code, _out, _err = _run_capturing(agents_cli.dispatch_sdlc, ["--version"])
        self.assertEqual(code, 0)
        self.assertEqual(str(REPOSITORY_ROOT / "bin" / "agentic-sdlc"), captured["command"][0])

    def test_missing_binary_fails_closed(self) -> None:
        """With no in-tree kernel either -- the installed-distribution case.

        A pip/pipx install vendors bin/cadre.py but not bin/agentic-sdlc
        (see pyproject.toml's force-include list), so this path is still
        reachable and must still fail closed with an install pointer rather
        than a traceback.
        """
        with mock.patch.dict(os.environ, {}, clear=False):
            os.environ.pop("AGENTIC_SDLC_BIN", None)
            with mock.patch.object(agents_cli.settings.shutil, "which", return_value=None):
                with mock.patch.object(agents_cli, "REPO_ROOT", Path(tempfile.mkdtemp())):
                    code, _out, err = _run_capturing(agents_cli.dispatch_sdlc, ["--version"])
        self.assertEqual(code, 1)
        # The quoted range must come from provider.json's kernel_compatibility,
        # not a hardcoded literal -- these drifted ten minor versions apart once.
        minimum = json.loads(
            (REPOSITORY_ROOT / "provider" / "provider.json").read_text(encoding="utf-8")
        )["kernel_compatibility"]["minimum"]
        self.assertIn("Agentic SDLC", err)
        self.assertIn(f"v{minimum}", err)

    def test_prefers_agentic_sdlc_bin_env_var_over_path_lookup(self) -> None:
        captured: dict[str, list[str]] = {}

        def fake_run(args, **_kwargs):
            captured["args"] = args
            return subprocess.CompletedProcess(args, 0)

        with mock.patch.dict(os.environ, {"AGENTIC_SDLC_BIN": "/fake/agentic-sdlc"}):
            with mock.patch.object(
                agents_cli.settings.shutil, "which", side_effect=AssertionError("should not be called")
            ):
                with mock.patch.object(agents_cli.subprocess, "run", side_effect=fake_run):
                    code = agents_cli.dispatch_sdlc(["plan", "--foo"])

        self.assertEqual(code, 0)
        self.assertEqual(captured["args"][0], "/fake/agentic-sdlc")
        self.assertEqual(captured["args"][1], "--provider")
        self.assertTrue(captured["args"][2].endswith("provider.json"))
        self.assertEqual(captured["args"][3:], ["plan", "--foo"])

    # -- PP-FR-4: provider suppression ----------------------------------

    def _argv_for(self, rest: list[str]) -> list[str]:
        captured: dict[str, list[str]] = {}

        def fake_run(args, **_kwargs):
            captured["args"] = args
            return subprocess.CompletedProcess(args, 0)

        with mock.patch.dict(os.environ, {"AGENTIC_SDLC_BIN": "/fake/agentic-sdlc"}):
            with mock.patch.object(agents_cli.subprocess, "run", side_effect=fake_run):
                agents_cli.dispatch_sdlc(rest)
        return captured["args"]

    def test_with_no_flag_the_argument_vector_is_unchanged(self) -> None:
        """PP-FR-4 acceptance (b). The default path must not move."""
        args = self._argv_for(["plan", "--foo"])
        self.assertEqual(args[0], "/fake/agentic-sdlc")
        self.assertEqual(args[1], "--provider")
        self.assertTrue(args[2].endswith("provider.json"))
        self.assertEqual(args[3:], ["plan", "--foo"])

    def test_a_caller_supplied_provider_suppresses_the_injected_one(self) -> None:
        """PP-FR-4 acceptance (a), at the argv level.

        Before this, `cadre sdlc --provider <other> provider list` failed with
        `duplicates profile ids: ['generic']` -- the foreign manifest loaded
        correctly and was then rejected for colliding with a bundle the caller
        never asked for.
        """
        args = self._argv_for(["--provider", "/other/provider.json", "provider", "list"])
        self.assertEqual(args, [
            "/fake/agentic-sdlc", "--provider", "/other/provider.json", "provider", "list",
        ])
        self.assertEqual(1, args.count("--provider"), "Cadre's bundle was injected alongside")

    def test_the_equals_form_is_recognised_too(self) -> None:
        args = self._argv_for(["--provider=/other/provider.json", "provider", "list"])
        self.assertEqual(1, args.count("--provider"))
        self.assertIn("/other/provider.json", args)

    def test_repeated_providers_keep_the_callers_order(self) -> None:
        """The kernel's --provider is action="append", so order is precedence."""
        args = self._argv_for(["--provider", "A", "--provider", "B", "list"])
        self.assertEqual(args[1:], ["--provider", "A", "--provider", "B", "list"])

    def test_no_default_provider_suppresses_without_a_replacement(self) -> None:
        args = self._argv_for(["--no-default-provider", "provider", "list"])
        self.assertEqual(args, ["/fake/agentic-sdlc", "provider", "list"])
        self.assertNotIn("--provider", args, "the flag itself must not be forwarded")

    def test_malformed_argv_is_forwarded_untouched(self) -> None:
        """The kernel reports it, in the kernel's wording, about the command the
        caller actually invoked. A usage block for a wrapper parser they never
        called would be a second and more confusing error."""
        args = self._argv_for(["decide", "--note", "--provider"])
        self.assertEqual(args[1], "--provider")
        self.assertTrue(args[2].endswith("provider.json"))
        self.assertEqual(args[3:], ["decide", "--note", "--provider"])

    def test_relays_the_delegate_exit_code(self) -> None:
        def fake_run(args, **_kwargs):
            return subprocess.CompletedProcess(args, 7)

        with mock.patch.dict(os.environ, {"AGENTIC_SDLC_BIN": "/fake/agentic-sdlc"}):
            with mock.patch.object(agents_cli.subprocess, "run", side_effect=fake_run):
                code = agents_cli.dispatch_sdlc(["--version"])
        self.assertEqual(code, 7)

    def test_interactive_flag_passes_cadre_interactive_via_explicit_env_not_os_environ(self) -> None:
        captured: dict[str, Any] = {}

        def fake_run(args, **kwargs):
            captured["args"] = args
            captured["env"] = kwargs.get("env")
            return subprocess.CompletedProcess(args, 0)

        with mock.patch.dict(os.environ, {"AGENTIC_SDLC_BIN": "/fake/agentic-sdlc"}, clear=False):
            os.environ.pop("CADRE_INTERACTIVE", None)
            with mock.patch.object(agents_cli.subprocess, "run", side_effect=fake_run):
                code = agents_cli.dispatch_sdlc(["--version"], interactive=True)
        self.assertEqual(code, 0)
        self.assertEqual(captured["env"]["CADRE_INTERACTIVE"], "1")
        self.assertNotIn("CADRE_INTERACTIVE", os.environ)


if __name__ == "__main__":
    unittest.main()
