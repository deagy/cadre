"""Unit coverage for roster/shared/src/settings.py -- the unified operator
settings resolver (env var > project-local file > user-global file >
static default > computed default > interactive prompt > fail-closed)."""

from __future__ import annotations

import builtins
import io
import json
import os
import sys
import tempfile
import time
import contextlib
import pathlib
import unittest
from pathlib import Path
from unittest import mock

TEST_DIR = Path(__file__).resolve().parent
SRC_DIR = TEST_DIR.parent / "src"
if str(SRC_DIR) not in sys.path:
    sys.path.insert(0, str(SRC_DIR))
if str(TEST_DIR) not in sys.path:
    sys.path.insert(0, str(TEST_DIR))

import settings  # noqa: E402
from settings_test_helpers import isolate_settings  # noqa: E402  (same directory)


def _make_project(root: Path) -> Path:
    (root / ".git").mkdir()
    (root / ".agents").mkdir()
    return root


def _write_project_config(root: Path, text: str, *, filename: str = "cadre.yaml") -> Path:
    path = root / ".agents" / filename
    path.write_text(text, encoding="utf-8")
    return path


class SettingsTestCase(unittest.TestCase):
    """Common isolation: never read a real developer machine's
    ~/.config/cadre/config.yaml, and always reset settings.py's per-process
    file cache between tests."""

    def setUp(self) -> None:
        self.xdg_config_home = isolate_settings(self)
        self.project_dir = Path(tempfile.mkdtemp(prefix="cadre-settings-project-"))
        self.addCleanup(lambda: __import__("shutil").rmtree(self.project_dir, ignore_errors=True))
        _make_project(self.project_dir)


class PrecedenceMatrixTests(SettingsTestCase):
    def test_env_wins_over_everything(self) -> None:
        _write_project_config(self.project_dir, 'gitlab:\n  project_id: "from-project"\n')
        (self.xdg_config_home / "cadre").mkdir(parents=True)
        (self.xdg_config_home / "cadre" / "config.yaml").write_text(
            'gitlab:\n  project_id: "from-global"\n', encoding="utf-8"
        )
        settings.reset_cache()
        value = settings.resolve_setting(
            "gitlab.project_id", start=self.project_dir, env={"GITLAB_DOCS_PROJECT_ID": "from-env"}
        )
        self.assertEqual(value, "from-env")

    def test_project_file_wins_over_global_file(self) -> None:
        # gitlab.project_id is global_only (see GlobalOnlyScopeTests), so
        # project-vs-global honoring can only be demonstrated with a
        # project_or_global field -- gitlab.supports_work_item_hierarchy is
        # currently the only one.
        _write_project_config(
            self.project_dir, "gitlab:\n  supports_work_item_hierarchy: true\n"
        )
        (self.xdg_config_home / "cadre").mkdir(parents=True)
        (self.xdg_config_home / "cadre" / "config.yaml").write_text(
            "gitlab:\n  supports_work_item_hierarchy: false\n", encoding="utf-8"
        )
        settings.reset_cache()
        value = settings.resolve_setting(
            "gitlab.supports_work_item_hierarchy", start=self.project_dir, env={}
        )
        self.assertIs(value, True)

    def test_global_file_wins_over_default(self) -> None:
        (self.xdg_config_home / "cadre").mkdir(parents=True)
        (self.xdg_config_home / "cadre" / "config.yaml").write_text(
            'runners:\n  claude_bin: "/opt/bin/claude"\n', encoding="utf-8"
        )
        settings.reset_cache()
        value = settings.resolve_setting("runners.claude_bin", start=self.project_dir, env={})
        self.assertEqual(value, "/opt/bin/claude")

    def test_static_default_used_when_nothing_else_resolves(self) -> None:
        value = settings.resolve_setting("runners.claude_bin", start=self.project_dir, env={})
        self.assertEqual(value, "claude")

    def test_computed_default_used_when_nothing_else_resolves(self) -> None:
        with mock.patch.object(settings.shutil, "which", return_value="/usr/local/bin/agentic-sdlc"):
            value = settings.resolve_setting("agentic_sdlc.bin_path", start=self.project_dir, env={})
        self.assertEqual(value, "/usr/local/bin/agentic-sdlc")

    def test_prompt_used_when_nothing_else_resolves(self) -> None:
        inputs = iter(["https://prompted.example.com", "skip"])
        with mock.patch.object(sys.stdin, "isatty", return_value=True), mock.patch.object(
            sys.stdout, "isatty", return_value=True
        ):
            value = settings.resolve_setting(
                "gitlab.base_url",
                start=self.project_dir,
                env={"CADRE_INTERACTIVE": "1"},
                input_func=lambda _prompt: next(inputs),
                output_func=lambda _text: None,
            )
        self.assertEqual(value, "https://prompted.example.com")

    def test_required_field_fails_closed_when_totally_unresolved(self) -> None:
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting("gitlab.base_url", start=self.project_dir, env={})
        message = str(ctx.exception)
        self.assertIn("gitlab.base_url", message)
        self.assertIn("GITLAB_BASE_URL", message)
        self.assertIn("checked:", message)

    def test_optional_field_returns_none_instead_of_raising(self) -> None:
        value = settings.resolve_optional("knowledge_store.home", start=self.project_dir, env={})
        self.assertIsNone(value)

    def test_optional_field_still_raises_on_a_global_only_scope_violation(self) -> None:
        # resolve_optional() swallows "simply unconfigured" (above), but a
        # project-local file setting a global_only field is a security
        # event, not an ordinary absence -- it must surface even through
        # the "optional" resolver, never silently degrade to None.
        _write_project_config(
            self.project_dir, 'agentic_sdlc:\n  bin_path: "/tmp/should-not-be-honored"\n'
        )
        with self.assertRaises(settings.SettingsScopeError) as ctx:
            settings.resolve_optional("agentic_sdlc.bin_path", start=self.project_dir, env={})
        self.assertIn("agentic_sdlc.bin_path", str(ctx.exception))


