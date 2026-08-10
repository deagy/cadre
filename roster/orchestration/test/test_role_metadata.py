"""Unit tests for role_metadata.py and generate_role_metadata.py.

Every role's AGENT.md now carries `---`-delimited frontmatter, and
roster/catalog.yaml / roster/orchestration/routing.yaml are purely generated
output derived from it -- never an input for role metadata. The single most
important test here is test_generator_is_identity_on_current_repository: run
the generator against a full copy of the real repository tree and assert
roster/catalog.yaml and roster/orchestration/routing.yaml come back
byte-identical. Everything else uses small synthetic, frontmatter-only
fixtures.
"""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
import tempfile
import unittest
import unittest.mock
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
REPOSITORY_ROOT = ROOT.parent.parent
sys.path.insert(0, str(ROOT / "src"))

import generate_role_metadata as grm  # noqa: E402
import role_metadata as rm  # noqa: E402

HEADER_TEMPLATE = "version: 1\nagents:\n"


def _write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def _routing_text(knowledge_focus: dict[str, str], extra: dict | None = None) -> str:
    payload = {"version": 1, "ignored_gates": [], "routes": [], "risk_rules": []}
    if extra:
        payload.update(extra)
    payload["knowledge_focus"] = knowledge_focus
    return json.dumps(payload, indent=2) + "\n"


def _catalog_text(entries: dict[str, dict[str, str]]) -> str:
    lines = [HEADER_TEMPLATE]
    for role_id, record in entries.items():
        lines.append(f"  {role_id}:\n")
        for field in grm.CATALOG_FIELD_ORDER:
            lines.append(f"    {field}: {record[field]}\n")
    return "".join(lines)


def _record(definition: str, **overrides: str) -> dict[str, str]:
    """A catalog.yaml-shaped record (used only to build the fixture's
    *expected/rendered* catalog.yaml content, never as generator input).
    """
    record = {
        "definition": definition,
        "phase": "build",
        "capability": "code_author",
        "model": "sonnet",
        "codex_model": "gpt-5.6-terra",
        "reasoning_effort": "medium",
    }
    record.update(overrides)
    return record


def _write_migrated_role(
    agents_root: Path, relative_dir: str, role_id: str, knowledge_focus: str, **overrides: str
) -> str:
    """Write a migrated (frontmatter) `AGENT.md` for `role_id` under
    `agents_root/relative_dir/`. Returns the definition path (relative to
    `agents_root`).
    """
    fields = {
        "id": role_id,
        "phase": "build",
        "capability": "code_author",
        "model": "sonnet",
        "codex_model": "gpt-5.6-terra",
        "reasoning_effort": "medium",
        "knowledge_focus": knowledge_focus,
    }
    fields.update(overrides)
    frontmatter = rm.render_frontmatter(fields)
    _write(agents_root / relative_dir / "AGENT.md", frontmatter + f"\n# {role_id}\n\nMigrated role.\n")
    return f"{relative_dir}/AGENT.md"


def _build_two_role_fixture(root: Path) -> None:
    """role-a and role-b are both migrated (frontmatter) roles; catalog.yaml
    and routing.yaml are written to already match what the generator would
    derive from that frontmatter, so `--check` fixtures start clean.
    """
    agents_root = root / "roster"
    definition_a = _write_migrated_role(agents_root, "domain/role-a", "role-a", "role-a knowledge focus")
    definition_b = _write_migrated_role(agents_root, "domain/role-b", "role-b", "role-b knowledge focus")

    catalog_entries = {"role-a": _record(definition_a), "role-b": _record(definition_b)}
    knowledge_focus = {"role-a": "role-a knowledge focus", "role-b": "role-b knowledge focus"}

    _write(agents_root / "catalog.yaml", _catalog_text(catalog_entries))
    _write(agents_root / "orchestration" / "routing.yaml", _routing_text(knowledge_focus))
    _write(agents_root / "catalog-order.txt", "role-a\nrole-b\n")
    _write(agents_root / "_catalog_header.yaml.tmpl", HEADER_TEMPLATE)


def _paths(root: Path) -> tuple[Path, Path, Path, Path, Path]:
    agents_root = root / "roster"
    return (
        agents_root,
        agents_root / "catalog.yaml",
        agents_root / "orchestration" / "routing.yaml",
        agents_root / "catalog-order.txt",
        agents_root / "_catalog_header.yaml.tmpl",
    )


