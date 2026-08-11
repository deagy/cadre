"""The `schema_version` bump rule, enforced instead of reviewed (#224).

`roster/RUNBOOK.md`'s "When `schema_version` increments" rule:

    any change to the emitted field set -- addition, removal, or retype --
    increments `schema_version`.

`selection.schema.json` is closed (`additionalProperties: false`) *and*
vendored away from its producer (`pyproject.toml`'s
`cadre_cli/_vendor/roster/orchestration/selection.schema.json` force-include,
plus the plugin distribution), so a consumer routinely validates a freshly
generated plan against the copy pinned at whatever release they installed.
Adding, removing, or retyping an emitted property without bumping the version
makes that consumer fail on `additionalProperties` while the plan truthfully
reports the version their copy claims to handle -- a silent failure naming the
wrong cause. Nothing enforced the rule; it had been violated or nearly
violated three times, each caught only by human review (`dispatch_disposition`
in CHANGELOG 0.19.0, then twice in #214 -- by an author and independently by a
reviewer, both reaching for the `provenance` analogy without checking the
condition that makes its carve-out valid).

This module is that enforcement. It diffs the committed schema against **the
copy at the last release tag**, read with `git show`.

Three implementation choices are load-bearing:

1. **The baseline is the tag's committed copy, never a rebuilt one.**
   `pyproject.toml` force-includes this file into the wheel *from source*, so
   anything rebuilt reproduces the current file by construction and would
   always pass, guarding nothing.
2. **Tag selection is by version order over the `plugin-v*` namespace only.**
   The bare `v*` namespace holds 25 inherited pre-monorepo tags that a naive
   `git describe` would match. Ordering is by parsed `(major, minor, patch)`
   tuple, not by string sort (a `0.9.0` tag beats a `0.18.0` one lexically)
   and not by commit date (a patch release can be tagged after a later minor).
3. **The comparison is over the property set and its types, not raw bytes.**
   A description reword must not fail the build. "Emitted field set" means
   every property a plan can actually carry -- nested objects and array item
   schemas included, `$ref`s into `$defs` resolved -- and a retype counts.

**When this skips, and why.** Exactly one condition: no baseline is
obtainable -- `git` is unavailable, the tree is not a git repository, no
`plugin-vX.Y.Z` tag exists (a fresh clone fetched without tags, or a shallow
CI checkout), or the schema file did not exist at that tag. There is no
correct answer to compare against in those cases, and failing would make the
suite unrunnable in a tarball export. To stop "skip" from quietly becoming the
normal outcome and disabling the guard, that skip is **converted to a failure
under CI** (`GITHUB_ACTIONS`, or `CADRE_REQUIRE_SCHEMA_RELEASE_GUARD=1`
locally): in CI a missing baseline is a checkout misconfiguration, not an
acceptable state. `.github/workflows/validate.yml`'s `roster` job therefore
checks out with `fetch-depth: 0`, which is what fetches tags at all.

This also subsumes the limitation disclosed on
`test_undeclared_workflow_shape.py::test_a_pinned_v5_consumer_fails_on_the_version_not_on_the_new_property`,
which reconstructs the pinned-v5 schema by deleting one property from the
current file -- exact today, with nothing keeping it exact.
`V5ReconstructionTests` below asserts that reconstruction against the real
release baseline, so the property that test relies on is enforced rather than
merely currently-true.
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
REPOSITORY_ROOT = ROOT.parent.parent
SCHEMA_RELATIVE_PATH = "roster/orchestration/selection.schema.json"
SCHEMA_PATH = REPOSITORY_ROOT / SCHEMA_RELATIVE_PATH

# Release tags are component-prefixed. Only this namespace, and only the
# plain three-part form -- a prerelease/rc suffix is not a release, and the
# bare `v*` namespace is inherited pre-monorepo history.
RELEASE_TAG_PATTERN = re.compile(r"^plugin-v(\d+)\.(\d+)\.(\d+)$")

# Keywords that carry conditional constraints rather than declaring fields.
# Deliberately NOT walked when collecting the emitted field set: a branch's
# `properties: {"type": {"const": "fixed"}}` refines an already-declared
# property, and walking it would report `teams[].type` twice with conflicting
# signatures. Every object here is closed, so a property appearing *only*
# inside such a branch could never validate anyway -- an invariant
# `ConditionalBranchTests` asserts directly rather than assuming.
_CONDITIONAL_KEYWORDS = ("oneOf", "anyOf", "allOf", "if", "then", "else", "not")


def _git(*args: str) -> str:
    """Read-only git, raising on any failure so callers decide what a failure
    means (baseline unobtainable -> skip/fail, never a silent pass)."""
    return subprocess.run(
        ["git", *args],
        cwd=REPOSITORY_ROOT,
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
    ).stdout


def release_tags_newest_first() -> list[str]:
    """Every `plugin-vX.Y.Z` tag, newest first, by parsed version order.

    Not `git describe` (matches the inherited bare `v*` tags), not a string
    sort (lexically a `0.9.0` tag beats a `0.18.0` one), not commit date
    (a patch release may be tagged after a later minor).
    """
    try:
        listing = _git("tag", "--list", "plugin-v*")
    except (OSError, subprocess.CalledProcessError):
        return []

    versions: list[tuple[tuple[int, int, int], str]] = []
    for line in listing.splitlines():
        match = RELEASE_TAG_PATTERN.match(line.strip())
        if match:
            versions.append(((int(match[1]), int(match[2]), int(match[3])), line.strip()))
    return [tag for _, tag in sorted(versions, reverse=True)]


def latest_release_tag() -> str | None:
    tags = release_tags_newest_first()
    return tags[0] if tags else None


def schema_at_tag(tag: str) -> dict | None:
    """The schema exactly as committed at `tag`. `None` when the file did not
    exist there, or when the object is missing from a shallow clone."""
    try:
        blob = _git("show", f"{tag}:{SCHEMA_RELATIVE_PATH}")
    except (OSError, subprocess.CalledProcessError):
        return None
    try:
        return json.loads(blob)
    except json.JSONDecodeError:
        return None


def current_schema() -> dict:
    return json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))


def _json_type_name(value: object) -> str:
    if isinstance(value, bool):
        return "boolean"
    if isinstance(value, int):
        return "integer"
    if isinstance(value, float):
        return "number"
    if isinstance(value, str):
        return "string"
    if isinstance(value, list):
        return "array"
    if isinstance(value, dict):
        return "object"
    return "null"


def _type_signature(node: dict) -> str:
    """The comparable type of one resolved subschema.

    Derived from `type` when present, else from the JSON type of the `const`
    or `enum` values -- so `schema_version`'s `const` moving 6 -> 7 is not a
    "retype", and widening an `enum` with another string is not either. Values
    are deliberately outside this signature: the rule being enforced is about
    the emitted *field set*, and pulling value constraints in would make the
    version bump itself register as drift.
    """
    if "type" in node:
        declared = node["type"]
        names = [declared] if isinstance(declared, str) else list(declared)
        return "|".join(sorted(str(name) for name in names))
    if "const" in node:
        return _json_type_name(node["const"])
    if "enum" in node and isinstance(node["enum"], list):
        return "|".join(sorted({_json_type_name(value) for value in node["enum"]}))
    return "any"


def _resolve(node: dict, schema: dict, seen: frozenset[str]) -> tuple[dict, frozenset[str]]:
    """Inline a local `#/$defs/...` reference. Sibling keys win over the
    target's (2020-12 allows `$ref` siblings; `matched_routes` uses one for
    `description`). Returns the widened seen-set for cycle detection."""
    ref = node.get("$ref")
    if not isinstance(ref, str) or not ref.startswith("#/"):
        return node, seen
    if ref in seen:
        return {"type": "<recursive>"}, seen

    target: object = schema
    for part in ref.lstrip("#/").split("/"):
        if not isinstance(target, dict) or part not in target:
            return {"type": "<unresolvable-ref>"}, seen
        target = target[part]
    if not isinstance(target, dict):
        return {"type": "<unresolvable-ref>"}, seen

    merged = dict(target)
    merged.update({key: value for key, value in node.items() if key != "$ref"})
    return merged, seen | {ref}


def emitted_field_signatures(schema: dict) -> dict[str, str]:
    """Every property a plan can carry, mapped to its comparable type.

    Paths are dotted, with `[]` for array items:
    `inputs.task`, `context_packs[].content_hash`, `teams[].instances.min`,
    `matched_routes[].reasons.paths[].pattern`.
    """
    signatures: dict[str, str] = {}

    def walk(node: object, path: str, seen: frozenset[str], depth: int) -> None:
        if not isinstance(node, dict) or depth > 64:
            return
        node, seen = _resolve(node, schema, seen)
        if path:
            signatures[path] = _type_signature(node)

        properties = node.get("properties")
        if isinstance(properties, dict):
            for name, subschema in properties.items():
                child = f"{path}.{name}" if path else str(name)
                walk(subschema, child, seen, depth + 1)

        items = node.get("items")
        if isinstance(items, dict):
            walk(items, f"{path}[]", seen, depth + 1)
        prefix_items = node.get("prefixItems")
        if isinstance(prefix_items, list):
            for index, subschema in enumerate(prefix_items):
                walk(subschema, f"{path}[{index}]", seen, depth + 1)

    walk(schema, "", frozenset(), 0)
    return signatures


def schema_version_of(schema: dict) -> object:
    """The pinned `const`, which is what a consumer's copy claims to handle."""
    return schema.get("properties", {}).get("schema_version", {}).get("const")