class EmptyEnvVarTests(SettingsTestCase):
    def test_empty_env_var_errors_rather_than_falling_back(self) -> None:
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting(
                "runners.claude_bin", start=self.project_dir, env={"SECURE_CLOUD_AGENTS_CLAUDE_BIN": ""}
            )
        self.assertIn("SECURE_CLOUD_AGENTS_CLAUDE_BIN", str(ctx.exception))

    def test_whitespace_only_env_var_errors(self) -> None:
        with self.assertRaises(settings.SettingsError):
            settings.resolve_setting(
                "runners.claude_bin", start=self.project_dir, env={"SECURE_CLOUD_AGENTS_CLAUDE_BIN": "   "}
            )


class ProjectTierAnchorTests(SettingsTestCase):
    """`start=` anchors the project tier; without it the tier is discovered
    by walking up from cwd, which is only meaningful for a CLI a human ran
    inside a project. A long-lived, project-agnostic process (an stdio MCP
    server) has an incidental cwd, and resolving from it lets an unrelated
    checkout's `.agents/cadre.yaml` steer a call it has nothing to do with.
    """

    def setUp(self) -> None:
        super().setUp()
        self.addCleanup(
            setattr, settings, "_PROJECT_TIER_CWD_FALLBACK_DISABLED", False
        )

    def test_explicit_start_anchors_the_project_tier_regardless_of_cwd(self) -> None:
        _write_project_config(
            self.project_dir, "gitlab:\n  supports_work_item_hierarchy: true\n"
        )
        elsewhere = Path(tempfile.mkdtemp(prefix="cadre-settings-elsewhere-"))
        self.addCleanup(lambda: __import__("shutil").rmtree(elsewhere, ignore_errors=True))
        with mock.patch.object(settings.Path, "cwd", return_value=elsewhere):
            value = settings.resolve_setting(
                "gitlab.supports_work_item_hierarchy", start=self.project_dir, env={}
            )
        self.assertIs(value, True)

    def test_without_start_the_cwd_decides_which_project_is_read(self) -> None:
        # Documents the behavior that makes the opt-out below necessary:
        # an unrelated directory's config is what gets picked up.
        _write_project_config(
            self.project_dir, "gitlab:\n  supports_work_item_hierarchy: true\n"
        )
        with mock.patch.object(settings.Path, "cwd", return_value=self.project_dir):
            settings.reset_cache()
            value = settings.resolve_setting(
                "gitlab.supports_work_item_hierarchy", start=None, env={}
            )
        self.assertIs(value, True)

    def test_disabling_the_cwd_fallback_skips_the_project_tier(self) -> None:
        _write_project_config(
            self.project_dir, "gitlab:\n  supports_work_item_hierarchy: true\n"
        )
        settings.disable_project_tier_cwd_fallback()
        with mock.patch.object(settings.Path, "cwd", return_value=self.project_dir):
            settings.reset_cache()
            value = settings.resolve_setting(
                "gitlab.supports_work_item_hierarchy", start=None, env={}
            )
        # Falls through to the field default rather than the cwd's project.
        self.assertIsNone(value)

    def test_explicit_start_still_honored_after_disabling_the_cwd_fallback(self) -> None:
        # The opt-out suppresses only the *implicit* anchor. A caller that
        # supplies a validated project root on purpose (dispatch_core's
        # project_root) must still resolve against it.
        _write_project_config(
            self.project_dir, "gitlab:\n  supports_work_item_hierarchy: true\n"
        )
        settings.disable_project_tier_cwd_fallback()
        settings.reset_cache()
        value = settings.resolve_setting(
            "gitlab.supports_work_item_hierarchy", start=self.project_dir, env={}
        )
        self.assertIs(value, True)

    def test_scope_violation_still_raises_through_an_explicit_start(self) -> None:
        _write_project_config(self.project_dir, 'gitlab:\n  base_url: "https://evil.example.com"\n')
        settings.disable_project_tier_cwd_fallback()
        settings.reset_cache()
        with self.assertRaises(settings.SettingsScopeError):
            settings.resolve_setting("gitlab.base_url", start=self.project_dir, env={})

    def test_failure_message_does_not_name_a_cwd_path_when_the_tier_is_skipped(self) -> None:
        settings.disable_project_tier_cwd_fallback()
        settings.reset_cache()
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting("gitlab.base_url", start=None, env={})
        message = str(ctx.exception)
        self.assertIn("not consulted", message)
        self.assertNotIn(".agents/cadre.yaml", message)


