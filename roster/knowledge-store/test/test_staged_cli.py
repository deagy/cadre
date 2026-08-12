"""CLI tests for the staged-record verbs: propose, list-staged, show-staged.

These cover the two properties the proposal turns on. First, staging is *per
project* — the shared global-fallback store refuses records structurally rather
than relying on `--source` discipline. Second, `show-staged` exists at all: a
database row cannot be read in a diff the way a committed file could, so
losing pull-request review means discoverability has to be built or the corpus
becomes invisible.
"""

from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path

SRC = Path(__file__).resolve().parents[1] / "src"
# Purpose-built fixtures, not the live corpus: these tests must exercise the
# awkward cases deliberately (dispositioned, untrusted-flagged, quotes,
# colons, unicode, YAML-keyword lookalikes) rather than whatever the real
# records happen to contain, and must not change meaning when the corpus does.
RECORDS = Path(__file__).resolve().parent / "fixtures"
# The fixture corpus deliberately spans every status, so importing it admits
# dispositioned records and `import-staged` requires a named human for that.
# Tests whose subject is something else still have to name one -- that is the
# point of the requirement, not an inconvenience to route around, and a test
# helper that quietly supplied it would hide the one thing worth noticing.
IMPORT_AUTHORIZER = "operator@example.invalid"
if str(SRC) not in sys.path:
    sys.path.insert(0, str(SRC))

import cli  # noqa: E402
import staged_records  # noqa: E402


def _a_record() -> tuple[Path, dict]:
    """A fixture `propose` accepts: status 'proposed', no disposition.

    Named explicitly rather than taken as `sorted(...)[0]`, which used to
    resolve to `accepted-with-disposition.md`. That was harmless only while
    `propose` would stage anything well-formed; now that it refuses an
    already-decided record, a positional pick would make every propose test
    assert the refusal instead of the behaviour it was written for -- and
    would do so again the moment a fixture is added with an earlier name.
    """
    path = RECORDS / "proposed-minimal.md"
    frontmatter, _ = staged_records.parse_record(path.read_text(encoding="utf-8"))
    return path, frontmatter


def _a_dispositioned_record() -> tuple[Path, dict]:
    """A fixture carrying a steward's decision -- legitimate for
    `import-staged`, refused by `propose`."""
    path = RECORDS / "accepted-with-disposition.md"
    frontmatter, _ = staged_records.parse_record(path.read_text(encoding="utf-8"))
    return path, frontmatter


def _a_finding(**overrides: object) -> dict:
    """A well-formed structured finding for `propose --from-finding`.

    Every key `finding_record.FINDING_KEYS` requires, plus `summary`
    (which becomes the record body). Callers
    override individual keys (including deleting one, for the
    missing-field tests) rather than hand-building a fresh mapping each time.
    """
    base: dict[str, object] = {
        "title": "a durable finding surfaced during review",
        "summary": "## Finding\n\nSome durable, evidenced observation worth keeping.\n",
        "evidence": ["roster/knowledge-store/src/cli.py:1"],
        "origin": {"task": "TASK-1", "artifact": "cli.py", "revision": "deadbeef"},
        "proposed_classification": "internal",
        "source_scope": "testing",
        "sensitivity_notes": "",
        "conflicts_or_staleness": "",
        "recommended_action": "ingest",
        "untrusted_instruction_risk": False,
        "staged_by": "reviewer-agent",
    }
    base.update(overrides)
    return base


class StagedCliTests(unittest.TestCase):
    def setUp(self) -> None:
        self.workspace = tempfile.TemporaryDirectory()
        self.addCleanup(self.workspace.cleanup)
        root = Path(self.workspace.name)
        self.config_path = root / "config.json"
        # An explicit --config is the TIER_EXPLICIT_CONFIG path: a real
        # partition this test owns, never the operator's own store.
        self.config_path.write_text(
            json.dumps({"database": str(root / "knowledge.db")}), encoding="utf-8"
        )

    def _run(self, *arguments: str) -> dict:
        return cli.run([*arguments, "--config", str(self.config_path)])

    def test_propose_stages_a_record_and_reports_its_digest(self) -> None:
        path, frontmatter = _a_record()
        result = self._run("propose", "--input", str(path))
        self.assertEqual(result["status"], "staged")
        self.assertEqual(result["id"], frontmatter["id"])
        self.assertEqual(result["content_digest"], frontmatter["content_digest"])
        # The response must not let a caller read "staged" as "available".
        self.assertIn("not ingestion", result["note"])

    def test_propose_rejects_a_malformed_record_without_staging_it(self) -> None:
        path, _ = _a_record()
        broken = path.read_text(encoding="utf-8").replace("recommended_action: ingest", "recommended_action: delete")
        target = Path(self.workspace.name) / "broken.md"
        target.write_text(broken, encoding="utf-8")
        with self.assertRaises(Exception) as caught:
            self._run("propose", "--input", str(target))
        self.assertIn("delete", str(caught.exception))
        self.assertEqual(self._run("list-staged")["records"], [])

    def test_propose_reads_stdin(self) -> None:
        path, frontmatter = _a_record()
        original = sys.stdin
        sys.stdin = __import__("io").StringIO(path.read_text(encoding="utf-8"))
        self.addCleanup(lambda: setattr(sys, "stdin", original))
        self.assertEqual(self._run("propose", "--input", "-")["id"], frontmatter["id"])

    def test_list_staged_filters_by_status(self) -> None:
        # Loaded through `import-staged`, not `propose`: this test needs a
        # corpus spanning every status, and `propose` now (correctly) refuses
        # to be the door a dispositioned record enters through. Importing is
        # the verb that exists for loading an already-decided corpus.
        self._run("import-staged", "--directory", str(RECORDS), "--authorized-by", IMPORT_AUTHORIZER)
        everything = self._run("list-staged")["records"]
        # Partition rather than a two-status sum: the earlier version assumed
        # the corpus held only proposed and accepted records, which was true of
        # the data at the time and not of the contract. Every status a record
        # can carry must be reachable through the filter.
        by_status = {
            status: self._run("list-staged", "--status", status)["records"]
            for status in ("proposed", "accepted", "rejected", "deferred")
        }
        self.assertEqual(len(everything), sum(len(records) for records in by_status.values()))
        for status, records in by_status.items():
            self.assertTrue(all(record["status"] == status for record in records), status)
        self.assertTrue(by_status["accepted"], "fixtures must include a dispositioned record")
        self.assertTrue(by_status["deferred"], "fixtures must include a deferred record")

    def test_show_staged_returns_the_full_record_not_just_a_summary(self) -> None:
        path, frontmatter = _a_record()
        self._run("propose", "--input", str(path))
        shown = self._run("show-staged", "--id", frontmatter["id"])
        self.assertEqual(shown["frontmatter"], frontmatter)
        self.assertIn("body", shown)
        # The rendered text must be re-proposable: what you read is exactly
        # what the contract would accept back.
        self.assertEqual(staged_records.validate_record(shown["text"]), [])

    def test_show_staged_names_an_unknown_id_rather_than_returning_empty(self) -> None:
        with self.assertRaises(ValueError) as caught:
            self._run("show-staged", "--id", "KS-20260101-not-here")
        self.assertIn("KS-20260101-not-here", str(caught.exception))

    def test_an_invalid_status_filter_is_rejected_by_the_parser(self) -> None:
        with self.assertRaises(SystemExit):
            self._run("list-staged", "--status", "archived")