class ScalarRoundTripTests(unittest.TestCase):
    def test_scalar_round_trip(self) -> None:
        values = [
            "plain value",
            "value: with colon-space",
            "value with trailing # comment marker",
            'value with a "double quote" inside',
            "[leading-bracket looks like flow syntax",
            "trailing space ",
            "",
            "normal-hyphenated-value",
        ]
        for value in values:
            with self.subTest(value=value):
                emitted = rm.emit_scalar(value)
                self.assertEqual(value, rm.read_scalar(emitted))

    def test_plain_values_are_not_quoted(self) -> None:
        self.assertEqual("prior architecture decisions", rm.emit_scalar("prior architecture decisions"))

    def test_values_needing_quoting_use_json(self) -> None:
        self.assertEqual('"a: b"', rm.emit_scalar("a: b"))
        self.assertEqual('""', rm.emit_scalar(""))

    def test_embedded_newline_is_quoted(self) -> None:
        # A bare `\n`/`\r` inside a value must always be quoted -- emitted
        # unquoted, it would split into an extra physical line and violate
        # the flat single-line-per-field frontmatter grammar.
        self.assertEqual(json.dumps("line one\nline two"), rm.emit_scalar("line one\nline two"))
        self.assertEqual(json.dumps("line one\rline two"), rm.emit_scalar("line one\rline two"))

    def test_yaml_11_reserved_words_are_quoted_in_any_case(self) -> None:
        # Unquoted, a real YAML 1.1 parser (e.g. yaml.safe_load, used by
        # schema_validate.py on catalog.yaml) would type-coerce any of these
        # to a bool or None instead of leaving them a string, diverging from
        # what this module's own read_scalar would read back.
        reserved_values = [
            "true", "True", "TRUE",
            "false", "False", "FALSE",
            "yes", "Yes", "YES",
            "no", "No", "NO",
            "on", "On", "ON",
            "off", "Off", "OFF",
            "null", "Null", "NULL",
            "~",
        ]
        for value in reserved_values:
            with self.subTest(value=value):
                self.assertEqual(json.dumps(value), rm.emit_scalar(value))

    def test_bare_numeric_values_are_quoted(self) -> None:
        # Unquoted, a real YAML parser resolves these as int/float, not
        # string.
        numeric_values = ["0", "1", "-1", "+1", "42", "3.14", "-0.5", ".5", "1e10", "-1.5e-10"]
        for value in numeric_values:
            with self.subTest(value=value):
                self.assertEqual(json.dumps(value), rm.emit_scalar(value))

    def test_non_numeric_non_reserved_values_stay_unquoted(self) -> None:
        # Guard against over-quoting: a value that merely contains digits or
        # letters resembling (but not equal to) a reserved word/number must
        # still emit unquoted, since it is not actually type-coercion-prone.
        self.assertEqual("v1-release-candidate", rm.emit_scalar("v1-release-candidate"))
        self.assertEqual("truest-form", rm.emit_scalar("truest-form"))
        self.assertEqual("not-really-null", rm.emit_scalar("not-really-null"))

    def test_reserved_word_round_trip_agrees_with_real_yaml_parser(self) -> None:
        # Synthetic round-trip proving yaml.safe_load and this module's own
        # read_scalar now agree on a reserved-word-valued frontmatter field,
        # where they would previously have diverged (yaml.safe_load would
        # have resolved the unquoted token to bool/None, read_scalar would
        # have returned the literal string).
        import yaml

        for value in ("yes", "NULL", "True", "off"):
            with self.subTest(value=value):
                emitted = rm.emit_scalar(value)
                line = f"knowledge_focus: {emitted}\n"
                parsed_by_real_yaml = yaml.safe_load(line)
                self.assertEqual({"knowledge_focus": value}, parsed_by_real_yaml)
                self.assertEqual(value, rm.read_scalar(emitted))