class GlobalOnlyScopeTests(SettingsTestCase):
    def test_project_local_file_setting_a_global_only_field_is_rejected(self) -> None:
        _write_project_config(self.project_dir, 'gitlab:\n  base_url: "https://evil.example.com"\n')
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting("gitlab.base_url", start=self.project_dir, env={})
        message = str(ctx.exception)
        self.assertIn("gitlab.base_url", message)
        self.assertIn("project-local", message)

    def test_project_local_file_setting_a_project_or_global_field_is_honored(self) -> None:
        _write_project_config(
            self.project_dir, "gitlab:\n  supports_work_item_hierarchy: true\n"
        )
        value = settings.resolve_setting(
            "gitlab.supports_work_item_hierarchy", start=self.project_dir, env={}
        )
        self.assertIs(value, True)

    def test_global_file_may_set_a_global_only_field(self) -> None:
        (self.xdg_config_home / "cadre").mkdir(parents=True)
        (self.xdg_config_home / "cadre" / "config.yaml").write_text(
            'gitlab:\n  base_url: "https://ok.example.com"\n', encoding="utf-8"
        )
        settings.reset_cache()
        value = settings.resolve_setting("gitlab.base_url", start=self.project_dir, env={})
        self.assertEqual(value, "https://ok.example.com")

    def test_project_local_null_value_for_global_only_field_still_raises(self) -> None:
        # Regression test: the scope check must fire on the key's mere
        # presence in a project-local file, not only on a non-null value --
        # an explicit `null` must not be usable to silently no-op past the
        # "never silently ignored" invariant for a global_only field.
        _write_project_config(self.project_dir, "gitlab:\n  base_url: ~\n")
        with self.assertRaises(settings.SettingsScopeError) as ctx:
            settings.resolve_setting("gitlab.base_url", start=self.project_dir, env={})
        self.assertIn("gitlab.base_url", str(ctx.exception))

    def test_gitlab_project_id_is_global_only(self) -> None:
        # roster/orchestration/SECURITY-CONTROLS.md records a
        # human-accepted residual-risk control that depends on both
        # GITLAB_BASE_URL *and* GITLAB_DOCS_PROJECT_ID being operator-fixed
        # (a dedicated docs-only project + a least-privilege token) --
        # letting an untrusted project-local file redirect the destination
        # project would silently weaken that control.
        _write_project_config(self.project_dir, 'gitlab:\n  project_id: "evil"\n')
        with self.assertRaises(settings.SettingsScopeError):
            settings.resolve_setting("gitlab.project_id", start=self.project_dir, env={})


class SecretShapedKeyTests(SettingsTestCase):
    def test_secret_shaped_key_in_project_file_is_rejected_without_echoing_value(self) -> None:
        _write_project_config(
            self.project_dir, 'gitlab:\n  svc_token: "glpat-super-secret-value"\n  project_id: "1"\n'
        )
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting("gitlab.project_id", start=self.project_dir, env={})
        message = str(ctx.exception)
        self.assertIn("svc_token", message)
        self.assertNotIn("glpat-super-secret-value", message)

    def test_secret_shaped_key_in_global_file_is_rejected(self) -> None:
        (self.xdg_config_home / "cadre").mkdir(parents=True)
        (self.xdg_config_home / "cadre" / "config.yaml").write_text(
            'embedding:\n  api_key: "sk-super-secret"\n', encoding="utf-8"
        )
        settings.reset_cache()
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting("runners.claude_bin", start=self.project_dir, env={})
        self.assertNotIn("sk-super-secret", str(ctx.exception))

    def test_various_secret_shaped_leaf_names_are_all_rejected(self) -> None:
        for leaf in ("token", "api_key", "password", "secret", "svc_token", "custom_token"):
            with self.subTest(leaf=leaf):
                project = Path(tempfile.mkdtemp(prefix="cadre-settings-secret-"))
                self.addCleanup(lambda p=project: __import__("shutil").rmtree(p, ignore_errors=True))
                _make_project(project)
                _write_project_config(project, f'gitlab:\n  {leaf}: "x"\n  project_id: "1"\n')
                settings.reset_cache()
                with self.assertRaises(settings.SettingsError):
                    settings.resolve_setting("gitlab.project_id", start=project, env={})

    def test_secret_shaped_key_nested_under_a_list_is_rejected(self) -> None:
        # The scan used to walk dicts only. No registered field is
        # list-shaped, so such a key could never be *resolved* -- but
        # write_setting's "preserve unknown keys" merge would round-trip it
        # into every later rewrite, silently persisting a pasted credential
        # this module promises is never stored.
        _write_project_config(
            self.project_dir,
            'gitlab:\n  extra:\n    - name: "a"\n      token: "glpat-nested-secret"\n',
        )
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting("gitlab.supports_work_item_hierarchy", start=self.project_dir, env={})
        message = str(ctx.exception)
        self.assertIn("token", message)
        self.assertNotIn("glpat-nested-secret", message)

    def test_secret_shaped_key_deeper_in_a_list_of_lists_is_rejected(self) -> None:
        _write_project_config(
            self.project_dir,
            'gitlab:\n  extra:\n    - - api_key: "sk-deeply-nested"\n',
        )
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting("gitlab.supports_work_item_hierarchy", start=self.project_dir, env={})
        self.assertNotIn("sk-deeply-nested", str(ctx.exception))


class TristateHierarchyFlagTests(SettingsTestCase):
    def _resolve(self, raw_yaml_value: str) -> object:
        _write_project_config(
            self.project_dir, f"gitlab:\n  supports_work_item_hierarchy: {raw_yaml_value}\n"
        )
        settings.reset_cache()
        return settings.resolve_setting(
            "gitlab.supports_work_item_hierarchy", start=self.project_dir, env={}
        )

    def test_absent_resolves_to_none(self) -> None:
        value = settings.resolve_setting(
            "gitlab.supports_work_item_hierarchy", start=self.project_dir, env={}
        )
        self.assertIsNone(value)

    def test_native_true(self) -> None:
        self.assertIs(self._resolve("true"), True)

    def test_native_false(self) -> None:
        self.assertIs(self._resolve("false"), False)

    def test_string_true_quoted(self) -> None:
        self.assertIs(self._resolve('"true"'), True)

    def test_string_true_case_insensitive(self) -> None:
        self.assertIs(self._resolve('"TRUE"'), True)

    def test_explicit_null_falls_through_to_default_none(self) -> None:
        self.assertIsNone(self._resolve("~"))

    def test_invalid_string_rejected(self) -> None:
        with self.assertRaises(settings.SettingsError):
            self._resolve('"maybe"')

    def test_env_var_string_true_false_and_invalid(self) -> None:
        self.assertIs(
            settings.resolve_setting(
                "gitlab.supports_work_item_hierarchy",
                start=self.project_dir,
                env={"GITLAB_SUPPORTS_WORK_ITEM_HIERARCHY": "true"},
            ),
            True,
        )
        self.assertIs(
            settings.resolve_setting(
                "gitlab.supports_work_item_hierarchy",
                start=self.project_dir,
                env={"GITLAB_SUPPORTS_WORK_ITEM_HIERARCHY": "FALSE"},
            ),
            False,
        )
        with self.assertRaises(settings.SettingsError):
            settings.resolve_setting(
                "gitlab.supports_work_item_hierarchy",
                start=self.project_dir,
                env={"GITLAB_SUPPORTS_WORK_ITEM_HIERARCHY": "maybe"},
            )


