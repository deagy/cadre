"""Content redaction, injection indicators, and chunking.

Both halves of this module now live in `roster/shared/src/` and are re-exported
here: `protect_content` in `content_protection.py`, `chunk_text` in
`text_chunking.py`. They moved as the context store (`roster/context-store/`)
came to need each of them -- protection in its phase 1, chunking in its phase 2
-- and the two stores may not import each other (see
`roster/orchestration/test/test_context_boundary.py`), so a routine both need
belongs to neither.

Re-exporting keeps every existing `from content import ...` caller, including
`service.py` and this store's test suite, working unchanged.
"""

from __future__ import annotations

import sys
from pathlib import Path

# Appended (never inserted at sys.path[0]), matching config.py's discipline in
# this same package: a caller's own same-named module always wins first.
_SHARED_SRC_DIR = Path(__file__).resolve().parents[2] / "shared" / "src"
if str(_SHARED_SRC_DIR) not in sys.path:
    sys.path.append(str(_SHARED_SRC_DIR))

from content_protection import (  # noqa: E402,F401  (sys.path set above; re-exported)
    INJECTION_PATTERNS,
    SECRET_PATTERNS,
    protect_content,
)
from text_chunking import chunk_text  # noqa: E402,F401  (re-exported)

__all__ = ["INJECTION_PATTERNS", "SECRET_PATTERNS", "protect_content", "chunk_text"]