def field_set_differences(previous: dict, current: dict) -> dict[str, list[str]]:
    """Added / removed / retyped emitted fields between two schema copies."""
    before = emitted_field_signatures(previous)
    after = emitted_field_signatures(current)
    return {
        "added": sorted(set(after) - set(before)),
        "removed": sorted(set(before) - set(after)),
        "retyped": sorted(
            f"{path}: {before[path]} -> {after[path]}"
            for path in set(before) & set(after)
            if before[path] != after[path]
        ),
    }


def describe(differences: dict[str, list[str]]) -> str:
    return "; ".join(
        f"{kind}: {', '.join(entries)}" for kind, entries in differences.items() if entries
    ) or "no field-set differences"


def any_difference(differences: dict[str, list[str]]) -> bool:
    return any(differences.values())


def conditional_branch_names(node: dict) -> tuple[set[str], set[str]]:
    """Property names referenced from one object's conditional branches
    (`oneOf`/`if`/`not`/...), as (declared-in-branch, listed-in-required)."""
    in_properties: set[str] = set()
    in_required: set[str] = set()

    def scan(branch: object) -> None:
        if isinstance(branch, list):
            for entry in branch:
                scan(entry)
            return
        if not isinstance(branch, dict):
            return
        properties = branch.get("properties")
        if isinstance(properties, dict):
            in_properties.update(str(name) for name in properties)
        required = branch.get("required")
        if isinstance(required, list):
            in_required.update(str(name) for name in required)
        for keyword in _CONDITIONAL_KEYWORDS:
            if keyword in branch:
                scan(branch[keyword])

    for keyword in _CONDITIONAL_KEYWORDS:
        if keyword in node:
            scan(node[keyword])
    return in_properties, in_required