class ProposalCannotApproveItselfTests(unittest.TestCase):
    """`propose` is the one verb a non-steward agent may run against this
    store, and it is safe only because a proposal cannot arrive already
    approved.

    Nothing enforced that. `validate_parsed` checks that `status` and
    `disposition.action` agree and type-checks `decided_by`; the rule that a
    record's stager cannot also be its decider lived only in
    `staged_store.disposition_record`, on the `disposition-staged` path --
    the verb an agent is not supposed to use. So a record handed to
    `propose --input` with `status: accepted` and a hand-written
    `disposition: {decided_by: knowledge-store-steward}` was staged as
    written, and `ingest-accepted` then made it retrievable: a proposing
    agent could author its own approval into the corpus without a steward
    ever touching the record.

    Both tests below fail against the store as it was.
    """

    def setUp(self) -> None:
        self.workspace = tempfile.TemporaryDirectory()
        self.addCleanup(self.workspace.cleanup)
        root = Path(self.workspace.name)
        self.config_path = root / "config.json"
        self.config_path.write_text(
            json.dumps({"database": str(root / "knowledge.db")}), encoding="utf-8"
        )

    def _run(self, *arguments: str) -> dict:
        return cli.run([*arguments, "--config", str(self.config_path)])

    def _self_approved_record(self) -> Path:
        """A record whose stager wrote its own acceptance -- the exact shape
        the store used to take at face value."""
        path, _ = _a_record()
        text = path.read_text(encoding="utf-8").replace(
            "status: proposed", "status: accepted"
        ).replace(
            "content_digest:",
            "disposition:\n"
            "  action: accepted\n"
            "  reason: Approved during review.\n"
            "  classification_used: internal\n"
            "  diverged_from_proposal: false\n"
            "  decided_by: fixture-author\n"
            "content_digest:",
        )
        target = Path(self.workspace.name) / "self-approved.md"
        target.write_text(text, encoding="utf-8")
        return target

    def test_propose_refuses_a_record_that_arrives_already_accepted(self) -> None:
        target = self._self_approved_record()
        with self.assertRaises(ValueError) as caught:
            self._run("propose", "--input", str(target))
        self.assertIn("accepted", str(caught.exception))
        # Refused *before* the write, not merely reported on.
        self.assertEqual(self._run("list-staged")["records"], [])

    def test_propose_refuses_a_disposition_block_even_on_a_proposed_record(self) -> None:
        # The status check alone is not enough: a caller could leave `status`
        # untouched and smuggle the decision in beside it, which the contract
        # validator would reject for incoherence today but only by accident of
        # the two rules overlapping. The disposition block is refused on its
        # own terms, so the guard does not depend on that overlap.
        path, _ = _a_record()
        text = path.read_text(encoding="utf-8").replace(
            "content_digest:",
            "disposition:\n"
            "  action: proposed\n"
            "  reason: whatever\n"
            "  classification_used: internal\n"
            "  diverged_from_proposal: false\n"
            "  decided_by: knowledge-store-steward\n"
            "content_digest:",
        )
        target = Path(self.workspace.name) / "smuggled.md"
        target.write_text(text, encoding="utf-8")
        with self.assertRaises(ValueError) as caught:
            self._run("propose", "--input", str(target))
        self.assertIn("disposition", str(caught.exception))
        self.assertEqual(self._run("list-staged")["records"], [])

    def test_render_only_refuses_it_too(self) -> None:
        # --render-only is a preview of what `propose` would accept. A preview
        # that blesses a record the write path refuses is worse than no
        # preview: it tells the caller to go ahead.
        target = self._self_approved_record()
        with self.assertRaises(ValueError):
            self._run("propose", "--input", str(target), "--render-only")

    def test_the_dispositioned_fixture_still_imports_with_authorization(self) -> None:
        # The guard is scoped to the proposal door, not to the record shape.
        # `import-staged` re-imports an existing corpus and must keep taking
        # dispositioned records, or migration breaks -- but it now names who
        # authorized admitting decisions this store never saw made.
        path, frontmatter = _a_dispositioned_record()
        directory = self._corpus_of(path)
        imported = self._run(
            "import-staged", "--directory", str(directory), "--authorized-by", IMPORT_AUTHORIZER
        )
        self.assertEqual(imported["ids"], [frontmatter["id"]])
        self.assertEqual(imported["dispositioned"], [frontmatter["id"]])
        self.assertEqual(imported["authorized_by"], IMPORT_AUTHORIZER)

    def _corpus_of(self, *paths: Path) -> Path:
        directory = Path(self.workspace.name) / "corpus"
        directory.mkdir(exist_ok=True)
        for path in paths:
            (directory / path.name).write_text(path.read_text(encoding="utf-8"), encoding="utf-8")
        return directory


