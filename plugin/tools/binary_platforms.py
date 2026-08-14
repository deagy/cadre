#!/usr/bin/env python3
"""The release-asset naming contract for `cmd/cadre` binary distribution.

Single source of truth for the supported GOOS/GOARCH matrix and the archive/
executable naming pattern, consumed by:

* `plugin/tools/test_binary_shim_contract.py` -- guards `Makefile` and
  `DISTRIBUTION.md` against this contract.
* the release workflow's own drift guard (`.github/workflows/**`, owned by a
  separate workstream) -- imports `SUPPORTED_PLATFORMS` from here rather than
  from a `test_*.py` module, so a CI gate does not depend on a file only a
  test runner is conventionally expected to touch.

Deliberately import-time side-effect free: no filesystem or network access at
module scope, so importing this module can never fail for a reason unrelated
to the platform contract itself.

`windows/arm64` is deliberately excluded, not merely absent: GitHub's hosted
`windows-latest` runner is x64, its `gcc` is x86_64 MinGW and cannot emit
ARM64 Windows objects, and `CGO_ENABLED=1` (required by the knowledge store,
see `Makefile`'s `cross-build` comment) turns that mismatch into a hard build
failure rather than a silently-stubbed binary. See `DISTRIBUTION.md`'s
"Platform support" section for the full record of this decision.
"""

from __future__ import annotations

# Hand-reviewed, not derived from any other file -- Makefile and
# DISTRIBUTION.md are each checked against this, not the other way around,
# so a platform-set change must touch this module deliberately.
SUPPORTED_PLATFORMS: tuple[tuple[str, str], ...] = (
    ("linux", "amd64"),
    ("linux", "arm64"),
    ("darwin", "amd64"),
    ("darwin", "arm64"),
    ("windows", "amd64"),
)


def archive_extension(goos: str) -> str:
    return "zip" if goos == "windows" else "tar.gz"


def archive_name(goos: str, goarch: str, *, version: str = "0.0.0") -> str:
    return f"cadre-v{version}-{goos}-{goarch}.{archive_extension(goos)}"


def executable_name(goos: str) -> str:
    return "cadre.exe" if goos == "windows" else "cadre"