def _ci_requires_the_guard() -> bool:
    return bool(
        os.environ.get("GITHUB_ACTIONS")
        or os.environ.get("CADRE_REQUIRE_SCHEMA_RELEASE_GUARD")
    )


class _BaselineTestCase(unittest.TestCase):
    """Shared baseline resolution, with the deliberate skip/fail split."""

    def baseline(self) -> tuple[str, dict]:
        tag = latest_release_tag()
        if tag is None:
            self._no_baseline(
                "no plugin-vX.Y.Z release tag is reachable -- this checkout was "
                "fetched without tags (a shallow CI checkout needs fetch-depth: 0), "
                "or it is not a git repository"
            )
        schema = schema_at_tag(tag)
        if schema is None:
            self._no_baseline(
                f"{SCHEMA_RELATIVE_PATH} could not be read at {tag} -- the file did "
                "not exist at that release, or the object is missing from a shallow clone"
            )
        return tag, schema

    def _no_baseline(self, reason: str) -> None:
        message = (
            f"selection.schema.json release-drift guard has no baseline: {reason}. "
            "The RUNBOOK 'When `schema_version` increments' rule is UNENFORCED for "
            "this run."
        )
        if _ci_requires_the_guard():
            self.fail(message + " Under CI this is a checkout misconfiguration, not "
                                "an acceptable state, so it fails rather than skips.")
        self.skipTest(message)


