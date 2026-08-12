#!/usr/bin/env python3
"""Safely install the suite's namespaced Codex role wrappers."""

from __future__ import annotations

import argparse
import json
import os
import re
import stat
import sys
from pathlib import Path

PROVENANCE_MARKER = "# GENERATED FILE: canonical source is roster/"
REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
DEFAULT_SOURCE = REPOSITORY_ROOT / "provider" / "codex-agents"
INDEX_FILENAME = "agents-index.json"
INDEX_SCHEMA_VERSION = 1
_WRAPPER_NAME_PREFIX = "agents-"
_WRAPPER_NAME_SUFFIX = ".toml"
_MODEL_LINE_PATTERN = re.compile(r'(?m)^model\s*=\s*(".*")\s*$')


def _nofollow_flag() -> int:
    return getattr(os, "O_NOFOLLOW", 0)


def _read_regular_file(path: Path) -> bytes:
    descriptor = os.open(path, os.O_RDONLY | _nofollow_flag())
    try:
        file_stat = os.fstat(descriptor)
        if not stat.S_ISREG(file_stat.st_mode):
            raise RuntimeError(f"Refusing non-regular wrapper: {path}")
        chunks = []
        while True:
            chunk = os.read(descriptor, 1024 * 1024)
            if not chunk:
                break
            chunks.append(chunk)
        return b"".join(chunks)
    finally:
        os.close(descriptor)


def _write_all(descriptor: int, content: bytes) -> None:
    offset = 0
    while offset < len(content):
        offset += os.write(descriptor, content[offset:])


def _write_owned_wrapper(destination: Path, content: bytes) -> str:
    if destination.is_symlink():
        raise RuntimeError(f"Refusing symlinked destination wrapper: {destination}")
    create_flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | _nofollow_flag()
    try:
        descriptor = os.open(destination, create_flags, 0o644)
    except FileExistsError:
        descriptor = os.open(destination, os.O_RDWR | _nofollow_flag())
    else:
        try:
            file_stat = os.fstat(descriptor)
            if not stat.S_ISREG(file_stat.st_mode):
                raise RuntimeError(f"Refusing non-regular destination wrapper: {destination}")
            _write_all(descriptor, content)
            return "installed"
        finally:
            os.close(descriptor)
    try:
        file_stat = os.fstat(descriptor)
        if not stat.S_ISREG(file_stat.st_mode):
            raise RuntimeError(f"Refusing non-regular destination wrapper: {destination}")
        existing = []
        while True:
            chunk = os.read(descriptor, 1024 * 1024)
            if not chunk:
                break
            existing.append(chunk)
        existing_content = b"".join(existing)
        if PROVENANCE_MARKER.encode("utf-8") not in existing_content:
            raise RuntimeError(f"Refusing to overwrite unowned namespaced Codex wrapper: {destination}")
        if existing_content == content:
            return "unchanged"
        os.ftruncate(descriptor, 0)
        os.lseek(descriptor, 0, os.SEEK_SET)
        _write_all(descriptor, content)
        return "installed"
    finally:
        os.close(descriptor)


def _role_id_from_wrapper_name(name: str) -> str:
    return name[len(_WRAPPER_NAME_PREFIX) : -len(_WRAPPER_NAME_SUFFIX)]


def _extract_model(content: bytes) -> str | None:
    """Best-effort extraction of the `model = "..."` line from a generated wrapper.

    The wrapper format is a small, fixed, generator-produced subset of TOML
    (see generate_global_plugin.py's `toml_string`, which encodes values with
    `json.dumps`), so a targeted line scan is sufficient and avoids requiring
    `tomllib` (Python 3.11+) on this repository's Python 3.10+ floor.
    """
    match = _MODEL_LINE_PATTERN.search(content.decode("utf-8"))
    if not match:
        return None
    return json.loads(match.group(1))


def _build_index(contents: list[tuple[Path, bytes]], target: Path) -> dict:
    roles: dict[str, dict[str, str | None]] = {}
    for wrapper, content in contents:
        role_id = _role_id_from_wrapper_name(wrapper.name)
        destination = (target / wrapper.name).resolve()
        roles[role_id] = {"path": str(destination), "model": _extract_model(content)}
    return {
        "generated_marker": PROVENANCE_MARKER,
        "schema_version": INDEX_SCHEMA_VERSION,
        "roles": roles,
    }


def sync_wrappers(source: Path, target: Path) -> dict[str, object]:
    wrappers = sorted(source.glob("agents-*.toml"))
    if not wrappers:
        raise ValueError(f"No namespaced Codex wrappers found under {source}")

    contents: list[tuple[Path, bytes]] = []
    for wrapper in wrappers:
        if wrapper.is_symlink() or not wrapper.is_file():
            raise RuntimeError(f"Refusing non-regular source wrapper: {wrapper}")
        contents.append((wrapper, _read_regular_file(wrapper)))

    target.mkdir(parents=True, exist_ok=True)
    installed: list[str] = []
    unchanged: list[str] = []
    for wrapper, content in contents:
        destination = target / wrapper.name
        status = _write_owned_wrapper(destination, content)
        if status == "unchanged":
            unchanged.append(str(destination))
        else:
            installed.append(str(destination))

    # Only construct and write the index once every wrapper above has been
    # written successfully; if any wrapper write raised, this code never
    # runs, so the index is left at its prior on-disk state (absent or
    # unchanged), never partially/inconsistently updated.
    index = _build_index(contents, target)
    index_content = (json.dumps(index, indent=2, sort_keys=True) + "\n").encode("utf-8")
    index_destination = target / INDEX_FILENAME
    index_status = _write_owned_wrapper(index_destination, index_content)

    return {
        "installed": installed,
        "unchanged": unchanged,
        "index_status": index_status,
        "index_path": str(index_destination),
    }


def main() -> int:
    parser = argparse.ArgumentParser(
        prog="cadre bootstrap-codex",
        description="Install namespaced Cadre Codex wrappers without touching bare role files.",
        allow_abbrev=False,
    )
    parser.add_argument("--source", type=Path, default=DEFAULT_SOURCE)
    parser.add_argument("--target", type=Path, default=Path.home() / ".codex" / "agents")
    options = parser.parse_args()
    result = sync_wrappers(options.source.resolve(), options.target.expanduser().resolve())
    print(
        f"Installed {len(result['installed'])}; unchanged {len(result['unchanged'])}. "
        f"Index {result['index_status']} at {result['index_path']}."
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, RuntimeError, ValueError) as error:
        print(f"agents: {error}", file=sys.stderr)
        raise SystemExit(1) from error
