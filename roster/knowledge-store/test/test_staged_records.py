"""Tests for the staged proposed-knowledge record contract.

Non-vacuity is enforced structurally, not by inspection. Every negative test
goes through `assert_defect`, which does three things in order:

1. asserts the shared valid fixture validates to exactly `[]`,
2. asserts the mutated record actually differs from that fixture, so a
   no-op "mutation" cannot silently test nothing,
3. asserts the mutated record produces a finding containing the specific
   message for the rule under test.

A validator that returned `[]` unconditionally fails step 3 of every
negative test. A validator that returned a constant non-empty list fails
step 1 of every negative test. A "mutation" that changed nothing fails step
2. No test in this file can pass against a validator that does not actually
decide the rule it names.
"""

from __future__ import annotations

import contextlib
import hashlib
import io
import json
import shutil
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "src"))

from staged_records import (  # noqa: E402
    DEFAULT_DIRECTORY,
    DISPOSITION_ACTIONS,
    DISPOSITION_KEYS,
    ID_PATTERN,
    OPTIONAL_KEYS,
    ORIGIN_KEYS,
    RECOMMENDED_ACTIONS,
    REQUIRED_KEYS,
    STATUS_VALUES,
    RecordFormatError,
    compute_digest,
    main,
    parse_record,
    validate_directory,
    validate_record,
)

SCHEMA_PATH = ROOT / "proposed-knowledge.schema.json"

VALID_BODY = """## Summary

A staged record's body carries the claim; its frontmatter carries the claim's
lifecycle. Amending status or appending a disposition therefore never
invalidates the digest of unchanged prose.

## Reusable rule

Compute content_digest only with compute_digest(), never by hand.
"""


def frontmatter_fields() -> list[tuple[str, str]]:
    """The rendered frontmatter of the canonical valid fixture, as ordered
    (key, rendered_text) pairs so a single field can be replaced, dropped, or
    added without re-templating the whole record.
    """
    return [
        ("id", "id: KS-20260809-staged-record-contract"),
        ("title", "title: staged records need a canonical, checkable format"),
        ("status", "status: proposed"),
        ("evidence", 'evidence:\n  - "roster/knowledge-store/AGENT.md:22"\n  - "PR #164 review"'),
        (
            "origin",
            "origin:\n"
            "  task: PR #164 contract hardening\n"
            "  artifact: roster/knowledge-store/proposed-knowledge/\n"
            '  revision: "d54e79c"',
        ),
        ("proposed_classification", "proposed_classification: internal"),
        ("source_scope", "source_scope: deagy/cadre"),
        ("sensitivity_notes", 'sensitivity_notes: ""'),
        ("conflicts_or_staleness", 'conflicts_or_staleness: ""'),
        ("recommended_action", "recommended_action: ingest"),
        ("untrusted_instruction_risk", "untrusted_instruction_risk: false"),
        ("staged_by", "staged_by: api-contract-engineer"),
        ("content_digest", f"content_digest: {compute_digest(VALID_BODY)}"),
    ]


DISPOSITION_BLOCK = (
    "disposition:\n"
    "  action: {action}\n"
    "  reason: durable, correctly scoped, and supported by its evidence\n"
    "  classification_used: internal\n"
    "  diverged_from_proposal: false\n"
    "  decided_by: knowledge-store-steward"
)


def render(fields: list[tuple[str, str]], body: str = VALID_BODY) -> str:
    block = "\n".join(text for _, text in fields)
    return f"---\n{block}\n---\n\n{body}"


def valid_record() -> str:
    return render(frontmatter_fields())


def with_field(key: str, text: str) -> list[tuple[str, str]]:
    """Replace one field's rendered text, keeping position and every sibling."""
    fields = frontmatter_fields()
    for index, (existing_key, _) in enumerate(fields):
        if existing_key == key:
            fields[index] = (key, text)
            return fields
    raise AssertionError(f"{key!r} is not a field of the valid fixture")


def without_field(key: str) -> list[tuple[str, str]]:
    fields = [(existing, text) for existing, text in frontmatter_fields() if existing != key]
    assert len(fields) == len(frontmatter_fields()) - 1, f"{key!r} is not a field of the valid fixture"
    return fields


def dispositioned(status: str, action: str | None = None) -> list[tuple[str, str]]:
    """The valid fixture advanced to a dispositioned state."""
    fields = with_field("status", f"status: {status}")
    fields.append(("disposition", DISPOSITION_BLOCK.format(action=action or status)))
    return fields