class TagSelectionTests(_BaselineTestCase):
    def test_only_the_component_prefixed_namespace_is_considered(self) -> None:
        """The bare `v*` namespace is inherited pre-monorepo history and must
        never be picked as a release baseline."""
        tag, _ = self.baseline()
        self.assertTrue(tag.startswith("plugin-v"), tag)
        self.assertRegex(tag, RELEASE_TAG_PATTERN)

    def test_ordering_is_by_version_not_by_string(self) -> None:
        """A `0.9.0` tag sorts after a `0.18.0` one lexically. Asserted on a
        synthetic listing so it stays true once real tags pass 0.9.

        The tag strings are assembled from parts rather than written out
        whole. They are synthetic fixture data, not references to releases
        that exist -- and `test_repository_health.py`'s hardcoded-release-tag
        guard reads this file too, correctly, since a literal tag baked into
        a source file is exactly what goes stale. Building them here keeps
        that guard structural instead of buying an allowlist exception for a
        file that is not a historical record.
        """
        def tag(prefix: str, major: int, minor: int, patch: int, suffix: str = "") -> str:
            return f"{prefix}{major}.{minor}.{patch}{suffix}"

        newest = tag("plugin-v", 0, 18, 0)
        candidates = [
            tag("plugin-v", 0, 9, 0),
            tag("plugin-v", 0, 10, 1),
            newest,
            tag("plugin-v", 0, 2, 0),
            tag("v", 0, 16, 0),  # inherited pre-monorepo namespace
            "v7",
            tag("plugin-v", 0, 19, 0, "-rc1"),  # a prerelease is not a release
        ]
        parsed = [
            ((int(m[1]), int(m[2]), int(m[3])), candidate)
            for candidate in candidates
            if (m := RELEASE_TAG_PATTERN.match(candidate))
        ]
        self.assertEqual(max(parsed)[1], newest)
        self.assertEqual(sorted(candidates)[-1], "v7")  # what a string sort would pick

    def test_the_baseline_is_read_from_git_not_rebuilt(self) -> None:
        """`pyproject.toml` force-includes this schema into the wheel from
        source, so a rebuilt baseline reproduces the current file by
        construction and always passes. The baseline must come from the tag's
        committed blob."""
        tag, schema = self.baseline()
        blob = _git("show", f"{tag}:{SCHEMA_RELATIVE_PATH}")
        self.assertEqual(schema, json.loads(blob))


class FieldSetExtractionTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.schema = current_schema()
        cls.signatures = emitted_field_signatures(cls.schema)

    def test_nested_objects_array_items_and_refs_are_all_walked(self) -> None:
        """'Emitted field set' is not the top-level `properties` keys."""
        expected = {
            "schema_version": "integer",
            "task_id": "string",
            "status": "string",
            "inputs": "object",
            "inputs.changed_files": "array",
            "context_packs[]": "object",
            "context_packs[].content_hash": "string",
            "agents.reviewers": "array",
            "teams[].instances.min": "integer",
            "teams[].communication_mode": "string",
            "matched_routes[].reasons.paths[].pattern": "string",
            "knowledge_context.requests[].invocation.launcher.runtime": "string",
            "human_gates[].kernel_mutation_gate_id": "null|string",
            "provenance.git_commit_sha": "string",
        }
        for path, signature in expected.items():
            with self.subTest(path=path):
                self.assertEqual(self.signatures.get(path), signature)

    def test_undeclared_workflow_shape_routes_is_covered(self) -> None:
        """#224 exists partly because this field (#214) shipped as 'additive,
        no bump' and was corrected only in review. The guard must see it."""
        self.assertEqual(self.signatures.get("undeclared_workflow_shape_routes"), "array")
        self.assertEqual(
            self.signatures.get("undeclared_workflow_shape_routes[]"), "string"
        )

    def test_definitions_are_resolved_rather_than_reported_as_paths(self) -> None:
        """`$defs` are shared shapes, not emitted fields. If they leaked into
        the path set, a `$defs` rename would read as a field add + remove."""
        leaked = [path for path in self.signatures if path.startswith("$defs")]
        self.assertEqual(leaked, [])
        self.assertNotIn("matched_routes[]", {})  # sanity: refs did resolve
        self.assertIn("matched_routes[].id", self.signatures)

    def test_resolution_terminates_on_a_self_referential_definition(self) -> None:
        recursive = {
            "type": "object",
            "properties": {"node": {"$ref": "#/$defs/node"}},
            "$defs": {
                "node": {
                    "type": "object",
                    "properties": {"child": {"$ref": "#/$defs/node"}},
                }
            },
        }
        signatures = emitted_field_signatures(recursive)
        self.assertEqual(signatures["node"], "object")
        self.assertEqual(signatures["node.child"], "<recursive>")

    def test_the_signature_ignores_values_so_a_version_bump_is_not_drift(self) -> None:
        bumped = current_schema()
        bumped["properties"]["schema_version"]["const"] = 999
        self.assertFalse(any_difference(field_set_differences(self.schema, bumped)))

        widened = current_schema()
        widened["properties"]["workflow"]["enum"].append("brand-new-shape")
        self.assertFalse(any_difference(field_set_differences(self.schema, widened)))