class ImportRequiresAuthorizationTests(unittest.TestCase):
    """`import-staged` was the door left standing once `propose` closed.

    It is the only remaining route by which a decision this store never
    watched being made can enter it. That is a real need -- re-importing an
    exported corpus, moving a store between machines -- but it is not a
    proposal, and for a while the only thing stopping an agent from using it
    to admit a fresh self-approved record was three docs saying "don't". This
    store has already learned once what that kind of convention is worth.

    The cost is scoped to the risky half: a batch of purely `proposed`
    records still imports with no ceremony.
    """

    def setUp(self) -> None:
        self.workspace = tempfile.TemporaryDirectory()
        self.addCleanup(self.workspace.cleanup)
        root = Path(self.workspace.name)
        self.config_path = root / "config.json"
        self.config_path.write_text(
            json.dumps({"database": str(root / "knowledge.db")}), encoding="utf-8"
        )
        self.corpus = root / "corpus"
        self.corpus.mkdir()

    def _run(self, *arguments: str) -> dict:
        return cli.run([*arguments, "--config", str(self.config_path)])

    def _place(self, path: Path, text: str | None = None, name: str | None = None) -> None:
        (self.corpus / (name or path.name)).write_text(
            text if text is not None else path.read_text(encoding="utf-8"), encoding="utf-8"
        )

    def test_a_dispositioned_batch_is_refused_without_authorization(self) -> None:
        path, frontmatter = _a_dispositioned_record()
        self._place(path)
        with self.assertRaises(ValueError) as caught:
            self._run("import-staged", "--directory", str(self.corpus))
        message = str(caught.exception)
        self.assertIn("--authorized-by", message)
        # The refusal names the records it is about, so an operator can tell
        # which of a large batch triggered it without bisecting the directory.
        self.assertIn(frontmatter["id"], message)
        self.assertEqual(self._run("list-staged")["records"], [], "a refused batch wrote rows")

    def test_a_proposed_only_batch_needs_no_authorization(self) -> None:
        # The common case -- seeding a store from proposals -- must stay
        # frictionless, or the requirement teaches people to pass
        # --authorized-by reflexively and it stops meaning anything.
        path, frontmatter = _a_record()
        self._place(path)
        imported = self._run("import-staged", "--directory", str(self.corpus))
        self.assertEqual(imported["ids"], [frontmatter["id"]])
        self.assertNotIn("authorized_by", imported)

    def test_one_dispositioned_record_gates_the_whole_batch(self) -> None:
        # Mixed batches follow the batch, not the record: importing is atomic,
        # so a partial import that admitted only the proposals would leave the
        # operator with neither the corpus they asked for nor a clear failure.
        proposed, _ = _a_record()
        decided, decided_frontmatter = _a_dispositioned_record()
        self._place(proposed)
        self._place(decided)
        with self.assertRaises(ValueError) as caught:
            self._run("import-staged", "--directory", str(self.corpus))
        self.assertIn(decided_frontmatter["id"], str(caught.exception))
        self.assertEqual(self._run("list-staged")["records"], [])

    def test_a_readme_beside_the_records_is_not_a_record(self) -> None:
        # `roster/knowledge-store/proposed-knowledge/` ships a README.md, so
        # before this the canonical snapshot directory could not be imported
        # at all: the glob picked the README up and the parse failed on its
        # missing frontmatter before reaching a single record.
        path, frontmatter = _a_record()
        self._place(path)
        self._place(path, text="# Not a record\n\nJust prose.\n", name="README.md")
        imported = self._run("import-staged", "--directory", str(self.corpus))
        self.assertEqual(imported["ids"], [frontmatter["id"]])

    def test_any_other_unparseable_file_still_fails_loudly(self) -> None:
        # The README skip is one name, not a policy of ignoring what does not
        # parse: a migration that silently drops files delivers a partial
        # corpus that looks complete.
        path, _ = _a_record()
        self._place(path)
        self._place(path, text="# Not a record\n\nJust prose.\n", name="notes.md")
        with self.assertRaises(ValueError):
            self._run("import-staged", "--directory", str(self.corpus))

    def test_the_authorization_is_persisted_not_only_echoed(self) -> None:
        # `--authorized-by` was echoed into the command's JSON and written
        # nowhere, so the "human accountable for admitting these decisions"
        # ceased to exist when the process did: the record kept an attributable
        # *decider* and had no attributable *admission*. Read back through a
        # separate invocation, which opens the store again.
        path, frontmatter = _a_dispositioned_record()
        self._place(path)
        self._run(
            "import-staged", "--directory", str(self.corpus), "--authorized-by", IMPORT_AUTHORIZER
        )
        shown = self._run("show-staged", "--id", frontmatter["id"])
        recorded = shown["import_authorizations"]
        self.assertEqual(len(recorded), 1)
        entry = recorded[0]
        self.assertEqual(entry["authorized_by"], IMPORT_AUTHORIZER)
        self.assertEqual(entry["record_id"], frontmatter["id"])
        self.assertEqual(entry["status_at_import"], frontmatter["status"])
        self.assertEqual(entry["content_digest"], frontmatter["content_digest"])
        self.assertEqual(entry["directory"], str(self.corpus))
        self.assertTrue(entry["imported_at"])

    def test_a_proposed_only_batch_records_no_authorization(self) -> None:
        # Symmetry with the gate: nothing was authorized, so nothing claims to
        # have been. An evidence row for a ceremony-free import would make the
        # log say a human vouched for something no human was asked about.
        path, frontmatter = _a_record()
        self._place(path)
        self._run("import-staged", "--directory", str(self.corpus))
        self.assertEqual(
            self._run("show-staged", "--id", frontmatter["id"])["import_authorizations"], []
        )

    def test_whitespace_is_not_an_authorization(self) -> None:
        # `if dispositioned and not authorized_by` accepted "   ", which then
        # became the stored, echoed, accountable human. The two sibling gates
        # in this store (delete_record, delete_ingested) both strip first.
        path, frontmatter = _a_dispositioned_record()
        self._place(path)
        with self.assertRaises(ValueError) as caught:
            self._run("import-staged", "--directory", str(self.corpus), "--authorized-by", "   ")
        self.assertIn("--authorized-by", str(caught.exception))
        self.assertEqual(self._run("list-staged")["records"], [])

    def test_a_named_authorizer_is_stored_without_surrounding_whitespace(self) -> None:
        path, frontmatter = _a_dispositioned_record()
        self._place(path)
        imported = self._run(
            "import-staged", "--directory", str(self.corpus),
            "--authorized-by", f"  {IMPORT_AUTHORIZER}\t",
        )
        self.assertEqual(imported["authorized_by"], IMPORT_AUTHORIZER)
        self.assertEqual(
            self._run("show-staged", "--id", frontmatter["id"])["import_authorizations"][0][
                "authorized_by"
            ],
            IMPORT_AUTHORIZER,
        )

    def test_authorization_cannot_launder_a_self_approval(self) -> None:
        # The one thing no named human can vouch for. A steward's decision the
        # store did not witness is still a decision; a record whose stager and
        # decider are the same actor is not one, so there is nothing to
        # authorize. Refused with --authorized-by present and correct.
        path, _ = _a_dispositioned_record()
        text = path.read_text(encoding="utf-8")
        frontmatter, _body = staged_records.parse_record(text)
        text = text.replace(
            f"decided_by: {frontmatter['disposition']['decided_by']}",
            f"decided_by: {frontmatter['staged_by']}",
        )
        self._place(path, text=text)
        with self.assertRaises(ValueError) as caught:
            self._run(
                "import-staged", "--directory", str(self.corpus),
                "--authorized-by", IMPORT_AUTHORIZER,
            )
        message = str(caught.exception)
        self.assertIn(frontmatter["staged_by"], message)
        self.assertIn("self-approval", message)
        self.assertEqual(self._run("list-staged")["records"], [])


