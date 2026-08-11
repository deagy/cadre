"""Context-entry handles: mint, validate, and nothing else.

A handle is `ctx_` followed by 32 lowercase hex characters (128 bits from
`secrets.token_hex`).

**Random, deliberately not content-derived.** Content addressing would give
free deduplication, and that is not worth what it costs here: identical content
stored by two different agents in two different scopes would collide onto one
handle, turning the store into an equality oracle across exactly the boundary
the scope model exists to hold. An agent could test whether a peer had already
stored a given string by storing it themselves and seeing whether the handle
came back already-present.

A handle is also never a path, satisfying the same no-local-path rule that
governs knowledge citations' `source_uri` and staged-record `evidence` fields
(`roster/shared/knowledge-use-policy.md`).

Handles are opaque. Nothing about scope, agent, task, or creation time is
recoverable from one, so a leaked handle is not a map of the store.
"""

from __future__ import annotations

import re
import secrets

HANDLE_PREFIX = "ctx_"
HANDLE_HEX_LENGTH = 32
_HANDLE_PATTERN = re.compile(rf"^{HANDLE_PREFIX}[0-9a-f]{{{HANDLE_HEX_LENGTH}}}$")


def mint_handle() -> str:
    return f"{HANDLE_PREFIX}{secrets.token_hex(HANDLE_HEX_LENGTH // 2)}"


def is_handle(value: object) -> bool:
    return isinstance(value, str) and bool(_HANDLE_PATTERN.match(value))


def validate_handle(value: object) -> str:
    """Return `value` if it is a well-formed handle, else raise.

    Validated before it reaches a query so a malformed handle fails with a
    message naming the expected shape, rather than silently matching nothing
    and being indistinguishable from an expired entry.
    """
    if not is_handle(value):
        raise ValueError(
            f"Malformed handle: expected '{HANDLE_PREFIX}' followed by "
            f"{HANDLE_HEX_LENGTH} lowercase hex characters, got {value!r}"
        )
    return value  # type: ignore[return-value]