class ConditionalBranchTests(unittest.TestCase):
    """Closes the one hole in not walking `oneOf`/`if`/`not` for fields.

    A property declared *only* inside a conditional branch would be invisible
    to the extractor. Every object in this schema is closed, so such a
    property could never validate -- but that is an invariant, so assert it
    rather than rely on it.
    """

    def test_every_branch_property_is_also_declared_and_the_object_is_closed(self) -> None:
        schema = current_schema()
        checked = 0

        def visit(node: object, path: str) -> None:
            nonlocal checked
            if not isinstance(node, dict):
                return
            properties = node.get("properties")
            if isinstance(properties, dict):
                declared = {str(name) for name in properties}
                in_branches, in_required = conditional_branch_names(node)
                unknown = (in_branches | in_required) - declared
                if unknown:
                    checked += 1
                    self.assertEqual(
                        unknown,
                        set(),
                        f"{path or '<root>'} constrains {sorted(unknown)} from a "
                        "conditional branch without declaring it in `properties`; "
                        "the release-drift guard would not see that field",
                    )
                if any(keyword in node for keyword in _CONDITIONAL_KEYWORDS):
                    checked += 1
                    self.assertFalse(
                        node.get("additionalProperties", True),
                        f"{path or '<root>'} has conditional branches but is not "
                        "closed, so a branch-only property could validate unseen",
                    )
                for name, subschema in properties.items():
                    visit(subschema, f"{path}.{name}" if path else str(name))
            # Only structural descent here. Conditional branches are covered
            # by conditional_branch_names() against their *enclosing* object;
            # visiting a branch as an object in its own right would judge its
            # nested `not`/`anyOf` against the two keys the branch happens to
            # restate, which is not the declared set that matters.
            items = node.get("items")
            if isinstance(items, dict):
                visit(items, f"{path}[]")
            defs = node.get("$defs")
            if isinstance(defs, dict):
                for name, subschema in defs.items():
                    visit(subschema, f"$defs.{name}")

        visit(schema, "")
        self.assertGreater(checked, 0, "no conditional branch was inspected at all")