class FromFindingTests(unittest.TestCase):
    """`propose --from-finding`: the low-friction path from a structured
    finding to a staged record, with id/digest/status generated rather than
    hand-authored.
    """

    def setUp(self) -> None:
        self.workspace = tempfile.TemporaryDirectory()
        self.addCleanup(self.workspace.cleanup)
        root = Path(self.workspace.name)
        self.config_path = root / "config.json"
        self.config_path.write_text(
            json.dumps({"database": str(root / "knowledge.db")}), encoding="utf-8"
        )

    def _run(self, *arguments: str) -> dict:
        return cli.run([*arguments, "--config", str(self.config_path)])

    def _write_finding(self, finding: dict, name: str = "finding.json") -> Path:
        path = Path(self.workspace.name) / name
        path.write_text(json.dumps(finding), encoding="utf-8")
        return path

    def test_propose_from_finding_generates_a_well_formed_record(self) -> None:
        finding = _a_finding()
        result = self._run("propose", "--from-finding", str(self._write_finding(finding)))
        self.assertEqual(result["status"], "staged")
        self.assertRegex(result["id"], staged_records.ID_PATTERN.pattern)
        shown = self._run("show-staged", "--id", result["id"])
        self.assertEqual(shown["frontmatter"]["title"], finding["title"])
        self.assertEqual(shown["frontmatter"]["status"], "proposed")
        self.assertEqual(shown["frontmatter"]["content_digest"], result["content_digest"])
        self.assertEqual(
            staged_records.compute_digest(shown["body"]), result["content_digest"]
        )
        # Never computed by hand here either -- it must be the one true
        # implementation's output, reachable through the normal validator.
        self.assertEqual(staged_records.validate_record(shown["text"]), [])

    def test_propose_from_finding_is_idempotent_for_identical_content(self) -> None:
        finding = _a_finding()
        path = self._write_finding(finding)
        first = self._run("propose", "--from-finding", str(path))
        second = self._run("propose", "--from-finding", str(path))
        self.assertEqual(first["id"], second["id"])
        self.assertEqual(second["status"], "already-staged")
        self.assertEqual(len(self._run("list-staged")["records"]), 1)

    def test_propose_from_finding_missing_field_names_it(self) -> None:
        finding = _a_finding()
        del finding["proposed_classification"]
        with self.assertRaises(ValueError) as caught:
            self._run("propose", "--from-finding", str(self._write_finding(finding)))
        self.assertIn("proposed_classification", str(caught.exception))
        self.assertEqual(self._run("list-staged")["records"], [])

    def test_propose_from_finding_does_not_default_untrusted_instruction_risk(self) -> None:
        finding = _a_finding()
        del finding["untrusted_instruction_risk"]
        with self.assertRaises(ValueError) as caught:
            self._run("propose", "--from-finding", str(self._write_finding(finding)))
        self.assertIn("untrusted_instruction_risk", str(caught.exception))
        self.assertEqual(self._run("list-staged")["records"], [])

    def test_propose_from_finding_does_not_default_proposed_classification(self) -> None:
        finding = _a_finding()
        del finding["proposed_classification"]
        with self.assertRaises(ValueError) as caught:
            self._run("propose", "--from-finding", str(self._write_finding(finding)))
        self.assertIn("proposed_classification", str(caught.exception))

    def test_propose_from_finding_requires_a_non_empty_body(self) -> None:
        finding = _a_finding(summary="   ")
        with self.assertRaises(ValueError) as caught:
            self._run("propose", "--from-finding", str(self._write_finding(finding)))
        self.assertIn("summary", str(caught.exception))

    def test_propose_from_finding_rejects_invalid_json(self) -> None:
        path = Path(self.workspace.name) / "broken.json"
        path.write_text("{not valid json", encoding="utf-8")
        with self.assertRaises(ValueError) as caught:
            self._run("propose", "--from-finding", str(path))
        self.assertIn("JSON", str(caught.exception))

    def test_propose_from_finding_reads_stdin(self) -> None:
        finding = _a_finding()
        original = sys.stdin
        sys.stdin = __import__("io").StringIO(json.dumps(finding))
        self.addCleanup(lambda: setattr(sys, "stdin", original))
        result = self._run("propose", "--from-finding", "-")
        self.assertEqual(result["status"], "staged")

    def test_propose_from_finding_collision_with_different_content_is_refused(self) -> None:
        import finding_record

        original_generate_id = finding_record.generate_id
        finding_record.generate_id = lambda title, digest, when=None: "KS-20260101-forced-collision"
        self.addCleanup(lambda: setattr(finding_record, "generate_id", original_generate_id))

        first = _a_finding(summary="the first, original finding body")
        second = _a_finding(
            title="an unrelated second finding", summary="a completely different finding body"
        )
        self._run("propose", "--from-finding", str(self._write_finding(first, "first.json")))
        with self.assertRaises(Exception) as caught:
            self._run("propose", "--from-finding", str(self._write_finding(second, "second.json")))
        self.assertIn("collides", str(caught.exception))
        # The first record must be untouched by the refused second write.
        shown = self._run("show-staged", "--id", "KS-20260101-forced-collision")
        self.assertIn("first, original finding body", shown["body"])
        self.assertEqual(len(self._run("list-staged")["records"]), 1)

    def test_input_and_from_finding_are_mutually_exclusive(self) -> None:
        # --input and --from-finding cannot both be given -- argparse enforces
        # this structurally rather than the two code paths racing at runtime.
        with self.assertRaises(SystemExit):
            self._run(
                "propose", "--input", "-", "--from-finding", "-",
            )


class RenderOnlyTests(unittest.TestCase):
    """`propose --render-only`: preview a record without staging it."""

    def setUp(self) -> None:
        self.workspace = tempfile.TemporaryDirectory()
        self.addCleanup(self.workspace.cleanup)
        root = Path(self.workspace.name)
        self.config_path = root / "config.json"
        self.config_path.write_text(
            json.dumps({"database": str(root / "knowledge.db")}), encoding="utf-8"
        )

    def _run(self, *arguments: str) -> dict:
        return cli.run([*arguments, "--config", str(self.config_path)])

    def test_render_only_with_from_finding_does_not_stage(self) -> None:
        finding = _a_finding()
        path = Path(self.workspace.name) / "finding.json"
        path.write_text(json.dumps(finding), encoding="utf-8")
        result = self._run("propose", "--from-finding", str(path), "--render-only")
        self.assertEqual(result["status"], "rendered")
        self.assertIn("text", result)
        self.assertEqual(staged_records.validate_record(result["text"]), [])
        self.assertEqual(self._run("list-staged")["records"], [])

    def test_render_only_with_input_does_not_stage(self) -> None:
        path, frontmatter = _a_record()
        result = self._run("propose", "--input", str(path), "--render-only")
        self.assertEqual(result["status"], "rendered")
        self.assertEqual(result["id"], frontmatter["id"])
        self.assertEqual(self._run("list-staged")["records"], [])

    def test_render_only_still_validates_and_refuses_a_broken_record(self) -> None:
        finding = _a_finding()
        del finding["source_scope"]
        path = Path(self.workspace.name) / "finding.json"
        path.write_text(json.dumps(finding), encoding="utf-8")
        with self.assertRaises(ValueError) as caught:
            self._run("propose", "--from-finding", str(path), "--render-only")
        self.assertIn("source_scope", str(caught.exception))


class StagingScopeTests(unittest.TestCase):
    """Decision 4: records are staged per project, enforced not conventional."""

    def test_the_shared_global_store_refuses_staging(self) -> None:
        original = cli.load_config

        def fallback_config(config_path, return_tier=False):
            configuration, _ = original(config_path, return_tier=True)
            return (configuration, cli.TIER_GLOBAL_FALLBACK) if return_tier else configuration

        cli.load_config = fallback_config
        self.addCleanup(lambda: setattr(cli, "load_config", original))

        workspace = tempfile.TemporaryDirectory()
        self.addCleanup(workspace.cleanup)
        config_path = Path(workspace.name) / "config.json"
        config_path.write_text(
            json.dumps({"database": str(Path(workspace.name) / "knowledge.db")}), encoding="utf-8"
        )
        path, _ = _a_record()
        for command in (
            ("propose", "--input", str(path)),
            ("list-staged",),
            ("show-staged", "--id", "KS-20260101-anything"),
        ):
            with self.subTest(command=command[0]):
                with self.assertRaises(ValueError) as caught:
                    cli.run([*command, "--config", str(config_path)])
                message = str(caught.exception)
                self.assertIn("per project", message)
                # The message has to say what to do, not only what failed.
                self.assertIn(".agents/knowledge-store/config.json", message)


