"""Offline, deterministic embedding and similarity, shared by both stores.

This module holds the *offline half* of what was
`roster/knowledge-store/src/embeddings.py`: feature-hashing vectors and cosine
similarity, pure functions with no network, no credentials, and no
configuration beyond a dimension count.

**The `openai-compatible` provider deliberately did not move.** It stays in the
knowledge store, along with the URL validation, credential lookup, and response
handling it needs. That split is the mechanism, not an accident of tidying:

The context store holds unreviewed agent working material, and whether that
material may be transmitted to a third-party embedding endpoint is an open
security decision (OD-5 in
`roster/orchestration/runs/cadre-feature-agent-context-store-2026-08-11/`). The
decision is "refused" for now. Implementing that refusal as a config check
would leave the remote code one edit away from reachable. Implementing it as a
module boundary means the context store cannot perform a remote embedding at
all -- there is no import path from it to any code that opens a socket, and
`test_context_boundary.py` asserts the import graph that keeps it that way.

The knowledge store's own precedent argued for at least this much: its
`config.py` already refuses remote embeddings whenever configuration resolved
project-locally, on the grounds that a project-local file is untrusted,
clonable content. Context entries are weaker provenance than a project-local
config file.

If OD-5 is later resolved in favour of remote embeddings for this store, the
change is deliberate and visible: it means moving that code here, or giving the
context store its own, and amending the boundary test. It is not a config edit.
"""

from __future__ import annotations

import hashlib
import math
import unicodedata


def normalize_vector(vector: list[float]) -> list[float]:
    if not all(
        isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value)
        for value in vector
    ):
        raise ValueError("Embedding vector must contain only finite numbers")
    magnitude = math.sqrt(sum(float(value) * float(value) for value in vector))
    return [float(value) / magnitude for value in vector] if magnitude else [float(value) for value in vector]


def tokens(text: str) -> list[str]:
    words: list[str] = []
    current: list[str] = []
    for character in text.lower():
        category = unicodedata.category(character)
        if character in {"_", "-"} or category.startswith(("L", "N")):
            current.append(character)
        elif current:
            words.append("".join(current))
            current = []
    if current:
        words.append("".join(current))
    return words


def hashing_embedding(text: str, dimensions: int) -> list[float]:
    """Deterministic, offline feature-hashing vector.

    Approximates lexical similarity rather than full semantic similarity. Good
    enough to find the entry you half-remember; not a substitute for a real
    embedding model, and honest about it in both stores' documentation.
    """
    words = tokens(text)
    features = list(words) + [f"{words[index]}::{words[index + 1]}" for index in range(len(words) - 1)]
    vector = [0.0] * dimensions
    for feature in features:
        digest = hashlib.sha256(feature.encode("utf-8")).digest()
        position = int.from_bytes(digest[0:4], "little") % dimensions
        sign = 1.0 if int.from_bytes(digest[4:8], "little") % 2 == 0 else -1.0
        vector[position] += sign
    return normalize_vector(vector)


def cosine_similarity(left: list[float], right: list[float]) -> float:
    if len(left) != len(right):
        return float("-inf")
    if not all(math.isfinite(value) for value in right):
        return float("-inf")
    return sum(left[index] * right[index] for index in range(len(left)))