class StagedRecordDefectMixin:
    def assert_valid(self, record: str) -> None:
        findings = validate_record(record)
        self.assertEqual(findings, [], f"expected a clean record, got findings: {findings}")

    def assert_defect(self, record: str, expected: str) -> list[str]:
        """Fault injection with a non-vacuity guard. See the module docstring."""
        baseline = validate_record(valid_record())
        self.assertEqual(
            baseline,
            [],
            "non-vacuity guard: the unmutated valid fixture must validate clean, otherwise this "
            f"negative test proves nothing about the injected defect. Findings: {baseline}",
        )
        self.assertNotEqual(
            record,
            valid_record(),
            "non-vacuity guard: the 'mutated' record is byte-identical to the valid fixture, so no "
            "defect was actually injected",
        )
        findings = validate_record(record)
        self.assertTrue(
            any(expected in finding for finding in findings),
            f"expected a finding containing {expected!r}; got {findings}",
        )
        return findings


class DigestTests(unittest.TestCase):
    def test_digest_matches_an_independently_computed_sha256(self) -> None:
        # The literal below came from `printf '%s' 'staged record body' |
        # sha256sum`, i.e. from outside this module's normalisation path
        # entirely, so `compute_digest` cannot be self-consistently wrong.
        self.assertEqual(
            compute_digest("staged record body"),
            "4e7c54fb943f0bbcdc9aa9a240629bac6c45438f9df811d51fcc0ac009eb3cd0",
        )
        self.assertEqual(
            compute_digest("staged record body"),
            hashlib.sha256(b"staged record body").hexdigest(),
        )

    def test_normalisation_is_insensitive_to_line_endings_and_edge_whitespace(self) -> None:
        canonical = compute_digest("first line\nsecond line")
        for variant in (
            "first line\r\nsecond line",
            "first line\rsecond line",
            "\n\nfirst line\nsecond line\n\n",
            "   \nfirst line\nsecond line\n   ",
        ):
            with self.subTest(variant=variant):
                self.assertEqual(compute_digest(variant), canonical)

    def test_normalisation_is_sensitive_to_interior_content(self) -> None:
        canonical = compute_digest("first line\nsecond line")
        for variant in (
            "first line\nSecond line",
            "first line\n\nsecond line",
            "first line\nsecond  line",
            "first line\nsecond line\nthird line",
        ):
            with self.subTest(variant=variant):
                self.assertNotEqual(compute_digest(variant), canonical)

    def test_body_excludes_the_frontmatter_so_a_disposition_preserves_the_digest(self) -> None:
        proposed_frontmatter, proposed_body = parse_record(valid_record())
        decided_frontmatter, decided_body = parse_record(render(dispositioned("accepted")))
        self.assertEqual(compute_digest(proposed_body), compute_digest(decided_body))
        self.assertEqual(
            proposed_frontmatter["content_digest"], decided_frontmatter["content_digest"]
        )


class ValidRecordTests(unittest.TestCase, StagedRecordDefectMixin):
    def test_valid_proposed_record_passes(self) -> None:
        self.assert_valid(valid_record())

    def test_valid_dispositioned_records_pass(self) -> None:
        for status in DISPOSITION_ACTIONS:
            with self.subTest(status=status):
                self.assert_valid(render(dispositioned(status)))

    def test_unknown_untrusted_instruction_risk_is_permitted(self) -> None:
        self.assert_valid(
            render(with_field("untrusted_instruction_risk", "untrusted_instruction_risk: unknown"))
        )

    def test_parse_record_returns_frontmatter_and_body(self) -> None:
        frontmatter, body = parse_record(valid_record())
        self.assertEqual(frontmatter["id"], "KS-20260809-staged-record-contract")
        self.assertEqual(frontmatter["status"], "proposed")
        self.assertIs(frontmatter["untrusted_instruction_risk"], False)
        self.assertEqual(
            frontmatter["evidence"], ["roster/knowledge-store/AGENT.md:22", "PR #164 review"]
        )
        self.assertEqual(sorted(frontmatter["origin"]), sorted(ORIGIN_KEYS))
        self.assertEqual(frontmatter["sensitivity_notes"], "")
        self.assertIn("## Summary", body)
        # The body starts after the closing delimiter: no frontmatter line and
        # no delimiter survives into it.
        self.assertNotIn("---", body)
        self.assertNotIn("content_digest: ", body)
        self.assertNotIn("staged_by:", body)