class MigrationTests(unittest.TestCase):
    """Step 3's safety check, committed rather than performed once by hand.

    The proposal is explicit that the ten committed records are migrated and
    verified by export-and-diff *before* the originals are deleted. That order
    only protects anything if the check keeps running, so it lives here.

    The comparison is by record id and content, never by filename: `export`
    writes `<id>.md`, and the ids deliberately differ from the filenames the
    records were first written under.
    """

    def setUp(self) -> None:
        self.workspace = tempfile.TemporaryDirectory()
        self.addCleanup(self.workspace.cleanup)
        root = Path(self.workspace.name)
        self.config_path = root / "config.json"
        self.config_path.write_text(
            json.dumps({"database": str(root / "knowledge.db")}), encoding="utf-8"
        )
        self.exported = root / "exported"

    def _run(self, *arguments: str) -> dict:
        return cli.run([*arguments, "--config", str(self.config_path)])

    def test_the_committed_corpus_survives_import_then_export(self) -> None:
        originals = {}
        for path in sorted(RECORDS.glob("*.md")):
            frontmatter, body = staged_records.parse_record(path.read_text(encoding="utf-8"))
            originals[frontmatter["id"]] = (frontmatter, body)

        imported = self._run("import-staged", "--directory", str(RECORDS), "--authorized-by", IMPORT_AUTHORIZER)
        self.assertEqual(imported["count"], len(originals))
        self.assertEqual(set(imported["ids"]), set(originals))

        exported = self._run("export-staged", "--output", str(self.exported))
        self.assertEqual(set(exported["ids"]), set(originals))

        for record_id, (frontmatter, body) in originals.items():
            with self.subTest(record=record_id):
                written = self.exported / f"{record_id}.md"
                self.assertTrue(written.is_file(), "export did not write this record")
                round_tripped, round_tripped_body = staged_records.parse_record(
                    written.read_text(encoding="utf-8")
                )
                self.assertEqual(round_tripped, frontmatter, "frontmatter changed in migration")
                self.assertEqual(
                    staged_records.compute_digest(round_tripped_body),
                    staged_records.compute_digest(body),
                    "body changed in migration",
                )
                self.assertEqual(staged_records.validate_record(written.read_text(encoding="utf-8")), [])

    def test_import_is_atomic_across_the_batch(self) -> None:
        # A half-imported migration is the worst outcome: the operator cannot
        # tell what made it without the diff they were about to rely on.
        staging = Path(self.workspace.name) / "batch"
        staging.mkdir()
        good = sorted(RECORDS.glob("*.md"))[0]
        # Names chosen so the VALID record sorts first: with the invalid one
        # first, a non-atomic import would fail before writing anything and
        # this assertion would pass without proving atomicity at all.
        (staging / "a-good.md").write_text(good.read_text(encoding="utf-8"), encoding="utf-8")
        (staging / "b-bad.md").write_text(
            good.read_text(encoding="utf-8").replace("recommended_action: ingest", "recommended_action: delete"),
            encoding="utf-8",
        )
        with self.assertRaises(ValueError) as caught:
            self._run("import-staged", "--directory", str(staging))
        self.assertIn("b-bad.md", str(caught.exception))
        self.assertEqual(self._run("list-staged")["records"], [], "a rejected batch left rows behind")

    def test_export_filters_by_status(self) -> None:
        self._run("import-staged", "--directory", str(RECORDS), "--authorized-by", IMPORT_AUTHORIZER)
        accepted = self._run("export-staged", "--output", str(self.exported / "accepted"), "--status", "accepted")
        self.assertTrue(accepted["ids"])
        for record_id in accepted["ids"]:
            frontmatter, _ = staged_records.parse_record(
                (self.exported / "accepted" / f"{record_id}.md").read_text(encoding="utf-8")
            )
            self.assertEqual(frontmatter["status"], "accepted")
            self.assertIn("disposition", frontmatter)

    def test_export_reports_an_empty_store_honestly(self) -> None:
        result = self._run("export-staged", "--output", str(self.exported))
        self.assertEqual(result["count"], 0)
        self.assertEqual(result["ids"], [])

    def test_import_rejects_a_directory_with_no_records(self) -> None:
        empty = Path(self.workspace.name) / "empty"
        empty.mkdir()
        with self.assertRaises(ValueError) as caught:
            self._run("import-staged", "--directory", str(empty))
        self.assertIn("No .md staged-record files", str(caught.exception))


