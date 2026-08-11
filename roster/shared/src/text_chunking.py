"""Text chunking, shared by both stores.

Moved out of `roster/knowledge-store/src/content.py` when the context store
gained semantic retrieval and needed to split entries the same way. The two
stores may not import each other (`roster/orchestration/test/
test_context_boundary.py`), so a routine both need lives in a directory neither
owns.

Chunk boundaries decide what a retrieval hit actually returns, so two divergent
copies would make the same query behave differently in the two stores for
reasons no one would think to look for. That is a milder failure than a
divergent secret-redaction list, but it is the same shape, and the fix is the
same.
"""

from __future__ import annotations


def chunk_text(text: str, config: dict[str, int]) -> list[str]:
    """Split on paragraph or sentence boundaries where one falls late enough.

    Falls back to a hard cut when no boundary sits past 55% of the window, so a
    single unbroken block still chunks rather than returning one oversized
    piece.
    """
    maximum = config["max_characters"]
    overlap = config["overlap_characters"]
    if len(text) <= maximum:
        return [text]
    chunks: list[str] = []
    start = 0
    while start < len(text):
        end = min(start + maximum, len(text))
        if end < len(text):
            boundary = max(text.rfind("\n\n", start, end + 1), text.rfind(". ", start, end + 1))
            if boundary > start + int(maximum * 0.55):
                end = boundary + 1
        chunk = text[start:end].strip()
        if chunk:
            chunks.append(chunk)
        if end >= len(text):
            break
        start = max(start + 1, end - overlap)
    return chunks
