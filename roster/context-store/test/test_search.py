"""Semantic retrieval: chunking, offline embeddings, ranking, and reindex.

The property that matters most here is not ranking quality -- the hashing
provider only approximates lexical similarity and both stores say so. It is
that ranking never becomes the thing standing between a caller and an entry
they may not read. A high-scoring hit a caller was not entitled to is still a
disclosure, however relevant it was.
"""

from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "src"))

_SHARED_TEST_DIR = ROOT.parent / "shared" / "test"
if str(_SHARED_TEST_DIR) not in sys.path:
    sys.path.append(str(_SHARED_TEST_DIR))

from config import DEFAULTS, SUPPORTED_EMBEDDING_PROVIDERS, load_config  # noqa: E402
from database import load_searchable_chunks, open_store, store_stats  # noqa: E402
from service import (  # noqa: E402
    ContextStoreError,
    embed_texts,
    put_entry,
    reindex_entries,
    search_entries,
)
from settings_test_helpers import isolate_settings  # noqa: E402


CALLER = {"agent": "a", "task_id": "T", "classification": "internal", "source": "demo"}

DB_TEXT = "The postgres connection pool exhausts under load when max_conns is set too low."
UI_TEXT = "The React component re-renders on every keystroke because the callback is not memoized."
K8S_TEXT = "Kubernetes ingress returns 502 when the readiness probe path is wrong."


class SearchTestCase(unittest.TestCase):
    def setUp(self) -> None:
        isolate_settings(self)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.config = json.loads(json.dumps(DEFAULTS))
        self.config["database"] = str(self.root / "context.db")
        self.db = open_store(self.config["database"])
        self.addCleanup(self.db.close)

    def put(self, content: str, **overrides: object) -> dict:
        options = {**CALLER, "label": "entry", "scope": "agent", "content": content}
        options.update(overrides)
        return put_entry(self.db, self.config, options)

    def search(self, query: str, **overrides: object) -> list[dict]:
        options = {**CALLER}
        options.update(overrides)
        return search_entries(self.db, self.config, query, options)["results"]

    def labels(self, query: str, **overrides: object) -> list[str]:
        return [result["label"] for result in self.search(query, **overrides)]


class IndexingTests(SearchTestCase):
    def test_put_writes_chunks(self) -> None:
        stored = self.put(DB_TEXT)
        self.assertEqual(stored["chunks"], 1)
        rows = self.db.execute(
            "SELECT * FROM entry_chunks WHERE handle = ?", (stored["handle"],)
        ).fetchall()
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["embedding_provider"], "hashing")
        self.assertEqual(rows[0]["embedding_dimensions"], self.config["embedding"]["dimensions"])

    def test_long_content_is_split_into_several_chunks(self) -> None:
        paragraph = "Some sentence about pooling. " * 40
        stored = self.put("\n\n".join([paragraph] * 6))
        self.assertGreater(stored["chunks"], 1)

    def test_chunks_are_removed_with_their_entry(self) -> None:
        from service import drop_entry

        stored = self.put(DB_TEXT)
        drop_entry(self.db, {**CALLER, "handle": stored["handle"], "reason": "cleanup"})
        remaining = self.db.execute(
            "SELECT COUNT(*) AS n FROM entry_chunks WHERE handle = ?", (stored["handle"],)
        ).fetchone()["n"]
        self.assertEqual(remaining, 0)

    def test_chunks_index_the_redacted_text_not_the_original(self) -> None:
        # Otherwise a secret stripped from `entries.content` would survive in
        # the search index and come straight back out as a result.
        self.put("connection string password: hunter2hunter2 trailing")
        rows = self.db.execute("SELECT content FROM entry_chunks").fetchall()
        self.assertTrue(rows)
        for row in rows:
            self.assertNotIn("hunter2hunter2", row["content"])

    def test_stats_reports_index_coverage(self) -> None:
        self.put(DB_TEXT)
        self.put(UI_TEXT)
        stats = store_stats(self.db)
        self.assertEqual(stats["indexed_entries"], 2)
        self.assertGreaterEqual(stats["chunks"], 2)