class ImportRestoresDispositionHistoryTests(unittest.TestCase):
    """The round trip `import-staged`'s docstring claims, actually closed.

    Export writes each record's disposition history to a `<id>.history.json`
    sidecar, because the frontmatter dialect is one level deep and cannot hold
    a list of decisions. Import globbed `*.md` and never looked at a sidecar,
    so the advertised uses -- "re-importing an exported corpus, moving a store
    between machines" -- silently discarded every earlier decision:
    `export-staged --check` straight after an import reported `history_drift`
    for each record that had one. The committed corpus ships 20 sidecars whose
    whole purpose is that this audit trail exists nowhere else.

    The fixture corpus has no histories, which is exactly why the existing
    round-trip tests could not see any of this. These build them the real way,
    through `disposition-staged`, then move a store.
    """

    def setUp(self) -> None:
        self.workspace = tempfile.TemporaryDirectory()
        self.addCleanup(self.workspace.cleanup)
        root = Path(self.workspace.name)
        self.source_config = root / "source-config.json"
        self.source_config.write_text(
            json.dumps({"database": str(root / "source.db")}), encoding="utf-8"
        )
        self.destination_config = root / "destination-config.json"
        self.destination_config.write_text(
            json.dumps({"database": str(root / "destination.db")}), encoding="utf-8"
        )
        self.exported = root / "exported"
        self.record_id = next(
            frontmatter["id"]
            for frontmatter in (
                staged_records.parse_record(path.read_text(encoding="utf-8"))[0]
                for path in sorted(RECORDS.glob("*.md"))
            )
            if frontmatter["status"] == "proposed"
        )
        self._source("import-staged", "--directory", str(RECORDS), "--authorized-by", IMPORT_AUTHORIZER)

    def _source(self, *arguments: str) -> dict:
        return cli.run([*arguments, "--config", str(self.source_config)])

    def _destination(self, *arguments: str) -> dict:
        return cli.run([*arguments, "--config", str(self.destination_config)])

    def _disposition(self, action: str, reason: str, decided_by: str = "a-steward") -> dict:
        return self._source(
            "disposition-staged", "--id", self.record_id, "--action", action,
            "--reason", reason, "--classification-used", "internal", "--decided-by", decided_by,
        )

    def _export(self, name: str = "exported") -> Path:
        destination = Path(self.workspace.name) / name
        self._source("export-staged", "--output", str(destination))
        return destination

    def test_moving_a_store_keeps_the_disposition_history(self) -> None:
        self._disposition("deferred", "waiting on a second opinion")
        self._disposition("accepted", "second opinion agreed")
        expected = self._source("show-staged", "--id", self.record_id)["disposition_history"]
        self.assertEqual([entry["action"] for entry in expected], ["deferred", "accepted"])

        directory = self._export()
        imported = self._destination(
            "import-staged", "--directory", str(directory), "--authorized-by", IMPORT_AUTHORIZER
        )
        self.assertEqual(imported["disposition_history_rows_restored"], 2)
        self.assertEqual(
            self._destination("show-staged", "--id", self.record_id)["disposition_history"],
            expected,
        )
        # The check the drift originally showed up in: clean on the machine the
        # corpus just arrived at.
        check = self._destination("export-staged", "--output", str(directory), "--check")
        self.assertTrue(check["clean"], check)
        self.assertEqual(check["history_drift"], [])

    def test_reimporting_the_same_export_restores_nothing_twice(self) -> None:
        self._disposition("accepted", "durable and well evidenced")
        directory = self._export()
        first = self._destination(
            "import-staged", "--directory", str(directory), "--authorized-by", IMPORT_AUTHORIZER
        )
        self.assertEqual(first["disposition_history_rows_restored"], 1)
        second = self._destination(
            "import-staged", "--directory", str(directory), "--authorized-by", IMPORT_AUTHORIZER
        )
        self.assertEqual(second["disposition_history_rows_restored"], 0)
        history = self._destination("show-staged", "--id", self.record_id)["disposition_history"]
        self.assertEqual([entry["sequence"] for entry in history], [1])

    def test_a_history_the_store_already_holds_differently_is_refused(self) -> None:
        # Append-only means a sidecar cannot quietly replace retained history:
        # that would be the one route to erasing a decision with no evidence
        # left behind, which is precisely what delete-staged's evidence table
        # exists to prevent.
        self._disposition("deferred", "waiting on a second opinion")
        early = self._export("early")
        self._disposition("accepted", "second opinion agreed")
        later = self._export("later")

        self._destination(
            "import-staged", "--directory", str(later), "--authorized-by", IMPORT_AUTHORIZER
        )
        with self.assertRaises(Exception) as caught:
            self._destination(
                "import-staged", "--directory", str(early), "--authorized-by", IMPORT_AUTHORIZER
            )
        self.assertIn("append-only", str(caught.exception))
        history = self._destination("show-staged", "--id", self.record_id)["disposition_history"]
        self.assertEqual([entry["action"] for entry in history], ["deferred", "accepted"])

    def test_a_record_contradicting_retained_history_is_refused_without_a_sidecar(self) -> None:
        # No sidecar is legitimate (two records in the committed corpus predate
        # the history table), but it must not become a way to leave a record
        # disagreeing with history the store still holds -- amending a
        # disposition is disposition-staged's job, and that appends.
        self._disposition("deferred", "waiting on a second opinion")
        directory = self._export()
        self._destination(
            "import-staged", "--directory", str(directory), "--authorized-by", IMPORT_AUTHORIZER
        )
        amended = Path(self.workspace.name) / "amended"
        amended.mkdir()
        record = (directory / f"{self.record_id}.md").read_text(encoding="utf-8")
        (amended / f"{self.record_id}.md").write_text(
            record.replace("deferred", "accepted"), encoding="utf-8"
        )
        with self.assertRaises(Exception) as caught:
            self._destination(
                "import-staged", "--directory", str(amended), "--authorized-by", IMPORT_AUTHORIZER
            )
        self.assertIn("disposition-staged", str(caught.exception))

    def test_a_sidecar_that_contradicts_its_record_is_refused(self) -> None:
        self._disposition("accepted", "durable and well evidenced")
        directory = self._export()
        sidecar = directory / f"{self.record_id}.history.json"
        history = json.loads(sidecar.read_text(encoding="utf-8"))
        history[0]["reason"] = "a reason the record does not carry"
        sidecar.write_text(json.dumps(history), encoding="utf-8")
        with self.assertRaises(ValueError) as caught:
            self._destination(
                "import-staged", "--directory", str(directory), "--authorized-by", IMPORT_AUTHORIZER
            )
        self.assertIn("disagrees", str(caught.exception))
        self.assertEqual(self._destination("list-staged")["records"], [])

    def test_a_malformed_sidecar_is_refused_rather_than_skipped(self) -> None:
        # Skipping it would restore the original bug through the back door,
        # and silently: the import would report success and lose the history.
        self._disposition("accepted", "durable and well evidenced")
        directory = self._export()
        (directory / f"{self.record_id}.history.json").write_text("{not json", encoding="utf-8")
        with self.assertRaises(ValueError) as caught:
            self._destination(
                "import-staged", "--directory", str(directory), "--authorized-by", IMPORT_AUTHORIZER
            )
        self.assertIn("not valid JSON", str(caught.exception))
        self.assertEqual(self._destination("list-staged")["records"], [])

    def test_a_sidecar_cannot_launder_a_self_approval(self) -> None:
        # The frontmatter check catches a self-approved *current* disposition.
        # A history entry is a decision too, so an earlier self-decision hidden
        # behind a legitimate latest one is the same laundering with an extra
        # step -- and the record would end up carrying it in its audit trail.
        self._disposition("accepted", "durable and well evidenced")
        directory = self._export()
        record = directory / f"{self.record_id}.md"
        staged_by = staged_records.parse_record(record.read_text(encoding="utf-8"))[0]["staged_by"]
        sidecar = directory / f"{self.record_id}.history.json"
        history = json.loads(sidecar.read_text(encoding="utf-8"))
        earlier = dict(history[0], sequence=1, action="deferred", decided_by=staged_by)
        history = [earlier, dict(history[0], sequence=2)]
        sidecar.write_text(json.dumps(history), encoding="utf-8")
        with self.assertRaises(ValueError) as caught:
            self._destination(
                "import-staged", "--directory", str(directory), "--authorized-by", IMPORT_AUTHORIZER
            )
        message = str(caught.exception)
        self.assertIn(staged_by, message)
        self.assertIn("self-approval", message)
        self.assertEqual(self._destination("list-staged")["records"], [])

    def test_a_gap_in_the_sequence_is_refused(self) -> None:
        self._disposition("accepted", "durable and well evidenced")
        directory = self._export()
        sidecar = directory / f"{self.record_id}.history.json"
        history = json.loads(sidecar.read_text(encoding="utf-8"))
        sidecar.write_text(json.dumps([dict(history[0], sequence=7)]), encoding="utf-8")
        with self.assertRaises(ValueError) as caught:
            self._destination(
                "import-staged", "--directory", str(directory), "--authorized-by", IMPORT_AUTHORIZER
            )
        self.assertIn("numbered from 1", str(caught.exception))

    def test_the_committed_corpus_round_trips_through_a_second_store(self) -> None:
        """The real corpus, not a fixture: 20 of its 22 records ship a history.

        This is the case the reviewer reproduced -- import the committed
        snapshot, export it, and `--check` reported `history_drift` on 20
        records. It is skipped rather than failed if the snapshot is absent, so
        the test does not become a reason not to move that directory.
        """
        snapshot = Path(__file__).resolve().parents[1] / "proposed-knowledge"
        if not snapshot.is_dir():
            self.skipTest("the committed proposed-knowledge snapshot is not present")
        histories = sorted(snapshot.glob("*.history.json"))
        if not histories:
            self.skipTest("the committed snapshot carries no disposition histories")
        imported = self._destination(
            "import-staged", "--directory", str(snapshot), "--authorized-by", IMPORT_AUTHORIZER
        )
        self.assertGreaterEqual(imported["disposition_history_rows_restored"], len(histories))
        check = self._destination("export-staged", "--output", str(snapshot), "--check")
        self.assertEqual(check["history_drift"], [])
        self.assertTrue(check["clean"], check)


