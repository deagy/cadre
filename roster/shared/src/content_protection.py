"""Secret redaction and prompt-injection indicators, shared by both stores.

Lifted out of `roster/knowledge-store/src/content.py` when the context store
(`roster/context-store/`) needed the same protection applied to every `put`.

The two stores are deliberately separated -- `roster/orchestration/test/
test_context_boundary.py` forbids either from importing the other's modules,
because that separation is what makes "no path exists from working context
into the curated corpus without a steward disposition" a property of the tree
rather than a promise in a document. A utility both stores need therefore has
to live somewhere neither one owns. `roster/shared/src/` is that place, and
already holds `settings.py`, which the knowledge store's own `config.py`
imports by the same `sys.path` append.

The alternative -- copying the pattern lists into the second store -- was
rejected. Two divergent copies of a secret-redaction list is a worse failure
than one extra shared module: the copy that falls behind stops redacting
something, silently, while continuing to look correct.

`chunk_text` deliberately stayed in the knowledge store. It is embedding-shaped
(its output is what gets vectorized), not protection-shaped, and the context
store has no chunking in phase 1.
"""

from __future__ import annotations

import re
from typing import Any


SECRET_PATTERNS = [
    ("private-key", re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----[\s\S]*?-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----")),
    ("bearer-token", re.compile(r"\bBearer\s+[A-Za-z0-9._~+/\-]+=*", re.IGNORECASE)),
    ("aws-access-key", re.compile(r"\b(?:AKIA|ASIA)[A-Z0-9]{16}\b")),
    ("github-token", re.compile(r"\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{30,}\b")),
    ("generic-secret", re.compile(r"\b(api[_-]?key|secret|password|token)\s*[:=]\s*[\"']?[^\s,\"']{8,}[\"']?", re.IGNORECASE)),
]
INJECTION_PATTERNS = [
    re.compile(r"ignore (?:all |any )?(?:previous|prior|above) instructions", re.IGNORECASE),
    re.compile(r"reveal (?:the )?(?:system|developer) prompt", re.IGNORECASE),
    re.compile(r"act as (?:the )?system", re.IGNORECASE),
    re.compile(r"bypass (?:security|policy|approval|guardrail)", re.IGNORECASE),
    re.compile(r"do not tell (?:the )?user", re.IGNORECASE),
]


def protect_content(content: str, enabled: bool = True) -> dict[str, Any]:
    protected = content
    redactions: list[str] = []
    if enabled:
        for label, pattern in SECRET_PATTERNS:
            def replacement(_: re.Match[str], current_label: str = label) -> str:
                redactions.append(current_label)
                return f"[REDACTED:{current_label}]"
            protected = pattern.sub(replacement, protected)
    return {
        "content": protected,
        "redactions": redactions,
        "injection_risk": any(pattern.search(protected) for pattern in INJECTION_PATTERNS),
    }
