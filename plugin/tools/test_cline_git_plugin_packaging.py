#!/usr/bin/env python3
"""Guard the dependency closure Cline needs for a Git-source install.

Cline discovers the three TypeScript entrypoints from the repository root
when a user runs ``cline plugin install https://github.com/deagy/cadre``.
Unlike this repository's development workspace, that installation resolves
bare runtime imports from a root ``node_modules``.  Keep the manifest and
lockfile that create it small, explicit, and independent from the Claude
Code/Codex marketplace package under ``plugin/``.

The root closure and ``cline-plugins/`` declare the same runtime packages
twice, and Dependabot updates them on independent pull requests, so either
side can be bumped alone.  A divergence would be invisible: CI would keep
testing ``cline-plugins/``'s versions while a Git-source install shipped the
root's.  Nothing here is hardcoded for that reason -- the expected closure is
derived from the workspace manifests, so drift fails rather than passing
against a stale literal.
"""

from __future__ import annotations

import json
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
ENTRYPOINTS = (
    "./cline-plugins/cline-agents/index.ts",
    "./cline-plugins/cline-lifecycle/index.ts",
    "./cline-plugins/cline/index.ts",
)
# Supplied by Cline's host sandbox at runtime, so deliberately absent from the
# root closure even though the workspace packages declare them.
HOST_SUPPLIED_SCOPE = "@cline/"


def _read_json(*parts: str) -> dict:
    return json.loads((REPO_ROOT.joinpath(*parts)).read_text(encoding="utf-8"))


def _workspace_runtime_dependencies(
    entrypoints: tuple[str, ...] = ENTRYPOINTS,
) -> dict[str, dict[str, str]]:
    """Map each plugin-owned runtime package to {workspace: declared version}.

    Returning every declaration, rather than a collapsed name->version dict,
    lets the tests below report *which* workspace disagrees when two of them
    pin the same package differently.

    `entrypoints` is a parameter only so the coupling below can be tested
    against a synthetic layout; production callers use the module constant.
    """
    declarations: dict[str, dict[str, str]] = {}
    for entrypoint in entrypoints:
        # Each entrypoint is assumed to sit directly under
        # cline-plugins/<workspace>/. If one ever moves a level deeper --
        # cline-plugins/cline/dist/index.ts -- .parent.name resolves to "dist"
        # and every lookup below silently targets the wrong workspace.
        workspace = Path(entrypoint).parent.name
        manifest_path = REPO_ROOT / "cline-plugins" / workspace / "package.json"
        if not manifest_path.is_file():
            # Naming the assumption, rather than letting a FileNotFoundError
            # point at a path nobody expected to exist (issue #134). The
            # derivation is what broke; the missing file is only the symptom.
            raise AssertionError(
                f"entrypoint {entrypoint!r} implies workspace {workspace!r}, but"
                f" cline-plugins/{workspace}/package.json does not exist."
                " _workspace_runtime_dependencies derives the workspace name with"
                " Path(entrypoint).parent.name, which assumes each entrypoint sits"
                " directly under cline-plugins/<workspace>/. If this entrypoint moved"
                " a level deeper, that assumption is what needs updating -- not this"
                " manifest path."
            )
        manifest = _read_json("cline-plugins", workspace, "package.json")
        for name, version in manifest.get("dependencies", {}).items():
            if name.startswith(HOST_SUPPLIED_SCOPE):
                continue
            declarations.setdefault(name, {})[workspace] = version
    return declarations