class FrontmatterParsingTests(unittest.TestCase):
    def test_strip_frontmatter_leaves_body_byte_identical(self) -> None:
        body = "# Role Title\n\n## Role\n\nDo the thing. Preserve   spacing and\ttabs.\n"
        frontmatter = rm.render_frontmatter(
            {
                "id": "sample-role",
                "phase": "build",
                "capability": "code_author",
                "model": "sonnet",
                "codex_model": "gpt-5.6-terra",
                "reasoning_effort": "medium",
                "knowledge_focus": "sample knowledge focus",
            }
        )
        text = frontmatter + body
        self.assertEqual(body, rm.strip_frontmatter(text))

    def test_strip_frontmatter_is_a_no_op_on_unmigrated_text(self) -> None:
        text = "# Role\n\nNo frontmatter here.\n"
        self.assertEqual(text, rm.strip_frontmatter(text))

    def test_is_migrated_requires_delimiter_at_byte_zero(self) -> None:
        self.assertTrue(rm.is_migrated("---\nid: x\n---\n"))
        self.assertFalse(rm.is_migrated("\n---\nid: x\n---\n"))
        self.assertFalse(rm.is_migrated("# Role\n---\n"))

    def test_parse_frontmatter_missing_closing_delimiter_raises(self) -> None:
        with self.assertRaisesRegex(ValueError, "no matching closing"):
            rm.parse_frontmatter("---\nid: x\n# Role\n")

    def test_render_frontmatter_requires_all_fields(self) -> None:
        with self.assertRaisesRegex(ValueError, "missing field"):
            rm.render_frontmatter({"id": "x"})

    def test_render_frontmatter_rejects_unknown_fields(self) -> None:
        fields = {
            "id": "sample-role",
            "phase": "build",
            "capability": "code_author",
            "model": "sonnet",
            "codex_model": "gpt-5.6-terra",
            "reasoning_effort": "medium",
            "knowledge_focus": "sample knowledge focus",
            "unexpected_field": "should not be accepted",
        }
        with self.assertRaisesRegex(ValueError, "unknown field.*unexpected_field"):
            rm.render_frontmatter(fields)

    def test_parse_frontmatter_unrecognized_line_raises(self) -> None:
        # A non-blank line inside the frontmatter block that does not match
        # the flat `key: value` shape (no colon at all here).
        with self.assertRaisesRegex(ValueError, "unrecognized frontmatter line"):
            rm.parse_frontmatter("---\nid: x\nthis line has no colon at all\n---\n")

    def test_field_with_embedded_newline_round_trips_through_render_and_parse(self) -> None:
        # Exercises the REAL round-trip path (render_frontmatter ->
        # parse_frontmatter), not the bare emit_scalar/read_scalar pair in
        # isolation (see test_scalar_round_trip above, which never caught
        # this): an unquoted embedded newline would split the frontmatter
        # block into an extra physical line that does not match the flat
        # `key: value` grammar, and parse_frontmatter would raise
        # "unrecognized frontmatter line" trying to read the continuation
        # back.
        fields = {
            "id": "sample-role",
            "phase": "build",
            "capability": "code_author",
            "model": "sonnet",
            "codex_model": "gpt-5.6-terra",
            "reasoning_effort": "medium",
            "knowledge_focus": "line one\nline two\nline three",
        }
        rendered = rm.render_frontmatter(fields)
        parsed_fields, body = rm.parse_frontmatter(rendered + "# Body\n")
        self.assertEqual(fields, parsed_fields)
        self.assertEqual("# Body\n", body)


class OrderFileTests(unittest.TestCase):
    def test_comments_and_blank_lines_are_ignored(self) -> None:
        content = "# header comment\nrole-a\n\n  role-b  # trailing comment\n"
        self.assertEqual(["role-a", "role-b"], rm.parse_order_file(content))

    def test_duplicate_id_raises(self) -> None:
        with self.assertRaisesRegex(ValueError, "duplicate role id"):
            rm.parse_order_file("role-a\nrole-a\n")

    def test_invalid_id_raises(self) -> None:
        with self.assertRaisesRegex(ValueError, "invalid role id"):
            rm.parse_order_file("Role_A\n")