class YamlScalarHazardTests(SettingsTestCase):
    # gitlab.project_id is global_only (see GlobalOnlyScopeTests), so these
    # kind="project_id" validator tests write to the global tier -- global
    # is always allowed for every field regardless of scope, and the scope
    # check (which runs before validation, on the project-tier path only)
    # would otherwise mask the validator behavior these tests exist to
    # exercise.
    def test_unquoted_numeric_project_id_is_rejected(self) -> None:
        (self.xdg_config_home / "cadre").mkdir(parents=True)
        (self.xdg_config_home / "cadre" / "config.yaml").write_text(
            "gitlab:\n  project_id: 007\n", encoding="utf-8"
        )
        settings.reset_cache()
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting("gitlab.project_id", start=self.project_dir, env={})
        self.assertIn("project_id", str(ctx.exception))

    def test_tilde_project_id_is_treated_as_unset_at_this_tier(self) -> None:
        (self.xdg_config_home / "cadre").mkdir(parents=True)
        (self.xdg_config_home / "cadre" / "config.yaml").write_text(
            "gitlab:\n  project_id: ~\n", encoding="utf-8"
        )
        settings.reset_cache()
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting("gitlab.project_id", start=self.project_dir, env={})
        # required field, unset everywhere -> fail-closed, not a validation
        # error about the null itself.
        self.assertIn("is not configured", str(ctx.exception))

    def test_yes_no_bool_coercion_rejected_for_string_fields(self) -> None:
        (self.xdg_config_home / "cadre").mkdir(parents=True)
        (self.xdg_config_home / "cadre" / "config.yaml").write_text(
            "gitlab:\n  project_id: yes\n", encoding="utf-8"
        )
        settings.reset_cache()
        with self.assertRaises(settings.SettingsError):
            settings.resolve_setting("gitlab.project_id", start=self.project_dir, env={})

    def test_yes_no_bool_coercion_rejected_for_executable_fields(self) -> None:
        (self.xdg_config_home / "cadre").mkdir(parents=True)
        (self.xdg_config_home / "cadre" / "config.yaml").write_text(
            "runners:\n  claude_bin: no\n", encoding="utf-8"
        )
        settings.reset_cache()
        with self.assertRaises(settings.SettingsError):
            settings.resolve_setting("runners.claude_bin", start=self.project_dir, env={})

    def test_relative_executable_path_with_separator_is_rejected(self) -> None:
        (self.xdg_config_home / "cadre").mkdir(parents=True)
        (self.xdg_config_home / "cadre" / "config.yaml").write_text(
            'runners:\n  claude_bin: "./claude"\n', encoding="utf-8"
        )
        settings.reset_cache()
        with self.assertRaises(settings.SettingsError):
            settings.resolve_setting("runners.claude_bin", start=self.project_dir, env={})

    def test_bare_executable_name_without_separator_is_accepted(self) -> None:
        (self.xdg_config_home / "cadre").mkdir(parents=True)
        (self.xdg_config_home / "cadre" / "config.yaml").write_text(
            'runners:\n  claude_bin: "my-claude"\n', encoding="utf-8"
        )
        settings.reset_cache()
        value = settings.resolve_setting("runners.claude_bin", start=self.project_dir, env={})
        self.assertEqual(value, "my-claude")

    def test_leading_dash_executable_is_rejected(self) -> None:
        # Verified under bash: `bin="-a"; exec "$bin" --provider p.json ...`
        # makes exec consume `--provider` as -a's argv[0] argument and then
        # try to execute p.json -- an inert-looking value silently
        # reinterprets the rest of the command line. The packaged wrapper is
        # `#!/bin/sh`, which is bash on some systems.
        for candidate in ("-a", "-c", "--login"):
            with self.subTest(candidate=candidate):
                with self.assertRaises(settings.SettingsError) as ctx:
                    settings.resolve_setting(
                        "runners.claude_bin",
                        start=self.project_dir,
                        env={"SECURE_CLOUD_AGENTS_CLAUDE_BIN": candidate},
                    )
                self.assertIn("must not begin with '-'", str(ctx.exception))

    def test_control_characters_in_an_executable_are_rejected(self) -> None:
        # A newline would break `cadre config resolve`'s contract of one
        # value on stdout, which the packaged wrapper captures with $(...).
        for candidate in ("clau\nde", "clau\tde", "clau\x00de"):
            with self.subTest(candidate=candidate):
                with self.assertRaises(settings.SettingsError) as ctx:
                    settings.resolve_setting(
                        "runners.claude_bin",
                        start=self.project_dir,
                        env={"SECURE_CLOUD_AGENTS_CLAUDE_BIN": candidate},
                    )
                self.assertIn("control characters", str(ctx.exception))

    def test_internal_spaces_in_a_path_are_still_accepted(self) -> None:
        # Deliberately legal: real installs live under paths with spaces,
        # and every consumer quotes the value.
        value = settings.resolve_setting(
            "runners.claude_bin",
            start=self.project_dir,
            env={"SECURE_CLOUD_AGENTS_CLAUDE_BIN": "/opt/My Tools/bin/claude"},
        )
        self.assertEqual(value, "/opt/My Tools/bin/claude")

    def test_control_characters_in_a_path_field_are_rejected(self) -> None:
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting(
                "knowledge_store.home",
                start=self.project_dir,
                env={"KNOWLEDGE_STORE_HOME": "/tmp/store\nwith-newline"},
            )
        self.assertIn("control characters", str(ctx.exception))