class Rule1RequiredKeysAndTypesTests(unittest.TestCase, StagedRecordDefectMixin):
    """Rule 1: missing key, wrong type, or a value outside its enum."""

    def test_every_required_key_is_individually_required(self) -> None:
        for key in REQUIRED_KEYS:
            with self.subTest(key=key):
                self.assert_defect(
                    render(without_field(key)), f"missing required frontmatter key {key!r}"
                )

    def test_unknown_top_level_key_is_rejected(self) -> None:
        fields = frontmatter_fields()
        fields.append(("ingest_now", "ingest_now: true"))
        self.assert_defect(render(fields), "unknown top-level frontmatter key 'ingest_now'")

    def test_wrong_type_for_a_string_key_is_rejected(self) -> None:
        self.assert_defect(
            render(with_field("title", "title: true")),
            "frontmatter key 'title' must be a string, got bool True",
        )

    def test_empty_string_for_a_non_empty_key_is_rejected(self) -> None:
        self.assert_defect(
            render(with_field("staged_by", 'staged_by: ""')),
            "frontmatter key 'staged_by' must be a non-empty string",
        )

    def test_status_outside_its_enum_is_rejected(self) -> None:
        self.assert_defect(
            render(with_field("status", "status: archived")),
            "frontmatter key 'status' must be one of proposed, accepted, rejected, deferred; "
            "got 'archived'",
        )

    def test_recommended_action_outside_its_enum_is_rejected(self) -> None:
        self.assert_defect(
            render(with_field("recommended_action", "recommended_action: publish")),
            "frontmatter key 'recommended_action' must be one of ingest, update, reclassify, defer; "
            "got 'publish'",
        )

    def test_id_pattern_is_enforced(self) -> None:
        for bad_id in ("ks-20260809-lowercase-prefix", "KS-2026-08-09-dashes", "KS-20260809-Upper"):
            with self.subTest(bad_id=bad_id):
                self.assert_defect(
                    render(with_field("id", f"id: {bad_id}")),
                    "frontmatter key 'id' must match",
                )

    def test_evidence_must_be_a_non_empty_list_of_strings(self) -> None:
        self.assert_defect(
            render(with_field("evidence", "evidence: a single string")),
            "frontmatter key 'evidence' must be a list of strings",
        )
        self.assert_defect(
            render(with_field("evidence", 'evidence:\n  - ""')),
            "evidence[0] must be a non-empty string",
        )

    def test_origin_must_be_a_mapping_with_all_three_keys(self) -> None:
        self.assert_defect(
            render(with_field("origin", "origin: PR #164")),
            "frontmatter key 'origin' must be a mapping",
        )
        self.assert_defect(
            render(with_field("origin", "origin:\n  task: PR #164\n  artifact: AGENT.md")),
            "origin is missing required key 'revision'",
        )

    def test_untrusted_instruction_risk_rejects_a_quoted_boolean(self) -> None:
        self.assert_defect(
            render(with_field("untrusted_instruction_risk", 'untrusted_instruction_risk: "true"')),
            "frontmatter key 'untrusted_instruction_risk' must be the boolean true, the boolean "
            "false, or the string 'unknown'",
        )

    def _edited_disposition(self, old: str, new: str) -> str:
        fields = dispositioned("accepted")
        replaced = fields[-1][1].replace(old, new)
        self.assertNotEqual(replaced, fields[-1][1], f"{old!r} was not found in the disposition block")
        fields[-1] = ("disposition", replaced)
        return render(fields)

    def test_disposition_field_types_are_enforced(self) -> None:
        self.assert_defect(
            self._edited_disposition(
                "diverged_from_proposal: false", 'diverged_from_proposal: "no"'
            ),
            "disposition.diverged_from_proposal must be the boolean",
        )
        self.assert_defect(
            self._edited_disposition(
                "  reason: durable, correctly scoped, and supported by its evidence\n", ""
            ),
            "disposition is missing required key 'reason'",
        )


