#!/usr/bin/env python3
"""Differential probe for the selector's git-derived inputs.

Builds a corpus of real checkouts, asks both implementations the same
questions about each, and reports every disagreement. Run directly:

    python3 roster/orchestration/test/probe_discover_parity.py

This is a probe, not a test: it constructs checkouts, shells out to git many
times, and takes a few seconds. What it proves is folded back into the
committed corpus and into Go unit tests, which is what CI runs.

It exists because the corpus alone cannot reach these code paths. Every
committed corpus case passes --files and --source explicitly, precisely so
the plan does not depend on the machine it was generated on -- which means
the discovery paths that run when those flags are *absent* are exactly the
ones the parity gate never exercises.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(REPOSITORY_ROOT / "roster" / "orchestration" / "src"))

import select_agents  # noqa: E402


def git(root: Path, *args: str) -> None:
    subprocess.run(
        ["git", *args],
        cwd=root,
        check=True,
        capture_output=True,
        env={**os.environ, "GIT_CONFIG_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0"},
    )


def new_checkout(parent: Path, name: str) -> Path:
    root = parent / name
    root.mkdir(parents=True)
    git(root, "init", "-q", "-b", "main")
    git(root, "config", "user.email", "probe@example.invalid")
    git(root, "config", "user.name", "Probe")
    return root


def commit_all(root: Path, message: str) -> None:
    git(root, "add", "-A")
    git(root, "commit", "-q", "-m", message, "--no-gpg-sign")


# Origin forms the slug parser has to survive. Each is a real remote URL set
# on a real checkout, so this measures the whole path, not a regex in
# isolation.
ORIGIN_FORMS = [
    "https://github.com/deagy/cadre.git",
    "https://github.com/deagy/cadre",
    "https://GitHub.com/Deagy/Cadre.git",
    "ssh://git@github.com/deagy/cadre.git",
    "ssh://git@github.com:22/deagy/cadre.git",
    "git@github.com:deagy/cadre.git",
    "git@github.com:deagy/cadre",
    "git@gitlab.example.com:group/subgroup/project.git",
    "https://gitlab.example.com/group/subgroup/deep/project.git",
    "https://host/owner/repo.GIT",
    "https://host/owner/repo.git/",
    "https://host//owner//repo.git",
    "file:///srv/git/owner/repo.git",
    "/srv/git/owner/repo.git",
    "../sibling/repo.git",
    "https://host/only-one-part.git",
    "https://host/",
    "https://user:token@host/owner/repo.git",
    "git@host:owner/repo with spaces.git",
    "https://host/owner/repo!bang.git",
    "https://host/owner/.git",
    "https://host/owner/repo.git.git",
]


def build_corpus(workspace: Path) -> list[dict[str, object]]:
    """Every checkout shape whose answers must agree, with why it is here."""
    cases: list[dict[str, object]] = []

    # --- origin URL forms -------------------------------------------------
    for index, origin in enumerate(ORIGIN_FORMS):
        root = new_checkout(workspace, f"origin-{index:02d}")
        (root / "seed.txt").write_text("seed\n")
        commit_all(root, "seed")
        git(root, "remote", "add", "origin", origin)
        cases.append({"root": str(root), "base": "", "why": f"origin form: {origin}"})

    # --- no origin at all -------------------------------------------------
    root = new_checkout(workspace, "no-origin")
    (root / "seed.txt").write_text("seed\n")
    commit_all(root, "seed")
    cases.append({"root": str(root), "base": "", "why": "no origin remote: falls back to local-<name>-<digest>"})

    # A directory name that is not a legal source name on its own, so the
    # sanitisation and the empty-basename fallback both get exercised.
    for name in ["Weird Name!", "....", "UPPER_Case"]:
        root = new_checkout(workspace, name)
        (root / "seed.txt").write_text("seed\n")
        commit_all(root, "seed")
        cases.append({"root": str(root), "base": "", "why": f"local source from directory name {name!r}"})

    # --- project-local knowledge store ------------------------------------
    root = new_checkout(workspace, "local-store")
    (root / ".agents" / "knowledge-store").mkdir(parents=True)
    (root / ".agents" / "knowledge-store" / "config.json").write_text("{}\n")
    (root / "seed.txt").write_text("seed\n")
    commit_all(root, "seed")
    cases.append({"root": str(root), "base": "", "why": "project-local store makes proposed-knowledge legal"})

    # A *directory* named config.json is not a config file. If either side
    # tests existence rather than file-ness they disagree here.
    root = new_checkout(workspace, "store-config-is-a-directory")
    (root / ".agents" / "knowledge-store" / "config.json").mkdir(parents=True)
    (root / "seed.txt").write_text("seed\n")
    commit_all(root, "seed")
    cases.append({"root": str(root), "base": "", "why": "config.json as a directory must not enable the staged source"})

    # --- git status shapes ------------------------------------------------
    root = new_checkout(workspace, "status-mixed")
    (root / "tracked.txt").write_text("one\n")
    (root / "to-delete.txt").write_text("gone\n")
    (root / "to-rename.txt").write_text("rename me\n")
    commit_all(root, "seed")
    (root / "tracked.txt").write_text("modified\n")
    (root / "to-delete.txt").unlink()
    git(root, "mv", "to-rename.txt", "renamed.txt")
    (root / "untracked.txt").write_text("new\n")
    (root / "nested").mkdir()
    (root / "nested" / "deep.txt").write_text("new\n")
    cases.append({"root": str(root), "base": "", "why": "modified + deleted + renamed + untracked, incl. a nested untracked file"})

    # Renames and copies append the original path as an extra NUL field.
    # Getting the skip wrong puts a phantom path into the plan.
    root = new_checkout(workspace, "status-staged-rename")
    (root / "original.txt").write_text("body\n")
    commit_all(root, "seed")
    git(root, "mv", "original.txt", "moved.txt")
    git(root, "add", "-A")
    cases.append({"root": str(root), "base": "", "why": "staged rename: the extra original-path field must be skipped"})

    # core.quotePath is why -z exists. A path needing quoting under --short
    # comes back mangled if parsed line-wise.
    root = new_checkout(workspace, "status-unicode")
    (root / "café.txt").write_text("accented\n")
    (root / "日本語.md").write_text("cjk\n")
    (root / "with space.txt").write_text("space\n")
    (root / 'quote"inside.txt').write_text("quote\n")
    (root / "back\\slash.txt").write_text("backslash\n")
    cases.append({"root": str(root), "base": "", "why": "paths git would quote under core.quotePath"})

    root = new_checkout(workspace, "status-clean")
    (root / "seed.txt").write_text("seed\n")
    commit_all(root, "seed")
    cases.append({"root": str(root), "base": "", "why": "clean tree: no changed files at all"})

    # --- base-ref diffs ---------------------------------------------------
    root = new_checkout(workspace, "diff-branch")
    (root / "seed.txt").write_text("seed\n")
    commit_all(root, "seed")
    git(root, "checkout", "-q", "-b", "feature")
    (root / "added.go").write_text("package main\n")
    (root / "seed.txt").write_text("changed\n")
    commit_all(root, "feature work")
    cases.append({"root": str(root), "base": "main", "why": "three-dot diff against a base ref"})
    cases.append({"root": str(root), "base": "feature", "why": "base ref equal to HEAD: an empty diff"})
    cases.append({"root": str(root), "base": "does-not-exist", "why": "unknown base ref: both must fail, not return empty"})

    # An uncommitted file must NOT appear in a base-ref diff -- the two
    # discovery modes answer different questions.
    (root / "uncommitted.txt").write_text("not in the diff\n")
    cases.append({"root": str(root), "base": "main", "why": "uncommitted file absent from a base-ref diff"})

    return cases


def python_answers(cases: list[dict[str, object]]) -> list[dict[str, object]]:
    answers = []
    for case in cases:
        root = Path(str(case["root"]))
        base = str(case["base"])

        answer: dict[str, object] = {"root": str(root)}
        try:
            changes = select_agents.discover_changed_files(base or None, root)
            answer["changed_file_source"] = changes["source"]
            answer["changed_files"] = list(changes["files"])
            answer["changed_files_error"] = ""
        except Exception as error:  # noqa: BLE001 -- the error itself is the answer
            answer["changed_file_source"] = ""
            answer["changed_files"] = []
            answer["changed_files_error"] = str(error)

        slug = select_agents._origin_slug(root)
        answer["origin_slug"] = slug or ""
        answer["has_origin_slug"] = slug is not None
        answer["project_source"] = select_agents.resolve_project_knowledge_source(root)
        answer["knowledge_sources"] = select_agents.resolve_knowledge_sources(root)
        answer["project_local_store"] = select_agents.has_project_local_knowledge_store(root)
        answers.append(answer)
    return answers


EXPLICIT_CASES = [
    {"files": ["a.go"], "sources": ["one"]},
    {"files": ["a.go,b.go", "c.go"], "sources": ["one", "two"]},
    {"files": ["a.go,a.go", "a.go"], "sources": ["dup", "dup"]},
    {"files": [" a.go , b.go "], "sources": [" spaced "]},
    {"files": ["a.go,,b.go"], "sources": ["one"]},
    {"files": [",", " "], "sources": ["one"]},
    {"files": [], "sources": []},
    {"files": ["a.go"], "sources": [""]},
    {"files": ["a.go"], "sources": ["one", ""]},
    {"files": ["a.go"], "sources": ["   "]},
    {"files": ["café.txt,日本語.md"], "sources": ["ünicode"]},
]


def python_explicit_answers() -> list[dict[str, object]]:
    answers = []
    for case in EXPLICIT_CASES:
        files = select_agents.explicit_files(list(case["files"])) or []
        sources: list[str] = []
        error = ""
        values = list(case["sources"])
        if values:
            blank = [index for index, value in enumerate(values) if not value.strip()]
            if blank:
                error = (
                    f"--source must be a non-empty knowledge-store source name; argument "
                    f"{blank[0] + 1} was empty. Omit --source entirely to use this repository's "
                    f"default sources."
                )
            else:
                sources = list(dict.fromkeys(values))
        answers.append({"files": files, "sources": sources, "sources_error": error})
    return answers


def run_go_probe(name: str, payload: object, workspace: Path, env_prefix: str) -> list[dict[str, object]]:
    input_path = workspace / f"{env_prefix.lower()}-in.json"
    output_path = workspace / f"{env_prefix.lower()}-out.json"
    input_path.write_text(json.dumps(payload))

    result = subprocess.run(
        ["go", "test", "./internal/selector/", "-run", name, "-count=1", "-v"],
        cwd=REPOSITORY_ROOT,
        capture_output=True,
        text=True,
        env={
            **os.environ,
            "CGO_ENABLED": "1",
            f"CADRE_{env_prefix}_PROBE_IN": str(input_path),
            f"CADRE_{env_prefix}_PROBE_OUT": str(output_path),
        },
    )
    if result.returncode != 0:
        sys.stderr.write(result.stdout + result.stderr)
        raise SystemExit(f"go probe {name} failed")
    return json.loads(output_path.read_text())


def compare(label: str, cases: list[dict[str, object]], python: list[object], go: list[object]) -> int:
    differences = 0
    for index, (expected, actual) in enumerate(zip(python, go)):
        if expected == actual:
            continue
        differences += 1
        why = cases[index].get("why", "") if index < len(cases) else ""
        print(f"\n  DIFFERS [{label} #{index}] {why}")
        keys = sorted(set(expected) | set(actual)) if isinstance(expected, dict) else []
        for key in keys:
            left, right = expected.get(key), actual.get(key)
            if left != right:
                print(f"    {key}:\n      python: {left!r}\n      go:     {right!r}")
    print(f"  {label}: {len(python)} cases, {len(python) - differences} identical, {differences} differing")
    return differences


def main() -> int:
    workspace = Path(tempfile.mkdtemp(prefix="cadre-discover-probe-"))
    try:
        print("building checkouts...")
        cases = build_corpus(workspace)
        print(f"  {len(cases)} checkout cases")

        expected = python_answers(cases)
        actual = run_go_probe("TestDiscoverParityProbe", cases, workspace, "DISCOVER")
        differences = compare("git-derived inputs", cases, expected, actual)

        explicit_expected = python_explicit_answers()
        explicit_actual = run_go_probe(
            "TestExplicitInputParityProbe", EXPLICIT_CASES, workspace, "EXPLICIT"
        )
        differences += compare("explicit inputs", EXPLICIT_CASES, explicit_expected, explicit_actual)

        print()
        if differences:
            print(f"FAIL: {differences} differing cases")
            return 1
        print(f"OK: {len(cases) + len(EXPLICIT_CASES)} cases, all identical")
        return 0
    finally:
        shutil.rmtree(workspace, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