def _resolve_workspace_package_key(
    workspace_packages: dict[str, dict[str, str]],
    dependency: str,
    by_workspace: dict[str, str],
) -> str:
    """Find the ``cline-plugins/package-lock.json`` "packages" key that holds
    our dependency's own resolved install.

    npm hoists a package to the top-level ``node_modules/<dependency>`` slot
    only when nothing else in the tree forces a conflicting version there.
    When something does, one of *our* workspaces' own correctly-pinned
    dependency can be pushed down into a workspace-scoped
    ``<workspace>/node_modules/<dependency>`` key instead (``cline-plugins``
    is the lockfile root, so workspaces are keyed by name alone, e.g.
    ``cline-agents/node_modules/zod`` -- NOT
    ``cline-plugins/cline-agents/node_modules/zod``). A missing top-level key
    is therefore not necessarily a missing dependency; it can just mean
    hoisting moved it.

    This is a different shape from a nested
    ``node_modules/<other>/node_modules/<dependency>`` entry, which is an
    unrelated transitive dependant's *own* pin (e.g.
    ``node_modules/dify-ai-provider/node_modules/zod``, a devDependency's own
    copy of zod). That shape must never be treated as ours: matching on the
    leaf package name alone, without restricting to workspaces we already
    know declare the dependency, could silently pick up somebody else's
    version and mask real drift.

    So the lookup is workspace-scoped, not a name-only glob: first the
    top-level key, then ``<workspace>/node_modules/<dependency>`` for each
    workspace that ``_workspace_runtime_dependencies()`` says declares this
    dependency. If more than one such nested candidate exists and they
    disagree, fail loudly rather than silently choosing one.

    A resolution being *valid* is not the same as it being the *only* one.
    Node resolves from the closest ``node_modules`` upward, so a
    ``<workspace>/node_modules/<dependency>`` entry is what code in that
    workspace actually loads -- even when a correct copy also sits at the top
    level. Returning the top-level key without looking for a disagreeing
    sibling would therefore pass a tree whose workspaces really do load
    different versions, and this comparison is the only check tying the root
    lockfile to ``cline-plugins/`` at all (the ``cline-git-source`` CI job
    proves resolvability but not version, and the release SBOM scans only
    ``cline-plugins/``). Every path below establishes exclusivity, not just
    existence.

    A top-level ``node_modules/<dependency>`` key existing is not by itself
    proof it is *our* copy: the key name only encodes the package name, not
    whose requirement won the slot, so a conflicting version forced there by
    some other dependant is indistinguishable by key alone. Every runtime
    dependency this repo's workspaces declare is pinned to an exact version
    (no ``^``/``~``/range operators) as of this writing, so the top-level
    entry is only trusted when its resolved version exactly matches one of
    our workspaces' own declared pins; otherwise the search falls through to
    the workspace-scoped keys below. If a future dependency starts using a
    real semver range, this exact-match check would need a proper range
    evaluator instead.
    """
    top_level_key = f"node_modules/{dependency}"
    declared_pins = {version.lstrip("^~=") for version in by_workspace.values()}
    scoped_keys = [
        f"{workspace}/node_modules/{dependency}"
        for workspace in sorted(by_workspace)
        if f"{workspace}/node_modules/{dependency}" in workspace_packages
    ]
    if (
        top_level_key in workspace_packages
        and workspace_packages[top_level_key]["version"] in declared_pins
    ):
        top_level = workspace_packages[top_level_key]
        # ``.get("integrity")`` rather than ``[...]``: a lockfile entry for a
        # git- or ``file:``-sourced dependency legitimately has no integrity
        # field, and a bare KeyError here would be the exact defect issue #186
        # complained about on the root-side lookup three assertions below --
        # reintroduced in the code fixing it. Missing compares as a distinct
        # value, so a registry copy and a git copy read as disagreeing, which
        # is the fail-closed direction.
        identity = (top_level["version"], top_level.get("integrity"))
        disagreeing = [
            key
            for key in scoped_keys
            if (
                workspace_packages[key]["version"],
                workspace_packages[key].get("integrity"),
            )
            != identity
        ]
        if disagreeing:
            raise AssertionError(
                f"{dependency} resolves to {top_level['version']} at the top-level key"
                f" {top_level_key!r}, but a declaring workspace also has its own copy at a"
                f" different version: {[(key, workspace_packages[key]['version']) for key in disagreeing]}"
                " in cline-plugins/package-lock.json. Node resolves from the closest"
                " node_modules upward, so that workspace loads its own copy, not the"
                " top-level one -- a valid top-level resolution is not proof it is the"
                " only resolution."
            )
        return top_level_key

    candidates = scoped_keys
    if not candidates:
        raise AssertionError(
            f"{dependency} was not found at the top-level key {top_level_key!r} in"
            " cline-plugins/package-lock.json, nor at any workspace-scoped"
            f" fallback key ({', '.join(f'{w}/node_modules/{dependency}' for w in sorted(by_workspace))})."
            " A missing top-level key alone would just mean npm hoisting moved the"
            " dependency elsewhere; this means it is genuinely absent from both"
            " shapes checked."
        )

    distinct = {
        (workspace_packages[key]["version"], workspace_packages[key]["integrity"])
        for key in candidates
    }
    if len(distinct) != 1:
        raise AssertionError(
            f"{dependency} resolves to disagreeing versions across the"
            f" workspace-scoped fallback keys {candidates} in"
            " cline-plugins/package-lock.json: "
            f"{[(key, workspace_packages[key]) for key in candidates]}. Refusing to"
            " silently pick one -- this indicates real drift between workspaces'"
            " own pins, not just hoisting."
        )
    return candidates[0]