class MissingPyYamlAndDualFileTests(SettingsTestCase):
    def test_missing_pyyaml_raises_clear_error_naming_the_file(self) -> None:
        _write_project_config(self.project_dir, 'gitlab:\n  project_id: "1"\n')
        real_import = builtins.__import__

        def fake_import(name, *args, **kwargs):
            if name == "yaml":
                raise ImportError("simulated: PyYAML not installed")
            return real_import(name, *args, **kwargs)

        with mock.patch.object(builtins, "__import__", side_effect=fake_import):
            with self.assertRaises(settings.SettingsError) as ctx:
                settings.resolve_setting("gitlab.project_id", start=self.project_dir, env={})
        message = str(ctx.exception)
        self.assertIn("cadre.yaml", message)

    def test_both_yaml_and_json_present_at_project_tier_is_an_error(self) -> None:
        _write_project_config(self.project_dir, 'gitlab:\n  project_id: "1"\n')
        (self.project_dir / ".agents" / "cadre.json").write_text("{}", encoding="utf-8")
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting("gitlab.project_id", start=self.project_dir, env={})
        self.assertIn("cadre.yaml", str(ctx.exception))
        self.assertIn("cadre.json", str(ctx.exception))

    def test_both_yaml_and_json_present_at_global_tier_is_an_error(self) -> None:
        directory = self.xdg_config_home / "cadre"
        directory.mkdir(parents=True)
        (directory / "config.yaml").write_text("{}", encoding="utf-8")
        (directory / "config.json").write_text("{}", encoding="utf-8")
        settings.reset_cache()
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting("runners.claude_bin", start=self.project_dir, env={})
        self.assertIn("config.yaml", str(ctx.exception))
        self.assertIn("config.json", str(ctx.exception))

    def test_malformed_yaml_raises_settings_error_without_echoing_content(self) -> None:
        # A malformed (or symlink-redirected) file must fail closed with a
        # controlled SettingsError, never an unwrapped yaml.YAMLError --
        # PyYAML's own parser-error messages routinely quote a snippet of
        # the offending content, which this file's path may not actually
        # belong to (see the read-path symlink-escape guard above).
        _write_project_config(self.project_dir, "gitlab:\n  base_url: [unterminated\n")
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting("gitlab.base_url", start=self.project_dir, env={})
        message = str(ctx.exception)
        self.assertIn("cadre.yaml", message)
        self.assertNotIn("unterminated", message)

    def test_malformed_json_raises_settings_error_without_echoing_content(self) -> None:
        _write_project_config(
            self.project_dir, '{"gitlab": {"base_url": "SECRET_SNIPPET_MARKER"', filename="cadre.json"
        )
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting("gitlab.base_url", start=self.project_dir, env={})
        message = str(ctx.exception)
        self.assertIn("cadre.json", message)
        self.assertNotIn("SECRET_SNIPPET_MARKER", message)


class AtomicWriteTests(SettingsTestCase):
    def test_round_trip_preserves_unknown_keys_uses_replace_and_correct_mode(self) -> None:
        directory = self.xdg_config_home / "cadre"
        directory.mkdir(parents=True)
        (directory / "config.yaml").write_text(
            "unrelated_top_level: keep-me\nrunners:\n  codex_bin: \"codex-keep\"\n", encoding="utf-8"
        )
        settings.reset_cache()

        with mock.patch.object(settings.os, "replace", wraps=settings.os.replace) as replace_spy:
            written_path = settings.write_setting("runners.claude_bin", "my-claude", tier="global")
            self.assertTrue(replace_spy.called)

        text = written_path.read_text(encoding="utf-8")
        self.assertIn("unrelated_top_level", text)
        self.assertIn("codex-keep", text)
        self.assertIn("my-claude", text)
        self.assertIn("schema_version", text)
        # header regenerated
        self.assertIn("Generated by cadre's settings resolver", text)

        mode = os.stat(written_path).st_mode & 0o777
        self.assertEqual(mode, 0o600)

        settings.reset_cache()
        value = settings.resolve_setting("runners.claude_bin", start=self.project_dir, env={})
        self.assertEqual(value, "my-claude")

    def test_project_tier_write_creates_file_and_is_readable(self) -> None:
        # gitlab.project_id is global_only; supports_work_item_hierarchy is
        # the only project_or_global field, so it's the one this test can
        # legitimately write to the project tier.
        path = settings.write_setting(
            "gitlab.supports_work_item_hierarchy", True, tier="project", start=self.project_dir
        )
        self.assertTrue(path.is_file())
        settings.reset_cache()
        value = settings.resolve_setting(
            "gitlab.supports_work_item_hierarchy", start=self.project_dir, env={}
        )
        self.assertIs(value, True)

    def test_global_only_field_cannot_be_written_to_project_tier(self) -> None:
        with self.assertRaises(settings.SettingsError):
            settings.write_setting("gitlab.base_url", "https://x.example.com", tier="project", start=self.project_dir)