class RankingTests(SearchTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.put(DB_TEXT, label="db")
        self.put(UI_TEXT, label="ui")
        self.put(K8S_TEXT, label="k8s")

    def test_the_most_relevant_entry_ranks_first(self) -> None:
        self.assertEqual(self.labels("database connection pool exhaustion")[0], "db")
        self.assertEqual(self.labels("component re-render memoized callback")[0], "ui")
        self.assertEqual(self.labels("ingress readiness probe 502")[0], "k8s")

    def test_results_are_ordered_by_descending_score(self) -> None:
        scores = [result["score"] for result in self.search("connection pool")]
        self.assertEqual(scores, sorted(scores, reverse=True))

    def test_top_bounds_the_result_count(self) -> None:
        self.assertEqual(len(self.search("pool", top=1)), 1)
        for bad in (0, 21, -1):
            with self.assertRaises(ContextStoreError):
                self.search("pool", top=bad)

    def test_an_empty_query_is_refused(self) -> None:
        for empty in ("", "   "):
            with self.assertRaises(ContextStoreError):
                self.search(empty)

    def test_results_carry_chunk_provenance(self) -> None:
        result = self.search("connection pool")[0]
        for field in ("chunk_id", "chunk_ordinal", "chunk_hash", "handle", "content_hash", "score"):
            self.assertIn(field, result)

    def test_the_bundle_is_labelled_untrusted_working_context(self) -> None:
        bundle = search_entries(self.db, self.config, "pool", {**CALLER})
        self.assertEqual(bundle["trust"], "untrusted_working_context")
        self.assertEqual(bundle["operation"], "search")
        self.assertIn("query_id", bundle)


class SearchAccessControlTests(SearchTestCase):
    """Ranking runs after access filtering, never instead of it."""

    def test_another_agents_entry_is_not_returned_however_relevant(self) -> None:
        self.put(DB_TEXT, label="theirs", agent="someone-else")
        self.assertEqual(self.labels("database connection pool exhaustion"), [])

    def test_a_different_dispatch_is_not_returned(self) -> None:
        self.put(DB_TEXT, label="theirs", scope="dispatch", dispatch_id="D-1", agent="someone-else")
        self.assertEqual(self.labels("connection pool", dispatch_id="D-2"), [])
        self.assertEqual(self.labels("connection pool", dispatch_id="D-1"), ["theirs"])

    def test_project_scope_is_visible_across_agents(self) -> None:
        self.put(DB_TEXT, label="shared", scope="project", agent="someone-else")
        self.assertEqual(self.labels("connection pool"), ["shared"])

    def test_a_different_classification_is_not_returned(self) -> None:
        self.put(DB_TEXT, label="secret", scope="project", classification="confidential")
        self.assertEqual(self.labels("connection pool", classification="internal"), [])
        self.assertEqual(self.labels("connection pool", classification="confidential"), ["secret"])

    def test_a_different_source_is_not_returned(self) -> None:
        self.put(DB_TEXT, label="other-project", scope="project", source="elsewhere")
        self.assertEqual(self.labels("connection pool", source="demo"), [])

    def test_the_scope_filter_narrows_rather_than_widens(self) -> None:
        self.put(DB_TEXT, label="mine")
        self.put(UI_TEXT, label="ours", scope="project")
        self.assertEqual(self.labels("keystroke callback", scope="project"), ["ours"])
        self.assertEqual(self.labels("connection pool", scope="agent"), ["mine"])

    def test_an_expired_entry_is_not_searchable(self) -> None:
        from database import sweep_expired

        stored = self.put(DB_TEXT, label="stale")
        with self.db:
            self.db.execute(
                "UPDATE entries SET expires_at = '2000-01-01T00:00:00.000Z' WHERE handle = ?",
                (stored["handle"],),
            )
        sweep_expired(self.db)
        self.assertEqual(self.labels("connection pool"), [])


class DimensionMismatchTests(SearchTestCase):
    def test_vectors_of_the_wrong_dimension_are_excluded_not_scored(self) -> None:
        # Parity with the knowledge store: a mismatched vector yields a
        # meaningless similarity rather than a low one, so it must not compete.
        self.put(DB_TEXT)
        mismatched = dict(self.config["embedding"])
        mismatched["dimensions"] = 64
        rows = load_searchable_chunks(self.db, mismatched, {
            "classification": "internal", "source": "demo", "scope": None,
        })
        self.assertEqual(rows, [])

    def test_a_corrupt_vector_is_skipped_rather_than_crashing_the_query(self) -> None:
        stored = self.put(DB_TEXT)
        self.put(UI_TEXT, label="intact")
        with self.db:
            self.db.execute(
                "UPDATE entry_chunks SET embedding_json = 'not json' WHERE handle = ?",
                (stored["handle"],),
            )
        self.assertEqual(self.labels("keystroke callback memoized"), ["intact"])


class ProviderTests(unittest.TestCase):
    def setUp(self) -> None:
        isolate_settings(self)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)

    def _write(self, payload: dict) -> Path:
        path = self.root / "config.json"
        path.write_text(json.dumps(payload), encoding="utf-8")
        return path

    def test_only_the_offline_provider_is_supported(self) -> None:
        self.assertEqual(SUPPORTED_EMBEDDING_PROVIDERS, ("hashing",))

    def test_a_remote_provider_is_refused_and_says_why(self) -> None:
        path = self._write({"embedding": {"provider": "openai-compatible", "base_url": "https://x.invalid"}})
        with self.assertRaises(ValueError) as ctx:
            load_config(str(path))
        message = str(ctx.exception)
        self.assertIn("openai-compatible", message)
        self.assertIn("OD-5", message)
        self.assertIn("not importable", message)

    def test_embed_texts_is_deterministic_and_offline(self) -> None:
        embedding = {"provider": "hashing", "model": "feature-hash-v1", "dimensions": 128}
        first = embed_texts(["repeatable text"], embedding)
        second = embed_texts(["repeatable text"], embedding)
        self.assertEqual(first, second)
        self.assertEqual(len(first[0]), 128)