class GeneratorIdentityTests(unittest.TestCase):
    def test_generator_is_identity_on_current_repository(self) -> None:
        with tempfile.TemporaryDirectory(prefix="role-metadata-identity-") as directory:
            copy_root = Path(directory) / "roster"
            shutil.copytree(REPOSITORY_ROOT / "roster", copy_root)
            catalog_path = copy_root / "catalog.yaml"
            routing_path = copy_root / "orchestration" / "routing.yaml"
            before_catalog = catalog_path.read_bytes()
            before_routing = routing_path.read_bytes()

            rendered = grm.generate(
                agents_root=copy_root,
                catalog_path=catalog_path,
                routing_path=routing_path,
                order_path=copy_root / "catalog-order.txt",
                header_template_path=copy_root / "_catalog_header.yaml.tmpl",
            )

            self.assertEqual(before_catalog, rendered[catalog_path].encode("utf-8"))
            self.assertEqual(before_routing, rendered[routing_path].encode("utf-8"))

    def test_check_passes_on_current_tree(self) -> None:
        # The expected file count is derived from the same generate() call
        # the CLI makes (it renders in memory and writes nothing), not
        # hardcoded: this assertion was left asserting a role count three
        # role-additions stale, which fails the build for a reason that has
        # nothing to do with what the test is actually checking -- that
        # --check reports the current tree as current, over the whole
        # rendered set rather than a subset.
        expected_file_count = len(grm.generate())
        generator = ROOT / "src" / "generate_role_metadata.py"
        result = subprocess.run(
            [sys.executable, str(generator), "--check"],
            cwd=REPOSITORY_ROOT,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn(f"{expected_file_count} role metadata files are current", result.stdout)


class ProviderOrphanTests(unittest.TestCase):
    """`generate_role_metadata` deletes files (stale provider wrappers left by
    a removed role), so its orphan path needs the same coverage
    test_generate_authority_aides.py gives its own removal logic.
    """

    def _isolated_provider(self, root: Path) -> Path:
        """A throwaway copy of the real provider/ tree.

        These tests exercise a code path that *deletes files*, so they must not
        mutate the repository's own provider/ -- a failure mid-test would leave
        orphans behind that every later test reading the real tree would then
        trip over. `--provider-root` with the default `--agents-root` renders
        the same content into a different directory, which is exactly what that
        flag combination is for.
        """
        target = root / "provider"
        shutil.copytree(REPOSITORY_ROOT / "provider", target)
        return target

    def _run(self, *extra: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(ROOT / "src" / "generate_role_metadata.py"), *extra],
            check=False, capture_output=True, text=True, encoding="utf-8",
        )

    def _seed_orphans(self, provider: Path) -> list[Path]:
        orphans = [
            provider / "codex-agents" / "agents-removed-role.toml",
            provider / "roles" / "review" / "removed-role" / "AGENT.md",
        ]
        for orphan in orphans:
            orphan.parent.mkdir(parents=True, exist_ok=True)
            orphan.write_text("# left behind by a removed role\n", encoding="utf-8")
        return orphans

    def test_check_reports_orphans_and_does_not_delete_them(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            provider = self._isolated_provider(Path(directory))
            orphans = self._seed_orphans(provider)
            result = self._run("--check", "--provider-root", str(provider))
            self.assertEqual(1, result.returncode, result.stdout)
            self.assertIn("orphaned", result.stderr)
            for orphan in orphans:
                self.assertTrue(orphan.is_file(), f"--check must not delete {orphan}")

    def test_write_mode_removes_orphans(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            provider = self._isolated_provider(Path(directory))
            orphans = self._seed_orphans(provider)
            result = self._run("--provider-root", str(provider))
            self.assertEqual(0, result.returncode, result.stderr)
            for orphan in orphans:
                self.assertFalse(orphan.exists(), f"write mode must remove {orphan}")
            self.assertEqual(0, self._run("--check", "--provider-root", str(provider)).returncode)

    def test_non_default_agents_root_rejects_a_provider_root_it_would_ignore(self) -> None:
        """Provider content is only rendered for this repository's own roster/
        tree, so silently accepting --provider-root alongside a fixture root
        would look like it targeted something it never touched.
        """
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _build_two_role_fixture(root)
            agents_root, catalog_path, routing_path, order_path, header_path = _paths(root)
            result = self._run(
                "--check",
                "--agents-root", str(agents_root),
                "--catalog", str(catalog_path),
                "--routing", str(routing_path),
                "--order", str(order_path),
                "--header-template", str(header_path),
                "--provider-root", str(root / "provider"),
            )
            self.assertEqual(1, result.returncode)
            self.assertIn("silently ignored", result.stderr)


class CheckModeFixtureTests(unittest.TestCase):
    def _run_check(self, root: Path) -> subprocess.CompletedProcess[str]:
        agents_root, catalog_path, routing_path, order_path, header_path = _paths(root)
        generator = ROOT / "src" / "generate_role_metadata.py"
        return subprocess.run(
            [
                sys.executable, str(generator), "--check",
                "--agents-root", str(agents_root),
                "--catalog", str(catalog_path),
                "--routing", str(routing_path),
                "--order", str(order_path),
                "--header-template", str(header_path),
            ],
            check=False, capture_output=True, text=True, encoding="utf-8",
        )

    def test_check_passes_on_freshly_built_fixture(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _build_two_role_fixture(root)
            result = self._run_check(root)
            self.assertEqual(0, result.returncode, result.stderr)

    def test_check_fails_on_hand_edited_catalog(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _build_two_role_fixture(root)
            _, catalog_path, _, _, _ = _paths(root)
            # Hand-edit formatting (not a value the generator would itself
            # re-derive differently) so the file on disk no longer matches
            # the generator's canonical rendering: the field value is still
            # "build", but with trailing whitespace the renderer never
            # emits.
            catalog_path.write_text(
                catalog_path.read_text(encoding="utf-8").replace("phase: build\n", "phase: build   \n", 1),
                encoding="utf-8",
            )
            result = self._run_check(root)
            self.assertNotEqual(0, result.returncode)
            self.assertIn("Role metadata derived files are stale", result.stderr)
            self.assertIn("catalog.yaml", result.stderr)

    def test_check_fails_on_hand_edited_knowledge_focus(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _build_two_role_fixture(root)
            _, _, routing_path, _, _ = _paths(root)
            # Hand-edit formatting inside the knowledge_focus block (extra
            # space after the colon) that the surgical splice's canonical
            # `json.dumps` re-rendering never reproduces, so a fresh
            # generate() call comes back different even though the value
            # itself is unchanged.
            routing_path.write_text(
                routing_path.read_text(encoding="utf-8").replace(
                    '"role-a": "role-a knowledge focus"', '"role-a":  "role-a knowledge focus"', 1
                ),
                encoding="utf-8",
            )
            result = self._run_check(root)
            self.assertNotEqual(0, result.returncode)
            self.assertIn("Role metadata derived files are stale", result.stderr)
            self.assertIn("routing.yaml", result.stderr)


class RoleModelBuildTests(unittest.TestCase):
    """`build_role_model` now reads role metadata exclusively from each
    `AGENT.md`'s frontmatter; catalog.yaml/routing.yaml are not read here at
    all (only the tests' own `--check` fixtures still write them, as
    expected *rendered output* for `CheckModeFixtureTests`).
    """

    def test_two_migrated_roles_build_role_model(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _build_two_role_fixture(root)
            agents_root, _catalog_path, _routing_path, order_path, _header_path = _paths(root)
            order_ids, roles = grm.build_role_model(agents_root, order_path)
            self.assertEqual(["role-a", "role-b"], order_ids)
            self.assertEqual("domain/role-a/AGENT.md", roles["role-a"]["definition"])
            self.assertEqual("domain/role-b/AGENT.md", roles["role-b"]["definition"])
            self.assertEqual("role-a knowledge focus", roles["role-a"]["knowledge_focus"])
            self.assertEqual("role-b knowledge focus", roles["role-b"]["knowledge_focus"])

            catalog_content = grm.render_catalog(order_ids, roles, HEADER_TEMPLATE)
            self.assertIn("role-b:", catalog_content)
            self.assertIn("gpt-5.6-terra", catalog_content)

    def test_unmigrated_agent_md_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _build_two_role_fixture(root)
            agents_root, _catalog_path, _routing_path, order_path, _header_path = _paths(root)

            # Overwrite role-b's AGENT.md with plain prose carrying no
            # frontmatter at all -- this must now be a generator error, not
            # a silently-accepted transitional state.
            (agents_root / "domain" / "role-b" / "AGENT.md").write_text(
                "# Role B\n\nNo frontmatter here.\n", encoding="utf-8"
            )

            with self.assertRaisesRegex(grm.RoleMetadataError, "does not carry"):
                grm.build_role_model(agents_root, order_path)

    def test_migrated_role_missing_required_field_fails_closed_with_no_fallback(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _build_two_role_fixture(root)
            agents_root, _catalog_path, _routing_path, order_path, _header_path = _paths(root)

            # Drop the frontmatter's knowledge_focus field entirely -- there
            # is no other source left to fall back to.
            role_b_path = agents_root / "domain" / "role-b" / "AGENT.md"
            content = role_b_path.read_text(encoding="utf-8")
            content = content.replace("knowledge_focus: role-b knowledge focus\n", "")
            role_b_path.write_text(content, encoding="utf-8")

            with self.assertRaisesRegex(grm.RoleMetadataError, r"role-b.*missing required field.*knowledge_focus"):
                grm.build_role_model(agents_root, order_path)

    def test_order_file_lists_id_with_no_matching_agent_md_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _build_two_role_fixture(root)
            agents_root, _catalog_path, _routing_path, order_path, _header_path = _paths(root)
            order_path.write_text("role-a\nrole-b\nrole-c\n", encoding="utf-8")

            with self.assertRaisesRegex(grm.RoleMetadataError, "no matching AGENT.md"):
                grm.build_role_model(agents_root, order_path)

    def test_discovered_agent_md_not_in_order_file_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _build_two_role_fixture(root)
            agents_root, _catalog_path, _routing_path, order_path, _header_path = _paths(root)
            _write_migrated_role(agents_root, "domain/role-c", "role-c", "role-c knowledge focus")

            with self.assertRaisesRegex(grm.RoleMetadataError, "not listed in"):
                grm.build_role_model(agents_root, order_path)

    def test_duplicate_id_across_two_agent_md_files_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _build_two_role_fixture(root)
            agents_root, _catalog_path, _routing_path, order_path, _header_path = _paths(root)

            # A second AGENT.md whose frontmatter claims role-b's id.
            _write_migrated_role(
                agents_root, "domain/role-b-duplicate", "role-b", "duplicate role-b knowledge focus"
            )

            with self.assertRaisesRegex(grm.RoleMetadataError, "duplicate role id"):
                grm.build_role_model(agents_root, order_path)


class TierConsistencyTests(unittest.TestCase):
    def test_tier_mismatch_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _build_two_role_fixture(root)
            agents_root, _catalog_path, _routing_path, order_path, _header_path = _paths(root)
            role_a_path = agents_root / "domain" / "role-a" / "AGENT.md"
            role_a_path.write_text(
                role_a_path.read_text(encoding="utf-8").replace(
                    "model: sonnet\ncodex_model: gpt-5.6-terra",
                    "model: opus\ncodex_model: gpt-5.6-terra",
                    1,
                ),
                encoding="utf-8",
            )
            with self.assertRaisesRegex(grm.RoleMetadataError, "requires codex_model"):
                grm.build_role_model(agents_root, order_path)


class FieldEnumValidationTests(unittest.TestCase):
    """Dedicated fixtures for `_validate_record`'s individual field-enum
    branches -- each of these previously had no dedicated test and was only
    reachable incidentally (if at all) through the broader identity/
    end-to-end tests, unlike the already-tested tier-consistency
    cross-check (see TierConsistencyTests).
    """

    def _build_role_a_with_override(self, root: Path, **overrides: str) -> tuple[Path, Path]:
        _build_two_role_fixture(root)
        agents_root, _catalog_path, _routing_path, order_path, _header_path = _paths(root)
        role_a_path = agents_root / "domain" / "role-a" / "AGENT.md"
        text = role_a_path.read_text(encoding="utf-8")
        for field, value in overrides.items():
            fields = {
                "id": "role-a",
                "phase": "build",
                "capability": "code_author",
                "model": "sonnet",
                "codex_model": "gpt-5.6-terra",
                "reasoning_effort": "medium",
                "knowledge_focus": "role-a knowledge focus",
            }
            fields[field] = value
            role_a_path.write_text(
                rm.render_frontmatter(fields) + "\n# role-a\n\nMigrated role.\n", encoding="utf-8"
            )
        return agents_root, order_path

    def test_invalid_phase_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            agents_root, order_path = self._build_role_a_with_override(root, phase="not-a-real-phase")
            with self.assertRaisesRegex(grm.RoleMetadataError, "phase 'not-a-real-phase' must be one of"):
                grm.build_role_model(agents_root, order_path)

    def test_invalid_capability_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            agents_root, order_path = self._build_role_a_with_override(root, capability="not-a-real-capability")
            with self.assertRaisesRegex(
                grm.RoleMetadataError, "capability 'not-a-real-capability' must be one of"
            ):
                grm.build_role_model(agents_root, order_path)

    def test_invalid_model_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            agents_root, order_path = self._build_role_a_with_override(root, model="not-a-real-model")
            with self.assertRaisesRegex(grm.RoleMetadataError, "model 'not-a-real-model' must be one of"):
                grm.build_role_model(agents_root, order_path)

    def test_invalid_codex_model_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            agents_root, order_path = self._build_role_a_with_override(root, codex_model="not-a-real-codex-model")
            with self.assertRaisesRegex(
                grm.RoleMetadataError, "codex_model 'not-a-real-codex-model' must be one of"
            ):
                grm.build_role_model(agents_root, order_path)

    def test_invalid_reasoning_effort_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            agents_root, order_path = self._build_role_a_with_override(
                root, reasoning_effort="not-a-real-effort"
            )
            with self.assertRaisesRegex(
                grm.RoleMetadataError, "reasoning_effort 'not-a-real-effort' must be one of"
            ):
                grm.build_role_model(agents_root, order_path)

    def test_empty_knowledge_focus_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            agents_root, order_path = self._build_role_a_with_override(root, knowledge_focus="")
            with self.assertRaisesRegex(
                grm.RoleMetadataError, "knowledge_focus must be a non-empty string"
            ):
                grm.build_role_model(agents_root, order_path)


class MissingIdFieldTests(unittest.TestCase):
    def test_frontmatter_missing_id_field_entirely_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _build_two_role_fixture(root)
            agents_root, _catalog_path, _routing_path, order_path, _header_path = _paths(root)

            # Hand-write frontmatter (bypassing render_frontmatter, which
            # itself requires 'id') that omits the 'id' field entirely --
            # every other required field is present and valid.
            content = (
                "---\n"
                "phase: build\n"
                "capability: code_author\n"
                "model: sonnet\n"
                "codex_model: gpt-5.6-terra\n"
                "reasoning_effort: medium\n"
                "knowledge_focus: role-c knowledge focus\n"
                "---\n"
                "# role-c\n\nMigrated role.\n"
            )
            _write(agents_root / "domain" / "role-c" / "AGENT.md", content)

            with self.assertRaisesRegex(grm.RoleMetadataError, "missing required field 'id'"):
                grm.build_role_model(agents_root, order_path)


class KnowledgeFocusSpliceInvariantGuardTests(unittest.TestCase):
    """Dedicated fixtures for `splice_knowledge_focus`'s own internal
    safety-net checks (not the "exactly one anchor" checks, which already
    have coverage in KnowledgeFocusSpliceTests). These two `RoleMetadataError`
    sites exist purely to catch a bug in the splice algorithm itself, not any
    real-world routing.yaml content -- so a dedicated fixture has to
    deliberately misuse the function to trigger them.
    """

    def test_id_set_mismatch_after_splice_fails_closed(self) -> None:
        # `roles` claims "role-b" exists, but neither the original
        # knowledge_focus block nor order_ids mentions it, so the rebuilt
        # block can never actually contain it -- the id-set invariant must
        # catch this rather than silently emitting a routing.yaml whose
        # knowledge_focus block is missing an id the caller asked for.
        original = _routing_text({"role-a": "role-a focus"})
        roles = {
            "role-a": {"knowledge_focus": "role-a focus"},
            "role-b": {"knowledge_focus": "role-b focus"},
        }
        with self.assertRaisesRegex(grm.RoleMetadataError, "knowledge_focus id-set mismatch after splice"):
            grm.splice_knowledge_focus(original, ["role-a"], roles)

    def test_splice_bug_that_corrupts_another_key_is_caught_by_invariant(self) -> None:
        # Simulate the exact class of algorithm bug this invariant exists to
        # catch: a brace-finder that returns the wrong region. Build a
        # fixture where "change_intake" (a sibling top-level dict key) sits
        # AFTER the real knowledge_focus block, then monkeypatch
        # `_find_knowledge_focus_block` to report change_intake's own brace
        # span instead of the real one. The splice then overwrites
        # change_intake's content with knowledge_focus-shaped JSON, and the
        # invariant must fail closed rather than silently corrupt that
        # unrelated key.
        payload = {
            "version": 1,
            "ignored_gates": [],
            "routes": [],
            "risk_rules": [],
            "knowledge_focus": {"role-a": "role-a focus"},
            "change_intake": {"keywords": ["implement"], "agents": [], "quality_gates": []},
        }
        original = json.dumps(payload, indent=2) + "\n"
        roles = {"role-a": {"knowledge_focus": "role-a focus"}}

        anchor = '  "change_intake": {'
        anchor_start = original.index(anchor)
        open_brace_index = original.index("{", anchor_start)
        depth = 0
        close_brace_index = None
        for index in range(open_brace_index, len(original)):
            character = original[index]
            if character == "{":
                depth += 1
            elif character == "}":
                depth -= 1
                if depth == 0:
                    close_brace_index = index
                    break
        assert close_brace_index is not None

        with unittest.mock.patch.object(
            grm, "_find_knowledge_focus_block", return_value=(open_brace_index, close_brace_index)
        ):
            with self.assertRaisesRegex(
                grm.RoleMetadataError, "splice unexpectedly altered routing.yaml key 'change_intake'"
            ):
                grm.splice_knowledge_focus(original, ["role-a"], roles)

    def test_no_matching_closing_brace_fails_closed(self) -> None:
        # The knowledge_focus anchor and its opening brace are present, but
        # the text is truncated before any closing brace -- the brace-finder
        # must fail closed rather than scan past the end of the string.
        original = '{\n  "knowledge_focus": {\n    "role-a": "x"\n'
        roles = {"role-a": {"knowledge_focus": "x"}}
        with self.assertRaisesRegex(grm.RoleMetadataError, "could not find a matching closing"):
            grm.splice_knowledge_focus(original, ["role-a"], roles)


class KnowledgeFocusSpliceTests(unittest.TestCase):
    def test_splice_preserves_everything_outside_the_block(self) -> None:
        original = _routing_text(
            {"role-a": "role-a focus", "role-b": "role-b focus"},
            extra={"change_intake": {"keywords": ["implement"], "agents": [], "quality_gates": []}},
        )
        roles = {
            "role-a": {"knowledge_focus": "role-a focus"},
            "role-b": {"knowledge_focus": "role-b focus"},
        }
        spliced = grm.splice_knowledge_focus(original, ["role-a", "role-b"], roles)
        self.assertEqual(original, spliced)

    def test_splice_updates_only_changed_values_and_preserves_order(self) -> None:
        original = _routing_text({"role-a": "old focus", "role-b": "role-b focus"})
        roles = {
            "role-a": {"knowledge_focus": "new focus"},
            "role-b": {"knowledge_focus": "role-b focus"},
        }
        spliced = grm.splice_knowledge_focus(original, ["role-a", "role-b"], roles)
        after = json.loads(spliced)
        self.assertEqual(["role-a", "role-b"], list(after["knowledge_focus"].keys()))
        self.assertEqual("new focus", after["knowledge_focus"]["role-a"])
        before = json.loads(original)
        before["knowledge_focus"] = after["knowledge_focus"]
        self.assertEqual(before, after)

    def test_splice_appends_new_roles_in_order_file_order(self) -> None:
        original = _routing_text({"role-a": "role-a focus"})
        roles = {
            "role-a": {"knowledge_focus": "role-a focus"},
            "role-b": {"knowledge_focus": "role-b focus"},
        }
        spliced = grm.splice_knowledge_focus(original, ["role-a", "role-b"], roles)
        after = json.loads(spliced)
        self.assertEqual(["role-a", "role-b"], list(after["knowledge_focus"].keys()))

    def test_missing_anchor_fails_closed(self) -> None:
        original = json.dumps({"version": 1, "routes": [], "risk_rules": []}, indent=2) + "\n"
        with self.assertRaisesRegex(grm.RoleMetadataError, "exactly one"):
            grm.splice_knowledge_focus(original, ["role-a"], {"role-a": {"knowledge_focus": "x"}})

    def test_duplicate_anchor_fails_closed(self) -> None:
        block = '  "knowledge_focus": {\n    "role-a": "x"\n  }'
        original = "{\n" + block + ",\n" + block.replace("role-a", "role-a-again") + "\n}\n"
        with self.assertRaisesRegex(grm.RoleMetadataError, "exactly one"):
            grm.splice_knowledge_focus(original, ["role-a"], {"role-a": {"knowledge_focus": "x"}})

    def test_spliced_result_passes_load_routing(self) -> None:
        sys.path.insert(0, str(ROOT / "src"))
        from routing import load_routing  # noqa: E402

        original = _routing_text({"role-a": "role-a focus"})
        roles = {"role-a": {"knowledge_focus": "updated focus"}}
        spliced = grm.splice_knowledge_focus(original, ["role-a"], roles)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "routing.yaml"
            path.write_text(spliced, encoding="utf-8")
            config = load_routing(path)
        self.assertEqual({"role-a": "updated focus"}, config["knowledge_focus"])


class RoutingIdNamespaceTests(unittest.TestCase):
    """`load_routing` pools context pack ids with route, risk rule, and team
    recipe ids.

    These live beside `test_spliced_result_passes_load_routing` above because
    `load_routing` is exactly the validator this module's generator runs over
    the routing.yaml content it is about to write (`_validate_routing_content`),
    so what `load_routing` rejects is what the generator cannot emit.

    The collision matters because a dispatch plan puts `matched_routes[].id`
    and `context_packs[].id` in the same document: an id claimed by both is
    ambiguous for any consumer keying on plan ids.
    `schema_validate.validate_routing` checks each array only against itself
    and cannot see the collision, so `load_routing` is the only guard.
    """

    def _load(self, extra: dict) -> object:
        sys.path.insert(0, str(ROOT / "src"))
        from routing import load_routing  # noqa: E402

        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "routing.yaml"
            path.write_text(_routing_text({"role-a": "role-a focus"}, extra), encoding="utf-8")
            return load_routing(path)

    def test_context_pack_id_colliding_with_a_route_is_rejected(self) -> None:
        with self.assertRaisesRegex(ValueError, "duplicate context pack id: shared-id"):
            self._load(
                {
                    "routes": [{"id": "shared-id", "keywords": ["alpha"]}],
                    "context_packs": [
                        {"id": "shared-id", "version": 1, "definition": "context-packs/x/CONTEXT.md"}
                    ],
                }
            )

    def test_context_pack_id_colliding_with_a_risk_rule_is_rejected(self) -> None:
        with self.assertRaisesRegex(ValueError, "duplicate context pack id: shared-id"):
            self._load(
                {
                    "risk_rules": [{"id": "shared-id", "keywords": ["alpha"]}],
                    "context_packs": [
                        {"id": "shared-id", "version": 1, "definition": "context-packs/x/CONTEXT.md"}
                    ],
                }
            )

    def test_context_pack_id_colliding_with_a_team_recipe_is_rejected(self) -> None:
        with self.assertRaisesRegex(ValueError, "duplicate context pack id: shared-id"):
            self._load(
                {
                    "team_recipes": [{"id": "shared-id"}],
                    "context_packs": [
                        {"id": "shared-id", "version": 1, "definition": "context-packs/x/CONTEXT.md"}
                    ],
                }
            )

    def test_distinct_context_pack_id_still_loads(self) -> None:
        # The pooling must not reject the ordinary case: routes and packs with
        # different ids coexist, which is what every real routing.yaml does.
        config = self._load(
            {
                "routes": [{"id": "route-id", "keywords": ["alpha"]}],
                "context_packs": [
                    {"id": "pack-id", "version": 1, "definition": "context-packs/x/CONTEXT.md"}
                ],
            }
        )
        self.assertEqual(["pack-id"], [pack["id"] for pack in config["context_packs"]])


if __name__ == "__main__":
    unittest.main()