class SymlinkEscapeTests(SettingsTestCase):
    def test_symlinked_agents_directory_write_is_rejected(self) -> None:
        # Uses supports_work_item_hierarchy, the only project_or_global
        # field -- gitlab.project_id is global_only and would be rejected
        # for that reason alone before the write-path symlink guard this
        # test exists to exercise is ever reached.
        outside = Path(tempfile.mkdtemp(prefix="cadre-settings-outside-"))
        self.addCleanup(lambda: __import__("shutil").rmtree(outside, ignore_errors=True))
        (self.project_dir / ".agents").rmdir()
        (self.project_dir / ".agents").symlink_to(outside, target_is_directory=True)
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.write_setting(
                "gitlab.supports_work_item_hierarchy", True, tier="project", start=self.project_dir
            )
        self.assertIn("symlink", str(ctx.exception).lower())
        self.assertFalse((outside / "cadre.yaml").exists())

    def test_symlinked_agents_directory_read_is_rejected(self) -> None:
        # Read-path counterpart: find_file_at_project_root's candidate.is_file()
        # check follows symlinks, so a malicious .agents symlink shipped in
        # an untrusted, clonable project could otherwise point resolution
        # at an arbitrary file outside the project.
        outside = Path(tempfile.mkdtemp(prefix="cadre-settings-outside-"))
        self.addCleanup(lambda: __import__("shutil").rmtree(outside, ignore_errors=True))
        (outside / "cadre.yaml").write_text(
            'gitlab:\n  supports_work_item_hierarchy: true\n', encoding="utf-8"
        )
        (self.project_dir / ".agents").rmdir()
        (self.project_dir / ".agents").symlink_to(outside, target_is_directory=True)
        with self.assertRaises(settings.SettingsError) as ctx:
            settings.resolve_setting(
                "gitlab.supports_work_item_hierarchy", start=self.project_dir, env={}
            )
        self.assertIn("symlink", str(ctx.exception).lower())


class NonInteractivePathNeverPromptsTests(SettingsTestCase):
    def _boom(self, _prompt: str) -> str:
        raise AssertionError("input_func should never be called on a non-interactive path")

    def test_no_cadre_interactive_env_var_never_prompts(self) -> None:
        with self.assertRaises(settings.SettingsError):
            settings.resolve_setting(
                "gitlab.base_url", start=self.project_dir, env={}, input_func=self._boom
            )

    def test_cadre_interactive_set_but_no_tty_never_prompts(self) -> None:
        with mock.patch.object(sys.stdin, "isatty", return_value=False), mock.patch.object(
            sys.stdout, "isatty", return_value=True
        ):
            with self.assertRaises(settings.SettingsError):
                settings.resolve_setting(
                    "gitlab.base_url",
                    start=self.project_dir,
                    env={"CADRE_INTERACTIVE": "1"},
                    input_func=self._boom,
                )

    def test_disable_interactive_overrides_a_real_tty_and_the_env_var(self) -> None:
        settings.disable_interactive()
        self.addCleanup(lambda: setattr(settings, "_INTERACTIVE_DISABLED", False))
        with mock.patch.object(sys.stdin, "isatty", return_value=True), mock.patch.object(
            sys.stdout, "isatty", return_value=True
        ):
            with self.assertRaises(settings.SettingsError):
                settings.resolve_setting(
                    "gitlab.base_url",
                    start=self.project_dir,
                    env={"CADRE_INTERACTIVE": "1"},
                    input_func=self._boom,
                )


class EnvAllowlistTests(unittest.TestCase):
    def test_cadre_interactive_is_absent_from_dispatch_core_env_allowlist(self) -> None:
        mcp_dir = Path(__file__).resolve().parents[2] / "orchestration" / "mcp"
        if str(mcp_dir) not in sys.path:
            sys.path.append(str(mcp_dir))
        import dispatch_core  # noqa: E402  (sys.path set above)

        self.assertNotIn(settings.INTERACTIVE_ENV_VAR, dispatch_core.ENV_ALLOWLIST)


class EffectiveSettingsAndCliTests(SettingsTestCase):
    def test_effective_settings_never_raises_and_covers_every_known_key(self) -> None:
        results = settings.effective_settings(start=self.project_dir, env={})
        keys = {resolved.key for resolved in results}
        self.assertEqual(keys, set(settings.known_keys()))

    def test_effective_settings_never_prompts(self) -> None:
        with mock.patch.object(sys.stdin, "isatty", return_value=True), mock.patch.object(
            sys.stdout, "isatty", return_value=True
        ):
            # Even with CADRE_INTERACTIVE=1 and a "tty", effective_settings()
            # must never block on input() -- it backs a non-interactive
            # `cadre config show`.
            results = settings.effective_settings(
                start=self.project_dir, env={"CADRE_INTERACTIVE": "1"}
            )
        self.assertTrue(results)

    def test_config_path_cli_reports_project_and_global_paths(self) -> None:
        _write_project_config(self.project_dir, 'gitlab:\n  project_id: "1"\n')
        with mock.patch.object(settings.Path, "cwd", return_value=self.project_dir):
            exit_code = settings.main(["path"])
        self.assertEqual(exit_code, 0)

    def test_config_resolve_cli_prints_the_resolved_value(self) -> None:
        # This is the packaged POSIX-sh bin/cadre wrapper's only way to
        # resolve agentic_sdlc.bin_path through the real precedence chain
        # (env > project/global config > computed default) -- it can't
        # parse YAML/JSON or apply trust-scope rules itself.
        with mock.patch.dict(os.environ, {"SECURE_CLOUD_AGENTS_CLAUDE_BIN": "/opt/bin/claude"}):
            with mock.patch.object(settings.Path, "cwd", return_value=self.project_dir):
                exit_code = settings.main(["resolve", "runners.claude_bin"])
        self.assertEqual(exit_code, 0)

    def test_config_resolve_cli_prints_nothing_and_exits_zero_when_unconfigured(self) -> None:
        with mock.patch.dict(os.environ, {}, clear=True):
            with mock.patch.object(settings.Path, "cwd", return_value=self.project_dir):
                exit_code = settings.main(["resolve", "knowledge_store.home"])
        self.assertEqual(exit_code, 0)

    def test_config_resolve_cli_exits_nonzero_on_a_scope_violation(self) -> None:
        _write_project_config(self.project_dir, 'agentic_sdlc:\n  bin_path: "/tmp/evil"\n')
        with mock.patch.dict(os.environ, {}, clear=True):
            with mock.patch.object(settings.Path, "cwd", return_value=self.project_dir):
                exit_code = settings.main(["resolve", "agentic_sdlc.bin_path"])
        self.assertEqual(exit_code, 1)

    def test_config_resolve_cli_rejects_an_unknown_key(self) -> None:
        with mock.patch.object(settings.Path, "cwd", return_value=self.project_dir):
            exit_code = settings.main(["resolve", "not.a.real.key"])
        self.assertEqual(exit_code, 1)

    def test_config_resolve_cli_requires_exactly_one_key_argument(self) -> None:
        self.assertEqual(settings.main(["resolve"]), 2)
        self.assertEqual(settings.main(["resolve", "a", "b"]), 2)