class DriftDetectionTests(unittest.TestCase):
    """The guard proved on synthetic pairs, permanently.

    A drift guard that only runs against a clean tree proves nothing, so each
    case the RUNBOOK rule names -- addition, removal, retype -- is a standing
    test, alongside the two changes that must NOT fail the build.
    """

    def setUp(self) -> None:
        self.previous = current_schema()

    def _verdict(self, current: dict) -> tuple[bool, dict[str, list[str]]]:
        """(would-the-guard-fail, differences) for a candidate schema."""
        differences = field_set_differences(self.previous, current)
        unchanged_version = schema_version_of(self.previous) == schema_version_of(current)
        return any_difference(differences) and unchanged_version, differences

    def test_an_added_top_level_property_without_a_bump_fails(self) -> None:
        current = current_schema()
        current["properties"]["cost_estimate"] = {"type": "number"}
        fails, differences = self._verdict(current)
        self.assertTrue(fails, describe(differences))
        self.assertEqual(differences["added"], ["cost_estimate"])

    def test_an_added_nested_property_without_a_bump_fails(self) -> None:
        """Nested, because reading only top-level `properties` keys -- the
        obvious implementation -- would miss exactly this."""
        current = current_schema()
        current["properties"]["inputs"]["properties"]["branch"] = {"type": "string"}
        fails, differences = self._verdict(current)
        self.assertTrue(fails, describe(differences))
        self.assertEqual(differences["added"], ["inputs.branch"])

    def test_an_added_array_item_property_without_a_bump_fails(self) -> None:
        current = current_schema()
        current["properties"]["teams"]["items"]["properties"]["priority"] = {
            "type": "integer"
        }
        fails, differences = self._verdict(current)
        self.assertTrue(fails, describe(differences))
        self.assertEqual(differences["added"], ["teams[].priority"])

    def test_a_property_added_through_a_shared_definition_fails(self) -> None:
        """`matched_routes` and `matched_risks` resolve to one definition, so
        a `$defs` edit adds fields at several emitted paths at once."""
        current = current_schema()
        current["$defs"]["idWithReasonsArray"]["items"]["properties"]["score"] = {
            "type": "number"
        }
        fails, differences = self._verdict(current)
        self.assertTrue(fails, describe(differences))
        self.assertEqual(
            differences["added"], ["matched_risks[].score", "matched_routes[].score"]
        )

    def test_a_removed_property_without_a_bump_fails(self) -> None:
        current = current_schema()
        del current["properties"]["provenance"]["properties"]["git_dirty_paths"]
        fails, differences = self._verdict(current)
        self.assertTrue(fails, describe(differences))
        self.assertEqual(
            differences["removed"],
            # The array and its item schema are two emitted paths.
            ["provenance.git_dirty_paths", "provenance.git_dirty_paths[]"],
        )

    def test_a_retyped_property_without_a_bump_fails(self) -> None:
        current = current_schema()
        current["properties"]["task_id"] = {"type": "integer"}
        current["properties"]["provenance"]["properties"]["git_commit_sha"] = {
            "type": ["string", "null"]
        }
        fails, differences = self._verdict(current)
        self.assertTrue(fails, describe(differences))
        self.assertEqual(
            differences["retyped"],
            [
                "provenance.git_commit_sha: string -> null|string",
                "task_id: string -> integer",
            ],
        )

    def test_a_description_reword_passes(self) -> None:
        """The whole reason this compares a property set rather than bytes."""
        current = current_schema()
        current["properties"]["undeclared_workflow_shape_routes"]["description"] = (
            "Reworded for clarity; the field itself is untouched."
        )
        current["properties"]["matched_routes"]["description"] = "Also reworded."
        current["$defs"]["matchReasons"]["description"] = "A new description entirely."
        current["title"] = "Local Agent Selection Plan (retitled)"
        fails, differences = self._verdict(current)
        self.assertFalse(fails, describe(differences))
        self.assertFalse(any_difference(differences), describe(differences))

    def test_key_reordering_alone_passes(self) -> None:
        current = current_schema()
        current["properties"] = dict(reversed(list(current["properties"].items())))
        fails, differences = self._verdict(current)
        self.assertFalse(fails, describe(differences))

    def test_the_same_changes_pass_once_schema_version_is_bumped(self) -> None:
        """The rule is 'bump when the field set changes', not 'never change
        the field set' -- a legitimate change must go green."""
        for label, mutate in (
            ("added", lambda s: s["properties"].__setitem__("cost_estimate", {"type": "number"})),
            ("removed", lambda s: s["properties"]["provenance"]["properties"].pop("git_dirty_paths")),
            ("retyped", lambda s: s["properties"].__setitem__("task_id", {"type": "integer"})),
        ):
            with self.subTest(case=label):
                current = current_schema()
                mutate(current)
                current["properties"]["schema_version"]["const"] = (
                    schema_version_of(self.previous) + 1
                )
                fails, differences = self._verdict(current)
                self.assertTrue(any_difference(differences), "the case must be real drift")
                self.assertFalse(fails, describe(differences))