class ExportCheckTests(unittest.TestCase):
    """`export-staged --check`: compare the store to a committed snapshot,
    writing nothing. A local-only signal -- CI has no store to compare
    against, only this machine does.
    """

    def setUp(self) -> None:
        self.workspace = tempfile.TemporaryDirectory()
        self.addCleanup(self.workspace.cleanup)
        root = Path(self.workspace.name)
        self.config_path = root / "config.json"
        self.config_path.write_text(
            json.dumps({"database": str(root / "knowledge.db")}), encoding="utf-8"
        )
        self.exported = root / "exported"

    def _run(self, *arguments: str) -> dict:
        return cli.run([*arguments, "--config", str(self.config_path)])

    def test_check_reports_clean_immediately_after_a_real_export(self) -> None:
        self._run("import-staged", "--directory", str(RECORDS), "--authorized-by", IMPORT_AUTHORIZER)
        self._run("export-staged", "--output", str(self.exported))
        result = self._run("export-staged", "--output", str(self.exported), "--check")
        self.assertEqual(result["status"], "checked")
        self.assertTrue(result["clean"])
        self.assertEqual(result["missing_from_snapshot"], [])
        self.assertEqual(result["stale_in_snapshot"], [])
        self.assertEqual(result["extra_in_snapshot"], [])
        self.assertEqual(result["history_drift"], [])
        self.assertIn("Local-only", result["note"])

    def test_check_writes_nothing(self) -> None:
        self._run("import-staged", "--directory", str(RECORDS), "--authorized-by", IMPORT_AUTHORIZER)
        result = self._run("export-staged", "--output", str(self.exported), "--check")
        self.assertFalse(self.exported.exists(), "--check must not create the output directory")
        self.assertFalse(result["clean"])
        self.assertTrue(result["missing_from_snapshot"])

    def test_check_detects_a_record_the_snapshot_has_gone_stale_on(self) -> None:
        self._run("import-staged", "--directory", str(RECORDS), "--authorized-by", IMPORT_AUTHORIZER)
        self._run("export-staged", "--output", str(self.exported))
        record_id = self._run("list-staged", "--status", "proposed")["records"][0]["id"]
        self._run(
            "disposition-staged", "--id", record_id, "--action", "accepted",
            "--reason", "drift test", "--classification-used", "internal",
            "--decided-by", "someone-else",
        )
        result = self._run("export-staged", "--output", str(self.exported), "--check")
        self.assertFalse(result["clean"])
        self.assertIn(record_id, result["stale_in_snapshot"])
        self.assertNotIn(record_id, result["missing_from_snapshot"])

    def test_check_detects_a_record_the_snapshot_still_has_after_deletion(self) -> None:
        self._run("import-staged", "--directory", str(RECORDS), "--authorized-by", IMPORT_AUTHORIZER)
        self._run("export-staged", "--output", str(self.exported))
        record_id = self._run("list-staged", "--status", "proposed")["records"][0]["id"]
        self._run("delete-staged", "--id", record_id, "--reason", "gone", "--deleted-by", "s")
        result = self._run("export-staged", "--output", str(self.exported), "--check")
        self.assertFalse(result["clean"])
        self.assertIn(record_id, result["extra_in_snapshot"])

    def test_check_ignores_the_readme(self) -> None:
        self._run("import-staged", "--directory", str(RECORDS), "--authorized-by", IMPORT_AUTHORIZER)
        self._run("export-staged", "--output", str(self.exported))
        (self.exported / "README.md").write_text("generated, do not edit\n", encoding="utf-8")
        result = self._run("export-staged", "--output", str(self.exported), "--check")
        self.assertTrue(result["clean"])


class DispositionTests(unittest.TestCase):
    """Step 4: the steward decision, with history that outlives an overwrite."""

    def setUp(self) -> None:
        self.workspace = tempfile.TemporaryDirectory()
        self.addCleanup(self.workspace.cleanup)
        root = Path(self.workspace.name)
        self.config_path = root / "config.json"
        self.config_path.write_text(
            json.dumps({"database": str(root / "knowledge.db")}), encoding="utf-8"
        )
        self.exported = root / "exported"
        # A record that arrives undispositioned, so the transitions below are
        # this test's doing rather than the corpus's.
        self.record_id = next(
            frontmatter["id"]
            for frontmatter in (
                staged_records.parse_record(path.read_text(encoding="utf-8"))[0]
                for path in sorted(RECORDS.glob("*.md"))
            )
            if frontmatter["status"] == "proposed"
        )
        self._run("import-staged", "--directory", str(RECORDS), "--authorized-by", IMPORT_AUTHORIZER)

    def _run(self, *arguments: str) -> dict:
        return cli.run([*arguments, "--config", str(self.config_path)])

    def _disposition(self, action: str, reason: str, decided_by: str = "a-steward") -> dict:
        return self._run(
            "disposition-staged", "--id", self.record_id, "--action", action,
            "--reason", reason, "--classification-used", "internal", "--decided-by", decided_by,
        )

    def test_a_disposition_updates_status_and_records_the_reason(self) -> None:
        result = self._disposition("accepted", "durable and well evidenced")
        self.assertEqual(result["status"], "accepted")
        shown = self._run("show-staged", "--id", self.record_id)
        self.assertEqual(shown["frontmatter"]["status"], "accepted")
        self.assertEqual(shown["frontmatter"]["disposition"]["reason"], "durable and well evidenced")

    def test_history_is_append_only_across_a_reversal(self) -> None:
        # The case a single overwritten field would lose: deferred, then
        # accepted. Both must survive, or this audit trail is worse than the
        # git history it replaced.
        self._disposition("deferred", "waiting on a second opinion")
        self._disposition("accepted", "second opinion agreed")
        history = self._run("show-staged", "--id", self.record_id)["disposition_history"]
        self.assertEqual([entry["action"] for entry in history], ["deferred", "accepted"])
        self.assertEqual([entry["sequence"] for entry in history], [1, 2])
        self.assertEqual(history[0]["reason"], "waiting on a second opinion")

    def test_the_proposer_cannot_disposition_their_own_record(self) -> None:
        staged_by = self._run("show-staged", "--id", self.record_id)["frontmatter"]["staged_by"]
        with self.assertRaises(Exception) as caught:
            self._disposition("accepted", "self approval", decided_by=staged_by)
        self.assertIn("cannot also disposition", str(caught.exception))
        self.assertEqual(
            self._run("show-staged", "--id", self.record_id)["frontmatter"]["status"], "proposed"
        )
        self.assertEqual(self._run("show-staged", "--id", self.record_id)["disposition_history"], [])

    def test_an_empty_reason_is_refused(self) -> None:
        with self.assertRaises(Exception) as caught:
            self._disposition("accepted", "   ")
        self.assertIn("not an audit trail", str(caught.exception))

    def test_an_illegal_disposition_leaves_no_history_row(self) -> None:
        # put_record validates before writing, so a rejected disposition must
        # not appear in history either -- otherwise history would record
        # decisions that never took effect.
        original = staged_records.validate_parsed

        def reject_everything(frontmatter, body):
            return ["synthetic contract failure"]

        staged_records.validate_parsed = reject_everything
        import staged_store

        staged_store.validate_parsed = reject_everything
        self.addCleanup(lambda: setattr(staged_store, "validate_parsed", original))
        self.addCleanup(lambda: setattr(staged_records, "validate_parsed", original))
        with self.assertRaises(Exception):
            self._disposition("accepted", "should not stick")
        staged_store.validate_parsed = original
        staged_records.validate_parsed = original
        self.assertEqual(self._run("show-staged", "--id", self.record_id)["disposition_history"], [])

    def test_export_writes_history_beside_the_record(self) -> None:
        self._disposition("deferred", "first pass")
        self._disposition("accepted", "resolved")
        result = self._run("export-staged", "--output", str(self.exported))
        self.assertGreaterEqual(result["histories"], 1)
        sidecar = self.exported / f"{self.record_id}.history.json"
        self.assertTrue(sidecar.is_file(), "export lost the disposition history")
        history = json.loads(sidecar.read_text(encoding="utf-8"))
        self.assertEqual([entry["action"] for entry in history], ["deferred", "accepted"])
        # And the record itself still carries the current disposition.
        frontmatter, _ = staged_records.parse_record(
            (self.exported / f"{self.record_id}.md").read_text(encoding="utf-8")
        )
        self.assertEqual(frontmatter["status"], "accepted")

    def test_dispositioning_an_unknown_record_names_it(self) -> None:
        with self.assertRaises(Exception) as caught:
            self._run(
                "disposition-staged", "--id", "KS-20260101-nope", "--action", "accepted",
                "--reason", "x", "--classification-used", "internal", "--decided-by", "s",
            )
        self.assertIn("KS-20260101-nope", str(caught.exception))