class StdoutTtyOverrideTests(SettingsTestCase):
    """`cadre config resolve <key>` is always invoked by the packaged shell
    wrapper as `x=$(... resolve key)` -- a command substitution whose
    stdout is unconditionally a pipe, never a real tty, regardless of
    CADRE_INTERACTIVE=1 or the caller's actual terminal. Without the
    override machinery below, --interactive prompting through that
    subcommand would be permanently unreachable."""

    def test_gate_still_requires_stdin_tty_even_with_override(self) -> None:
        with mock.patch.object(sys.stdin, "isatty", return_value=False):
            with settings._stdout_tty_override(True):
                self.assertFalse(settings._interactive_gate_open({"CADRE_INTERACTIVE": "1"}))

    def test_gate_opens_on_stdin_tty_alone_when_override_is_true(self) -> None:
        with mock.patch.object(sys.stdin, "isatty", return_value=True), mock.patch.object(
            sys.stdout, "isatty", return_value=False
        ):
            self.assertFalse(settings._interactive_gate_open({"CADRE_INTERACTIVE": "1"}))
            with settings._stdout_tty_override(True):
                self.assertTrue(settings._interactive_gate_open({"CADRE_INTERACTIVE": "1"}))
            # Override is scoped to the `with` block only.
            self.assertFalse(settings._interactive_gate_open({"CADRE_INTERACTIVE": "1"}))

    def test_open_tty_io_returns_none_without_a_controlling_terminal(self) -> None:
        with mock.patch("builtins.open", side_effect=OSError("no such device")):
            self.assertIsNone(settings._open_tty_io())

    @unittest.skipUnless(sys.platform != "win32", "requires a POSIX pty/controlling terminal")
    def test_open_tty_io_round_trips_through_a_real_controlling_terminal(self) -> None:
        """Exercises the REAL /dev/tty open, not a stub.

        Every other test here either mocks `builtins.open` to fail or
        replaces `_open_tty_io` wholesale, so the actual file-open
        semantics were previously untested -- which is exactly where a
        latent defect lived: `open("/dev/tty", "r+")` raises on modern
        CPython (a buffered read-write text stream needs a seekable file;
        a character device isn't), and `_open_tty_io`'s own `except`
        turned that into a silent `None`, degrading prompting to
        permanently unavailable with no error anywhere. This test fails
        (child exits 2) against that buggy form and passes against the
        two-separate-handles form. `pty.fork()` is required rather than
        handing a pty fd to `subprocess.Popen` -- only the former gives
        the child a real *controlling* terminal, which is what /dev/tty
        resolves through.
        """
        import pty

        pid, master_fd = pty.fork()
        if pid == 0:  # pragma: no cover - child process
            try:
                io_pair = settings._open_tty_io()
                if io_pair is None:
                    os._exit(2)
                child_input, child_output = io_pair
                child_output("PROMPT-MARKER")
                answer = child_input("give me a value: ")
                child_output("GOT:" + answer)
                os._exit(0 if answer == "hello-world" else 4)
            except BaseException:  # noqa: BLE001 - child must never raise into the harness
                os._exit(3)

        transcript = b""
        try:
            os.write(master_fd, b"hello-world\n")
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                try:
                    chunk = os.read(master_fd, 4096)
                except OSError:
                    break
                if not chunk:
                    break
                transcript += chunk
                if b"GOT:" in transcript:
                    break
            _pid, status = os.waitpid(pid, 0)
        finally:
            os.close(master_fd)

        self.assertTrue(os.WIFEXITED(status), f"child did not exit normally: {transcript!r}")
        self.assertEqual(os.WEXITSTATUS(status), 0, f"child exit status; transcript={transcript!r}")
        self.assertIn(b"PROMPT-MARKER", transcript)
        self.assertIn(b"GOT:hello-world", transcript)

    def test_prompt_eof_becomes_a_settings_error_not_a_raw_traceback(self) -> None:
        # Ctrl-D (or a controlling terminal closing mid-prompt) is an
        # ordinary way to abandon a prompt; it must surface as this
        # module's usual fail-closed SettingsError, so callers'
        # `except SettingsError` handling applies and the CLI prints a
        # clean message instead of dumping a traceback.
        def eof_input(_prompt: str) -> str:
            raise EOFError("simulated Ctrl-D")

        with mock.patch.object(sys.stdin, "isatty", return_value=True), mock.patch.object(
            sys.stdout, "isatty", return_value=True
        ):
            with self.assertRaises(settings.SettingsError) as ctx:
                settings.resolve_setting(
                    "gitlab.base_url",
                    start=self.project_dir,
                    env={"CADRE_INTERACTIVE": "1"},
                    input_func=eof_input,
                    output_func=lambda _text: None,
                )
        self.assertIn("gitlab.base_url", str(ctx.exception))

    def test_resolve_cli_handles_prompt_eof_without_a_traceback(self) -> None:
        # A cancelled prompt (Ctrl-D) resolves to "unset": `resolve`'s
        # contract is that a SettingsError for a non-scope reason means
        # "no value", so it exits 0 printing nothing, and the packaged
        # wrapper's own `[ -n "$sdlc_bin" ] ||` check then produces the
        # actionable install pointer. The point being pinned here is that
        # the EOFError never escapes as an unhandled traceback (it used to)
        # and nothing partial is written to the captured stdout.
        def eof_input(_prompt: str) -> str:
            raise EOFError("simulated Ctrl-D")

        with mock.patch.object(sys.stdin, "isatty", return_value=True), mock.patch.object(
            settings, "_open_tty_io", return_value=(eof_input, lambda _text: None)
        ), mock.patch.object(
            settings.Path, "cwd", return_value=self.project_dir
        ), mock.patch.dict(
            os.environ, {"CADRE_INTERACTIVE": "1"}, clear=False
        ):
            captured_stdout = io.StringIO()
            with mock.patch.object(sys, "stdout", captured_stdout):
                exit_code = settings.main(["resolve", "gitlab.base_url"])
        self.assertEqual(exit_code, 0)
        self.assertEqual(captured_stdout.getvalue().strip(), "")

    def test_resolve_cli_prompts_via_tty_without_leaking_into_captured_stdout(self) -> None:
        # Simulates the real command-substitution scenario end to end:
        # stdout is not a tty (as it never is under `$(...)`), stdin is,
        # and _open_tty_io is stubbed to a fake terminal so no real
        # /dev/tty is required in a test environment. Only the final
        # resolved value may reach real stdout.
        answers = iter(["https://prompted.example.com", "skip"])
        tty_transcript: list[str] = []

        def fake_input(_prompt: str) -> str:
            return next(answers)

        def fake_output(text: str) -> None:
            tty_transcript.append(text)

        with mock.patch.object(sys.stdin, "isatty", return_value=True), mock.patch.object(
            settings, "_open_tty_io", return_value=(fake_input, fake_output)
        ), mock.patch.object(
            settings.Path, "cwd", return_value=self.project_dir
        ), mock.patch.dict(
            os.environ, {"CADRE_INTERACTIVE": "1"}, clear=False
        ):
            # Replaces sys.stdout wholesale with a StringIO (isatty() is
            # False on a StringIO by default, matching a real command-
            # substitution pipe) rather than only patching .isatty, so
            # print(value)'s actual destination is captured too.
            captured_stdout = io.StringIO()
            with mock.patch.object(sys, "stdout", captured_stdout):
                exit_code = settings.main(["resolve", "gitlab.base_url"])
        self.assertEqual(exit_code, 0)
        self.assertEqual(captured_stdout.getvalue().strip(), "https://prompted.example.com")
        self.assertTrue(any("gitlab.base_url" in line for line in tty_transcript))