class Rule2DeletionEscalationTests(unittest.TestCase, StagedRecordDefectMixin):
    """Rule 2: `recommended_action: delete` is its own error, not a generic
    enum failure -- it is the case a human is most likely to attempt."""

    def test_delete_reports_the_act_vs_capability_escalation(self) -> None:
        """Re-decided again when delete-ingested shipped (issue #184): the store now has

        deletion capability, so the reason is act-vs-capability, not capability absence.
        """
        findings = self.assert_defect(
            render(with_field("recommended_action", "recommended_action: delete")),
            "proposing a deletion and being authorized to perform one are different acts",
        )
        joined = "\n".join(findings)
        self.assertIn("escalates to knowledge-store-steward and an authorized human", joined)
        self.assertNotIn("no deletion capability at all", joined)

    def test_delete_does_not_fall_through_to_the_generic_enum_message(self) -> None:
        findings = validate_record(
            render(with_field("recommended_action", "recommended_action: delete"))
        )
        self.assertFalse(
            any("must be one of ingest, update, reclassify, defer" in finding for finding in findings),
            f"'delete' must produce its own dedicated escalation error, not a generic enum "
            f"failure; got {findings}",
        )

    def test_the_dedicated_message_differs_from_the_generic_enum_message(self) -> None:
        delete_findings = validate_record(
            render(with_field("recommended_action", "recommended_action: delete"))
        )
        other_findings = validate_record(
            render(with_field("recommended_action", "recommended_action: publish"))
        )
        self.assertNotEqual(delete_findings, other_findings)


class Rule3DigestTests(unittest.TestCase, StagedRecordDefectMixin):
    """Rule 3: content_digest must equal the sha256 of the normalised body."""

    def test_body_edited_without_recomputing_the_digest_is_rejected(self) -> None:
        record = render(frontmatter_fields(), body=VALID_BODY + "\nAn uncounted extra claim.\n")
        findings = self.assert_defect(record, "content_digest does not match the record body")
        self.assertIn("compute_digest(body)", "\n".join(findings))

    def test_digest_edited_without_touching_the_body_is_rejected(self) -> None:
        wrong = compute_digest("a different body entirely")
        self.assert_defect(
            render(with_field("content_digest", f"content_digest: {wrong}")),
            "content_digest does not match the record body",
        )

    def test_non_sha256_shaped_digest_is_rejected(self) -> None:
        self.assert_defect(
            render(with_field("content_digest", "content_digest: NOT-A-DIGEST")),
            "frontmatter key 'content_digest' must be 64 lowercase hex characters",
        )

    def test_uppercase_digest_is_rejected(self) -> None:
        upper = compute_digest(VALID_BODY).upper()
        self.assert_defect(
            render(with_field("content_digest", f"content_digest: {upper}")),
            "must be 64 lowercase hex characters",
        )

    def test_crlf_checkout_of_the_valid_record_still_validates(self) -> None:
        self.assert_valid(valid_record().replace("\n", "\r\n"))


class Rule4StatusDispositionCoherenceTests(unittest.TestCase, StagedRecordDefectMixin):
    """Rule 4: status 'proposed' forbids disposition; any other status
    requires one whose action equals the status."""

    def test_proposed_with_a_disposition_is_rejected(self) -> None:
        fields = frontmatter_fields()
        fields.append(("disposition", DISPOSITION_BLOCK.format(action="accepted")))
        self.assert_defect(
            render(fields), "status 'proposed' requires 'disposition' to be absent"
        )

    def test_dispositioned_status_without_a_disposition_is_rejected(self) -> None:
        for status in DISPOSITION_ACTIONS:
            with self.subTest(status=status):
                self.assert_defect(
                    render(with_field("status", f"status: {status}")),
                    f"status {status!r} requires a 'disposition' mapping",
                )

    def test_disposition_action_disagreeing_with_status_is_rejected(self) -> None:
        self.assert_defect(
            render(dispositioned("accepted", action="rejected")),
            "disposition.action 'rejected' does not match status 'accepted'",
        )