class TestClineGitPluginPackaging(unittest.TestCase):
    def setUp(self) -> None:
        self.declarations = _workspace_runtime_dependencies()
        self.assertTrue(
            self.declarations,
            "no plugin-owned runtime dependencies found in cline-plugins/*/package.json;"
            " the derivation below would vacuously pass",
        )

    def test_workspaces_agree_on_every_shared_runtime_dependency(self) -> None:
        for name, by_workspace in self.declarations.items():
            with self.subTest(dependency=name):
                self.assertEqual(
                    len(set(by_workspace.values())),
                    1,
                    f"cline-plugins workspaces pin conflicting {name} versions:"
                    f" {by_workspace}. The root closure can only carry one, so a"
                    " Git-source install would ship the wrong version to at least"
                    " one entrypoint.",
                )

    def test_root_manifest_explicitly_declares_all_discovered_entrypoints(self) -> None:
        manifest = _read_json("package.json")
        self.assertTrue(manifest["private"])
        self.assertEqual(manifest["cline"]["plugins"], [{"paths": list(ENTRYPOINTS)}])
        self.assertNotIn("workspaces", manifest)
        self.assertNotIn("devDependencies", manifest)
        for entrypoint in ENTRYPOINTS:
            self.assertTrue((REPO_ROOT / entrypoint.removeprefix("./")).is_file())

    def test_root_manifest_matches_the_workspace_runtime_dependencies(self) -> None:
        expected = {
            name: sorted(by_workspace.values())[0]
            for name, by_workspace in self.declarations.items()
        }
        self.assertEqual(
            _read_json("package.json")["dependencies"],
            expected,
            "root package.json has drifted from cline-plugins/*/package.json."
            " Bump both sides together: the root closure is what a"
            " `cline plugin install <git-url>` actually resolves.",
        )

    def test_root_lockfile_pins_the_runtime_dependency_closure(self) -> None:
        manifest_dependencies = _read_json("package.json")["dependencies"]
        lockfile = _read_json("package-lock.json")
        self.assertEqual(lockfile["lockfileVersion"], 3)
        self.assertEqual(lockfile["packages"][""]["dependencies"], manifest_dependencies)
        for dependency, version in manifest_dependencies.items():
            with self.subTest(dependency=dependency):
                self.assertEqual(
                    lockfile["packages"][f"node_modules/{dependency}"]["version"], version
                )

    def test_both_lockfiles_resolve_the_same_runtime_versions(self) -> None:
        root = _read_json("package-lock.json")["packages"]
        workspace = _read_json("cline-plugins", "package-lock.json")["packages"]
        for dependency, by_workspace in self.declarations.items():
            with self.subTest(dependency=dependency):
                key = _resolve_workspace_package_key(workspace, dependency, by_workspace)
                root_key = f"node_modules/{dependency}"
                # The root lockfile has no workspaces (asserted separately), so
                # a top-level-only lookup is structurally right there -- but say
                # so rather than raising a bare KeyError, which sends a reader
                # looking for a resolver bug instead of a missing dependency.
                self.assertIn(
                    root_key,
                    root,
                    f"{dependency} is declared by a cline-plugins workspace but absent from the"
                    " root package-lock.json. A Git-source install ships the root closure, so"
                    " the dependency would be missing at runtime there.",
                )
                self.assertEqual(
                    root[f"node_modules/{dependency}"]["version"],
                    workspace[key]["version"],
                    f"{dependency} resolves to different versions in the root"
                    " lockfile and cline-plugins/package-lock.json. CI tests the"
                    " latter; a Git-source install ships the former.",
                )
                self.assertEqual(
                    root[f"node_modules/{dependency}"]["integrity"],
                    workspace[key]["integrity"],
                    f"{dependency} resolves to the same version in both lockfiles but a"
                    " different artifact. Same version, different tarball is a substitution,"
                    " not drift -- treat it as such.",
                )

    def test_a_sibling_without_an_integrity_field_is_not_a_crash(self) -> None:
        """A git- or ``file:``-sourced entry has no integrity field.

        Indexing it directly would raise a bare ``KeyError`` -- the same defect
        #186 called out on the root-side lookup, reintroduced by the code that
        fixed it. Missing integrity is treated as a distinct value, so a
        registry copy and a non-registry copy read as disagreeing rather than
        crashing or being waved through.
        """
        packages = {
            "node_modules/zod": {"version": "4.4.3", "integrity": "sha512-ours"},
            "cline-agents/node_modules/zod": {"version": "4.4.3"},
        }
        with self.assertRaises(AssertionError) as caught:
            _resolve_workspace_package_key(
                packages, "zod", {"cline": "4.4.3", "cline-agents": "4.4.3"}
            )
        self.assertIn("cline-agents/node_modules/zod", str(caught.exception))

    def test_hoisting_fallback_does_not_match_an_unrelated_transitive_dependant(
        self,
    ) -> None:
        # Synthetic fixture: `zod` is declared by the `cline` workspace, but
        # npm hoisted a *different* package's own `zod` pin into the
        # top-level slot instead, pushing `cline`'s own dependency down into
        # `cline/node_modules/zod`. An unrelated transitive dependant
        # (`dify-ai-provider`) also carries its own nested `zod`, at
        # `node_modules/dify-ai-provider/node_modules/zod` -- a leaf-name
        # match that must never be confused for one of our workspaces' own
        # copy.
        workspace_packages = {
            "node_modules/zod": {"version": "4.0.0", "integrity": "sha512-unrelated"},
            "node_modules/dify-ai-provider/node_modules/zod": {
                "version": "3.25.76",
                "integrity": "sha512-dify-own-pin",
            },
            "cline/node_modules/zod": {
                "version": "3.24.1",
                "integrity": "sha512-clines-own-pin",
            },
        }
        by_workspace = {"cline": "^3.24.1"}

        key = _resolve_workspace_package_key(workspace_packages, "zod", by_workspace)

        self.assertEqual(key, "cline/node_modules/zod")
        self.assertNotEqual(
            key,
            "node_modules/dify-ai-provider/node_modules/zod",
            "the fallback must not match an unrelated transitive dependant's own"
            " nested pin just because the leaf package name matches",
        )

    def test_a_deeper_entrypoint_names_the_derivation_it_breaks(self) -> None:
        """Issue #134: the workspace name is derived from the directory layout.

        `Path(entrypoint).parent.name` assumes each entrypoint sits directly
        under ``cline-plugins/<workspace>/``. An entrypoint one level deeper
        resolves to the wrong directory, and the resulting failure used to be a
        ``FileNotFoundError`` naming a path nobody expected rather than the
        assumption that produced it.
        """
        with self.assertRaises(AssertionError) as caught:
            _workspace_runtime_dependencies(("./cline-plugins/cline/dist/index.ts",))
        message = str(caught.exception)
        self.assertIn("dist", message)
        self.assertIn("Path(entrypoint).parent.name", message)
        self.assertIn("directly under", message)

    def test_the_real_entrypoints_still_resolve(self) -> None:
        """The guard must not fire on the layout as it exists today."""
        declarations = _workspace_runtime_dependencies()
        self.assertTrue(declarations)
        for workspaces in declarations.values():
            for workspace in workspaces:
                self.assertTrue(
                    (REPO_ROOT / "cline-plugins" / workspace / "package.json").is_file()
                )

    def test_a_valid_top_level_entry_does_not_hide_a_disagreeing_workspace_copy(
        self,
    ) -> None:
        """Issue #186: a valid resolution is not proof it is the only one.

        Node resolves from the closest ``node_modules`` upward, so a workspace
        holding its own copy loads that one -- even when a correctly pinned
        copy also sits at the top level. Returning the top-level key without
        checking for a disagreeing sibling would pass a tree whose workspaces
        genuinely load different versions, and this comparison is the only
        check tying the root lockfile to ``cline-plugins/``.
        """
        packages = {
            "node_modules/zod": {"version": "4.4.3", "integrity": "sha512-ours"},
            "cline-agents/node_modules/zod": {"version": "6.6.6", "integrity": "sha512-other"},
        }
        with self.assertRaises(AssertionError) as caught:
            _resolve_workspace_package_key(
                packages, "zod", {"cline": "4.4.3", "cline-agents": "4.4.3"}
            )
        message = str(caught.exception)
        self.assertIn("cline-agents/node_modules/zod", message)
        self.assertIn("6.6.6", message)
        self.assertIn("only resolution", message)

    def test_a_workspace_copy_matching_the_top_level_entry_is_not_a_conflict(
        self,
    ) -> None:
        """npm can legitimately write both keys at the same version.

        Only a *disagreeing* sibling is drift; an identical one is duplication,
        and failing on it would make the check reject correct trees.
        """
        packages = {
            "node_modules/zod": {"version": "4.4.3", "integrity": "sha512-ours"},
            "cline-agents/node_modules/zod": {"version": "4.4.3", "integrity": "sha512-ours"},
        }
        self.assertEqual(
            _resolve_workspace_package_key(
                packages, "zod", {"cline": "4.4.3", "cline-agents": "4.4.3"}
            ),
            "node_modules/zod",
        )

    def test_hoisting_fallback_fails_loudly_on_disagreeing_nested_candidates(
        self,
    ) -> None:
        # If more than one of our own workspaces declares the dependency and
        # npm hoisted neither of their pins to the top-level slot, the
        # fallback must refuse to silently pick one rather than mask real
        # drift between them.
        workspace_packages = {
            "node_modules/zod": {"version": "4.0.0", "integrity": "sha512-unrelated"},
            "cline/node_modules/zod": {
                "version": "3.24.1",
                "integrity": "sha512-cline-pin",
            },
            "cline-agents/node_modules/zod": {
                "version": "3.24.2",
                "integrity": "sha512-cline-agents-pin",
            },
        }
        by_workspace = {"cline": "^3.24.1", "cline-agents": "^3.24.2"}

        with self.assertRaises(AssertionError):
            _resolve_workspace_package_key(workspace_packages, "zod", by_workspace)

    def test_claude_and_codex_marketplace_package_remains_npm_free(self) -> None:
        manifests = [
            path.relative_to(REPO_ROOT)
            for path in (REPO_ROOT / "plugin").rglob("package.json")
            if "node_modules" not in path.parts
        ]
        self.assertEqual(manifests, [])


if __name__ == "__main__":
    unittest.main()