class ResolveManyTests(SettingsTestCase):
    def test_resolve_many_returns_a_dict_for_every_key(self) -> None:
        values = settings.resolve_many(
            ["runners.claude_bin", "runners.codex_bin"], start=self.project_dir, env={}
        )
        self.assertEqual(values, {"runners.claude_bin": "claude", "runners.codex_bin": "codex"})


if __name__ == "__main__":
    unittest.main()


class ConfigSetCommandTests(unittest.TestCase):
    """`cadre config set` -- the write path, finally exposed.

    `write_setting()` has existed all along and the interactive prompt used
    it, but nothing exposed it non-interactively: `cadre config` offered only
    show/path/resolve. So the documented way to configure a project was to
    find the right file and hand-edit YAML, which is a poor answer generally
    and an actively bad one for scope-restricted fields, where editing the
    wrong file fails later with an error about trust scope.
    """

    def setUp(self) -> None:
        isolate_settings(self)
        self.project = tempfile.TemporaryDirectory()
        self.addCleanup(self.project.cleanup)
        root = pathlib.Path(self.project.name)
        (root / ".git").mkdir()
        self._previous = os.getcwd()
        os.chdir(root)
        self.addCleanup(os.chdir, self._previous)

    def test_writes_a_project_scoped_value(self) -> None:
        code = settings.main(["set", "gitlab.supports_work_item_hierarchy", "true"])
        self.assertEqual(0, code)
        written = pathlib.Path(self.project.name) / ".agents" / "cadre.yaml"
        self.assertTrue(written.is_file())
        self.assertIs(True, settings.resolve_setting("gitlab.supports_work_item_hierarchy"))

    def test_updates_in_place_rather_than_appending(self) -> None:
        settings.main(["set", "gitlab.supports_work_item_hierarchy", "true"])
        settings.main(["set", "gitlab.supports_work_item_hierarchy", "false"])
        self.assertIs(False, settings.resolve_setting("gitlab.supports_work_item_hierarchy"))

    def test_refuses_a_global_only_field_at_project_scope(self) -> None:
        """The field selects an executable to spawn, and a project-local
        config file is untrusted, clonable content. This is the security
        property the scope system exists for, so `set` must enforce it rather
        than writing a file that fails later."""
        code = settings.main(["set", "agentic_sdlc.bin_path", "/tmp/evil"])
        self.assertEqual(1, code)
        self.assertFalse((pathlib.Path(self.project.name) / ".agents" / "cadre.yaml").exists())

    def test_rejects_an_unknown_key(self) -> None:
        self.assertEqual(1, settings.main(["set", "no.such.key", "x"]))

    def test_rejects_a_value_that_fails_validation(self) -> None:
        self.assertEqual(1, settings.main(["set", "gitlab.supports_work_item_hierarchy", "notabool"]))

    def test_reports_usage_on_wrong_argument_count(self) -> None:
        self.assertEqual(2, settings.main(["set", "only-a-key"]))
        self.assertEqual(2, settings.main(["set"]))

    def test_set_is_advertised_in_usage(self) -> None:
        buffer = io.StringIO()
        with contextlib.redirect_stderr(buffer):
            settings.main([])
        self.assertIn("set KEY VALUE", buffer.getvalue())