class Rule5AutomaticDeferTests(unittest.TestCase, StagedRecordDefectMixin):
    """Rule 5: untrusted_instruction_risk true forces the automatic defer."""

    def _risky(self, fields: list[tuple[str, str]]) -> list[tuple[str, str]]:
        return [
            (key, "untrusted_instruction_risk: true" if key == "untrusted_instruction_risk" else text)
            for key, text in fields
        ]

    def test_injection_risk_cannot_be_accepted(self) -> None:
        findings = self.assert_defect(
            render(self._risky(dispositioned("accepted"))),
            "untrusted_instruction_risk is true, so status must not be 'accepted'",
        )
        self.assertIn("automatic-defer rule", "\n".join(findings))

    def test_injection_risk_disposition_must_be_deferred(self) -> None:
        findings = self.assert_defect(
            render(self._risky(dispositioned("rejected"))),
            "untrusted_instruction_risk is true, so disposition.action must be 'deferred'",
        )
        self.assertIn("automatic-defer rule", "\n".join(findings))

    def test_injection_risk_deferred_is_accepted(self) -> None:
        self.assert_valid(render(self._risky(dispositioned("deferred"))))

    def test_injection_risk_still_proposed_is_accepted(self) -> None:
        self.assert_valid(render(self._risky(frontmatter_fields())))


class Rule6AbsoluteLocalPathTests(unittest.TestCase, StagedRecordDefectMixin):
    """Rule 6: no absolute local path in evidence[] or origin values."""

    LOCAL_PATHS = (
        "/home/deagy/sdk/cadre/roster/knowledge-store/AGENT.md:22",
        "/Users/deagy/sdk/cadre/roster/knowledge-store/AGENT.md:22",
        "~/sdk/cadre/roster/knowledge-store/AGENT.md:22",
        "C:\\Users\\deagy\\sdk\\cadre\\roster\\knowledge-store\\AGENT.md",
    )

    def test_absolute_local_path_in_evidence_is_rejected(self) -> None:
        for local_path in self.LOCAL_PATHS:
            with self.subTest(local_path=local_path):
                findings = self.assert_defect(
                    render(with_field("evidence", f'evidence:\n  - "{local_path}"')),
                    "evidence[0] contains an absolute local path",
                )
                self.assertIn("source_uri redaction rule", "\n".join(findings))
                self.assertIn("knowledge-use-policy.md", "\n".join(findings))

    def test_absolute_local_path_in_origin_is_rejected(self) -> None:
        for key in ORIGIN_KEYS:
            with self.subTest(key=key):
                origin_lines = [
                    "  task: PR #164 contract hardening",
                    "  artifact: roster/knowledge-store/proposed-knowledge/",
                    '  revision: "d54e79c"',
                ]
                index = ORIGIN_KEYS.index(key)
                origin_lines[index] = f"  {key}: /home/deagy/sdk/cadre/roster"
                findings = self.assert_defect(
                    render(with_field("origin", "origin:\n" + "\n".join(origin_lines))),
                    f"origin.{key} contains an absolute local path",
                )
                self.assertIn("source_uri redaction rule", "\n".join(findings))

    def test_repository_relative_and_url_references_are_not_false_positives(self) -> None:
        for reference in (
            "roster/knowledge-store/AGENT.md:22",
            "https://github.com/deagy/cadre/pull/164",
            "kernel/contracts/g3.json:1",
            "a.md:3",
        ):
            with self.subTest(reference=reference):
                self.assert_valid(render(with_field("evidence", f'evidence:\n  - "{reference}"')))