class ReindexTests(SearchTestCase):
    def test_entries_stored_before_indexing_existed_are_picked_up(self) -> None:
        # A phase-1 store upgraded in place has entries with no chunks. They
        # would otherwise stay invisible to search until they expired.
        stored = self.put(DB_TEXT)
        with self.db:
            self.db.execute("DELETE FROM entry_chunks WHERE handle = ?", (stored["handle"],))
        self.assertEqual(self.labels("connection pool"), [])
        report = reindex_entries(self.db, self.config)
        self.assertEqual(report["reindexed_entries"], 1)
        self.assertEqual(self.labels("connection pool"), ["entry"])

    def test_reindex_skips_already_indexed_entries(self) -> None:
        self.put(DB_TEXT)
        self.assertEqual(reindex_entries(self.db, self.config)["reindexed_entries"], 0)

    def test_force_rebuilds_everything(self) -> None:
        self.put(DB_TEXT)
        self.put(UI_TEXT)
        self.assertEqual(reindex_entries(self.db, self.config, force=True)["reindexed_entries"], 2)

    def test_reindexing_replaces_rather_than_accumulates(self) -> None:
        # Mixing vectors written under different chunking settings would score
        # them against each other as if comparable.
        stored = self.put(DB_TEXT)
        before = self.db.execute(
            "SELECT COUNT(*) AS n FROM entry_chunks WHERE handle = ?", (stored["handle"],)
        ).fetchone()["n"]
        reindex_entries(self.db, self.config, force=True)
        after = self.db.execute(
            "SELECT COUNT(*) AS n FROM entry_chunks WHERE handle = ?", (stored["handle"],)
        ).fetchone()["n"]
        self.assertEqual(before, after)

    def test_reindex_after_a_chunking_change_produces_the_new_shape(self) -> None:
        long_text = "\n\n".join(["A sentence about connection pooling. " * 20] * 4)
        stored = self.put(long_text)
        original = self.db.execute(
            "SELECT COUNT(*) AS n FROM entry_chunks WHERE handle = ?", (stored["handle"],)
        ).fetchone()["n"]
        self.config["chunking"] = {"max_characters": 200, "overlap_characters": 20}
        reindex_entries(self.db, self.config, force=True)
        rechunked = self.db.execute(
            "SELECT COUNT(*) AS n FROM entry_chunks WHERE handle = ?", (stored["handle"],)
        ).fetchone()["n"]
        self.assertGreater(rechunked, original)


class SearchAuditTests(SearchTestCase):
    def test_a_search_is_recorded_by_query_hash_never_query_text(self) -> None:
        self.put(DB_TEXT)
        self.search("a very distinctive search phrase")
        row = self.db.execute("SELECT * FROM access_runs WHERE operation = 'search'").fetchone()
        self.assertIsNotNone(row)
        self.assertTrue(row["query_hash"])
        self.assertNotIn("a very distinctive search phrase", str(dict(row)))
        self.assertEqual(row["agent"], CALLER["agent"])

    def test_a_search_returning_nothing_is_still_recorded(self) -> None:
        self.search("nothing matches this")
        row = self.db.execute("SELECT result_count FROM access_runs WHERE operation = 'search'").fetchone()
        self.assertEqual(row["result_count"], 0)


if __name__ == "__main__":
    unittest.main()