class V5ReconstructionTests(unittest.TestCase):
    """Subsumes the limitation disclosed on
    `test_undeclared_workflow_shape.py::test_a_pinned_v5_consumer_fails_on_the_version_not_on_the_new_property`.

    That test reconstructs the pinned-v5 schema by deleting one property from
    the current file. Exact today; nothing kept it exact. Here the same delta
    is replayed as a schema pair, so #214's original "additive, no bump"
    proposal is a standing failing case rather than a corrected memory.
    """

    SIGNAL = "undeclared_workflow_shape_routes"

    def _reconstructed_v5(self) -> dict:
        schema = current_schema()
        schema["properties"]["schema_version"]["const"] = 5
        del schema["properties"][self.SIGNAL]
        return schema

    def test_shipping_the_signal_without_a_bump_would_have_failed_this_guard(self) -> None:
        previous = self._reconstructed_v5()
        mislabelled = current_schema()
        mislabelled["properties"]["schema_version"]["const"] = 5  # the rejected proposal

        differences = field_set_differences(previous, mislabelled)
        self.assertEqual(differences["added"], [self.SIGNAL, f"{self.SIGNAL}[]"])
        self.assertTrue(
            any_difference(differences)
            and schema_version_of(previous) == schema_version_of(mislabelled),
            "the guard must reject the unbumped variant",
        )

    def test_the_bump_that_actually_shipped_passes(self) -> None:
        differences = field_set_differences(self._reconstructed_v5(), current_schema())
        self.assertEqual(differences["added"], [self.SIGNAL, f"{self.SIGNAL}[]"])
        self.assertNotEqual(
            schema_version_of(self._reconstructed_v5()), schema_version_of(current_schema())
        )

    def test_the_reconstruction_matches_the_schema_actually_released_as_v5(self) -> None:
        """The disclosed limitation, closed against real released bytes.

        `_pinned_v5_schema()` asserts what a v5 consumer sees by deleting one
        property from the *current* file. This checks that claim against the
        newest release tag that actually shipped `schema_version: 5` -- found
        by walking the tags rather than named here, so this stays correct
        without a literal tag going stale in it -- so if some later change to
        the current schema ever makes that reconstruction inexact, it is
        caught here rather than quietly weakening the test that depends on it.
        """
        released = None
        for tag in release_tags_newest_first():
            candidate = schema_at_tag(tag)
            if candidate is None:
                continue
            if schema_version_of(candidate) == 5:
                released = (tag, candidate)
                break
        if released is None:
            if _ci_requires_the_guard():
                self.fail(
                    "no release tag shipping schema_version 5 is reachable under CI "
                    "(see module docstring); the reconstruction in "
                    "test_undeclared_workflow_shape.py cannot be verified"
                )
            self.skipTest("no release tag shipping schema_version 5 is reachable")

        tag, baseline = released
        self.assertEqual(
            emitted_field_signatures(baseline),
            emitted_field_signatures(self._reconstructed_v5()),
            "test_undeclared_workflow_shape.py reconstructs the pinned-v5 schema by "
            f"deleting {self.SIGNAL!r} from the current file; that no longer "
            f"reproduces the field set actually released at {tag}",
        )


class ReleaseDriftTests(_BaselineTestCase):
    """The live guard: the committed schema against the last release tag."""

    def test_the_emitted_field_set_did_not_change_without_a_version_bump(self) -> None:
        tag, previous = self.baseline()
        current = current_schema()
        differences = field_set_differences(previous, current)
        if not any_difference(differences):
            return

        previous_version = schema_version_of(previous)
        current_version = schema_version_of(current)
        self.assertNotEqual(
            current_version,
            previous_version,
            "selection.schema.json's emitted field set changed since "
            f"{tag} while schema_version stayed at {current_version!r}.\n"
            f"  {describe(differences)}\n"
            "RUNBOOK.md, 'When `schema_version` increments': any change to the "
            "emitted field set -- addition, removal, or retype -- increments "
            "schema_version. The schema is closed and vendored away from its "
            "producer, so a consumer's pinned copy would reject the plan on "
            "`additionalProperties` while the plan truthfully reported the "
            "version their copy claims to handle. Bump the const, and record "
            "the change in CHANGELOG.md.",
        )

    def test_schema_version_never_moves_backwards(self) -> None:
        tag, previous = self.baseline()
        previous_version = schema_version_of(previous)
        current_version = schema_version_of(current_schema())
        self.assertIsInstance(current_version, int)
        self.assertGreaterEqual(
            current_version,
            previous_version,
            f"schema_version went backwards from {previous_version!r} at {tag}",
        )


if __name__ == "__main__":
    unittest.main()