class Rule7IdUniquenessTests(unittest.TestCase, StagedRecordDefectMixin):
    """Rule 7: id uniqueness is a directory-level property."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="staged-records-")
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)

    def test_two_records_sharing_an_id_are_rejected(self) -> None:
        (self.directory / "first.md").write_text(valid_record(), encoding="utf-8")
        (self.directory / "second.md").write_text(valid_record(), encoding="utf-8")
        findings = validate_directory(self.directory)
        self.assertTrue(
            any("duplicate record id 'KS-20260809-staged-record-contract'" in f for f in findings),
            f"expected a duplicate-id finding, got {findings}",
        )

    def test_distinct_ids_in_the_same_directory_are_accepted(self) -> None:
        # Non-vacuity guard for the test above: the same two files, differing
        # only in their ids, must validate clean -- so the duplicate finding
        # is caused by the collision and nothing else.
        (self.directory / "first.md").write_text(valid_record(), encoding="utf-8")
        (self.directory / "second.md").write_text(
            render(with_field("id", "id: KS-20260809-a-second-record")), encoding="utf-8"
        )
        self.assertEqual(validate_directory(self.directory), [])

    def test_a_single_file_validator_cannot_see_the_collision(self) -> None:
        # Establishes that rule 7 genuinely needs the directory-level check:
        # each colliding record is individually valid.
        self.assert_valid(valid_record())

    def test_missing_directory_is_reported(self) -> None:
        findings = validate_directory(self.directory / "does-not-exist")
        self.assertTrue(
            any("staged-record directory does not exist" in f for f in findings),
            f"got {findings}",
        )


class MalformedRecordTests(unittest.TestCase, StagedRecordDefectMixin):
    def test_missing_opening_delimiter_is_reported(self) -> None:
        self.assert_defect(
            valid_record().replace("---\n", "", 1),
            "record must begin with a '---' frontmatter delimiter",
        )

    def test_unclosed_frontmatter_is_reported(self) -> None:
        record = valid_record()
        head, _, _ = record.partition("\n---\n")
        self.assert_defect(head + "\n", "frontmatter is never closed")

    def test_duplicate_frontmatter_key_is_reported(self) -> None:
        fields = frontmatter_fields()
        fields.append(("status", "status: accepted"))
        self.assert_defect(render(fields), "duplicate frontmatter key 'status'")

    def test_unsupported_yaml_constructs_are_reported_not_misread(self) -> None:
        cases = {
            "evidence: [a, b]": "flow-style collections are not supported",
            "evidence: |": "block scalars",
        }
        for text, expected in cases.items():
            with self.subTest(text=text):
                self.assert_defect(render(with_field("evidence", text)), expected)

    def test_three_level_nesting_is_rejected_rather_than_flattened(self) -> None:
        self.assert_defect(
            render(with_field("origin", "origin:\n  task:\n    nested: value")),
            "supports one level of nesting only",
        )

    def test_tab_indentation_is_reported(self) -> None:
        self.assert_defect(
            render(with_field("evidence", 'evidence:\n\t- "a"')), "tab indentation is not valid"
        )

    def test_parse_record_raises_for_non_records(self) -> None:
        with self.assertRaises(RecordFormatError):
            parse_record("# Just a Markdown heading\n")


class SchemaAgreementTests(unittest.TestCase):
    """The JSON Schema document and the Python validator must not diverge."""

    def setUp(self) -> None:
        self.schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))

    def test_required_and_optional_keys_agree(self) -> None:
        self.assertEqual(list(self.schema["required"]), list(REQUIRED_KEYS))
        self.assertEqual(
            sorted(self.schema["properties"]), sorted(set(REQUIRED_KEYS) | set(OPTIONAL_KEYS))
        )
        self.assertIs(self.schema["additionalProperties"], False)

    def test_enums_and_patterns_agree(self) -> None:
        properties = self.schema["properties"]
        self.assertEqual(list(properties["status"]["enum"]), list(STATUS_VALUES))
        self.assertEqual(
            list(properties["recommended_action"]["enum"]), list(RECOMMENDED_ACTIONS)
        )
        self.assertEqual(
            list(properties["disposition"]["properties"]["action"]["enum"]),
            list(DISPOSITION_ACTIONS),
        )
        self.assertEqual(properties["id"]["pattern"], ID_PATTERN.pattern)
        self.assertEqual(list(properties["origin"]["required"]), list(ORIGIN_KEYS))
        self.assertEqual(list(properties["disposition"]["required"]), list(DISPOSITION_KEYS))

    def test_schema_does_not_offer_delete_as_a_recommended_action(self) -> None:
        self.assertNotIn("delete", self.schema["properties"]["recommended_action"]["enum"])

    def test_valid_fixture_satisfies_the_schema_document(self) -> None:
        try:
            import jsonschema
        except ImportError:  # pragma: no cover - depends on the environment
            self.skipTest("jsonschema is not installed (it is not a knowledge-store dependency)")
        frontmatter, _ = parse_record(valid_record())
        jsonschema.Draft202012Validator(self.schema).validate(frontmatter)

    def test_schema_rejects_what_the_validator_rejects(self) -> None:
        try:
            import jsonschema
        except ImportError:  # pragma: no cover - depends on the environment
            self.skipTest("jsonschema is not installed (it is not a knowledge-store dependency)")
        validator = jsonschema.Draft202012Validator(self.schema)
        frontmatter, _ = parse_record(valid_record())
        frontmatter["ingest_now"] = True
        self.assertTrue(list(validator.iter_errors(frontmatter)))


class CommittedProposedKnowledgeTests(unittest.TestCase):
    """The real, committed `proposed-knowledge/` directory must conform.

    This asserts conformance unconditionally. If a record has not yet been
    migrated to the frontmatter format, this test fails and names the record
    -- that is the intended signal, not a reason to weaken the assertion.
    """

    def test_committed_records_are_well_formed(self) -> None:
        findings = validate_directory(DEFAULT_DIRECTORY)
        self.assertEqual(
            findings,
            [],
            "committed staged records under roster/knowledge-store/proposed-knowledge/ do not "
            "conform to the staged-record contract:\n" + "\n".join(findings),
        )

    def test_the_directory_actually_contains_records(self) -> None:
        # Non-vacuity guard for the test above: an empty directory would make
        # it pass while checking nothing.
        self.assertTrue(
            list(DEFAULT_DIRECTORY.glob("*.md")),
            f"{DEFAULT_DIRECTORY} contains no *.md records, so the conformance test above is vacuous",
        )

    def test_a_corrupted_copy_of_the_real_directory_is_reported(self) -> None:
        # Non-vacuity guard of a different kind: proves validate_directory can
        # fail at all on this exact corpus, rather than passing because it
        # silently skips these files.
        with tempfile.TemporaryDirectory(prefix="staged-records-copy-") as temporary:
            copy = Path(temporary) / "proposed-knowledge"
            shutil.copytree(DEFAULT_DIRECTORY, copy)
            records = sorted(copy.glob("*.md"))
            self.assertTrue(records, "nothing was copied, so this guard proves nothing")
            target = records[0]
            target.write_text(
                target.read_text(encoding="utf-8") + "\nAn uncounted extra claim.\n",
                encoding="utf-8",
            )
            findings = validate_directory(copy)
            self.assertTrue(
                any("content_digest does not match the record body" in f for f in findings),
                f"expected the appended body text to break the digest; got {findings}",
            )


class DistinctMessageTests(unittest.TestCase):
    """Each rule must produce its own actionable message, not a shared one."""

    def test_each_rule_produces_a_distinct_first_finding(self) -> None:
        risky = [
            (key, "untrusted_instruction_risk: true" if key == "untrusted_instruction_risk" else text)
            for key, text in dispositioned("accepted")
        ]
        proposed_with_disposition = frontmatter_fields()
        proposed_with_disposition.append(("disposition", DISPOSITION_BLOCK.format(action="accepted")))

        cases = {
            "rule1_missing_key": render(without_field("title")),
            "rule2_delete": render(with_field("recommended_action", "recommended_action: delete")),
            "rule3_digest": render(frontmatter_fields(), body=VALID_BODY + "\nextra\n"),
            "rule4_status_disposition": render(proposed_with_disposition),
            "rule5_automatic_defer": render(risky),
            "rule6_absolute_path": render(
                with_field("evidence", 'evidence:\n  - "/home/deagy/sdk/cadre/AGENT.md"')
            ),
        }
        messages = {}
        for name, record in cases.items():
            findings = validate_record(record)
            self.assertTrue(findings, f"{name} produced no finding at all")
            messages[name] = findings[0]
        self.assertEqual(
            len(set(messages.values())),
            len(messages),
            f"rules share a message instead of each naming its own fix: {messages}",
        )


class CommandLineTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="staged-records-cli-")
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)

    def _run(self, argv: list[str]) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = main(argv)
        return code, out.getvalue(), err.getvalue()

    def test_clean_directory_exits_zero(self) -> None:
        (self.directory / "record.md").write_text(valid_record(), encoding="utf-8")
        code, stdout, _ = self._run(["--directory", str(self.directory)])
        self.assertEqual(code, 0)
        self.assertIn("staged-record validation passed", stdout)
        # The success line must not overclaim: it says nothing about whether
        # any agent emitted a handoff, because nothing can.
        self.assertIn("does not verify that any agent emitted", stdout)

    def test_defective_directory_exits_one_and_names_the_defect_on_stderr(self) -> None:
        (self.directory / "record.md").write_text(
            render(without_field("status")), encoding="utf-8"
        )
        code, _, stderr = self._run(["--directory", str(self.directory)])
        self.assertEqual(code, 1)
        self.assertIn("missing required frontmatter key 'status'", stderr)

    def test_check_flag_is_accepted_as_a_no_op(self) -> None:
        (self.directory / "record.md").write_text(valid_record(), encoding="utf-8")
        code, _, _ = self._run(["--directory", str(self.directory), "--check"])
        self.assertEqual(code, 0)


if __name__ == "__main__":
    unittest.main()