class DeletionTests(unittest.TestCase):
    """Step 7: deletion, with evidence that outlives the record.

    Scope note: these delete rows from the staging table. A staged record has
    never been ingested, so none of this is the ingested-content lifecycle
    capability `SECURITY.md` withholds.
    """

    def setUp(self) -> None:
        self.workspace = tempfile.TemporaryDirectory()
        self.addCleanup(self.workspace.cleanup)
        root = Path(self.workspace.name)
        self.config_path = root / "config.json"
        self.config_path.write_text(
            json.dumps({"database": str(root / "knowledge.db")}), encoding="utf-8"
        )
        self._run("import-staged", "--directory", str(RECORDS), "--authorized-by", IMPORT_AUTHORIZER)
        self.proposed = self._first_with_status("proposed")
        self.accepted = self._first_with_status("accepted")

    def _run(self, *arguments: str) -> dict:
        return cli.run([*arguments, "--config", str(self.config_path)])

    def _first_with_status(self, status: str) -> str:
        return self._run("list-staged", "--status", status)["records"][0]["id"]

    def test_deleting_a_proposed_record_removes_it_and_leaves_evidence(self) -> None:
        before = self._run("show-staged", "--id", self.proposed)["frontmatter"]
        result = self._run(
            "delete-staged", "--id", self.proposed,
            "--reason", "duplicate of an existing record", "--deleted-by", "a-steward",
        )
        self.assertEqual(result["status"], "deleted")
        with self.assertRaises(ValueError):
            self._run("show-staged", "--id", self.proposed)

        evidence = self._run("deletion-evidence")["deletions"]
        self.assertEqual(len(evidence), 1)
        entry = evidence[0]
        self.assertEqual(entry["record_id"], self.proposed)
        # The digest and title survive, so the deletion names what was removed.
        self.assertEqual(entry["content_digest"], before["content_digest"])
        self.assertEqual(entry["title"], before["title"])
        self.assertEqual(entry["status_at_deletion"], "proposed")
        self.assertEqual(entry["reason"], "duplicate of an existing record")
        self.assertEqual(entry["deleted_by"], "a-steward")

    def test_deleting_an_accepted_record_requires_an_authorized_human(self) -> None:
        with self.assertRaises(Exception) as caught:
            self._run(
                "delete-staged", "--id", self.accepted,
                "--reason", "changed my mind", "--deleted-by", "a-steward",
            )
        self.assertIn("authorized human", str(caught.exception))
        # Refused means untouched, and unrecorded: a refused deletion is not
        # a deletion and must not appear in the evidence log.
        self.assertTrue(self._run("show-staged", "--id", self.accepted))
        self.assertEqual(self._run("deletion-evidence")["deletions"], [])

    def test_an_accepted_record_can_be_deleted_with_authorization(self) -> None:
        self._run(
            "delete-staged", "--id", self.accepted, "--reason", "superseded and withdrawn",
            "--deleted-by", "a-steward", "--authorized-by", "the repository owner",
        )
        entry = self._run("deletion-evidence")["deletions"][0]
        self.assertEqual(entry["authorized_by"], "the repository owner")
        self.assertEqual(entry["status_at_deletion"], "accepted")

    def test_an_empty_reason_is_refused(self) -> None:
        with self.assertRaises(Exception) as caught:
            self._run(
                "delete-staged", "--id", self.proposed, "--reason", "  ", "--deleted-by", "s",
            )
        self.assertIn("indistinguishable from data loss", str(caught.exception))

    def test_evidence_survives_the_record_and_accumulates(self) -> None:
        self._run(
            "delete-staged", "--id", self.proposed, "--reason", "first", "--deleted-by", "s",
        )
        second = self._first_with_status("proposed")
        self._run("delete-staged", "--id", second, "--reason", "second", "--deleted-by", "s")
        evidence = self._run("deletion-evidence")["deletions"]
        self.assertEqual(len(evidence), 2)
        self.assertEqual({e["record_id"] for e in evidence}, {self.proposed, second})
        # Neither record exists any more; both deletions are still on record.
        for record_id in (self.proposed, second):
            with self.assertRaises(ValueError):
                self._run("show-staged", "--id", record_id)

    def test_deleting_an_unknown_record_names_it(self) -> None:
        with self.assertRaises(Exception) as caught:
            self._run(
                "delete-staged", "--id", "KS-20260101-nope", "--reason", "x", "--deleted-by", "s",
            )
        self.assertIn("KS-20260101-nope", str(caught.exception))

    def test_a_still_proposed_record_may_be_deleted_by_its_own_proposer(self) -> None:
        # No decision exists yet to protect, so this is just withdrawing a
        # draft -- the authorship/approval separation invariant does not
        # apply to a record nobody has approved or rejected.
        staged_by = self._run("show-staged", "--id", self.proposed)["frontmatter"]["staged_by"]
        result = self._run(
            "delete-staged", "--id", self.proposed, "--reason", "withdrawing my own draft",
            "--deleted-by", staged_by,
        )
        self.assertEqual(result["status"], "deleted")

    def test_a_dispositioned_record_cannot_be_deleted_by_its_own_proposer(self) -> None:
        # Decision 2026-08-09: once a record carries a disposition, the
        # proposer/decider separation extends to deleting it, mirroring
        # disposition_record's own proposer-cannot-decide check.
        staged_by = self._run("show-staged", "--id", self.accepted)["frontmatter"]["staged_by"]
        with self.assertRaises(Exception) as caught:
            self._run(
                "delete-staged", "--id", self.accepted, "--reason", "trying to erase the outcome",
                "--deleted-by", staged_by, "--authorized-by", "an authorized human",
            )
        self.assertIn("already carries a disposition", str(caught.exception))
        # Refused means untouched and unrecorded, matching the accepted/
        # authorized-by refusal case above.
        self.assertTrue(self._run("show-staged", "--id", self.accepted))
        self.assertEqual(self._run("deletion-evidence")["deletions"], [])

    def test_a_dispositioned_record_can_be_deleted_by_someone_other_than_its_proposer(self) -> None:
        staged_by = self._run("show-staged", "--id", self.accepted)["frontmatter"]["staged_by"]
        deleter = staged_by + "-someone-else"
        result = self._run(
            "delete-staged", "--id", self.accepted, "--reason", "superseded",
            "--deleted-by", deleter, "--authorized-by", "an authorized human",
        )
        self.assertEqual(result["status"], "deleted")


if __name__ == "__main__":
    unittest.main()
