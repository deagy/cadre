"""Deterministic local and OpenAI-compatible embedding providers.

The offline half -- feature hashing, vector normalization, tokenization, and
cosine similarity -- now lives in `roster/shared/src/text_embedding.py` and is
re-exported here. It moved when the context store gained semantic retrieval and
needed the same offline provider.

**The `openai-compatible` provider deliberately stayed.** It is the only code
in either store that opens a socket or reads a credential, and keeping it here
means the context store -- which may not import this module -- structurally
cannot perform a remote embedding of unreviewed agent working material. See
`text_embedding.py`'s module docstring for why that boundary is the mechanism
rather than a side effect.
"""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

# Appended (never inserted at sys.path[0]), matching config.py's discipline in
# this same package: a caller's own same-named module always wins first.
_SHARED_SRC_DIR = Path(__file__).resolve().parents[2] / "shared" / "src"
if str(_SHARED_SRC_DIR) not in sys.path:
    sys.path.append(str(_SHARED_SRC_DIR))

from text_embedding import (  # noqa: E402  (sys.path set above; re-exported)
    cosine_similarity,  # noqa: F401
    hashing_embedding,
    normalize_vector as _normalize,
    tokens as _tokens,  # noqa: F401
)

MAX_RESPONSE_BYTES = 4 * 1024 * 1024


class _RejectRedirects(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request: Any, file_pointer: Any, code: int, message: str, headers: Any, new_url: str) -> None:
        return None


def _open_request(request: urllib.request.Request, timeout: float) -> Any:
    return urllib.request.build_opener(_RejectRedirects()).open(request, timeout=timeout)


def _remote_embeddings(texts: list[str], config: dict[str, Any]) -> list[list[float]]:
    base_url = config.get("base_url")
    if not isinstance(base_url, str) or not base_url or "example.invalid" in base_url:
        raise ValueError("Set embedding.base_url for the openai-compatible provider")
    parsed_url = urllib.parse.urlparse(base_url)
    if parsed_url.scheme != "https" or not parsed_url.netloc or parsed_url.username or parsed_url.password:
        raise ValueError("embedding.base_url must be an HTTPS URL without embedded credentials")
    key_name = config.get("api_key_env")
    if not isinstance(key_name, str) or not key_name:
        raise ValueError("embedding.api_key_env must be a non-empty string")
    key = os.environ.get(key_name)
    if not key:
        raise ValueError(f"Missing embedding credential in {key_name}")
    request = urllib.request.Request(
        f"{base_url.rstrip('/')}/embeddings",
        data=json.dumps({"model": config["model"], "input": texts}, ensure_ascii=False).encode("utf-8"),
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {key}"},
        method="POST",
    )
    try:
        with _open_request(request, timeout=float(config.get("timeout_seconds", 30))) as response:
            if response.status < 200 or response.status >= 300:
                raise RuntimeError(f"Embedding endpoint returned HTTP {response.status}")
            try:
                body = response.read(MAX_RESPONSE_BYTES + 1)
            except TypeError:
                # Minimal test doubles and some compatible response objects do not
                # accept a size argument; retain the post-read hard limit there.
                body = response.read()
            if len(body) > MAX_RESPONSE_BYTES:
                raise RuntimeError("Embedding endpoint response exceeded the size limit")
            payload = json.loads(body.decode("utf-8"))
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"Embedding endpoint returned HTTP {error.code}") from error
    except urllib.error.URLError as error:
        raise RuntimeError(f"Embedding endpoint request failed: {error.reason}") from error
    except (TimeoutError, OSError) as error:
        raise RuntimeError("Embedding endpoint request timed out or failed") from error
    except json.JSONDecodeError as error:
        raise RuntimeError("Embedding endpoint returned invalid JSON") from error

    data = payload.get("data") if isinstance(payload, dict) else None
    if not isinstance(data, list):
        raise RuntimeError("Embedding endpoint returned an invalid response")
    try:
        if any(
            not isinstance(item, dict)
            or isinstance(item.get("index"), bool)
            or not isinstance(item.get("index"), int)
            for item in data
        ):
            raise TypeError
        ordered = sorted(data, key=lambda item: item["index"])
        if [item["index"] for item in ordered] != list(range(len(texts))):
            raise ValueError
        vectors = [item["embedding"] for item in ordered]
    except (KeyError, TypeError, ValueError) as error:
        raise RuntimeError("Embedding endpoint returned an invalid response") from error
    if len(vectors) != len(texts) or any(not isinstance(vector, list) for vector in vectors):
        raise RuntimeError("Embedding endpoint returned an invalid response")
    dimensions = config["dimensions"]
    if any(len(vector) != dimensions for vector in vectors):
        raise RuntimeError(f"Embedding endpoint vectors must have exactly {dimensions} dimensions")
    try:
        return [_normalize(vector) for vector in vectors]
    except (TypeError, ValueError) as error:
        raise RuntimeError("Embedding endpoint returned invalid vector values") from error


def embed_texts(texts: list[str], config: dict[str, Any]) -> list[list[float]]:
    if config["provider"] == "hashing":
        return [hashing_embedding(text, config["dimensions"]) for text in texts]
    vectors: list[list[float]] = []
    batch_size = config["batch_size"]
    for start in range(0, len(texts), batch_size):
        vectors.extend(_remote_embeddings(texts[start:start + batch_size], config))
    return vectors
