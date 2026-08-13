#!/usr/bin/env python3
"""Portable, standard-library bootstrap and lifecycle CLI for Agentic SDLC."""

from __future__ import annotations

import argparse
import fnmatch
import hashlib
import json
import os
import re
import secrets
import stat
import subprocess
import sys
from collections.abc import Callable
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib.parse import quote



# Bump this on every tagged release (`git tag vX.Y.Z`); nothing else in this
# repo does it automatically. It drifted badly once already: v0.4.0 through
# v0.12.0 (9 tagged releases, 2026-07-25 through 2026-08-04, adding `decide`,
# GitLab/GitHub gate-tracking issues, gate-status publishing, and reviewer
# reporting/nudging) all shipped with this constant still reading "0.3.0"
# from the v0.3.0 tag. That silently broke every consumer pinning a provider
# manifest's `kernel_compatibility` range against this value (see
# `deagy/cadre-lifecycle`'s `provider.json`, which requested exactly
# `[0.3.0, 0.4.0)` and therefore could only ever resolve the original,
# feature-incomplete v0.3.0 tag no matter which later tag a consumer's
# bootstrap script actually installed from) -- verify this string matches
# the tag being cut before pushing it.
VERSION = "0.13.2"

# Packaged as the `agentic-sdlc` pip/pipx-installable distribution (see
# kernel/pyproject.toml); contracts/ is bundled as package data
# by hatchling's force-include at build time, but the checked-out source tree
# deliberately keeps a single canonical copy at kernel/contracts/
# (not duplicated under this package directory) since
# engine/agentic_sdlc_langgraph/runtime.py also reads it
# directly from that fixed repo-relative path. Resolve either location
# correctly: a bundled copy at agentic_sdlc/contracts/ (installed/built) if
# present, else the sibling ../contracts/ (running from a checkout, installed
# editable, or via `python -m agentic_sdlc`/bin/agentic-sdlc).
_PACKAGE_DIR = Path(__file__).resolve().parent
_BUNDLED_CONTRACTS = _PACKAGE_DIR / "contracts"
if _BUNDLED_CONTRACTS.is_dir():
    PLUGIN_ROOT = _PACKAGE_DIR
else:
    PLUGIN_ROOT = _PACKAGE_DIR.parent
CONTRACTS = PLUGIN_ROOT / "contracts"
PROFILES = PLUGIN_ROOT / "profiles"
EXTENSIONS = PLUGIN_ROOT / "extensions"
PROFILE_SEARCH_PATH: list[Path] = []
EXTENSIONS_SEARCH_PATH: list[Path] = []
AGENT_CATALOG_SEARCH_PATH: list[Path] = []
LOADED_PROVIDERS: list[dict[str, Any]] = []
OVERLAY = ".agentic-sdlc"
def lifecycle_contract() -> dict[str, Any]:
    return load_json(CONTRACTS / "lifecycle-gates.json")


GATE_IDS = [f"G{number}" for number in range(1, 11)]
REQUIRED_AUTHORITY_ROLES = {
    "product_owner": ["G1", "G2", "G6"],
    "engineering_lead": ["G2", "G6"],
    "system_architect": ["G3"],
    "governance_lead": ["G4"],
    "security_lead": ["G5"],
    "release_owner": ["G7", "G8"],
    "release_authority": ["G9"],
    "service_owner": ["G10"],
}
CONDITIONAL_AUTHORITY_ROLES = {
    "data_control_owner": ["G4"],
    "human_key_owner": ["G5"],
    "uat_product_owner": ["G6"],
    "implicated_security_lead": ["G10"],
    "implicated_governance_lead": ["G10"],
}
AUTHORITY_ROLES = {**REQUIRED_AUTHORITY_ROLES, **CONDITIONAL_AUTHORITY_ROLES}
ROLE_LABELS = {
    "product_owner": "Product Owner", "engineering_lead": "Engineering Lead",
    "system_architect": "System Architect", "governance_lead": "Governance Lead",
    "data_control_owner": "Data/Control Owner", "security_lead": "Security Lead",
    "human_key_owner": "Human Key Owner", "uat_product_owner": "Product Owner",
    "implicated_security_lead": "Security Lead",
    "implicated_governance_lead": "Governance Lead",
    "release_owner": "Release Owner", "release_authority": "Release Authority",
    "service_owner": "Service Owner",
}
MANAGED_START = "<!-- agentic-sdlc:start -->"
MANAGED_END = "<!-- agentic-sdlc:end -->"
GITHUB_REVIEW_URI = re.compile(
    r"^github-review:(?P<owner>[A-Za-z0-9_.-]+)/(?P<repo>[A-Za-z0-9_.-]+):"
    r"pull/(?P<pull>[0-9]+):review/(?P<review>[0-9]+):reviewer/(?P<login>[A-Za-z0-9-]+)$"
)
# GitLab MR approval-evidence adapter (RG-1). SPECULATIVE / not currently required: this was
# built on a mistaken premise that the consuming Secure Cloud provider's source control was
# GitLab (team-profile.yaml actually declares GitHub for source_control/approvals -- GitLab is
# only used for CI/CD there, which the existing GitHub adapter above already covers). It ships
# as a real, callable CLI surface (approve-from-gitlab / approve-from-gitlab-mr, visible in
# --help) rather than being dead/disabled code -- "speculative" describes why it was built, not
# whether it is reachable, so do not assume it needs activating before it can be used or
# misused. Kept as an optional capability rather than discarded, since the code itself is
# correct and tested. Per product-owner amendment, this provides the same trust level as the
# GitHub adapter above -- a trusted API attestation read from GitLab's own approval state, not
# independent non-repudiation/signing. Per the data-minimization amendment, only the approver's
# pseudonymous username is ever persisted (never name/email/avatar). Only gitlab.com identities
# (`gitlab.com/<user>`) are recognized by convention-based binding; a self-hosted GitLab instance
# requires the explicit `gitlab_username` authority field, since the platform this adapter was
# built against is itself self-hosted and gitlab.com may not be the actual instance in use.
GITLAB_MR_URI = re.compile(
    r"^gitlab-mr:(?P<project_path>[A-Za-z0-9_./-]+):merge_requests/(?P<iid>\d+):"
    r"approval/(?P<approval_id>[^:]+):approver/(?P<username>[A-Za-z0-9_.-]+)$"
)
# GitLab issue linkage for G1 Intent / G2 Requirements Baseline. Deliberately
# NOT an approval-evidence adapter: linking a GitLab issue as a gate's source
# never marks that gate approved, and gate approval (the human_approval_{gate}
# interrupt / approve-from-* commands above) is unaffected by whether a
# source is linked. This is why there is no "approver" concept in the URI
# below (unlike GITHUB_REVIEW_URI/GITLAB_MR_URI) -- an issue link records
# where the intent/requirements content came from, not who signed off on it.
GITLAB_ISSUE_URI = re.compile(
    r"^gitlab-issue:(?P<project_path>[A-Za-z0-9_./-]+):issues/(?P<iid>\d+)$"
)
# GitHub issue linkage for G1 Intent / G2 Requirements Baseline -- the GitHub
# counterpart to GITLAB_ISSUE_URI above, same rationale: deliberately NOT an
# approval-evidence adapter (no "approver" concept in the URI), never marks a
# gate approved, and gate approval is unaffected by whether a source is
# linked. See record_source_issue_link's docstring for the shared behavior.
GITHUB_ISSUE_URI = re.compile(
    r"^github-issue:(?P<owner>[A-Za-z0-9_.-]+)/(?P<repo>[A-Za-z0-9_.-]+):issues/(?P<number>\d+)$"
)


def now() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def is_valid_datetime(value: Any) -> bool:
    if not isinstance(value, str):
        return False
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return False
    return parsed.tzinfo is not None


def semver_tuple(value: str) -> tuple[int, int, int]:
    match = re.fullmatch(r"([0-9]+)\.([0-9]+)\.([0-9]+)", value)
    if not match:
        raise ValueError(f"invalid semantic version: {value}")
    return tuple(int(part) for part in match.groups())  # type: ignore[return-value]


def provider_resource(root: Path, value: Any, field: str, *, directory: bool) -> Path:
    if not isinstance(value, str) or not value:
        raise ValueError(f"provider {field} must be a non-empty relative path")
    candidate = (root / value).resolve()
    if not candidate.is_relative_to(root):
        raise ValueError(f"provider {field} escapes its manifest directory")
    if directory and not candidate.is_dir():
        raise ValueError(f"provider {field} directory does not exist: {value}")
    if not directory and not candidate.is_file():
        raise ValueError(f"provider {field} file does not exist: {value}")
    return candidate


def profile_ids() -> set[str]:
    return {
        path.name
        for root in PROFILE_SEARCH_PATH
        if root.is_dir()
        for path in root.iterdir()
        if path.is_dir() and (path / "profile.json").is_file()
    }


def extension_ids() -> set[str]:
    return {
        path.name
        for root in EXTENSIONS_SEARCH_PATH
        if root.is_dir()
        for path in root.iterdir()
        if path.is_dir() and (path / "extension.json").is_file()
    }


def load_provider(manifest_path: str) -> None:
    path = Path(manifest_path).resolve()
    manifest = load_json(path)
    if manifest.get("schema_version") != 1:
        raise ValueError(f"unsupported provider schema in {path}")
    allowed_manifest_keys = {"schema_version", "id", "version", "kernel_compatibility", "agent_catalog", "profile_roots", "extension_roots", "dependencies", "dispatch_bindings"}
    unknown_manifest_keys = set(manifest) - allowed_manifest_keys
    if unknown_manifest_keys:
        raise ValueError(f"provider manifest contains unknown fields: {sorted(unknown_manifest_keys)}")
    provider_id = manifest.get("id")
    provider_version = manifest.get("version")
    if not isinstance(provider_id, str) or not re.fullmatch(r"[a-z0-9][a-z0-9-]*", provider_id):
        raise ValueError(f"invalid provider id in {path}")
    if provider_id in {item["id"] for item in LOADED_PROVIDERS}:
        raise ValueError(f"duplicate provider id: {provider_id}")
    version = semver_tuple(str(provider_version))
    compatibility = manifest.get("kernel_compatibility")
    if not isinstance(compatibility, dict):
        raise ValueError(f"provider {provider_id} is missing kernel_compatibility")
    minimum_str = str(compatibility.get("minimum"))
    maximum_str = str(compatibility.get("maximum_exclusive"))
    minimum = semver_tuple(minimum_str)
    maximum = semver_tuple(maximum_str)
    if not minimum <= semver_tuple(VERSION) < maximum:
        raise ValueError(
            f"provider {provider_id} declares kernel_compatibility [{minimum_str}, {maximum_str}), "
            f"which does not include this kernel's version {VERSION}; install a provider compatible "
            f"with kernel {VERSION}, or a kernel version within the provider's declared range"
        )
    for dependency in manifest.get("dependencies", []):
        if not isinstance(dependency, dict) or not isinstance(dependency.get("id"), str):
            raise ValueError(f"provider {provider_id} has malformed dependency metadata")
        if dependency["id"] not in {item["id"] for item in LOADED_PROVIDERS}:
            raise ValueError(f"provider {provider_id} requires provider {dependency['id']} to be loaded first")

    root = path.parent
    catalog = provider_resource(root, manifest.get("agent_catalog"), "agent_catalog", directory=False)
    catalog_data = load_json(catalog)
    if catalog_data.get("schema_version") != 1 or not isinstance(catalog_data.get("agents"), dict):
        raise ValueError(f"provider {provider_id} agent catalog must contain an agents object")
    valid_kinds = {"author", "reviewer", "specialist"}
    valid_capabilities = {"author", "reviewer", "dispatch"}
    for agent_id, agent in catalog_data["agents"].items():
        if not re.fullmatch(r"[a-z0-9][a-z0-9-]*", str(agent_id)) or not isinstance(agent, dict):
            raise ValueError(f"provider {provider_id} has an invalid agent id: {agent_id}")
        if agent.get("kind") not in valid_kinds:
            raise ValueError(f"provider {provider_id} agent {agent_id} has unknown kind")
        capabilities = agent.get("capabilities", [])
        if not isinstance(capabilities, list) or set(capabilities) - valid_capabilities:
            raise ValueError(f"provider {provider_id} agent {agent_id} has unknown capabilities")
        if agent.get("kind") == "reviewer" and set(capabilities) - {"reviewer"}:
            raise ValueError(f"provider {provider_id} reviewer {agent_id} must remain read-only")
    profile_roots = [
        provider_resource(root, item, "profile_roots", directory=True)
        for item in manifest.get("profile_roots", [])
    ]
    extension_roots = [
        provider_resource(root, item, "extension_roots", directory=True)
        for item in manifest.get("extension_roots", [])
    ]
    if not profile_roots:
        raise ValueError(f"provider {provider_id} must define at least one profile root")

    existing_profiles = profile_ids()
    supplied_profiles = {
        child.name
        for profile_root in profile_roots
        for child in profile_root.iterdir()
        if child.is_dir() and (child / "profile.json").is_file()
    }
    duplicate_profiles = existing_profiles.intersection(supplied_profiles)
    if duplicate_profiles:
        raise ValueError(f"provider {provider_id} duplicates profile ids: {sorted(duplicate_profiles)}")
    existing_extensions = extension_ids()
    supplied_extensions = {
        child.name
        for extension_root in extension_roots
        for child in extension_root.iterdir()
        if child.is_dir() and (child / "extension.json").is_file()
    }
    duplicate_extensions = existing_extensions.intersection(supplied_extensions)
    if duplicate_extensions:
        raise ValueError(f"provider {provider_id} duplicates extension ids: {sorted(duplicate_extensions)}")
    for profile_root in profile_roots:
        for profile_dir in profile_root.iterdir():
            profile_path = profile_dir / "profile.json"
            if not profile_path.is_file():
                continue
            profile = load_json(profile_path)
            if profile.get("id") != profile_dir.name or not isinstance(profile.get("version"), str) or not isinstance(profile.get("gate_bindings"), dict):
                raise ValueError(f"provider {provider_id} has malformed profile: {profile_path}")
    for extension_root in extension_roots:
        for extension_dir in extension_root.iterdir():
            extension_path = extension_dir / "extension.json"
            if not extension_path.is_file():
                continue
            extension = load_json(extension_path)
            if extension.get("schema_version") != 1 or extension.get("id") != extension_dir.name or not isinstance(extension.get("version"), str):
                raise ValueError(f"provider {provider_id} has malformed extension: {extension_path}")

    PROFILE_SEARCH_PATH.extend(profile_roots)
    EXTENSIONS_SEARCH_PATH.extend(extension_roots)
    AGENT_CATALOG_SEARCH_PATH.append(catalog)
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    LOADED_PROVIDERS.append(
        {
            "id": provider_id,
            "version": ".".join(str(part) for part in version),
            "manifest_sha256": f"sha256:{digest}",
            "catalog_sha256": fingerprint(catalog_data),
            "dependencies": manifest.get("dependencies", []),
        }
    )


def approval_source_policy(project: dict[str, Any]) -> dict[str, Any]:
    policy = project.get("approval_sources", {})
    if not isinstance(policy, dict):
        raise ValueError("project approval_sources must be a JSON object")
    source = policy.get("human_gate_default", "manual")
    allow_manual_fallback = policy.get("allow_manual_fallback", True)
    if source not in {"manual", "github-review", "gitlab-mr"}:
        raise ValueError("project approval_sources.human_gate_default must be 'manual', 'github-review', or 'gitlab-mr'")
    if not isinstance(allow_manual_fallback, bool):
        raise ValueError("project approval_sources.allow_manual_fallback must be a boolean")
    return {
        "human_gate_default": source,
        "allow_manual_fallback": allow_manual_fallback,
    }


def github_login_from_identity(value: Any) -> str | None:
    if not isinstance(value, str):
        return None
    if value.startswith("github.com/"):
        login = value.removeprefix("github.com/").strip("/")
        return login or None
    return None


def authority_github_login(authority: dict[str, Any]) -> str | None:
    explicit = authority.get("github_login")
    if isinstance(explicit, str) and explicit:
        return explicit
    return github_login_from_identity(authority.get("assignee"))


def parse_github_review_uri(value: str) -> dict[str, str] | None:
    match = GITHUB_REVIEW_URI.fullmatch(value)
    if not match:
        return None
    return match.groupdict()


def normalize_commit_sha(value: Any) -> str | None:
    if not isinstance(value, str):
        return None
    cleaned = value.strip().lower()
    return cleaned or None


def fetch_github_pr_reviews(repo: str, pr: int) -> list[dict[str, Any]]:
    mock_path = os.environ.get("AGENTIC_SDLC_TEST_GITHUB_REVIEWS_FILE")
    if mock_path:
        payload = json.loads(Path(mock_path).read_text(encoding="utf-8"))
    else:
        encoded_repo_parts = "/".join(quote(part, safe="") for part in repo.split("/", 1))
        result = subprocess.run(
            ["gh", "api", f"repos/{encoded_repo_parts}/pulls/{pr}/reviews"],
            text=True,
            capture_output=True,
            check=False,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip() or "unknown gh api failure"
            raise ValueError(f"unable to fetch GitHub reviews for {repo} PR {pr}: {detail}")
        payload = json.loads(result.stdout)
    if not isinstance(payload, list):
        raise ValueError("GitHub reviews response must be a JSON array")
    reviews = [item for item in payload if isinstance(item, dict)]
    if len(reviews) != len(payload):
        raise ValueError("GitHub reviews response contains non-object entries")
    return reviews


def select_github_review(
    reviews: list[dict[str, Any]], reviewer_login: str, commit_sha: str | None = None
) -> dict[str, Any]:
    normalized_login = reviewer_login.lower()
    normalized_commit = normalize_commit_sha(commit_sha)
    matching: list[dict[str, Any]] = []
    for review in reviews:
        user = review.get("user")
        login = user.get("login") if isinstance(user, dict) else None
        submitted_at = review.get("submitted_at")
        review_commit = normalize_commit_sha(review.get("commit_id"))
        if not isinstance(login, str) or login.lower() != normalized_login:
            continue
        if not is_valid_datetime(submitted_at):
            continue
        if normalized_commit and review_commit != normalized_commit:
            continue
        matching.append(review)
    if not matching:
        commit_text = f" at commit {commit_sha}" if commit_sha else ""
        raise ValueError(f"no GitHub review found for reviewer {reviewer_login}{commit_text}")
    matching.sort(key=lambda review: str(review.get("submitted_at")))
    latest = matching[-1]
    if latest.get("state") != "APPROVED" or latest.get("dismissed_state") in {"DISMISSED", "dismissed"}:
        raise ValueError(f"latest GitHub review for reviewer {reviewer_login} is not an effective approval")
    return latest


def gitlab_username_from_identity(value: Any) -> str | None:
    if not isinstance(value, str):
        return None
    if value.startswith("gitlab.com/"):
        username = value.removeprefix("gitlab.com/").strip("/")
        return username or None
    return None


def authority_gitlab_username(authority: dict[str, Any]) -> str | None:
    explicit = authority.get("gitlab_username")
    if isinstance(explicit, str) and explicit:
        return explicit
    return gitlab_username_from_identity(authority.get("assignee"))


def parse_gitlab_mr_uri(value: str) -> dict[str, str] | None:
    match = GITLAB_MR_URI.fullmatch(value)
    if not match:
        return None
    return match.groupdict()


def gitlab_approval_records_from_api_response(raw: Any) -> list[dict[str, Any]]:
    """Normalize GitLab's single merge-request approvals-state object into a
    per-approver record list, mirroring the GitHub per-review shape. Only the
    fields required for evidence (username, approval identifier, decision
    state, decision time, reviewed commit) are extracted; GitLab API fields
    such as name/email/avatar_url are intentionally never read here (data
    minimization amendment)."""
    if not isinstance(raw, dict):
        raise ValueError("GitLab approvals API response must be a JSON object")
    approved_by = raw.get("approved_by", [])
    commit_sha = raw.get("sha")
    records: list[dict[str, Any]] = []
    if isinstance(approved_by, list):
        for entry in approved_by:
            user = entry.get("user") if isinstance(entry, dict) else None
            username = user.get("username") if isinstance(user, dict) else None
            if not isinstance(username, str) or not username:
                continue
            user_id = user.get("id") if isinstance(user, dict) else None
            # Presence in `approved_by` is GitLab's per-user approval signal
            # and is independent of the MR-level `approved` flag (which only
            # reflects whether the overall approval-rule threshold has been
            # met). Do not conflate the two: a user who has approved but is
            # still waiting on other approvers must still show "approved"
            # here; gate/threshold sufficiency is decided elsewhere. `state`
            # is therefore always "approved" for every real GitLab response --
            # GitLab's approvals API has no per-user pending/rejected value,
            # unlike GitHub's per-review state. The {"approved","active"}
            # check in select_gitlab_approval() below and any non-"approved"
            # state are unreachable from real data; they exist only for
            # forward compatibility / parity with the GitHub adapter's shape.
            #
            # `decided_at` is the MR-level `updated_at`, not a genuine
            # per-approver timestamp -- GitLab's approvals endpoint does not
            # expose one. `updated_at` changes on any MR update (a later
            # commit push, description edit, another approver acting), so it
            # can misrepresent exactly when this specific approver decided.
            # Evidence consumers should treat this as an approximation, not
            # a precise decision time.
            #
            # `commit_sha` is likewise the MR-level `sha`, applied uniformly
            # to every approver. GitLab's approvals endpoint does not expose
            # a per-approval "commit reviewed" field the way GitHub's
            # per-review `commit_id` does. Correctness of `--commit-sha`
            # filtering in approve-from-gitlab-mr depends on the GitLab
            # project having "reset approvals on push" enabled; otherwise a
            # stale approval against an old commit could remain in
            # `approved_by` and be attributed to the current head SHA. This
            # precondition is not verified by this adapter.
            records.append({
                "approval_id": str(user_id) if user_id is not None else username,
                "username": username,
                "state": "approved",
                "decided_at": raw.get("updated_at"),
                "commit_sha": commit_sha,
            })
    return records


def fetch_gitlab_mr_approvals(project_path: str, mr_iid: int) -> list[dict[str, Any]]:
    mock_path = os.environ.get("AGENTIC_SDLC_TEST_GITLAB_APPROVALS_FILE")
    if mock_path:
        raw_response = json.loads(Path(mock_path).read_text(encoding="utf-8"))
    else:
        encoded_project = quote(project_path, safe="")
        result = subprocess.run(
            ["glab", "api", f"projects/{encoded_project}/merge_requests/{mr_iid}/approvals"],
            text=True,
            capture_output=True,
            check=False,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip() or "unknown glab api failure"
            raise ValueError(f"unable to fetch GitLab MR approvals for {project_path} MR {mr_iid}: {detail}")
        raw_response = json.loads(result.stdout)
    # Both the mocked and real branches feed the raw `glab api` response
    # shape through the same normalizer call, so tests exercising the mock
    # path also exercise the data-minimization wiring (Amendment B).
    payload = gitlab_approval_records_from_api_response(raw_response)
    if not isinstance(payload, list):
        raise ValueError("GitLab approvals response must be a JSON array")
    approvals = [item for item in payload if isinstance(item, dict)]
    if len(approvals) != len(payload):
        raise ValueError("GitLab approvals response contains non-object entries")
    return approvals


def select_gitlab_approval(
    approvals: list[dict[str, Any]], approver_username: str, commit_sha: str | None = None
) -> dict[str, Any]:
    normalized_username = approver_username.lower()
    normalized_commit = normalize_commit_sha(commit_sha)
    matching: list[dict[str, Any]] = []
    for approval in approvals:
        username = approval.get("username")
        decided_at = approval.get("decided_at")
        approval_commit = normalize_commit_sha(approval.get("commit_sha"))
        if not isinstance(username, str) or username.lower() != normalized_username:
            continue
        if not is_valid_datetime(decided_at):
            continue
        if normalized_commit and approval_commit != normalized_commit:
            continue
        matching.append(approval)
    if not matching:
        commit_text = f" at commit {commit_sha}" if commit_sha else ""
        raise ValueError(f"no GitLab approval found for approver {approver_username}{commit_text}")
    matching.sort(key=lambda approval: str(approval.get("decided_at")))
    latest = matching[-1]
    if str(latest.get("state")).lower() not in {"approved", "active"}:
        raise ValueError(f"latest GitLab approval for approver {approver_username} is not an effective approval")
    return latest


def parse_gitlab_issue_uri(value: str) -> dict[str, str] | None:
    match = GITLAB_ISSUE_URI.fullmatch(value)
    if not match:
        return None
    return match.groupdict()


def parse_github_issue_uri(value: str) -> dict[str, str] | None:
    match = GITHUB_ISSUE_URI.fullmatch(value)
    if not match:
        return None
    return match.groupdict()


def fetch_gitlab_issue(project_path: str, issue_iid: int) -> dict[str, Any]:
    """Fetch a single GitLab issue's linkable fields. No author/assignee
    identity is ever read here -- an issue link has no approver concept
    (unlike the MR approval adapter), so there is nothing to minimize away;
    only the fields needed to identify and reference the issue are kept."""
    mock_path = os.environ.get("AGENTIC_SDLC_TEST_GITLAB_ISSUE_FILE")
    if mock_path:
        raw_response = json.loads(Path(mock_path).read_text(encoding="utf-8"))
    else:
        encoded_project = quote(project_path, safe="")
        result = subprocess.run(
            ["glab", "api", f"projects/{encoded_project}/issues/{issue_iid}"],
            text=True,
            capture_output=True,
            check=False,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip() or "unknown glab api failure"
            raise ValueError(f"unable to fetch GitLab issue for {project_path} issue {issue_iid}: {detail}")
        raw_response = json.loads(result.stdout)
    if not isinstance(raw_response, dict):
        raise ValueError("GitLab issue API response must be a JSON object")
    title = raw_response.get("title")
    state = raw_response.get("state")
    if not isinstance(title, str) or not title:
        raise ValueError(f"GitLab issue {project_path}#{issue_iid} response is missing a title")
    if state not in {"opened", "closed"}:
        raise ValueError(f"GitLab issue {project_path}#{issue_iid} response has an unrecognized state: {state!r}")
    return {
        "iid": issue_iid,
        "title": title,
        "state": state,
        "web_url": raw_response.get("web_url"),
        "updated_at": raw_response.get("updated_at"),
    }


def fetch_github_issue(repo: str, issue_number: int) -> dict[str, Any]:
    """Fetch a single GitHub issue's linkable fields. No author/assignee
    identity is ever read here -- an issue link has no approver concept
    (unlike the PR review approval adapter), so there is nothing to minimize
    away; only the fields needed to identify and reference the issue are
    kept. GitHub's issue API uses "open"/"closed" (not GitLab's "opened") for
    state and "html_url" (not "web_url") for the browser link -- both are
    normalized into the same shape fetch_gitlab_issue returns so the shared
    linking code downstream (record_source_issue_link) is forge-agnostic."""
    mock_path = os.environ.get("AGENTIC_SDLC_TEST_GITHUB_ISSUE_FILE")
    if mock_path:
        raw_response = json.loads(Path(mock_path).read_text(encoding="utf-8"))
    else:
        result = subprocess.run(
            ["gh", "api", f"repos/{repo}/issues/{issue_number}"],
            text=True,
            capture_output=True,
            check=False,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip() or "unknown gh api failure"
            raise ValueError(f"unable to fetch GitHub issue for {repo} issue {issue_number}: {detail}")
        raw_response = json.loads(result.stdout)
    if not isinstance(raw_response, dict):
        raise ValueError("GitHub issue API response must be a JSON object")
    title = raw_response.get("title")
    state = raw_response.get("state")
    if not isinstance(title, str) or not title:
        raise ValueError(f"GitHub issue {repo}#{issue_number} response is missing a title")
    if state not in {"open", "closed"}:
        raise ValueError(f"GitHub issue {repo}#{issue_number} response has an unrecognized state: {state!r}")
    return {
        "iid": issue_number,
        "title": title,
        "state": state,
        "web_url": raw_response.get("html_url"),
        "updated_at": raw_response.get("updated_at"),
    }


def is_gate_self_approval(assignee_id: Any, gate_record: dict[str, Any]) -> bool:
    """Pure run-record predicate: is `assignee_id` a preparer or the
    independent verifier of `gate_record`? Shared by `gate_issues.py`
    (gitlab tracking issues) and `gate_reviewers.py` (github reviewer
    report) -- extracted here so both call one implementation instead of
    maintaining duplicate copies. No forge coupling: takes/returns plain
    data, never touches GitLab/GitHub.

    Kernel-side, `preparers` is always `[]` and `independent_verifier` is
    always `None` (`make_gate_record`), so this passes vacuously today --
    implemented anyway since it becomes load-bearing once preparers are
    populated by any other path."""
    preparers = {item.get("id") for item in gate_record.get("preparers", []) if isinstance(item, dict)}
    verifier = gate_record.get("independent_verifier")
    verifier_id = verifier.get("id") if isinstance(verifier, dict) else None
    return assignee_id in preparers or (verifier_id is not None and assignee_id == verifier_id)


def human_requirement_for_gate(gate: dict[str, Any], authority_id: str) -> dict[str, Any] | None:
    for requirement in gate.get("authority_requirements", []):
        if requirement.get("authority_type") == "human-approver" and requirement.get("authority_id") == authority_id:
            return requirement
    return None


def gate_index(gate_id: str) -> int:
    return GATE_IDS.index(gate_id)


def approved_human_approvals(gate: dict[str, Any]) -> list[dict[str, Any]]:
    return [approval for approval in gate.get("human_approvals", []) if approval.get("status") == "approved"]


def has_all_required_human_approvals(gate: dict[str, Any], authorities: dict[str, Any]) -> bool:
    approvals = approved_human_approvals(gate)
    for requirement in gate.get("authority_requirements", []):
        if requirement.get("authority_type") != "human-approver" or requirement.get("applicability") != "applicable":
            continue
        authority_id = requirement.get("authority_id")
        expected_assignee = authorities.get(authority_id, {}).get("assignee")
        if not expected_assignee:
            return False
        if not any(
            isinstance(approval.get("approver"), dict)
            and approval["approver"].get("id") == expected_assignee
            and approval["approver"].get("role") == requirement.get("role")
            for approval in approvals
        ):
            return False
    return True


def can_mark_gate_approved(record: dict[str, Any], gate: dict[str, Any], authorities: dict[str, Any]) -> bool:
    if gate.get("status") not in {"ready", "approved"}:
        return False
    if gate.get("applicability") != "applicable":
        return False
    if not gate.get("artifact_bindings") or not gate.get("evidence_refs"):
        return False
    verifier = gate.get("independent_verifier")
    if not isinstance(verifier, dict):
        return False
    if not gate.get("independence_declaration", {}).get("verifier_confirmed_not_preparer"):
        return False
    gate_position = gate_index(gate["gate_id"])
    for prior in record.get("lifecycle_gates", [])[:gate_position]:
        if prior.get("applicability") != "not-applicable" and prior.get("status") != "approved":
            return False
    return has_all_required_human_approvals(gate, authorities)


def _resolve_gate_authority(
    record: dict[str, Any], authorities: dict[str, Any], gate_id: str, authority_role: str
) -> tuple[dict[str, Any], dict[str, Any], dict[str, Any], str]:
    """Resolve and validate the gate/authority/requirement/assignment eligibility
    shared by every human-approval-recording adapter (record_github_approval,
    record_gitlab_approval, record_gate_decision). Extracted after the third
    near-verbatim copy of this block let one adapter's dedup filter silently
    diverge from the other two -- a shared helper removes the recurring risk,
    not just the duplication. Raises the same ValueError messages every existing
    adapter already raised, so behavior for all three is unchanged."""
    gate = next((item for item in record.get("lifecycle_gates", []) if item.get("gate_id") == gate_id), None)
    if gate is None:
        raise ValueError(f"unknown gate in run record: {gate_id}")
    authority = authorities.get(authority_role)
    if not isinstance(authority, dict):
        raise ValueError(f"unknown authority role: {authority_role}")
    requirement = human_requirement_for_gate(gate, authority_role)
    if requirement is None:
        raise ValueError(f"{gate_id} does not require authority role {authority_role}")
    if requirement.get("applicability") != "applicable":
        raise ValueError(f"{gate_id} authority role {authority_role} is not applicable")
    expected_assignee = authority.get("assignee")
    if authority.get("status") != "assigned" or not expected_assignee:
        raise ValueError(f"authority {authority_role} is not assigned")
    return gate, authority, requirement, expected_assignee


def _replace_approval_entry(
    existing: list[dict[str, Any]], approver_id: str, role_label: str, new_entry: dict[str, Any]
) -> list[dict[str, Any]]:
    """Replace a prior *approved* entry by the same approver id+role with
    new_entry, preserving any other-status history (e.g. a prior rejection's
    own evidence/rationale is never silently dropped) -- matches the semantics
    record_github_approval/record_gitlab_approval already had."""
    remaining = [
        item
        for item in existing
        if not (
            item.get("status") == "approved"
            and isinstance(item.get("approver"), dict)
            and item["approver"].get("id") == approver_id
            and item["approver"].get("role") == role_label
        )
    ]
    remaining.append(new_entry)
    return remaining


def record_github_approval(
    root: Path,
    task_id: str,
    gate_id: str,
    authority_role: str,
    repo: str,
    pr: int,
    review_id: int,
    reviewer_login: str,
    commit_sha: str,
    decided_at: str | None,
) -> dict[str, Any]:
    _, project, authorities, _, _ = load_overlay(root)
    path = confined_path(root, OVERLAY, "runs", task_id, "run-record.json")
    record = load_json(path)
    approval_source_policy(project)
    gate, authority, requirement, expected_assignee = _resolve_gate_authority(record, authorities, gate_id, authority_role)
    expected_login = authority_github_login(authority)
    normalized_reviewer_login = reviewer_login.lower()
    normalized_expected_login = expected_login.lower() if isinstance(expected_login, str) else None
    if normalized_expected_login and normalized_reviewer_login != normalized_expected_login:
        raise ValueError(
            f"GitHub reviewer {reviewer_login} does not match assigned authority login {expected_login}"
        )
    review_uri = f"github-review:{repo}:pull/{pr}:review/{review_id}:reviewer/{normalized_reviewer_login}"
    if parse_github_review_uri(review_uri) is None:
        raise ValueError(f"invalid GitHub review URI components for {review_uri}")
    chosen_time = decided_at or now()
    if not is_valid_datetime(chosen_time):
        raise ValueError("--decided-at must be a valid RFC 3339 date-time")
    role_label = requirement.get("role")
    evidence_payload = {
        "task_id": task_id,
        "gate_id": gate_id,
        "authority_id": authority_role,
        "repo": repo,
        "pull": pr,
        "review_id": review_id,
        "reviewer_login": reviewer_login,
        "decided_at": chosen_time,
        "commit_sha": commit_sha,
    }
    approval = {
        "status": "approved",
        "approver": {"id": expected_assignee, "role": role_label, "kind": "human"},
        "decided_at": chosen_time,
        "evidence_refs": [{
            "evidence_id": f"{gate_id.lower()}-{authority_role}-github-review-{review_id}",
            "uri": review_uri,
            "hash_algorithm": "sha256",
            "hash": fingerprint(evidence_payload).removeprefix("sha256:"),
            "classification": record.get("classification", project.get("classification", "internal")),
        }],
    }
    gate["human_approvals"] = _replace_approval_entry(gate.get("human_approvals", []), expected_assignee, role_label, approval)
    if can_mark_gate_approved(record, gate, authorities):
        gate["status"] = "approved"
        gate["decided_at"] = max(
            [approval_item.get("decided_at") for approval_item in approved_human_approvals(gate) if approval_item.get("decided_at")] or [chosen_time]
        )
        record["current_lifecycle_phase"] = derive_current_phase(record)
    write_json(path, record)
    return {
        "task_id": task_id,
        "gate_id": gate_id,
        "authority_id": authority_role,
        "review_uri": review_uri,
        "gate_status": gate.get("status"),
        "current_phase": derive_current_phase(record),
    }


def record_gitlab_approval(
    root: Path,
    task_id: str,
    gate_id: str,
    authority_role: str,
    project_path: str,
    mr_iid: int,
    approval_id: str,
    approver_username: str,
    commit_sha: str,
    decided_at: str | None,
) -> dict[str, Any]:
    """Record GitLab MR approval evidence. This is a trusted-API-attestation
    adapter with the same trust level as record_github_approval: it treats
    GitLab's own approval state as authoritative and does not attempt
    independent signing or non-repudiation beyond that. Per the data
    minimization amendment, only the pseudonymous approver username is ever
    persisted in the evidence record or URI -- never name, email, or
    avatar_url."""
    _, project, authorities, _, _ = load_overlay(root)
    path = confined_path(root, OVERLAY, "runs", task_id, "run-record.json")
    record = load_json(path)
    approval_source_policy(project)
    gate, authority, requirement, expected_assignee = _resolve_gate_authority(record, authorities, gate_id, authority_role)
    expected_username = authority_gitlab_username(authority)
    normalized_approver_username = approver_username.lower()
    normalized_expected_username = expected_username.lower() if isinstance(expected_username, str) else None
    if normalized_expected_username and normalized_approver_username != normalized_expected_username:
        raise ValueError(
            f"GitLab approver {approver_username} does not match assigned authority username {expected_username}"
        )
    approval_uri = f"gitlab-mr:{project_path}:merge_requests/{mr_iid}:approval/{approval_id}:approver/{normalized_approver_username}"
    if parse_gitlab_mr_uri(approval_uri) is None:
        raise ValueError(f"invalid GitLab MR approval URI components for {approval_uri}")
    chosen_time = decided_at or now()
    if not is_valid_datetime(chosen_time):
        raise ValueError("--decided-at must be a valid RFC 3339 date-time")
    role_label = requirement.get("role")
    evidence_payload = {
        "task_id": task_id,
        "gate_id": gate_id,
        "authority_id": authority_role,
        "project_path": project_path,
        "merge_request_iid": mr_iid,
        "approval_id": approval_id,
        "approver_username": normalized_approver_username,
        "decided_at": chosen_time,
        "commit_sha": commit_sha,
    }
    approval = {
        "status": "approved",
        "approver": {"id": expected_assignee, "role": role_label, "kind": "human"},
        "decided_at": chosen_time,
        "evidence_refs": [{
            "evidence_id": f"{gate_id.lower()}-{authority_role}-gitlab-mr-{approval_id}",
            "uri": approval_uri,
            "hash_algorithm": "sha256",
            "hash": fingerprint(evidence_payload).removeprefix("sha256:"),
            "classification": record.get("classification", project.get("classification", "internal")),
        }],
    }
    gate["human_approvals"] = _replace_approval_entry(gate.get("human_approvals", []), expected_assignee, role_label, approval)
    if can_mark_gate_approved(record, gate, authorities):
        gate["status"] = "approved"
        gate["decided_at"] = max(
            [approval_item.get("decided_at") for approval_item in approved_human_approvals(gate) if approval_item.get("decided_at")] or [chosen_time]
        )
        record["current_lifecycle_phase"] = derive_current_phase(record)
    write_json(path, record)
    return {
        "task_id": task_id,
        "gate_id": gate_id,
        "authority_id": authority_role,
        "approval_uri": approval_uri,
        "gate_status": gate.get("status"),
        "current_phase": derive_current_phase(record),
    }


def record_gate_decision(
    root: Path,
    task_id: str,
    gate_id: str,
    authority_role: str,
    decision: str,
    actor_id: str,
    evidence_uri: str,
    note: str | None,
    decided_at: str | None,
) -> dict[str, Any]:
    """Record a platform-agnostic human decision (approved/rejected/request-changes)
    for a gate. Unlike record_github_approval/record_gitlab_approval, the caller's
    identity is asserted directly rather than derived from a platform review
    payload, so the actor is required to match the assigned authority exactly, and
    self-approval is refused synchronously against gate.preparers/independent_verifier
    rather than relying solely on a later validate_repository() pass. An "approved"
    decision is also checked synchronously against the project's approval_sources
    policy (the same check validate_repository() performs, but here at write time):
    a project that requires github-review/gitlab-mr sourcing with no manual
    fallback cannot be satisfied by an arbitrary --evidence-uri string."""
    if decision not in {"approved", "rejected", "request-changes"}:
        raise ValueError(f"unknown decision: {decision}")
    _, project, authorities, _, _ = load_overlay(root)
    path = confined_path(root, OVERLAY, "runs", task_id, "run-record.json")
    record = load_json(path)
    approval_policy = approval_source_policy(project)
    gate, authority, requirement, expected_assignee = _resolve_gate_authority(record, authorities, gate_id, authority_role)
    if actor_id != expected_assignee:
        raise ValueError(f"actor {actor_id} does not match assigned authority {expected_assignee} for role {authority_role}")
    preparers = {item.get("id") for item in gate.get("preparers", []) if isinstance(item, dict)}
    verifier = gate.get("independent_verifier")
    verifier_id = verifier.get("id") if isinstance(verifier, dict) else None
    if actor_id in preparers:
        raise ValueError(f"{authority_role} authority {actor_id} is a preparer for {gate_id}; cannot decide on own work")
    if verifier_id and actor_id == verifier_id:
        raise ValueError(f"{authority_role} authority {actor_id} is the independent verifier for {gate_id}; cannot also decide")
    if not evidence_uri:
        raise ValueError("--evidence-uri is required")
    if decision == "approved":
        if (
            approval_policy["human_gate_default"] == "github-review"
            and not approval_policy["allow_manual_fallback"]
            and (not evidence_uri.startswith("github-review:") or parse_github_review_uri(evidence_uri) is None)
        ):
            raise ValueError(f"{gate_id} approval must be backed by a GitHub review (project approval_sources requires github-review)")
        if (
            approval_policy["human_gate_default"] == "gitlab-mr"
            and not approval_policy["allow_manual_fallback"]
            and (not evidence_uri.startswith("gitlab-mr:") or parse_gitlab_mr_uri(evidence_uri) is None)
        ):
            raise ValueError(f"{gate_id} approval must be backed by a GitLab MR approval (project approval_sources requires gitlab-mr)")
    chosen_time = decided_at or now()
    if not is_valid_datetime(chosen_time):
        raise ValueError("--decided-at must be a valid RFC 3339 date-time")
    role_label = requirement.get("role")
    evidence_payload = {
        "task_id": task_id,
        "gate_id": gate_id,
        "authority_id": authority_role,
        "decision": decision,
        "actor_id": actor_id,
        "evidence_uri": evidence_uri,
        "decided_at": chosen_time,
    }
    approval_status = "approved" if decision == "approved" else "rejected"
    approval: dict[str, Any] = {
        "status": approval_status,
        "approver": {"id": actor_id, "role": role_label, "kind": "human"},
        "decided_at": chosen_time,
        "evidence_refs": [{
            "evidence_id": f"{gate_id.lower()}-{authority_role}-decide-{fingerprint(evidence_payload).removeprefix('sha256:')[:12]}",
            "uri": evidence_uri,
            "hash_algorithm": "sha256",
            "hash": fingerprint(evidence_payload).removeprefix("sha256:"),
            "classification": record.get("classification", project.get("classification", "internal")),
        }],
    }
    if note:
        approval["note"] = note
    gate["human_approvals"] = _replace_approval_entry(gate.get("human_approvals", []), actor_id, role_label, approval)
    if decision != "approved" and gate.get("status") == "approved":
        gate["status"] = "pending"
    if decision == "approved" and can_mark_gate_approved(record, gate, authorities):
        gate["status"] = "approved"
        gate["decided_at"] = max(
            [approval_item.get("decided_at") for approval_item in approved_human_approvals(gate) if approval_item.get("decided_at")] or [chosen_time]
        )
        record["current_lifecycle_phase"] = derive_current_phase(record)
    elif decision == "request-changes":
        gate["status"] = "request-changes"
        gate["decided_at"] = chosen_time
        record["current_lifecycle_phase"] = derive_current_phase(record)
    write_json(path, record)
    return {
        "task_id": task_id,
        "gate_id": gate_id,
        "authority_id": authority_role,
        "decision": decision,
        "actor_id": actor_id,
        "gate_status": gate.get("status"),
        "current_phase": derive_current_phase(record),
    }


RECORD_FIELD_BY_GATE = {"G1": "intent_record_id", "G2": "requirements_baseline_id"}


def record_source_issue_link(
    root: Path,
    task_id: str,
    gate_id: str,
    authority_role: str,
    issue_uri: str,
    issue: dict[str, Any],
    *,
    parse_uri: Callable[[str], dict[str, str] | None],
    source_label: str,
    evidence_id_infix: str,
    evidence_payload: dict[str, Any],
) -> dict[str, Any]:
    """Shared implementation behind record_gitlab_issue_link and
    record_github_issue_link: link a forge issue as the recorded source for
    a G1/G2 gate's contribution. Deliberately does not touch human_approvals
    or gate status -- a source link is not an approval; see
    GITLAB_ISSUE_URI/GITHUB_ISSUE_URI's module-level comments. Authorization
    mirrors the approval adapters (only an assigned, applicable authority
    for the gate may attach a link), but nothing here can mark a gate
    approved. Every check and mutation below is identical for both forges;
    only the URI scheme/shape, error wording, and evidence payload differ,
    which is why those are the only parameters callers vary."""
    record_field = RECORD_FIELD_BY_GATE.get(gate_id)
    if record_field is None:
        raise ValueError(f"gate {gate_id} does not accept a {source_label} source link")
    _, project, authorities, _, _ = load_overlay(root)
    path = confined_path(root, OVERLAY, "runs", task_id, "run-record.json")
    record = load_json(path)
    gate = next((item for item in record.get("lifecycle_gates", []) if item.get("gate_id") == gate_id), None)
    if gate is None:
        raise ValueError(f"unknown gate in run record: {gate_id}")
    authority = authorities.get(authority_role)
    if not isinstance(authority, dict):
        raise ValueError(f"unknown authority role: {authority_role}")
    requirement = human_requirement_for_gate(gate, authority_role)
    if requirement is None:
        raise ValueError(f"{gate_id} does not require authority role {authority_role}")
    if requirement.get("applicability") != "applicable":
        raise ValueError(f"{gate_id} authority role {authority_role} is not applicable")
    if authority.get("status") != "assigned" or not authority.get("assignee"):
        raise ValueError(f"authority {authority_role} is not assigned")
    if parse_uri(issue_uri) is None:
        raise ValueError(f"invalid {source_label} URI components for {issue_uri}")
    evidence_id_prefix = f"{gate_id.lower()}-source-{evidence_id_infix}-"
    evidence_entry = {
        "evidence_id": f"{evidence_id_prefix}{issue['iid']}",
        "uri": issue_uri,
        "hash_algorithm": "sha256",
        "hash": fingerprint(evidence_payload).removeprefix("sha256:"),
        "classification": record.get("classification", project.get("classification", "internal")),
    }
    # Filter by prefix, not exact evidence_id: record[record_field] holds
    # exactly one URI at a time, so relinking a *different* issue must
    # still replace (not accumulate alongside) the prior source-link
    # evidence for this gate, not just an exact re-link of the same issue.
    remaining = [
        item for item in gate.get("evidence_refs", [])
        if not str(item.get("evidence_id", "")).startswith(evidence_id_prefix)
    ]
    remaining.append(evidence_entry)
    gate["evidence_refs"] = remaining
    record[record_field] = issue_uri
    write_json(path, record)
    return {
        "task_id": task_id,
        "gate_id": gate_id,
        "record_field": record_field,
        "issue_uri": issue_uri,
        "issue_title": issue["title"],
        "issue_state": issue["state"],
    }


def record_gitlab_issue_link(
    root: Path,
    task_id: str,
    gate_id: str,
    authority_role: str,
    project_path: str,
    issue: dict[str, Any],
) -> dict[str, Any]:
    """Link a GitLab issue as the recorded source for a G1/G2 gate's
    contribution. See record_source_issue_link's docstring for the full
    behavioral contract this delegates to."""
    issue_uri = f"gitlab-issue:{project_path}:issues/{issue['iid']}"
    evidence_payload = {
        "task_id": task_id,
        "gate_id": gate_id,
        "authority_id": authority_role,
        "project_path": project_path,
        "issue_iid": issue["iid"],
        "title": issue["title"],
        "state": issue["state"],
        "web_url": issue.get("web_url"),
    }
    return record_source_issue_link(
        root, task_id, gate_id, authority_role, issue_uri, issue,
        parse_uri=parse_gitlab_issue_uri,
        source_label="GitLab issue",
        evidence_id_infix="gitlab-issue",
        evidence_payload=evidence_payload,
    )


def record_github_issue_link(
    root: Path,
    task_id: str,
    gate_id: str,
    authority_role: str,
    repo: str,
    issue: dict[str, Any],
) -> dict[str, Any]:
    """Link a GitHub issue as the recorded source for a G1/G2 gate's
    contribution -- the GitHub counterpart to record_gitlab_issue_link. See
    record_source_issue_link's docstring for the full behavioral contract
    this delegates to."""
    issue_uri = f"github-issue:{repo}:issues/{issue['iid']}"
    evidence_payload = {
        "task_id": task_id,
        "gate_id": gate_id,
        "authority_id": authority_role,
        "repo": repo,
        "issue_number": issue["iid"],
        "title": issue["title"],
        "state": issue["state"],
        "web_url": issue.get("web_url"),
    }
    return record_source_issue_link(
        root, task_id, gate_id, authority_role, issue_uri, issue,
        parse_uri=parse_github_issue_uri,
        source_label="GitHub issue",
        evidence_id_infix="github-issue",
        evidence_payload=evidence_payload,
    )


def load_json(path: Path) -> dict[str, Any]:
    with path.open(encoding="utf-8") as handle:
        value = json.load(handle)
    if not isinstance(value, dict):
        raise ValueError(f"{path} must contain a JSON object")
    return value


def write_json(path: Path, value: Any, *, overwrite: bool = True) -> bool:
    if path.exists() and not overwrite:
        return False
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")
    return True


def confined_path(root: Path, *parts: str) -> Path:
    """Resolve a project path and reject symlinks/junctions that escape the root."""
    resolved_root = root.resolve()
    candidate = resolved_root.joinpath(*parts)
    resolved_candidate = candidate.resolve(strict=False)
    if resolved_candidate != resolved_root and resolved_root not in resolved_candidate.parents:
        raise ValueError(f"project path escapes root: {candidate}")
    return candidate


class RepairFilesystem:
    """Descriptor-confined I/O for the mutable surface of ``repair``.

    Planning with ``Path.resolve`` followed by ordinary path I/O leaves a gap:
    an attacker can exchange a checked directory or file for a symlink before a
    repair writes it.  Repair therefore pins the project root and each parent
    directory with file descriptors, refuses symlinks at every component, and
    writes replacement files through a temporary file in the pinned directory.
    """

    def __init__(self, root: Path) -> None:
        if not hasattr(os, "O_NOFOLLOW") or not hasattr(os, "O_DIRECTORY"):
            raise ValueError("secure repair I/O requires O_NOFOLLOW and O_DIRECTORY support")
        self.root = root.absolute()
        try:
            self._root_fd = self._open_root_fd(self.root)
        except OSError as error:
            raise ValueError(f"cannot securely open project root: {error}") from error

    def close(self) -> None:
        os.close(self._root_fd)

    @staticmethod
    def _component(value: str) -> str:
        if not value or value in {".", ".."} or "/" in value or "\\" in value:
            raise ValueError(f"unsafe repair path component: {value!r}")
        return value

    @classmethod
    def _open_root_fd(cls, root: Path) -> int:
        """Pin every supplied absolute-root component without following it."""
        if not root.is_absolute():
            raise ValueError("secure repair root must be absolute")
        fd = os.open("/", os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            for part in root.parts[1:]:
                next_fd = os.open(
                    cls._component(part),
                    os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW,
                    dir_fd=fd,
                )
                os.close(fd)
                fd = next_fd
            return fd
        except Exception:
            os.close(fd)
            raise

    def _directory_fd(self, parts: tuple[str, ...], *, create: bool = False) -> int:
        fd = os.dup(self._root_fd)
        try:
            for part in parts:
                name = self._component(part)
                try:
                    next_fd = os.open(
                        name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd
                    )
                except FileNotFoundError:
                    if not create:
                        raise
                    os.mkdir(name, dir_fd=fd)
                    next_fd = os.open(
                        name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd
                    )
                except OSError as error:
                    raise ValueError(
                        f"unsafe repair directory component {'/'.join(parts)}: {error}"
                    ) from error
                os.close(fd)
                fd = next_fd
            return fd
        except Exception:
            os.close(fd)
            raise

    def file_state(self, parts: tuple[str, ...]) -> str:
        if not parts:
            raise ValueError("repair file path is empty")
        try:
            parent = self._directory_fd(parts[:-1])
        except FileNotFoundError:
            return "missing"
        try:
            try:
                metadata = os.stat(self._component(parts[-1]), dir_fd=parent, follow_symlinks=False)
            except FileNotFoundError:
                return "missing"
            if stat.S_ISLNK(metadata.st_mode):
                raise ValueError(f"unsafe repair symlink: {'/'.join(parts)}")
            if not stat.S_ISREG(metadata.st_mode):
                raise ValueError(f"repair path is not a regular file: {'/'.join(parts)}")
            return "regular"
        finally:
            os.close(parent)

    def read_text(self, parts: tuple[str, ...]) -> str:
        if self.file_state(parts) != "regular":
            raise ValueError(f"missing repair file: {'/'.join(parts)}")
        parent = self._directory_fd(parts[:-1])
        try:
            try:
                descriptor = os.open(self._component(parts[-1]), os.O_RDONLY | os.O_NOFOLLOW, dir_fd=parent)
            except OSError as error:
                raise ValueError(f"cannot securely read {'/'.join(parts)}: {error}") from error
            try:
                if not stat.S_ISREG(os.fstat(descriptor).st_mode):
                    raise ValueError(f"repair path is not a regular file: {'/'.join(parts)}")
                with os.fdopen(descriptor, "r", encoding="utf-8") as handle:
                    descriptor = -1
                    return handle.read()
            finally:
                if descriptor >= 0:
                    os.close(descriptor)
        finally:
            os.close(parent)

    def write_text(self, parts: tuple[str, ...], content: str, *, overwrite: bool) -> bool:
        if not parts:
            raise ValueError("repair file path is empty")
        parent = self._directory_fd(parts[:-1], create=True)
        temp_name = ""
        try:
            name = self._component(parts[-1])
            state = self.file_state(parts)
            if state == "regular" and not overwrite:
                return False
            for _attempt in range(20):
                candidate = f".agentic-sdlc-repair-{os.getpid()}-{secrets.token_hex(8)}"
                try:
                    descriptor = os.open(
                        candidate,
                        os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW,
                        0o600,
                        dir_fd=parent,
                    )
                except FileExistsError:
                    continue
                temp_name = candidate
                try:
                    with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
                        descriptor = -1
                        handle.write(content)
                        handle.flush()
                        os.fsync(handle.fileno())
                finally:
                    if descriptor >= 0:
                        os.close(descriptor)
                break
            else:
                raise ValueError("could not create a secure temporary repair file")
            if overwrite:
                # rename replaces a raced-in symlink itself; it never follows it.
                os.replace(temp_name, name, src_dir_fd=parent, dst_dir_fd=parent)
            else:
                # ``link`` is an atomic no-clobber installation in this pinned
                # directory.  Unlike rename, it fails if a decision appeared
                # after planning, so repair cannot overwrite it.
                os.link(temp_name, name, src_dir_fd=parent, dst_dir_fd=parent, follow_symlinks=False)
                os.unlink(temp_name, dir_fd=parent)
            temp_name = ""
            return True
        finally:
            if temp_name:
                try:
                    os.unlink(temp_name, dir_fd=parent)
                except FileNotFoundError:
                    pass
            os.close(parent)


def fingerprint(value: Any) -> str:
    canonical = json.dumps(value, sort_keys=True, separators=(",", ":")).encode("utf-8")
    return "sha256:" + hashlib.sha256(canonical).hexdigest()


def dispatch_fingerprint(dispatch: dict[str, Any]) -> str:
    """Bind every decision-relevant dispatch field, excluding only generated metadata."""
    payload = {
        key: value
        for key, value in dispatch.items()
        if key not in {"generated_at", "dispatch_fingerprint"}
    }
    return fingerprint(payload)


def unique(values: list[str]) -> list[str]:
    return list(dict.fromkeys(values))


def safe_task_id(value: str) -> str:
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", value) or value in {".", ".."}:
        raise ValueError("task ID must already be a portable, non-lossy ID using only letters, numbers, dot, underscore, or hyphen")
    return value


def detect_repository(root: Path) -> dict[str, Any]:
    signatures = {
        "python": ["pyproject.toml", "requirements.txt", "setup.py"],
        "node": ["package.json", "pnpm-lock.yaml", "yarn.lock"],
        "go": ["go.mod"],
        "rust": ["Cargo.toml"],
        "java": ["pom.xml", "build.gradle", "build.gradle.kts"],
        "dotnet": ["*.sln", "*.csproj"],
        "terraform": ["*.tf"],
        "containers": ["Dockerfile", "compose.yaml", "docker-compose.yml"],
    }
    names = [path.name for path in root.iterdir()] if root.exists() else []
    stacks = [name for name, patterns in signatures.items() if any(any(fnmatch.fnmatch(item, pattern) for item in names) for pattern in patterns)]
    web_markers = {"package.json", "go.mod", "requirements.txt", "pyproject.toml"}
    directories = {path.name for path in root.iterdir() if path.is_dir()} if root.exists() else set()
    service_layout = bool(web_markers.intersection(names) and {"src", "app", "api", "cmd", "internal"}.intersection(directories))
    multi_tier_markers = "package.json" in names and ("go.mod" in names or "requirements.txt" in names or "pyproject.toml" in names)
    proposed = "web-service" if service_layout or multi_tier_markers else "quick"
    commands: dict[str, list[str]] = {}
    if "python" in stacks:
        commands["test"] = [sys.executable, "-m", "unittest", "discover"]
    if "node" in stacks:
        commands["test_candidate"] = ["use-project-pinned-package-manager", "test"]
    if "go" in stacks:
        commands["test"] = ["go", "test", "./..."]
        commands["static_analysis"] = ["go", "vet", "./..."]
    return {
        "root": str(root.resolve()),
        "detected_stacks": stacks,
        "directories": sorted(directories.intersection({"src", "app", "api", "cmd", "internal", "infra", "deploy"})),
        "proposed_profile": proposed,
        "command_candidates": commands,
        "warnings": ["Detection never assigns human authority or compliance applicability."],
    }


def merge_profile(profile_id: str) -> dict[str, Any]:
    candidates = [
        root / profile_id / "profile.json"
        for root in PROFILE_SEARCH_PATH
        if (root / profile_id / "profile.json").is_file()
    ]
    if not candidates:
        raise ValueError(f"unknown profile: {profile_id}")
    if len(candidates) > 1:
        raise ValueError(f"duplicate profile: {profile_id}")
    path = candidates[0]
    child = load_json(path)
    if child.get("id") != profile_id or not isinstance(child.get("version"), str):
        raise ValueError(f"profile {profile_id} has malformed metadata; id and version are required")
    if not isinstance(child.get("gate_bindings"), dict):
        raise ValueError(f"profile {profile_id} must define gate_bindings")
    parent_id = child.get("extends")
    if not parent_id:
        result = child
    else:
        parent = merge_profile(str(parent_id))
        result = dict(parent)
        result.update({key: value for key, value in child.items() if key not in {"agents", "routing", "gate_bindings"}})
        result["agents"] = unique(list(parent.get("agents", [])) + list(child.get("agents", [])))
        result["routing"] = list(parent.get("routing", [])) + list(child.get("routing", []))
        merged_bindings = dict(parent.get("gate_bindings", {}))
        for gate_id, binding in child.get("gate_bindings", {}).items():
            merged_bindings[gate_id] = binding
        result["gate_bindings"] = merged_bindings
    result["id"] = profile_id
    result.setdefault("gate_bindings", {})
    known_gates = set(GATE_IDS)
    unknown_gates = set(result["gate_bindings"]) - known_gates
    if unknown_gates:
        raise ValueError(f"profile {profile_id} references unknown lifecycle gates: {sorted(unknown_gates)}")
    known_slots = {slot for gate in lifecycle_contract()["gates"] for slot in gate.get("required_contributions", [])}
    for binding in result["gate_bindings"].values():
        if not isinstance(binding, dict) or not isinstance(binding.get("contributions"), dict):
            raise ValueError(f"profile {profile_id} has malformed gate contribution binding")
        unknown_slots = set(binding.get("contributions", {})) - known_slots
        if unknown_slots:
            raise ValueError(f"profile {profile_id} references unknown contribution slots: {sorted(unknown_slots)}")
        known_agents = set(load_agent_catalog())
        for contribution in binding.get("contributions", {}).values():
            if not isinstance(contribution, dict) or any(not isinstance(contribution.get(field), list) for field in ("agents", "tasks", "artifacts")):
                raise ValueError(f"profile {profile_id} has malformed contribution metadata")
        unknown_agents = {agent for contribution in binding.get("contributions", {}).values() for agent in contribution.get("agents", [])} - known_agents
        if unknown_agents:
            raise ValueError(f"profile {profile_id} references unknown agents: {sorted(unknown_agents)}")
    result["agents"] = unique(list(result.get("agents", [])) + [
        agent for binding in result["gate_bindings"].values()
        for contribution in binding.get("contributions", {}).values()
        for agent in contribution.get("agents", [])
    ])
    for route in result.get("routing", []):
        referenced = set(route.get("agents", [])) | set(route.get("reviewers", [])) | set(route.get("support", []))
        unknown = referenced - set(load_agent_catalog())
        if unknown:
            raise ValueError(f"profile {profile_id} route {route.get('id')} references unknown agents: {sorted(unknown)}")
    return result


def managed_agents_block() -> str:
    return "\n".join([
        MANAGED_START,
        "## Agentic SDLC",
        "",
        "This repository uses the portable Agentic SDLC project overlay in `.agentic-sdlc/`.",
        "Use its orchestration skill or CLI for multi-role delivery work. Run records are authoritative.",
        "Never infer gate approval, production/destructive authority, risk acceptance, or compliance applicability.",
        "Artifact authors must remain separate from independent reviewers and human approvers.",
        MANAGED_END,
    ])


def update_agents_md(root: Path) -> None:
    path = confined_path(root, "AGENTS.md")
    existing = path.read_text(encoding="utf-8") if path.exists() else ""
    path.write_text(rendered_agents_md(existing), encoding="utf-8")


def rendered_agents_md(existing: str) -> str:
    """Return the stable AGENTS.md content after updating our managed block."""
    block = managed_agents_block()
    if (MANAGED_START in existing) != (MANAGED_END in existing):
        raise ValueError("AGENTS.md contains an incomplete Agentic SDLC managed block")
    if MANAGED_START in existing and MANAGED_END in existing:
        before, remainder = existing.split(MANAGED_START, 1)
        _, after = remainder.split(MANAGED_END, 1)
        prefix = before.rstrip()
        suffix = after.lstrip("\n")
        content = (prefix + "\n\n" if prefix else "") + block + ("\n" + suffix if suffix else "\n")
    else:
        content = existing.rstrip() + ("\n\n" if existing.strip() else "") + block + "\n"
    return content


def toml_string(value: str) -> str:
    return json.dumps(value)


ASK_HUMAN_RULE = (
    "You are a dispatched subagent: you cannot ask the human directly. If you reach a "
    "decision only a human can make, stop and return a clearly labeled blocking question "
    "in your result instead of guessing or proceeding."
)

RICH_CONTENT_ADAPTATION_NOTE = (
    "Adapted from a cloud/GitLab-specific role definition bundled with secure-cloud-agents. "
    "Its shared-policy references (agents/shared/*.md paths) belong to that source "
    "repository and will not resolve here — review and tailor this role for this "
    "project's own stack, policies, and gates before relying on it."
)


def agent_wrapper_instructions(agent_id: str, reviewer: bool) -> str:
    return (
        f"Act as the portable Agentic SDLC role {agent_id}. "
        "Bind work to the task revision and lifecycle gate. "
        "Never approve a lifecycle or mutation gate. "
        + ("Remain independent and do not modify the artifact under review." if reviewer else "Prepare artifacts for independent review; do not self-review.")
        + " " + ASK_HUMAN_RULE
    )


def load_agent_catalog() -> dict[str, Any]:
    merged: dict[str, Any] = {}
    for path in AGENT_CATALOG_SEARCH_PATH:
        if path.exists():
            catalog = load_json(path)["agents"]
            for metadata in catalog.values():
                definition = metadata.get("definition")
                if isinstance(definition, str) and not Path(definition).is_absolute():
                    resolved_definition = (path.parent / definition).resolve()
                    if not resolved_definition.is_relative_to(path.parent.resolve()):
                        raise ValueError(f"agent definition escapes provider root: {definition}")
                    metadata["definition"] = str(resolved_definition)
            merged.update(catalog)
    return merged


def rich_agent_content(metadata: dict[str, Any]) -> str | None:
    definition = metadata.get("definition")
    if not definition:
        return None
    path = Path(definition)
    return path.read_text(encoding="utf-8").strip() if path.is_file() else None


def agent_wrapper_body(agent_id: str, reviewer: bool, metadata: dict[str, Any], profile: dict[str, Any]) -> str:
    if profile.get("rich_content_source"):
        rich = rich_agent_content(metadata)
        if rich is not None:
            return "\n\n".join([rich, RICH_CONTENT_ADAPTATION_NOTE, ASK_HUMAN_RULE])
    return agent_wrapper_instructions(agent_id, reviewer)


def write_codex_agent_wrappers(
    root: Path, profile: dict[str, Any], catalog: dict[str, Any], dry_run: bool = False
) -> tuple[list[str], list[str]]:
    created: list[str] = []
    existing: list[str] = []
    wrapper_dir = confined_path(root, ".codex", "agents")
    if not dry_run:
        wrapper_dir.mkdir(parents=True, exist_ok=True)
    for agent_id in profile.get("agents", []):
        metadata = catalog.get(agent_id)
        if not metadata:
            continue
        target = wrapper_dir / f"{agent_id}.toml"
        if target.exists():
            existing.append(str(target.relative_to(root)))
            continue
        reviewer = metadata["kind"] == "reviewer"
        content = "\n".join([
            f"name = {toml_string(agent_id)}",
            f"description = {toml_string('Portable Agentic SDLC ' + metadata.get('kind', 'specialist') + ' for ' + metadata.get('phase', 'lifecycle'))}",
            f"sandbox_mode = {toml_string('read-only' if reviewer else 'workspace-write')}",
            f"developer_instructions = {toml_string(agent_wrapper_body(agent_id, reviewer, metadata, profile))}",
            "",
        ])
        if not dry_run:
            target.write_text(content, encoding="utf-8")
        created.append(str(target.relative_to(root)))
    return created, existing


def write_claude_agent_wrappers(
    root: Path, profile: dict[str, Any], catalog: dict[str, Any], dry_run: bool = False
) -> tuple[list[str], list[str]]:
    created: list[str] = []
    existing: list[str] = []
    wrapper_dir = confined_path(root, ".claude", "agents")
    if not dry_run:
        wrapper_dir.mkdir(parents=True, exist_ok=True)
    for agent_id in profile.get("agents", []):
        metadata = catalog.get(agent_id)
        if not metadata:
            continue
        target = wrapper_dir / f"{agent_id}.md"
        if target.exists():
            existing.append(str(target.relative_to(root)))
            continue
        reviewer = metadata["kind"] == "reviewer"
        description = "Portable Agentic SDLC " + metadata.get("kind", "specialist") + " for " + metadata.get("phase", "lifecycle")
        frontmatter = "\n".join([
            "---",
            f"name: {agent_id}",
            f"description: {description}",
            f"tools: {'Read, Grep, Glob, Bash' if reviewer else 'Read, Grep, Glob, Bash, Edit, Write'}",
            "---",
            "",
        ])
        if not dry_run:
            target.write_text(frontmatter + agent_wrapper_body(agent_id, reviewer, metadata, profile) + "\n", encoding="utf-8")
        created.append(str(target.relative_to(root)))
    return created, existing


def write_agent_wrappers(
    root: Path, profile: dict[str, Any], runner: str = "both", dry_run: bool = False
) -> tuple[list[str], list[str]]:
    catalog = load_agent_catalog()
    created: list[str] = []
    existing: list[str] = []
    if runner in ("codex", "both"):
        wrapper_created, wrapper_existing = write_codex_agent_wrappers(root, profile, catalog, dry_run)
        created.extend(wrapper_created)
        existing.extend(wrapper_existing)
    if runner in ("claude", "both"):
        wrapper_created, wrapper_existing = write_claude_agent_wrappers(root, profile, catalog, dry_run)
        created.extend(wrapper_created)
        existing.extend(wrapper_existing)
    return created, existing


def impact_item(item_id: str, extension: str) -> dict[str, Any]:
    return {
        "id": item_id,
        "extension": extension,
        "applicability": "unknown",
        "definition_reference": None,
        "rationale": None,
        "owner": None,
        "evidence_refs": [],
    }


def initialization_artifacts(
    root: Path,
    profile_id: str | None,
    extension_ids: list[str],
    project_id: str,
    classification: str,
    detected: dict[str, Any] | None = None,
) -> tuple[dict[str, Any], list[tuple[str, dict[str, Any]]], dict[str, Any]]:
    """Build the conservative bootstrap artifacts without writing them.

    Both ``init`` and ``repair`` use this one builder.  Keeping creation and
    repair on the same representation prevents a repair from becoming a
    second, subtly different initializer.
    """
    detected = detect_repository(root) if detected is None else detected
    profile = merge_profile(profile_id) if profile_id else {
        "id": "kernel-only", "routing": [], "ignored_gates": [],
        "gate_bindings": [], "impact_categories": [],
    }
    impact = [impact_item(item_id, "generic-software") for item_id in profile.get("impact_categories", [])]
    specialized_boms: list[dict[str, Any]] = []
    for extension_id in extension_ids:
        extension_path = next(
            (candidate for path in EXTENSIONS_SEARCH_PATH if (candidate := path / extension_id / "extension.json").exists()),
            None,
        )
        if extension_path is None:
            raise ValueError(f"unknown extension: {extension_id}")
        extension = load_json(extension_path)
        if extension.get("schema_version") != 1 or extension.get("id") != extension_id or not isinstance(extension.get("version"), str):
            raise ValueError(f"extension {extension_id} has malformed metadata")
        impact.extend(impact_item(item_id, extension_id) for item_id in extension.get("impact_categories", []))
        specialized_boms.extend(impact_item(bom, extension_id) for bom in extension.get("specialized_boms", []))
    project = {
        "schema_version": 1, "project_id": project_id,
        "classification": classification, "profile": profile_id,
        "profile_digest": fingerprint(profile), "extensions": extension_ids,
        "providers": [item["id"] for item in LOADED_PROVIDERS],
        "approval_sources": {"human_gate_default": "manual", "allow_manual_fallback": True},
        "detected": detected,
        "environments": [{"name": "local", "persistence": "unknown", "production": "unknown"}],
    }
    authorities = {
        role: {
            "status": "unknown", "assignee": None,
            "applicability": "unknown" if role in CONDITIONAL_AUTHORITY_ROLES else "applicable",
            "rationale": None, "evidence_reference": None, "gates": gates,
        }
        for role, gates in AUTHORITY_ROLES.items()
    }
    impact_profile = {
        "profile_id": f"{project_id}-impact", "status": "blocked",
        "impact_categories": impact, "specialized_boms": specialized_boms,
        "blocking_unknowns": [item["id"] for item in impact + specialized_boms],
    }
    routing = {
        "version": 1, "profile": profile_id, "routes": profile.get("routing", []),
        "change_intake": profile.get("change_intake", {}),
        "ignored_gates": profile.get("ignored_gates", []),
        "gate_bindings": profile.get("gate_bindings", {}),
    }
    commands = {"version": 1, "commands": detected["command_candidates"], "confirmed": False}
    return profile, [
        ("project.json", project), ("authorities.json", authorities),
        ("impact-profile.json", impact_profile), ("routing.json", routing),
        ("commands.json", commands),
    ], routing


def version_lock(profile_id: str | None, profile: dict[str, Any], routing: dict[str, Any]) -> dict[str, Any]:
    return {
        "plugin_version": VERSION, "kernel_version": VERSION, "contracts": 2,
        "contract_digest": fingerprint(lifecycle_contract()), "profile": profile_id,
        "profile_digest": fingerprint(profile),
        "dispatch_binding_digest": fingerprint(routing.get("gate_bindings", {})),
        "providers": LOADED_PROVIDERS,
    }


def repair(args: argparse.Namespace) -> int:
    """Safely reconcile an incomplete or stale initialization.

    Existing project artifacts are decisions, not generated cache files.  This
    command therefore creates only *missing* baseline artifacts and refreshes
    the uniquely delimited AGENTS.md block; it never replaces existing JSON,
    wrappers, run records, approvals, or other user content.
    """
    # Do not resolve the supplied root before opening it with O_NOFOLLOW: that
    # would silently accept a final-component symlink selected by an operator.
    root = Path(args.root).absolute()
    apply = bool(getattr(args, "apply", False))
    blockers: list[dict[str, str]] = []
    actions: list[dict[str, str]] = []
    protected: list[str] = []
    try:
        filesystem = RepairFilesystem(root)
        overlay_fd = filesystem._directory_fd((OVERLAY,))
    except (OSError, ValueError) as error:
        blockers.append({"path": OVERLAY, "reason": f"no safe existing initialization: {error}"})
        print(json.dumps({"status": "blocked", "mutation": False, "root": str(root), "actions": actions, "protected": protected, "blockers": blockers}, indent=2))
        return 1
    else:
        os.close(overlay_fd)

    try:
        managed_json = ("project.json", "authorities.json", "impact-profile.json", "routing.json", "commands.json")
        loaded: dict[str, dict[str, Any]] = {}
        for name in managed_json:
            try:
                state = filesystem.file_state((OVERLAY, name))
                if state == "regular":
                    value = json.loads(filesystem.read_text((OVERLAY, name)))
                    if not isinstance(value, dict):
                        raise ValueError("must contain a JSON object")
                    loaded[name] = value
            except (OSError, ValueError, json.JSONDecodeError) as error:
                blockers.append({"path": f"{OVERLAY}/{name}", "reason": f"unsafe or unreadable managed JSON: {error}"})
        project = loaded.get("project.json")
        if project is None:
            blockers.append({"path": f"{OVERLAY}/project.json", "reason": "missing project identity; cannot safely reconstruct it"})
        elif not isinstance(project.get("project_id"), str) or not isinstance(project.get("classification"), str):
            blockers.append({"path": f"{OVERLAY}/project.json", "reason": "missing project_id or classification"})

        profile: dict[str, Any] = {}
        artifacts: list[tuple[str, dict[str, Any]]] = []
        routing: dict[str, Any] = {}
        if not blockers and project is not None:
            profile_id = project.get("profile")
            extensions = project.get("extensions", [])
            if profile_id is not None and not isinstance(profile_id, str):
                blockers.append({"path": f"{OVERLAY}/project.json", "reason": "profile must be a string or null"})
            elif not isinstance(extensions, list) or not all(isinstance(item, str) for item in extensions):
                blockers.append({"path": f"{OVERLAY}/project.json", "reason": "extensions must be a string list"})
            else:
                try:
                    profile, artifacts, routing = initialization_artifacts(
                        root, profile_id, extensions, project["project_id"], project["classification"]
                    )
                except (OSError, ValueError, json.JSONDecodeError) as error:
                    blockers.append({"path": OVERLAY, "reason": f"cannot load current provider/profile: {error}"})
                else:
                    if project.get("profile_digest") != fingerprint(profile):
                        blockers.append({
                            "path": f"{OVERLAY}/project.json",
                            "reason": "provider profile has changed; review and migrate project decisions explicitly",
                        })

        agents_text = ""
        try:
            if filesystem.file_state(("AGENTS.md",)) == "regular":
                agents_text = filesystem.read_text(("AGENTS.md",))
                starts, ends = agents_text.count(MANAGED_START), agents_text.count(MANAGED_END)
                if starts != ends or starts > 1:
                    blockers.append({"path": "AGENTS.md", "reason": "incomplete or ambiguous Agentic SDLC managed block"})
                elif rendered_agents_md(agents_text) != agents_text:
                    actions.append({"path": "AGENTS.md", "action": "refresh_managed_block"})
            else:
                actions.append({"path": "AGENTS.md", "action": "create_managed_block"})
        except (OSError, ValueError) as error:
            blockers.append({"path": "AGENTS.md", "reason": f"unsafe or unreadable managed file: {error}"})

        catalog: dict[str, Any] = {}
        expected_lock: dict[str, Any] = {}
        if not blockers and project is not None:
            for name, _value in artifacts:
                try:
                    state = filesystem.file_state((OVERLAY, name))
                except (OSError, ValueError) as error:
                    blockers.append({"path": f"{OVERLAY}/{name}", "reason": f"unsafe managed artifact: {error}"})
                    continue
                if state == "regular":
                    protected.append(f"{OVERLAY}/{name}")
                else:
                    actions.append({"path": f"{OVERLAY}/{name}", "action": "recreate_missing_baseline"})
            expected_lock = version_lock(project.get("profile"), profile, routing)
            try:
                lock_state = filesystem.file_state((OVERLAY, "version.lock"))
                if lock_state == "regular":
                    lock_value = json.loads(filesystem.read_text((OVERLAY, "version.lock")))
                    if not isinstance(lock_value, dict):
                        raise ValueError("must contain a JSON object")
                    immutable_keys = ("profile", "profile_digest", "dispatch_binding_digest", "providers")
                    drift = [key for key in immutable_keys if lock_value.get(key) != expected_lock[key]]
                    if drift:
                        blockers.append({
                            "path": f"{OVERLAY}/version.lock",
                            "reason": "lock provenance has changed (" + ",".join(drift) + "); review and migrate explicitly",
                        })
                    else:
                        changed = [key for key in ("plugin_version", "kernel_version", "contracts", "contract_digest") if lock_value.get(key) != expected_lock[key]]
                        if changed:
                            actions.append({"path": f"{OVERLAY}/version.lock", "action": "upgrade_lock:" + ",".join(changed)})
                        else:
                            protected.append(f"{OVERLAY}/version.lock")
                else:
                    actions.append({"path": f"{OVERLAY}/version.lock", "action": "recreate_missing_lock"})
            except (OSError, ValueError, json.JSONDecodeError) as error:
                blockers.append({"path": f"{OVERLAY}/version.lock", "reason": f"unsafe or unreadable lock: {error}"})
            if project.get("profile") is not None:
                catalog = load_agent_catalog()
                for runner, extension in (("codex", "toml"), ("claude", "md")):
                    if args.runner not in (runner, "both"):
                        continue
                    for agent_id in profile.get("agents", []):
                        if agent_id not in catalog:
                            continue
                        parts = (f".{runner}", "agents", f"{agent_id}.{extension}")
                        try:
                            state = filesystem.file_state(parts)
                        except (OSError, ValueError) as error:
                            blockers.append({"path": "/".join(parts), "reason": f"unsafe wrapper path: {error}"})
                            continue
                        if state == "regular":
                            protected.append("/".join(parts))
                        else:
                            actions.append({"path": "/".join(parts), "action": "recreate_missing_wrapper"})

        if blockers:
            print(json.dumps({"status": "blocked", "mutation": False, "root": str(root), "actions": actions, "protected": protected, "blockers": blockers}, indent=2))
            return 1
        if not apply:
            print(json.dumps({"status": "repair-available" if actions else "current", "mutation": False, "root": str(root), "actions": actions, "protected": protected, "blockers": []}, indent=2))
            return 0

        action_paths = {item["path"] for item in actions}
        writes: list[tuple[tuple[str, ...], str, bool]] = []
        for name, value in artifacts:
            if f"{OVERLAY}/{name}" in action_paths:
                writes.append(((OVERLAY, name), json.dumps(value, indent=2) + "\n", False))
        if f"{OVERLAY}/version.lock" in action_paths:
            if filesystem.file_state((OVERLAY, "version.lock")) == "regular":
                lock_value = json.loads(filesystem.read_text((OVERLAY, "version.lock")))
                if not isinstance(lock_value, dict):
                    raise ValueError("version.lock must contain a JSON object")
                immutable_keys = ("profile", "profile_digest", "dispatch_binding_digest", "providers")
                if any(lock_value.get(key) != expected_lock[key] for key in immutable_keys):
                    raise ValueError("version.lock provenance changed during repair planning")
                lock_value.update({key: expected_lock[key] for key in ("plugin_version", "kernel_version", "contracts", "contract_digest")})
                writes.append(((OVERLAY, "version.lock"), json.dumps(lock_value, indent=2) + "\n", True))
            else:
                writes.append(((OVERLAY, "version.lock"), json.dumps(expected_lock, indent=2) + "\n", False))
        if "AGENTS.md" in action_paths:
            current_agents = filesystem.read_text(("AGENTS.md",)) if filesystem.file_state(("AGENTS.md",)) == "regular" else ""
            writes.append((("AGENTS.md",), rendered_agents_md(current_agents), True))
        if project is not None and project.get("profile") is not None:
            for runner, extension in (("codex", "toml"), ("claude", "md")):
                if args.runner not in (runner, "both"):
                    continue
                for agent_id in profile.get("agents", []):
                    metadata = catalog.get(agent_id)
                    parts = (f".{runner}", "agents", f"{agent_id}.{extension}")
                    if not metadata or "/".join(parts) not in action_paths:
                        continue
                    reviewer = metadata["kind"] == "reviewer"
                    if runner == "codex":
                        content = "\n".join([
                            f"name = {toml_string(agent_id)}",
                            f"description = {toml_string('Portable Agentic SDLC ' + metadata.get('kind', 'specialist') + ' for ' + metadata.get('phase', 'lifecycle'))}",
                            f"sandbox_mode = {toml_string('read-only' if reviewer else 'workspace-write')}",
                            f"developer_instructions = {toml_string(agent_wrapper_body(agent_id, reviewer, metadata, profile))}",
                            "",
                        ])
                    else:
                        content = "\n".join([
                            "---", f"name: {agent_id}",
                            f"description: Portable Agentic SDLC {metadata.get('kind', 'specialist')} for {metadata.get('phase', 'lifecycle')}",
                            f"tools: {'Read, Grep, Glob, Bash' if reviewer else 'Read, Grep, Glob, Bash, Edit, Write'}",
                            "---", "", agent_wrapper_body(agent_id, reviewer, metadata, profile), "",
                        ])
                    writes.append((parts, content, False))
        for parts, content, overwrite in writes:
            filesystem.write_text(parts, content, overwrite=overwrite)
        print(json.dumps({"status": "repaired" if actions else "current", "mutation": bool(actions), "root": str(root), "actions": actions, "protected": protected, "blockers": []}, indent=2))
        return 0
    except (OSError, ValueError, json.JSONDecodeError) as error:
        print(json.dumps({"status": "blocked", "mutation": False, "root": str(root), "actions": actions, "protected": protected, "blockers": [{"path": OVERLAY, "reason": f"secure repair failed: {error}"}]}, indent=2))
        return 1
    finally:
        filesystem.close()


def initialize(args: argparse.Namespace) -> int:
    if getattr(args, "repair", False):
        return repair(args)
    if getattr(args, "apply", False):
        raise ValueError("--apply is only valid with init --repair")
    root = Path(args.root).resolve()
    dry_run = bool(getattr(args, "dry_run", False))
    if not dry_run:
        root.mkdir(parents=True, exist_ok=True)
    detected = detect_repository(root)
    profile_id = None if args.profile in {None, "kernel-only"} else (detected["proposed_profile"] if args.profile == "auto" else args.profile)
    extension_ids = unique(args.extension or [])
    profile, overlay_files, routing = initialization_artifacts(
        root, profile_id, extension_ids, args.project_id or root.name, args.classification, detected
    )
    overlay = confined_path(root, OVERLAY)
    if not dry_run:
        overlay.mkdir(parents=True, exist_ok=True)
        (overlay / "runs").mkdir(exist_ok=True)
    lock_path = overlay / "version.lock"
    agents_md_path = confined_path(root, "AGENTS.md")
    if dry_run:
        would_create: list[str] = []
        existing_unchanged: list[str] = []
        for name, _value in overlay_files:
            target = overlay / name
            (would_create if not target.exists() else existing_unchanged).append(f"{OVERLAY}/{name}")
        (would_create if not lock_path.exists() else existing_unchanged).append(f"{OVERLAY}/version.lock")
        wrappers_would_create, wrappers_existing = (
            write_agent_wrappers(root, profile, args.runner, dry_run=True) if profile_id else ([], [])
        )
        # update_agents_md() runs unconditionally on every real init (it creates
        # AGENTS.md if absent, or replaces its managed block in place if present),
        # so a faithful preview must report its effect even though dry-run never
        # calls it.
        agents_md_status = "would_create" if not agents_md_path.exists() else "would_update_managed_block"
        print(json.dumps({
            "status": "dry-run",
            "mutation": False,
            "root": str(root),
            "profile": profile_id,
            "would_create": would_create,
            "existing_unchanged": existing_unchanged,
            "agent_wrappers_would_create": wrappers_would_create,
            "agent_wrappers_existing": wrappers_existing,
            "agents_md": agents_md_status,
            "detected": detected,
        }, indent=2))
        return 0
    created = []
    for name, value in overlay_files:
        if write_json(overlay / name, value, overwrite=False):
            created.append(f"{OVERLAY}/{name}")
    if not lock_path.exists():
        write_json(lock_path, version_lock(profile_id, profile, routing), overwrite=False)
        created.append(f"{OVERLAY}/version.lock")
    agents_md_status = "created" if not agents_md_path.exists() else "updated_managed_block"
    update_agents_md(root)
    wrappers, _wrappers_existing = write_agent_wrappers(root, profile, args.runner) if profile_id else ([], [])
    print(json.dumps({"status": "initialized", "root": str(root), "profile": profile_id, "created": created, "agent_wrappers_created": wrappers, "agents_md": agents_md_status, "ready": False, "blockers": ["Human authorities and impact applicability require explicit decisions."]}, indent=2))
    return 0


def load_overlay(root: Path) -> tuple[Path, dict[str, Any], dict[str, Any], dict[str, Any], dict[str, Any]]:
    overlay = confined_path(root, OVERLAY)
    return overlay, load_json(overlay / "project.json"), load_json(overlay / "authorities.json"), load_json(overlay / "impact-profile.json"), load_json(overlay / "routing.json")


def choose_workflow(text: str, routes: list[dict[str, Any]]) -> tuple[str, list[dict[str, Any]]]:
    lowered = text.lower()
    matched = [route for route in routes if any(phrase.lower() in lowered for phrase in route.get("phrases", []))]
    if any(phrase in lowered for phrase in ["deploy to production", "production deployment"]):
        return "production-release", matched
    if any(phrase in lowered for phrase in ["major incident", "incident response", "service outage"]):
        return "support-escalation", matched
    if any(route.get("id") == "runtime-assurance" for route in matched):
        return "runtime-assurance", matched
    if any(route.get("id") == "debugging" for route in matched):
        return "debugging", matched
    intake = any(route.get("id") == "product-intake" for route in matched)
    design = any(route.get("id") not in {"product-intake", "runtime-assurance", "debugging"} for route in matched)
    if intake and not design:
        return "product-intake", matched
    if matched:
        return "new-service", matched
    return "needs-triage", matched


def lifecycle_sequence(gate_ids: list[str], ignored_gates: list[str]) -> tuple[list[str], set[str]]:
    unknown = set(ignored_gates) - set(GATE_IDS)
    if unknown:
        raise ValueError(f"ignored_gates contains unknown lifecycle gates: {sorted(unknown)}")
    if not gate_ids:
        return [], set()
    highest = max(GATE_IDS.index(gate_id) for gate_id in gate_ids)
    sequence = GATE_IDS[: highest + 1]
    ignored = set(ignored_gates).intersection(sequence)
    return sequence, ignored


def gate_dispatch_binding(gate: dict[str, Any], routing: dict[str, Any]) -> dict[str, list[str]]:
    result = {"agents": [], "tasks": [], "artifacts": []}
    binding = routing.get("gate_bindings", {}).get(gate["id"], {})
    contributions = binding.get("contributions", {}) if isinstance(binding, dict) else {}
    for slot in gate.get("required_contributions", []):
        contribution = contributions.get(slot)
        if not isinstance(contribution, dict):
            continue
        for field in result:
            result[field].extend(contribution.get(field, []))
    return {field: unique(values) for field, values in result.items()}


def gate_agent_artifacts(gate: dict[str, Any]) -> list[dict[str, str]]:
    return []


def make_gate_record(
    gate: dict[str, Any], impact: dict[str, Any], authorities: dict[str, Any], ignored: bool = False
) -> dict[str, Any]:
    affected_unknown = bool(impact.get("blocking_unknowns")) and gate["id"] in {"G3", "G4", "G5", "G7"}
    authority_requirements = []
    for authority_id in gate.get("authority_requirements", []):
        if authority_id not in AUTHORITY_ROLES:
            continue
        assigned = authorities.get(authority_id, {}).get("status") == "assigned"
        authority_requirements.append({"authority_id": authority_id, "authority_type": "human-approver", "role": ROLE_LABELS[authority_id], "applicability": "applicable" if assigned else "unknown", "rationale": "Assigned in project authority map" if assigned else "Authority is not assigned"})
    return {
        "tier": "lifecycle",
        "gate_id": gate["id"],
        "name": gate["name"],
        "status": "blocked" if affected_unknown else "pending",
        "applicability": "not-applicable" if ignored else ("unknown" if affected_unknown else "applicable"),
        "applicability_rationale": "Explicitly configured lifecycle gate ignore" if ignored else ("Impact applicability is unresolved" if affected_unknown else "Lifecycle gate applies by default"),
        "artifact_bindings": [],
        "preparers": [],
        "independent_verifier": None,
        "independence_declaration": {"verifier_confirmed_not_preparer": False, "verifier_made_material_correction": False},
        "authority_requirements": authority_requirements,
        "human_approvals": [],
        "decided_at": None,
        "evidence_refs": [],
        "knowledge_status": "unavailable",
        "findings": [],
        "exceptions": [],
        "invalidation_history": [],
        "required_reentry_gate": gate["id"] if affected_unknown else None,
    }


def derive_current_phase(record: dict[str, Any]) -> str:
    lifecycle = load_json(CONTRACTS / "lifecycle-gates.json")["gates"]
    phase_by_gate = {gate["id"]: gate["phase"] for gate in lifecycle}
    for gate in record.get("lifecycle_gates", []):
        if gate.get("applicability") == "not-applicable":
            continue
        if gate.get("status") != "approved":
            return phase_by_gate.get(gate.get("gate_id"), record.get("current_lifecycle_phase", "intent"))
    return "feedback"


def plan_task(args: argparse.Namespace) -> int:
    root = Path(args.root).resolve()
    overlay, project, authorities, impact, routing = load_overlay(root)
    workflow, matched = choose_workflow(args.task, routing.get("routes", []))
    if not LOADED_PROVIDERS and workflow != "needs-triage":
        raise ValueError("agent dispatch requires a loaded provider; rerun with --provider <manifest>, or use a kernel-only lifecycle operation")
    primary: list[str] = []
    reviewers: list[str] = []
    support: list[str] = []
    gates: list[str] = []
    for route in matched:
        primary.extend(route.get("agents", []))
        reviewers.extend(route.get("reviewers", []))
        support.extend(route.get("support", []))
        gates.extend(route.get("gates", []))
    change_intake = routing.get("change_intake", {})
    normalized_task = args.task.lower()
    change_work = any(
        re.search(rf"(^|[^a-z0-9]){re.escape(keyword.lower())}([^a-z0-9]|$)", normalized_task)
        for keyword in change_intake.get("keywords", [])
    )
    if change_work:
        support.extend(change_intake.get("agents", []))
        gates.extend(change_intake.get("quality_gates", []))
    primary, reviewers, support, gates = map(unique, [primary, reviewers, support, gates])
    gates.sort(key=lambda gate_id: int(gate_id.removeprefix("G")))
    lifecycle = load_json(CONTRACTS / "lifecycle-gates.json")["gates"]
    configured_ignored = routing.get("ignored_gates", [])
    sequence, ignored_gates = lifecycle_sequence(gates, configured_ignored)
    gates = [gate_id for gate_id in sequence if gate_id not in ignored_gates]
    reviewers = [agent for agent in reviewers if agent not in primary]
    if workflow == "production-release":
        support = unique(support + ["release-engineer"])
        gates = unique(gates + ["G8", "G9"])
    mutations = load_json(CONTRACTS / "mutation-gates.json")["human_only"]
    matched_human_gates: dict[str, dict[str, Any]] = {}
    for gate in mutations:
        for phrase in gate["phrases"]:
            if phrase in args.task.lower():
                matched_human_gates[gate["id"]] = {
                    "id": gate["id"],
                    "required": True,
                    "reason": f"Matched human-only phrase: {phrase}",
                }
    human_gates = list(matched_human_gates.values())
    required = []
    for gate_id in gates:
        contributing_routes = [route["id"] for route in matched if gate_id in route.get("gates", [])]
        if not contributing_routes:
            contributing_routes = [f"workflow:{workflow}"]
        required.append({
            "id": gate_id,
            "required": True,
            "reason": "Required by matched project route or workflow",
            "contributing_routes": contributing_routes,
        })
    gate_contracts = {gate["id"]: gate for gate in lifecycle}
    gate_bindings = {gate_id: gate_dispatch_binding(gate_contracts[gate_id], routing) for gate_id in sequence}
    gate_agents = [agent for gate_id in sequence if gate_id not in ignored_gates for agent in gate_bindings[gate_id]["agents"]]
    support = unique(support + gate_agents)
    support = [agent for agent in support if agent not in primary and agent not in reviewers]
    gate_dispatch = [
        {
            "gate_id": gate_id,
            "status": "ignored" if gate_id in ignored_gates else "required",
            "agents": gate_bindings[gate_id]["agents"],
            "tasks": gate_bindings[gate_id]["tasks"],
            "artifacts": gate_bindings[gate_id]["artifacts"],
        }
        for gate_id in sequence
    ]
    task_id = safe_task_id(args.task_id)
    task_dir = confined_path(root, OVERLAY, "runs", task_id)
    dispatch = {
        "schema_version": 2,
        "task_id": task_id,
        "generated_at": now(),
        "status": "needs-triage" if workflow == "needs-triage" else "ready",
        "workflow": workflow,
        "inputs": {"task": args.task, "classification": project["classification"]},
        "matched_routes": [route["id"] for route in matched],
        "matched_risks": [],
        "agents": {"primary": primary, "reviewers": reviewers, "support": support},
        "required_quality_gates": required,
        "ignored_quality_gates": sorted(ignored_gates, key=GATE_IDS.index),
        "gate_dispatch": gate_dispatch,
        "human_gates": human_gates,
        "knowledge_context": {"status": "unavailable", "reason": "No portable knowledge source configured", "requests": []},
    }
    dispatch_hash = dispatch_fingerprint(dispatch)
    dispatch["dispatch_fingerprint"] = dispatch_hash
    record = {
        "version": 2,
        "task_id": task_id,
        "recorded_at": now(),
        "classification": project["classification"],
        "mode": "planning-review-only",
        "baseline_revision": "unresolved",
        "scope": args.task,
        "dispatch_fingerprint": dispatch_hash,
        "kernel_version": VERSION,
        "contract_digest": fingerprint(lifecycle_contract()),
        "provider_bindings": LOADED_PROVIDERS,
        "profile": project.get("profile"),
        "profile_digest": project.get("profile_digest"),
        "dispatch_binding_digest": fingerprint(routing.get("gate_bindings", {})),
        "disposition": "pending",
        "intent_record_id": None,
        "requirements_baseline_id": None,
        "current_lifecycle_phase": "intent",
        "knowledge_retrieval": {"status": "unavailable", "reason": "No portable knowledge source configured", "query_ids": [], "evidence_refs": [], "influence": "none"},
        "impact_profile": impact,
        "lifecycle_gates": [make_gate_record(gate, impact, authorities, gate["id"] in ignored_gates) for gate in lifecycle],
        "specialist_attestations": [],
        "re_entry_history": [],
        "execution_summary": {
            "gates": {
                gate_id: {
                    "configured": gate_id in sequence,
                    "ignored": gate_id in ignored_gates,
                    "ignore_reason": "Configured in project routing" if gate_id in ignored_gates else None,
                    "required_agents": gate_bindings.get(gate_id, {"agents": []})["agents"],
                    "dispatched_agents": [],
                    "required_tasks": gate_bindings.get(gate_id, {"tasks": []})["tasks"],
                    "completed_tasks": [],
                    "required_agent_artifacts": gate_agent_artifacts(gate_contracts[gate_id]),
                    "produced_agent_artifacts": [],
                }
                for gate_id in GATE_IDS
            }
        },
    }
    dispatch_path = task_dir / "dispatch-plan.json"
    record_path = task_dir / "run-record.json"
    if dispatch_path.exists():
        existing = load_json(dispatch_path)
        existing_task = existing.get("inputs", {}).get("task")
        if existing_task != args.task:
            raise ValueError(f"task ID {task_id} already exists with different task text; use a new task ID")
        if existing.get("dispatch_fingerprint") != dispatch_hash:
            raise ValueError(f"task ID {task_id} routing has changed; use a new task ID or explicitly invalidate the existing run")
    if record_path.exists():
        existing_record = load_json(record_path)
        if existing_record.get("scope") != args.task:
            raise ValueError(f"task ID {task_id} already exists with different task text; use a new task ID")
        if existing_record.get("dispatch_fingerprint") != dispatch_hash:
            raise ValueError(f"task ID {task_id} has an existing run record for different task or routing state; use a new task ID")
    write_json(dispatch_path, dispatch)
    if not record_path.exists():
        advance_lifecycle(record, routing)
        write_json(record_path, record)
    print(json.dumps(dispatch, indent=2))
    return 0


def advance_lifecycle(record: dict[str, Any], routing: dict[str, Any]) -> dict[str, Any]:
    """Move only the next eligible gate to ready; never infer approval."""
    contracts = {item["id"]: item for item in lifecycle_contract()["gates"]}
    gates = record.get("lifecycle_gates", [])
    for index, gate in enumerate(gates):
        if gate.get("applicability") == "not-applicable" or gate.get("status") in {"approved", "invalidated"}:
            continue
        if gate.get("status") not in {"pending", "blocked"}:
            continue
        contract = contracts.get(gate.get("gate_id"), {})
        prerequisites = contract.get("prerequisites", [])
        prior = {item.get("gate_id"): item for item in gates}
        if any(prior.get(req, {}).get("status") != "approved" for req in prerequisites):
            continue
        if gate.get("applicability") != "applicable":
            continue
        if any(req.get("applicability") == "unknown" for req in gate.get("authority_requirements", [])):
            continue
        binding = gate_dispatch_binding(contract, routing)
        required_fields = ["tasks", "artifacts"] if contract.get("human_only") else ["agents", "tasks", "artifacts"]
        if contract.get("required_contributions") and not all(binding[field] for field in required_fields):
            continue
        gate["status"] = "ready"
        break
    record["current_lifecycle_phase"] = derive_current_phase(record)
    return record


def valid_exception(exception: dict[str, Any]) -> bool:
    required = {"exception_id", "finding_id", "justification", "compensating_controls", "owner", "approver", "expires_at", "remediation_plan"}
    if not required.issubset(exception) or not exception.get("compensating_controls"):
        return False
    owner, approver = exception.get("owner"), exception.get("approver")
    if not isinstance(owner, dict) or not isinstance(approver, dict):
        return False
    if owner.get("kind") != "human" or approver.get("kind") != "human" or owner.get("id") == approver.get("id"):
        return False
    try:
        expiry = datetime.fromisoformat(str(exception["expires_at"]).replace("Z", "+00:00"))
    except ValueError:
        return False
    if expiry.tzinfo is None:
        return False
    return expiry > datetime.now(timezone.utc)


def validate_repository(args: argparse.Namespace) -> int:
    root = Path(args.root).resolve()
    errors: list[str] = []
    blockers: list[str] = []
    try:
        import jsonschema  # type: ignore
    except ImportError:
        jsonschema = None
        errors.append(
            "full validation dependency is unavailable; install kernel/requirements-validation.txt"
        )
    try:
        overlay, project, authorities, impact, routing = load_overlay(root)
    except (OSError, ValueError, json.JSONDecodeError) as error:
        print(json.dumps({"valid": False, "ready": False, "errors": [str(error)], "blockers": []}, indent=2))
        return 1
    if project.get("profile") not in profile_ids():
        errors.append("project profile is not installed")
    try:
        approval_policy = approval_source_policy(project)
    except ValueError as error:
        errors.append(str(error))
        approval_policy = {"human_gate_default": "manual", "allow_manual_fallback": True}
    for environment in project.get("environments", []):
        environment_name = environment.get("name", "unnamed")
        if environment.get("persistence") == "unknown":
            blockers.append(f"environment persistence is unknown: {environment_name}")
        if environment.get("production") == "unknown":
            blockers.append(f"environment production status is unknown: {environment_name}")
    try:
        commands = load_json(overlay / "commands.json")
        lock = load_json(overlay / "version.lock")
    except (OSError, ValueError, json.JSONDecodeError) as error:
        errors.append(str(error))
        commands, lock = {}, {}
    if commands and commands.get("confirmed") is not True:
        blockers.append("detected project commands are not confirmed")
    if lock and lock.get("kernel_version") != VERSION:
        errors.append(
            f"project kernel lock {lock.get('kernel_version')} does not match installed version {VERSION}"
        )
    if lock and lock.get("providers", []) != LOADED_PROVIDERS:
        errors.append("loaded providers do not match the project provider lock")
    for role in AUTHORITY_ROLES:
        value = authorities.get(role)
        if not isinstance(value, dict):
            errors.append(f"missing authority role: {role}")
            continue
        if role in CONDITIONAL_AUTHORITY_ROLES:
            applicability = value.get("applicability")
            if applicability == "unknown":
                blockers.append(f"conditional authority applicability {role} is unresolved")
            elif applicability == "applicable" and (
                value.get("status") != "assigned" or not value.get("assignee")
            ):
                blockers.append(f"applicable conditional authority {role} is unassigned")
            elif applicability == "not-applicable" and not value.get("rationale"):
                errors.append(f"conditional authority {role} not-applicable requires a rationale")
        elif value.get("status") != "assigned" or not value.get("assignee"):
            blockers.append(f"authority {role} is unresolved")
        if (
            value.get("status") == "assigned"
            and value.get("applicability", "applicable") == "applicable"
            and approval_policy["human_gate_default"] == "github-review"
            and not authority_github_login(value)
            and not approval_policy["allow_manual_fallback"]
        ):
            blockers.append(f"authority {role} is missing a GitHub login binding required for GitHub review approvals")
        if (
            value.get("status") == "assigned"
            and value.get("applicability", "applicable") == "applicable"
            and approval_policy["human_gate_default"] == "gitlab-mr"
            and not authority_gitlab_username(value)
            and not approval_policy["allow_manual_fallback"]
        ):
            blockers.append(f"authority {role} is missing a GitLab username binding required for GitLab MR approvals")
    unknown_impact = [item.get("id", "unnamed") for item in impact.get("impact_categories", []) + impact.get("specialized_boms", []) if item.get("applicability") == "unknown"]
    blockers.extend(f"impact applicability is unknown: {item}" for item in unknown_impact)
    blockers.extend(f"impact profile blocker: {item}" for item in impact.get("blocking_unknowns", []))
    route_ids: set[str] = set()
    unknown_ignored = set(routing.get("ignored_gates", [])) - set(GATE_IDS)
    if unknown_ignored:
        errors.append(f"routing ignored_gates contains unknown lifecycle gates: {sorted(unknown_ignored)}")
    agent_catalog = load_agent_catalog()
    known_agents = set(agent_catalog)
    for route in routing.get("routes", []):
        route_id = route.get("id")
        if not route_id or route_id in route_ids:
            errors.append(f"duplicate or missing route ID: {route_id}")
        route_ids.add(route_id)
        overlap = set(route.get("agents", [])).intersection(route.get("reviewers", []))
        if overlap:
            errors.append(f"route {route_id} assigns author and reviewer roles to: {sorted(overlap)}")
        unknown_agents = (
            set(route.get("agents", []))
            | set(route.get("reviewers", []))
            | set(route.get("support", []))
        ) - known_agents
        if unknown_agents:
            errors.append(f"route {route_id} references unknown agents: {sorted(unknown_agents)}")
    run_root = confined_path(root, OVERLAY, "runs")
    task_directories = [path for path in run_root.iterdir() if path.is_dir()] if run_root.exists() else []
    for task_directory in task_directories:
        record_path = confined_path(root, OVERLAY, "runs", task_directory.name, "run-record.json")
        dispatch_path = confined_path(root, OVERLAY, "runs", task_directory.name, "dispatch-plan.json")
        if not record_path.exists() or not dispatch_path.exists():
            errors.append(f"{task_directory}: dispatch plan and authoritative run record must both exist")
            continue
        try:
            record = load_json(record_path)
        except (OSError, ValueError, json.JSONDecodeError) as error:
            errors.append(str(error))
            continue
        required_top = {"version", "task_id", "dispatch_fingerprint", "recorded_at", "classification", "mode", "baseline_revision", "scope", "disposition", "intent_record_id", "requirements_baseline_id", "current_lifecycle_phase", "knowledge_retrieval", "impact_profile", "lifecycle_gates", "specialist_attestations", "re_entry_history", "kernel_version", "contract_digest", "provider_bindings", "profile", "profile_digest", "dispatch_binding_digest"}
        missing_top = required_top.difference(record)
        if missing_top:
            errors.append(f"{record_path}: missing required fields: {sorted(missing_top)}")
        if jsonschema is not None:
            schema = load_json(CONTRACTS / "run-record.schema.json")
            validator = jsonschema.Draft202012Validator(
                schema,
                format_checker=jsonschema.FormatChecker(),
            )
            for schema_error in validator.iter_errors(record):
                location = ".".join(str(part) for part in schema_error.absolute_path) or "<root>"
                errors.append(f"{record_path}: schema {location}: {schema_error.message}")
        if not is_valid_datetime(record.get("recorded_at")):
            errors.append(f"{record_path}: schema recorded_at: {record.get('recorded_at')!r} is not a 'date-time'")
        gate_records = record.get("lifecycle_gates", [])
        if [gate.get("gate_id") for gate in gate_records] != GATE_IDS:
            errors.append(f"{record_path}: lifecycle gates must be exactly G1-G10 in order")
        gate_contracts = {gate["id"]: gate for gate in load_json(CONTRACTS / "lifecycle-gates.json")["gates"]}
        execution_gates = record.get("execution_summary", {}).get("gates", {})
        configured_gate_ids = {
            item.get("gate_id")
            for item in load_json(confined_path(root, OVERLAY, "runs", task_directory.name, "dispatch-plan.json")).get("gate_dispatch", [])
            if item.get("status") == "required"
        }
        ignored_gate_ids = {
            item.get("gate_id")
            for item in load_json(confined_path(root, OVERLAY, "runs", task_directory.name, "dispatch-plan.json")).get("gate_dispatch", [])
            if item.get("status") == "ignored"
        }
        invalidation_started = False
        for index, gate in enumerate(gate_records):
            gate_id = gate.get("gate_id")
            for evidence in gate.get("evidence_refs", []):
                if not isinstance(evidence, dict):
                    errors.append(f"{record_path}: {gate_id} evidence_refs contains a non-object entry")
                    continue
                uri = evidence.get("uri")
                if isinstance(uri, str) and uri.startswith("gitlab-issue:") and parse_gitlab_issue_uri(uri) is None:
                    errors.append(f"{record_path}: {gate_id} has an invalid GitLab issue URI")
                if isinstance(uri, str) and uri.startswith("github-issue:") and parse_github_issue_uri(uri) is None:
                    errors.append(f"{record_path}: {gate_id} has an invalid GitHub issue URI")
            contract = gate_contracts.get(gate_id, {})
            execution = execution_gates.get(gate_id)
            if execution is None:
                errors.append(f"{record_path}: {gate_id} is missing its required execution record")
                execution = {}
            if execution is not None:
                if execution.get("configured") != (gate_id in configured_gate_ids or gate_id in ignored_gate_ids):
                    errors.append(f"{record_path}: {gate_id} execution configuration does not match dispatch plan")
                if execution.get("ignored") != (gate_id in ignored_gate_ids):
                    errors.append(f"{record_path}: {gate_id} ignore state does not match dispatch plan")
                binding = gate_dispatch_binding(contract, routing)
                expected_agents = binding["agents"]
                expected_artifacts = gate_agent_artifacts(contract)
                if execution.get("configured") and execution.get("required_agents") != expected_agents:
                    errors.append(f"{record_path}: {gate_id} required agent set does not match lifecycle contract")
                if execution.get("configured") and execution.get("required_tasks") != binding["tasks"]:
                    errors.append(f"{record_path}: {gate_id} required task set does not match lifecycle contract")
                if execution.get("required_agent_artifacts") != expected_artifacts:
                    errors.append(f"{record_path}: {gate_id} required agent artifacts do not match lifecycle contract")
                if execution.get("ignored") and not execution.get("ignore_reason"):
                    errors.append(f"{record_path}: {gate_id} ignored gate requires an explicit reason")
                if gate.get("status") in {"ready", "approved"} and execution.get("configured") and not execution.get("ignored"):
                    if set(execution.get("dispatched_agents", [])) != set(expected_agents):
                        errors.append(f"{record_path}: {gate_id} advanced without dispatching every configured agent")
                    if set(execution.get("completed_tasks", [])) != set(binding["tasks"]):
                        errors.append(f"{record_path}: {gate_id} advanced without completing every configured task")
                    produced = {
                        (item.get("agent_id"), item.get("artifact_id"))
                        for item in execution.get("produced_agent_artifacts", [])
                        if item.get("revision") and item.get("digest")
                    }
                    required = {(item["agent_id"], item["artifact_id"]) for item in expected_artifacts}
                    if not required.issubset(produced):
                        errors.append(f"{record_path}: {gate_id} advanced without immutable artifacts from every configured agent")
            if gate.get("status") in {"ready", "approved"} and execution.get("configured") and any(
                prior.get("status") not in {"approved", "invalidated"}
                and execution_gates.get(prior.get("gate_id"), {}).get("configured")
                and not execution_gates.get(prior.get("gate_id"), {}).get("ignored")
                for prior in gate_records[:index]
            ):
                errors.append(f"{record_path}: {gate_id} violates lexical gate order")
            preparers = {identity.get("id") for identity in gate.get("preparers", []) if isinstance(identity, dict)}
            verifier = gate.get("independent_verifier")
            if isinstance(verifier, dict) and verifier.get("id") in preparers:
                errors.append(f"{record_path}: {gate_id} verifier is also a preparer")
            if gate.get("independence_declaration", {}).get("verifier_made_material_correction"):
                errors.append(f"{record_path}: {gate_id} verifier made a material correction and lost approval authority")
            if invalidation_started and gate.get("status") != "invalidated":
                errors.append(f"{record_path}: downstream gate {gate_id} must be invalidated")
            if gate.get("status") == "invalidated":
                invalidation_started = True
                if not gate.get("required_reentry_gate"):
                    errors.append(f"{record_path}: {gate_id} invalidation is missing required re-entry gate")
            if gate.get("decided_at") is not None and not is_valid_datetime(gate.get("decided_at")):
                errors.append(f"{record_path}: schema lifecycle_gates.{index}.decided_at: {gate.get('decided_at')!r} is not a 'date-time'")
            for approval_index, approval in enumerate(gate.get("human_approvals", [])):
                if approval.get("decided_at") is not None and not is_valid_datetime(approval.get("decided_at")):
                    errors.append(
                        f"{record_path}: schema lifecycle_gates.{index}.human_approvals.{approval_index}.decided_at: "
                        f"{approval.get('decided_at')!r} is not a 'date-time'"
                    )
            for invalidation_index, invalidation in enumerate(gate.get("invalidation_history", [])):
                if not is_valid_datetime(invalidation.get("invalidated_at")):
                    errors.append(
                        f"{record_path}: schema lifecycle_gates.{index}.invalidation_history.{invalidation_index}.invalidated_at: "
                        f"{invalidation.get('invalidated_at')!r} is not a 'date-time'"
                    )
            for exception_index, exception in enumerate(gate.get("exceptions", [])):
                if not is_valid_datetime(exception.get("expires_at")):
                    errors.append(
                        f"{record_path}: schema lifecycle_gates.{index}.exceptions.{exception_index}.expires_at: "
                        f"{exception.get('expires_at')!r} is not a 'date-time'"
                    )
            if gate.get("status") == "approved":
                if any(
                    prior.get("status") != "approved"
                    and prior.get("applicability") != "not-applicable"
                    for prior in gate_records[:index]
                ):
                    errors.append(f"{record_path}: {gate_id} approved before all prerequisite gates")
                if gate.get("applicability") != "applicable" or not gate.get("evidence_refs") or not gate.get("artifact_bindings"):
                    errors.append(f"{record_path}: {gate_id} has an unsafe approval without applicability, evidence, or artifact binding")
                requirements = gate.get("authority_requirements", [])
                requirement_ids: set[str] = set()
                for requirement in requirements:
                    authority_id = requirement.get("authority_id")
                    if authority_id in requirement_ids:
                        errors.append(f"{record_path}: {gate_id} has duplicate authority requirement {authority_id}")
                    requirement_ids.add(authority_id)
                    if authority_id in ROLE_LABELS and (
                        requirement.get("authority_type") != "human-approver"
                        or requirement.get("role") != ROLE_LABELS[authority_id]
                    ):
                        errors.append(f"{record_path}: {gate_id} authority {authority_id} is relabeled")
                    if requirement.get("applicability") == "not-applicable" and not requirement.get("rationale"):
                        errors.append(f"{record_path}: {gate_id} not-applicable authority {authority_id} lacks rationale")
                gate_contract = gate_contracts.get(gate_id, {})
                expected_requirement_ids = set(gate_contract.get("authority_requirements", []))
                missing_requirements = expected_requirement_ids - requirement_ids
                if missing_requirements:
                    errors.append(
                        f"{record_path}: {gate_id} is missing authority requirements "
                        f"{sorted(missing_requirements)}"
                    )
                if any(requirement.get("applicability") == "unknown" for requirement in requirements):
                    errors.append(f"{record_path}: {gate_id} approved with unresolved authority applicability")
                if not isinstance(verifier, dict) or not gate.get("independence_declaration", {}).get("verifier_confirmed_not_preparer"):
                    errors.append(f"{record_path}: {gate_id} lacks an independent verifier declaration")
                elif verifier.get("kind") == "agent":
                    # `role` is expected to be the agent-catalog id (e.g.
                    # "code-reviewer"), not a free-text label -- only
                    # `kind: "agent"` verifiers are looked up this way.
                    # `kind` here is the identity's own kind (human/agent/
                    # service, run-record.schema.json's `identity` $def) --
                    # a different axis from the agent catalog's per-role
                    # kind (author/reviewer/specialist) checked below. Do
                    # not drop this guard: a human/service verifier's
                    # `role` is not a catalog id, and checking it against
                    # the catalog unconditionally regressed to always
                    # rejecting them (issue #9).
                    verifier_role = verifier.get("role")
                    if not isinstance(verifier_role, str):
                        errors.append(f"{record_path}: {gate_id} verifier role must be a string")
                    elif agent_catalog.get(verifier_role, {}).get("kind") != "reviewer":
                        errors.append(f"{record_path}: {gate_id} verifier agent is not a catalog reviewer")
                approvals = [approval for approval in gate.get("human_approvals", []) if approval.get("status") == "approved"]
                approval_roles = {approval.get("approver", {}).get("role") for approval in approvals if isinstance(approval.get("approver"), dict)}
                required_roles = {
                    requirement.get("role") for requirement in requirements
                    if requirement.get("authority_type") == "human-approver"
                    and requirement.get("applicability") == "applicable"
                }
                if not required_roles.issubset(approval_roles):
                    errors.append(f"{record_path}: {gate_id} lacks required human roles {sorted(required_roles - approval_roles)}")
                for approval in gate.get("human_approvals", []):
                    approver = approval.get("approver")
                    if approval.get("status") == "approved" and (not isinstance(approver, dict) or approver.get("kind") != "human"):
                        errors.append(f"{record_path}: {gate_id} approval is not human")
                    if isinstance(approver, dict) and (approver.get("id") in preparers or (isinstance(verifier, dict) and approver.get("id") == verifier.get("id"))):
                        errors.append(f"{record_path}: {gate_id} approver is not independent")
                    if approval.get("status") == "approved" and (
                        not approval.get("decided_at") or not approval.get("evidence_refs")
                    ):
                        errors.append(f"{record_path}: {gate_id} approval lacks decision time or approval evidence")
                    github_review_refs = []
                    gitlab_mr_refs = []
                    for evidence in approval.get("evidence_refs", []):
                        if not isinstance(evidence, dict):
                            errors.append(f"{record_path}: {gate_id} approval evidence_refs contains a non-object entry")
                            continue
                        uri = evidence.get("uri")
                        if isinstance(uri, str) and uri.startswith("github-review:"):
                            parsed_review = parse_github_review_uri(uri)
                            if parsed_review is None:
                                errors.append(f"{record_path}: {gate_id} approval has an invalid GitHub review URI")
                            else:
                                github_review_refs.append(parsed_review)
                        elif isinstance(uri, str) and uri.startswith("gitlab-mr:"):
                            parsed_approval = parse_gitlab_mr_uri(uri)
                            if parsed_approval is None:
                                errors.append(f"{record_path}: {gate_id} approval has an invalid GitLab MR approval URI")
                            else:
                                gitlab_mr_refs.append(parsed_approval)
                    if approval.get("status") == "approved":
                        if (
                            approval_policy["human_gate_default"] == "github-review"
                            and not approval_policy["allow_manual_fallback"]
                            and not github_review_refs
                        ):
                            errors.append(f"{record_path}: {gate_id} approval must be backed by a GitHub review")
                        if github_review_refs and isinstance(approver, dict):
                            approver_login = github_login_from_identity(approver.get("id"))
                            for parsed_review in github_review_refs:
                                if approver_login and approver_login != parsed_review["login"]:
                                    errors.append(f"{record_path}: {gate_id} GitHub review login does not match approver identity")
                        if (
                            approval_policy["human_gate_default"] == "gitlab-mr"
                            and not approval_policy["allow_manual_fallback"]
                            and not gitlab_mr_refs
                        ):
                            errors.append(f"{record_path}: {gate_id} approval must be backed by a GitLab MR approval")
                        if gitlab_mr_refs and isinstance(approver, dict):
                            approver_username = gitlab_username_from_identity(approver.get("id"))
                            for parsed_approval in gitlab_mr_refs:
                                if approver_username and approver_username != parsed_approval["username"]:
                                    errors.append(f"{record_path}: {gate_id} GitLab MR approver does not match approver identity")
                for requirement in requirements:
                    if requirement.get("authority_type") != "human-approver" or requirement.get("applicability") != "applicable":
                        continue
                    authority_id = requirement.get("authority_id")
                    expected_assignee = authorities.get(authority_id, {}).get("assignee")
                    matching = [
                        approval for approval in approvals
                        if isinstance(approval.get("approver"), dict)
                        and approval["approver"].get("id") == expected_assignee
                        and approval["approver"].get("role") == requirement.get("role")
                    ]
                    if not expected_assignee or not matching:
                        errors.append(f"{record_path}: {gate_id} approval is not bound to assigned authority {authority_id}")
                    expected_github_login = authority_github_login(authorities.get(authority_id, {}))
                    if expected_github_login:
                        for approval in matching:
                            github_review_refs = [
                                parsed_review
                                for evidence in approval.get("evidence_refs", [])
                                for parsed_review in [parse_github_review_uri(evidence.get("uri", ""))]
                                if parsed_review is not None
                            ]
                            if github_review_refs and any(
                                str(parsed_review["login"]).lower() != str(expected_github_login).lower()
                                for parsed_review in github_review_refs
                            ):
                                errors.append(f"{record_path}: {gate_id} approval GitHub reviewer does not match assigned authority {authority_id}")
                    expected_gitlab_username = authority_gitlab_username(authorities.get(authority_id, {}))
                    if expected_gitlab_username:
                        for approval in matching:
                            gitlab_mr_refs = [
                                parsed_approval
                                for evidence in approval.get("evidence_refs", [])
                                for parsed_approval in [parse_gitlab_mr_uri(evidence.get("uri", ""))]
                                if parsed_approval is not None
                            ]
                            if gitlab_mr_refs and any(
                                str(parsed_approval["username"]).lower() != str(expected_gitlab_username).lower()
                                for parsed_approval in gitlab_mr_refs
                            ):
                                errors.append(f"{record_path}: {gate_id} approval GitLab approver does not match assigned authority {authority_id}")
                if not gate.get("decided_at"):
                    errors.append(f"{record_path}: {gate_id} approved without a gate decision timestamp")
                if any(not binding.get("digest") for binding in gate.get("artifact_bindings", [])):
                    errors.append(f"{record_path}: {gate_id} approved with a mutable artifact binding")
                open_severe = [finding for finding in gate.get("findings", []) if finding.get("severity") in {"critical", "high"} and finding.get("status") == "open"]
                if open_severe:
                    errors.append(f"{record_path}: {gate_id} has unresolved critical/high findings")
                exception_findings = {exception.get("finding_id") for exception in gate.get("exceptions", []) if valid_exception(exception)}
                for finding in gate.get("findings", []):
                    if finding.get("status") == "accepted-exception" and finding.get("finding_id") not in exception_findings:
                        errors.append(f"{record_path}: {gate_id} accepted finding lacks a valid exception")
                if gate_id in {"G3", "G4", "G5", "G7"} and record.get("impact_profile", {}).get("blocking_unknowns"):
                    errors.append(f"{record_path}: {gate_id} approved while impact applicability is unknown")
        for invalidation_index, invalidation in enumerate(record.get("re_entry_history", [])):
            if not is_valid_datetime(invalidation.get("invalidated_at")):
                errors.append(
                    f"{record_path}: schema re_entry_history.{invalidation_index}.invalidated_at: "
                    f"{invalidation.get('invalidated_at')!r} is not a 'date-time'"
                )
    for task_directory in task_directories:
        dispatch_path = confined_path(root, OVERLAY, "runs", task_directory.name, "dispatch-plan.json")
        record_path = confined_path(root, OVERLAY, "runs", task_directory.name, "run-record.json")
        if not dispatch_path.exists() or not record_path.exists():
            continue
        try:
            dispatch = load_json(dispatch_path)
            record = load_json(record_path)
        except (OSError, ValueError, json.JSONDecodeError) as error:
            errors.append(str(error))
            continue
        if jsonschema is not None:
            schema = load_json(CONTRACTS / "selection.schema.json")
            validator = jsonschema.Draft202012Validator(schema, format_checker=jsonschema.FormatChecker())
            for schema_error in validator.iter_errors(dispatch):
                location = ".".join(str(part) for part in schema_error.absolute_path) or "<root>"
                errors.append(f"{dispatch_path}: schema {location}: {schema_error.message}")
        if dispatch.get("task_id") != record.get("task_id") or dispatch.get("task_id") != task_directory.name:
            errors.append(f"{task_directory}: task IDs do not match directory, dispatch, and run record")
        if dispatch.get("inputs", {}).get("task") != record.get("scope"):
            errors.append(f"{task_directory}: dispatch task and run-record scope do not match")
        if dispatch.get("dispatch_fingerprint") != record.get("dispatch_fingerprint"):
            errors.append(f"{task_directory}: dispatch and run-record fingerprints do not match")
        computed_fingerprint = dispatch_fingerprint(dispatch)
        if dispatch.get("dispatch_fingerprint") != computed_fingerprint:
            errors.append(f"{dispatch_path}: stored dispatch fingerprint does not match current dispatch content")
        overlap = set(dispatch.get("agents", {}).get("primary", [])).intersection(dispatch.get("agents", {}).get("reviewers", []))
        if overlap:
            errors.append(f"{dispatch_path}: dispatch author/reviewer overlap: {sorted(overlap)}")
        expected_sequence = dispatch.get("gate_dispatch", [])
        required_ids = [gate.get("id") for gate in dispatch.get("required_quality_gates", [])]
        dispatched_required_ids = [item.get("gate_id") for item in expected_sequence if item.get("status") == "required"]
        dispatched_ignored_ids = [item.get("gate_id") for item in expected_sequence if item.get("status") == "ignored"]
        if required_ids != dispatched_required_ids:
            errors.append(f"{dispatch_path}: required quality gates do not match gate dispatch")
        if dispatch.get("ignored_quality_gates", []) != dispatched_ignored_ids:
            errors.append(f"{dispatch_path}: configured ignored gates do not match gate dispatch")
        if [item.get("gate_id") for item in expected_sequence] != [
            item.get("gate_id") for item in sorted(expected_sequence, key=lambda item: GATE_IDS.index(item.get("gate_id")))
        ]:
            errors.append(f"{dispatch_path}: gate dispatch must be in lexical order")
        execution_gates = record.get("execution_summary", {}).get("gates", {})
        for item in expected_sequence:
            gate_id = item.get("gate_id")
            execution = execution_gates.get(gate_id)
            if item.get("status") == "ignored":
                if not execution or not execution.get("ignored") or not execution.get("ignore_reason"):
                    errors.append(f"{dispatch_path}: ignored {gate_id} lacks explicit execution waiver")
                continue
            if execution and execution.get("ignored"):
                errors.append(f"{dispatch_path}: required {gate_id} is marked ignored in the run record")
            if execution:
                if execution.get("required_tasks") != item.get("tasks"):
                    errors.append(f"{dispatch_path}: {gate_id} task dispatch does not match the lifecycle contract")
                if item.get("artifacts") and execution.get("required_agent_artifacts"):
                    errors.append(f"{dispatch_path}: {gate_id} artifact dispatch does not match configured agents")
    result = {"valid": not errors, "ready": not errors and not blockers, "errors": errors, "blockers": blockers}
    print(json.dumps(result, indent=2))
    if errors:
        return 1
    return 2 if blockers else 0


def gate_status_projection(root: Path, task_id: str) -> dict[str, Any]:
    """Pure, read-only projection of a task's lifecycle-gate state: loads
    `run-record.json`, calls `advance_lifecycle()` against an IN-MEMORY copy
    only, and returns the projected fields below -- it NEVER writes
    `run-record.json` (or anything else) back to disk. This is the sole
    entry point `gate_status.py` (`publish-gate-status`/`list-gate-status`)
    is allowed to use to read task state; that module must never open
    `run-record.json` directly, so every field it could possibly render is
    exactly what is projected here.

    `status()` below is refactored to call this for its own printed summary
    (see that function for why it does not simply reuse this return value
    for its persisted write: it needs the *full* mutated record, not the
    minimal projection, so it performs its own equivalent, deterministic
    load + `advance_lifecycle()` + `write_json()` sequence separately;
    `advance_lifecycle()` is a pure function of `(record, routing)` and
    neither call writes to disk before the other reads, so both computations
    agree)."""
    root = Path(root)
    record = load_json(confined_path(root, OVERLAY, "runs", task_id, "run-record.json"))
    routing = load_overlay(root)[4]
    advance_lifecycle(record, routing)  # in-memory only; caller must never persist `record` from here
    gates = [
        {
            "gate_id": gate["gate_id"],
            "status": gate["status"],
            "applicability": gate["applicability"],
            "required_reentry_gate": gate.get("required_reentry_gate"),
        }
        for gate in record["lifecycle_gates"]
    ]
    return {
        "task_id": task_id,
        "current_phase": derive_current_phase(record),
        "gates": gates,
        "re_entry_history": record["re_entry_history"],
        "classification": record.get("classification"),
    }


def status(args: argparse.Namespace) -> int:
    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    projection = gate_status_projection(root, task_id)
    record_path = confined_path(root, OVERLAY, "runs", task_id, "run-record.json")
    record = load_json(record_path)
    routing = load_overlay(root)[4]
    advance_lifecycle(record, routing)
    write_json(record_path, record)
    print(json.dumps({
        "task_id": task_id,
        "current_phase": projection["current_phase"],
        "gates": projection["gates"],
        "re_entry_history": projection["re_entry_history"],
    }, indent=2))
    return 0


def invalidate(args: argparse.Namespace) -> int:
    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    path = confined_path(root, OVERLAY, "runs", task_id, "run-record.json")
    record = load_json(path)
    start = GATE_IDS.index(args.earliest_gate)
    invalidated = GATE_IDS[start:]
    stamp = now()
    for gate in record["lifecycle_gates"]:
        if gate["gate_id"] in invalidated:
            gate["status"] = "invalidated"
            gate["required_reentry_gate"] = args.earliest_gate
    record["current_lifecycle_phase"] = load_json(CONTRACTS / "lifecycle-gates.json")["gates"][start]["phase"]
    affected_bindings = [
        binding
        for gate in record["lifecycle_gates"][start:]
        for binding in gate.get("artifact_bindings", [])
    ]
    history = {
        "invalidated_at": stamp,
        "actor": args.actor,
        "reason": args.reason,
        "earliest_gate": args.earliest_gate,
        "invalidated_gate_ids": invalidated,
        "affected_artifact_bindings": affected_bindings,
        "superseding_artifact_id": None,
    }
    record["re_entry_history"].append(history)
    for gate in record["lifecycle_gates"][start:]:
        gate["invalidation_history"].append(history)
    write_json(path, record)
    print(json.dumps({"task_id": task_id, "earliest_gate": args.earliest_gate, "invalidated_gate_ids": invalidated}, indent=2))
    return 0


def reenter(args: argparse.Namespace) -> int:
    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    path = confined_path(root, OVERLAY, "runs", task_id, "run-record.json")
    record = load_json(path)
    start = GATE_IDS.index(args.earliest_gate)
    for gate in record["lifecycle_gates"][start:]:
        gate["status"] = "pending"
        gate["required_reentry_gate"] = None
        gate["artifact_bindings"] = []
        gate["evidence_refs"] = []
        gate["human_approvals"] = []
        gate["decided_at"] = None
        # A gate-level source link (record_gitlab_issue_link) sets both
        # gate["evidence_refs"] and record[record_field] as a pair; clearing
        # evidence_refs above without also clearing the paired top-level
        # field here would leave intent_record_id/requirements_baseline_id
        # pointing at now-deleted evidence.
        record_field = RECORD_FIELD_BY_GATE.get(gate["gate_id"])
        if record_field is not None:
            record[record_field] = None
    advance_lifecycle(record, load_overlay(root)[4])
    record.setdefault("re_entry_history", []).append({
        "invalidated_at": now(), "actor": args.actor, "reason": args.reason,
        "earliest_gate": args.earliest_gate, "invalidated_gate_ids": [],
        "affected_artifact_bindings": [], "superseding_artifact_id": None,
    })
    write_json(path, record)
    print(json.dumps({"task_id": task_id, "earliest_gate": args.earliest_gate, "status": "reentered"}, indent=2))
    return 0


def upgrade(args: argparse.Namespace) -> int:
    root = Path(args.root).resolve()
    lock_path = confined_path(root, OVERLAY, "version.lock")
    lock = load_json(lock_path)
    digest = fingerprint(lifecycle_contract())
    changes = []
    if lock.get("kernel_version") != VERSION:
        changes.append({"field": "kernel_version", "from": lock.get("kernel_version"), "to": VERSION})
    if lock.get("contract_digest") != digest:
        changes.append({"field": "contract_digest", "from": lock.get("contract_digest"), "to": digest})
    result = {"status": "changes-available" if changes else "current", "mutation": False, "changes": changes}
    if args.apply:
        lock.update({"plugin_version": VERSION, "kernel_version": VERSION, "contracts": 2, "contract_digest": digest})
        write_json(lock_path, lock)
        result.update({"status": "upgraded", "mutation": True})
    print(json.dumps(result, indent=2))
    return 0


def approve_from_github(args: argparse.Namespace) -> int:
    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    result = record_github_approval(
        root,
        task_id,
        args.gate,
        args.role,
        args.repo,
        args.pr,
        args.review_id,
        args.reviewer_login,
        args.commit_sha,
        args.decided_at,
    )
    print(json.dumps(result, indent=2))
    return 0


def approve_from_github_pr(args: argparse.Namespace) -> int:
    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    _, _, authorities, _, _ = load_overlay(root)
    authority = authorities.get(args.role)
    if not isinstance(authority, dict):
        raise ValueError(f"unknown authority role: {args.role}")
    reviewer_login = args.reviewer_login or authority_github_login(authority)
    if not reviewer_login:
        raise ValueError(f"authority {args.role} has no GitHub login binding and --reviewer-login was not supplied")
    reviews = fetch_github_pr_reviews(args.repo, args.pr)
    review = select_github_review(reviews, reviewer_login, args.commit_sha)
    review_id = review.get("id")
    submitted_at = review.get("submitted_at")
    commit_sha = review.get("commit_id")
    if not isinstance(review_id, int):
        raise ValueError("selected GitHub review is missing a numeric id")
    if not is_valid_datetime(submitted_at):
        raise ValueError("selected GitHub review is missing a valid submitted_at timestamp")
    if not isinstance(commit_sha, str) or not commit_sha:
        raise ValueError("selected GitHub review is missing a commit_id")
    result = record_github_approval(
        root,
        task_id,
        args.gate,
        args.role,
        args.repo,
        args.pr,
        review_id,
        reviewer_login,
        commit_sha,
        submitted_at,
    )
    result["selected_review_id"] = review_id
    result["selected_commit_sha"] = commit_sha
    print(json.dumps(result, indent=2))
    return 0


def approve_from_gitlab(args: argparse.Namespace) -> int:
    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    result = record_gitlab_approval(
        root,
        task_id,
        args.gate,
        args.role,
        args.project_path,
        args.mr_iid,
        args.approval_id,
        args.approver_username,
        args.commit_sha,
        args.decided_at,
    )
    print(json.dumps(result, indent=2))
    return 0


def decide_gate(args: argparse.Namespace) -> int:
    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    result = record_gate_decision(
        root,
        task_id,
        args.gate,
        args.role,
        args.decision,
        args.actor_id,
        args.evidence_uri,
        args.note,
        args.decided_at,
    )
    print(json.dumps(result, indent=2))
    return 0


def approve_from_gitlab_mr(args: argparse.Namespace) -> int:
    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    _, _, authorities, _, _ = load_overlay(root)
    authority = authorities.get(args.role)
    if not isinstance(authority, dict):
        raise ValueError(f"unknown authority role: {args.role}")
    approver_username = args.approver_username or authority_gitlab_username(authority)
    if not approver_username:
        raise ValueError(f"authority {args.role} has no GitLab username binding and --approver-username was not supplied")
    approvals = fetch_gitlab_mr_approvals(args.project_path, args.mr_iid)
    approval = select_gitlab_approval(approvals, approver_username, args.commit_sha)
    approval_id = approval.get("approval_id")
    decided_at = approval.get("decided_at")
    commit_sha = approval.get("commit_sha")
    if not approval_id:
        raise ValueError("selected GitLab approval is missing an approval id")
    if not is_valid_datetime(decided_at):
        raise ValueError("selected GitLab approval is missing a valid decided_at timestamp")
    if not isinstance(commit_sha, str) or not commit_sha:
        raise ValueError("selected GitLab approval is missing a commit sha")
    result = record_gitlab_approval(
        root,
        task_id,
        args.gate,
        args.role,
        args.project_path,
        args.mr_iid,
        str(approval_id),
        approver_username,
        commit_sha,
        decided_at,
    )
    result["selected_approval_id"] = approval_id
    result["selected_commit_sha"] = commit_sha
    print(json.dumps(result, indent=2))
    return 0


def link_intent_from_gitlab_issue(args: argparse.Namespace) -> int:
    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    issue = fetch_gitlab_issue(args.project_path, args.issue_iid)
    result = record_gitlab_issue_link(root, task_id, "G1", args.role, args.project_path, issue)
    print(json.dumps(result, indent=2))
    return 0


def link_requirements_from_gitlab_issue(args: argparse.Namespace) -> int:
    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    issue = fetch_gitlab_issue(args.project_path, args.issue_iid)
    result = record_gitlab_issue_link(root, task_id, "G2", args.role, args.project_path, issue)
    print(json.dumps(result, indent=2))
    return 0


def link_intent_from_github_issue(args: argparse.Namespace) -> int:
    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    issue = fetch_github_issue(args.repo, args.issue_number)
    result = record_github_issue_link(root, task_id, "G1", args.role, args.repo, issue)
    print(json.dumps(result, indent=2))
    return 0


def link_requirements_from_github_issue(args: argparse.Namespace) -> int:
    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    issue = fetch_github_issue(args.repo, args.issue_number)
    result = record_github_issue_link(root, task_id, "G2", args.role, args.repo, issue)
    print(json.dumps(result, indent=2))
    return 0


def cmd_create_gate_issues(args: argparse.Namespace) -> int:
    """Publish gate/approval GitLab tracking issues for a task's lifecycle
    gates (`agentic_sdlc/gate_issues.py`). Strictly orthogonal to the
    approval adapters above -- this never touches human_approvals,
    gate.status, evidence_refs, or disposition; see gate_issues.py's module
    docstring."""
    from . import gate_issues  # local import: see gate_issues.py's own docstring for why

    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    gates = [item.strip() for item in args.gates.split(",") if item.strip()] if args.gates else None
    try:
        result = gate_issues.run(
            root=root,
            task_id=task_id,
            project_path=args.project_path,
            as_bot=args.as_bot,
            gates=gates,
            apply=args.apply,
            plan_digest=args.plan_digest,
            allow_classification=args.allow_classification,
            link_type=args.link_type,
            include_scope=args.include_scope,
            reconcile_assignees=args.reconcile_assignees,
            break_lock=args.break_lock,
            i_know_this_is_mocked=args.i_know_this_is_mocked,
        )
    except gate_issues.GateIssuesBlocked as error:
        print(json.dumps({"error": str(error)}, indent=2), file=sys.stderr)
        return 2
    print(json.dumps(result, indent=2))
    if result.get("refusals") or result.get("drift_detected"):
        return 2
    return 0


def cmd_list_gate_issues(args: argparse.Namespace) -> int:
    from . import gate_issues  # local import: see gate_issues.py's own docstring for why

    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    print(json.dumps(gate_issues.read_ledger(root, task_id), indent=2))
    return 0


def cmd_create_github_gate_issues(args: argparse.Namespace) -> int:
    """Publish gate/approval GitHub tracking issues for a task's lifecycle
    gates (`agentic_sdlc/gate_issues_github.py`) -- the GitHub mirror of
    `create-gate-issues`. Strictly orthogonal to the approval adapters, same
    as `gate_issues.py`; see that module's docstring for the full reasoning."""
    from . import gate_issues_github  # local import: mirrors gate_issues.py's own local-import convention

    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    gates = [item.strip() for item in args.gates.split(",") if item.strip()] if args.gates else None
    try:
        result = gate_issues_github.run(
            root=root,
            task_id=task_id,
            repo=args.repo,
            as_bot=args.as_bot,
            gates=gates,
            apply=args.apply,
            plan_digest=args.plan_digest,
            allow_classification=args.allow_classification,
            include_scope=args.include_scope,
            reconcile_assignees=args.reconcile_assignees,
            allow_public_repo=args.allow_public_repo,
            break_lock=args.break_lock,
            i_know_this_is_mocked=args.i_know_this_is_mocked,
        )
    except gate_issues_github.GateIssuesGithubBlocked as error:
        print(json.dumps({"error": str(error)}, indent=2), file=sys.stderr)
        return 2
    print(json.dumps(result, indent=2))
    if result.get("refusals") or result.get("drift_detected"):
        return 2
    return 0


def cmd_list_github_gate_issues(args: argparse.Namespace) -> int:
    from . import gate_issues_github  # local import: mirrors gate_issues.py's own local-import convention

    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    print(json.dumps(gate_issues_github.read_ledger(root, task_id), indent=2))
    return 0


def cmd_publish_gate_status(args: argparse.Namespace) -> int:
    """Publish/update a one-way, read-only gate-status summary comment on a
    task's GitHub PR or GitLab MR (`agentic_sdlc/gate_status.py`)."""
    from . import gate_status  # local import: see gate_status.py's own docstring for why

    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    try:
        result = gate_status.run(
            root=root,
            task_id=task_id,
            forge=args.forge,
            repo=args.repo,
            pr=args.pr,
            project_path=args.project_path,
            mr_iid=args.mr_iid,
            as_bot=args.as_bot,
            allow_classification=args.allow_classification,
            apply=args.apply,
            break_lock=args.break_lock,
            i_know_this_is_mocked=args.i_know_this_is_mocked,
        )
    except gate_status.GateStatusBlocked as error:
        print(json.dumps({"error": str(error)}, indent=2), file=sys.stderr)
        return 2
    print(json.dumps(result, indent=2))
    return 0


def cmd_list_gate_status(args: argparse.Namespace) -> int:
    from . import gate_status  # local import: see gate_status.py's own docstring for why

    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    print(json.dumps(gate_status.list_ledgers(root, task_id), indent=2))
    return 0


def cmd_request_gate_reviewers(args: argparse.Namespace) -> int:
    """Read-only report -- see gate_reviewers.py's module docstring. There
    is no `--apply` and no write call anywhere in this code path."""
    from . import gate_reviewers  # local import: mirrors gate_issues.py's own local-import convention

    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    gates = [item.strip() for item in args.gates.split(",") if item.strip()] if args.gates else None
    result = gate_reviewers.run(
        root=root,
        task_id=task_id,
        repo=args.repo,
        pr=args.pr,
        as_bot=args.as_bot,
        gates=gates,
        allow_classification=args.allow_classification,
    )
    print(json.dumps(result, indent=2))
    has_problem = any(item["classification"] in gate_reviewers.PROBLEM_CLASSIFICATIONS for item in result["reviewers"])
    if result.get("refusals") or has_problem:
        return 2
    return 0


def cmd_request_gate_reviewers_gitlab(args: argparse.Namespace) -> int:
    """Read-only report -- see gate_reviewers_gitlab.py's module docstring.
    There is no `--apply` and no write call anywhere in this code path."""
    from . import gate_reviewers_gitlab  # local import: mirrors gate_issues.py's own local-import convention

    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    gates = [item.strip() for item in args.gates.split(",") if item.strip()] if args.gates else None
    result = gate_reviewers_gitlab.run(
        root=root,
        task_id=task_id,
        project_path=args.project_path,
        mr_iid=args.mr_iid,
        as_bot=args.as_bot,
        gates=gates,
        allow_classification=args.allow_classification,
    )
    print(json.dumps(result, indent=2))
    has_problem = any(
        item["classification"] in gate_reviewers_gitlab.PROBLEM_CLASSIFICATIONS for item in result["reviewers"]
    )
    if result.get("refusals") or has_problem:
        return 2
    return 0


def cmd_publish_reviewer_nudge(args: argparse.Namespace) -> int:
    """Publish/update an advisory, GitHub-only reviewer-nudge comment
    (`agentic_sdlc/reviewer_nudge.py`) -- never a review request, see that
    module's own docstring."""
    from . import reviewer_nudge  # local import: mirrors gate_status.py's own local-import convention

    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    gates = [item.strip() for item in args.gates.split(",") if item.strip()] if args.gates else None
    try:
        result = reviewer_nudge.run(
            root=root,
            task_id=task_id,
            repo=args.repo,
            pr=args.pr,
            as_bot=args.as_bot,
            gates=gates,
            allow_classification=args.allow_classification,
            apply=args.apply,
            break_lock=args.break_lock,
            i_know_this_is_mocked=args.i_know_this_is_mocked,
        )
    except reviewer_nudge.ReviewerNudgeBlocked as error:
        print(json.dumps({"error": str(error)}, indent=2), file=sys.stderr)
        return 2
    print(json.dumps(result, indent=2))
    return 0


def cmd_list_reviewer_nudge(args: argparse.Namespace) -> int:
    from . import reviewer_nudge  # local import: mirrors gate_status.py's own local-import convention

    root = Path(args.root).resolve()
    task_id = safe_task_id(args.task_id)
    print(json.dumps(reviewer_nudge.read_ledger(root, task_id), indent=2))
    return 0


def provider_introspection(args: argparse.Namespace) -> int:
    if args.resource_kind == "provider":
        if args.action == "list":
            print(json.dumps(LOADED_PROVIDERS, indent=2))
        else:
            provider = next((item for item in LOADED_PROVIDERS if item["id"] == args.provider_id), None)
            if provider is None:
                raise ValueError(f"unknown loaded provider: {args.provider_id}")
            print(json.dumps(provider, indent=2))
        return 0
    if args.resource_kind == "profile":
        print(json.dumps(sorted(profile_ids()), indent=2))
        return 0
    print(json.dumps(sorted(extension_ids()), indent=2))
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="agentic-sdlc", description=__doc__)
    parser.add_argument("--version", action="version", version=VERSION)
    parser.add_argument(
        "--provider",
        action="append",
        default=[],
        help="Load a versioned external profile, catalog, and extension provider manifest",
    )
    subparsers = parser.add_subparsers(dest="command", required=True)
    detect = subparsers.add_parser("detect", help="Detect repository stack without changing files")
    detect.add_argument("--root", default=".")
    detect.set_defaults(handler=lambda args: (print(json.dumps(detect_repository(Path(args.root)), indent=2)) or 0))
    init = subparsers.add_parser("init", help="Initialize a conservative project overlay")
    init.add_argument("--root", default=".")
    init.add_argument("--profile", default=None, help="Provider profile id; omit for kernel-only lifecycle operation")
    init.add_argument("--extension", action="append", help="Enable an impact-profile extension by id (resolved at init time; see EXTENSIONS_SEARCH_PATH)")
    init.add_argument("--project-id")
    init.add_argument("--classification", default="internal")
    init.add_argument("--runner", choices=["codex", "claude", "both"], default="both", help="Which agent runner(s) to generate subagent wrappers for")
    init_mode = init.add_mutually_exclusive_group()
    init_mode.add_argument("--force", action="store_true", help="Reserved for future use; in this release init never overwrites existing wrapper or managed overlay files, with or without --force")
    init_mode.add_argument("--dry-run", action="store_true", help="Report what init would create without writing anything to disk")
    init.add_argument("--repair", action="store_true", help="Inspect an existing initialization and plan safe repairs; use --apply to make them")
    init.add_argument("--apply", action="store_true", help="Apply repairs selected by init --repair")
    init.set_defaults(handler=initialize)
    repair_parser = subparsers.add_parser("repair", help="Inspect or safely repair an existing initialization")
    repair_parser.add_argument("--root", default=".")
    repair_parser.add_argument("--runner", choices=["codex", "claude", "both"], default="both", help="Which missing wrapper set(s) to recreate")
    repair_parser.add_argument("--apply", action="store_true", help="Apply the safe repair plan; default is read-only inspection")
    repair_parser.set_defaults(handler=repair)
    plan = subparsers.add_parser("plan", help="Create a dispatch plan and pending run record")
    plan.add_argument("--root", default=".")
    plan.add_argument("--task-id", required=True)
    plan.add_argument("--task", required=True)
    plan.set_defaults(handler=plan_task)
    validate = subparsers.add_parser("validate", help="Validate configuration and fail closed on unresolved decisions")
    validate.add_argument("--root", default=".")
    validate.set_defaults(handler=validate_repository)
    show = subparsers.add_parser("status", help="Show a task's gate state")
    show.add_argument("--root", default=".")
    show.add_argument("--task-id", required=True)
    show.set_defaults(handler=status)
    approve = subparsers.add_parser("approve-from-github", help="Record a human gate approval from a GitHub PR review")
    approve.add_argument("--root", default=".")
    approve.add_argument("--task-id", required=True)
    approve.add_argument("--gate", choices=GATE_IDS, required=True)
    approve.add_argument("--role", choices=sorted(AUTHORITY_ROLES), required=True, help="Authority role recording the approval")
    approve.add_argument("--repo", required=True, help="GitHub repository in owner/name form")
    approve.add_argument("--pr", type=int, required=True, help="Pull request number")
    approve.add_argument("--review-id", type=int, required=True, help="GitHub review identifier")
    approve.add_argument("--reviewer-login", required=True, help="GitHub login that authored the review")
    approve.add_argument("--commit-sha", required=True, help="Commit SHA reviewed by the GitHub approval")
    approve.add_argument("--decided-at", help="Approval time in RFC 3339 format; defaults to now")
    approve.set_defaults(handler=approve_from_github)
    approve_auto = subparsers.add_parser("approve-from-github-pr", help="Fetch an approved GitHub PR review and record it as human gate approval evidence")
    approve_auto.add_argument("--root", default=".")
    approve_auto.add_argument("--task-id", required=True)
    approve_auto.add_argument("--gate", choices=GATE_IDS, required=True)
    approve_auto.add_argument("--role", choices=sorted(AUTHORITY_ROLES), required=True, help="Authority role recording the approval")
    approve_auto.add_argument("--repo", required=True, help="GitHub repository in owner/name form")
    approve_auto.add_argument("--pr", type=int, required=True, help="Pull request number")
    approve_auto.add_argument("--reviewer-login", help="GitHub login to match; defaults to the authority GitHub binding")
    approve_auto.add_argument("--commit-sha", help="Optional commit SHA to require when selecting an approved review")
    approve_auto.set_defaults(handler=approve_from_github_pr)
    approve_gitlab = subparsers.add_parser("approve-from-gitlab", help="Record a human gate approval from a GitLab MR approval")
    approve_gitlab.add_argument("--root", default=".")
    approve_gitlab.add_argument("--task-id", required=True)
    approve_gitlab.add_argument("--gate", choices=GATE_IDS, required=True)
    approve_gitlab.add_argument("--role", choices=sorted(AUTHORITY_ROLES), required=True, help="Authority role recording the approval")
    approve_gitlab.add_argument("--project-path", required=True, help="GitLab project path (namespace/project)")
    approve_gitlab.add_argument("--mr-iid", type=int, required=True, help="Merge request internal ID (iid)")
    approve_gitlab.add_argument("--approval-id", required=True, help="GitLab approval identifier")
    approve_gitlab.add_argument("--approver-username", required=True, help="GitLab username that authored the approval")
    approve_gitlab.add_argument("--commit-sha", required=True, help="Commit SHA reviewed by the GitLab approval")
    approve_gitlab.add_argument("--decided-at", help="Approval time in RFC 3339 format; defaults to now")
    approve_gitlab.set_defaults(handler=approve_from_gitlab)
    approve_gitlab_auto = subparsers.add_parser("approve-from-gitlab-mr", help="Fetch an approved GitLab MR approval and record it as human gate approval evidence")
    approve_gitlab_auto.add_argument("--root", default=".")
    approve_gitlab_auto.add_argument("--task-id", required=True)
    approve_gitlab_auto.add_argument("--gate", choices=GATE_IDS, required=True)
    approve_gitlab_auto.add_argument("--role", choices=sorted(AUTHORITY_ROLES), required=True, help="Authority role recording the approval")
    approve_gitlab_auto.add_argument("--project-path", required=True, help="GitLab project path (namespace/project)")
    approve_gitlab_auto.add_argument("--mr-iid", type=int, required=True, help="Merge request internal ID (iid)")
    approve_gitlab_auto.add_argument("--approver-username", help="GitLab username to match; defaults to the authority GitLab binding")
    approve_gitlab_auto.add_argument("--commit-sha", help="Optional commit SHA to require when selecting an approved approval")
    approve_gitlab_auto.set_defaults(handler=approve_from_gitlab_mr)
    link_intent = subparsers.add_parser("link-intent-from-gitlab-issue", help="Link a GitLab issue as the recorded source for G1 Intent")
    link_intent.add_argument("--root", default=".")
    link_intent.add_argument("--task-id", required=True)
    link_intent.add_argument("--role", choices=sorted(AUTHORITY_ROLES), required=True, help="Authority role recording the link")
    link_intent.add_argument("--project-path", required=True, help="GitLab project path (namespace/project)")
    link_intent.add_argument("--issue-iid", type=int, required=True, help="Issue internal ID (iid)")
    link_intent.set_defaults(handler=link_intent_from_gitlab_issue)
    link_requirements = subparsers.add_parser("link-requirements-from-gitlab-issue", help="Link a GitLab issue as the recorded source for G2 Requirements Baseline")
    link_requirements.add_argument("--root", default=".")
    link_requirements.add_argument("--task-id", required=True)
    link_requirements.add_argument("--role", choices=sorted(AUTHORITY_ROLES), required=True, help="Authority role recording the link")
    link_requirements.add_argument("--project-path", required=True, help="GitLab project path (namespace/project)")
    link_requirements.add_argument("--issue-iid", type=int, required=True, help="Issue internal ID (iid)")
    link_requirements.set_defaults(handler=link_requirements_from_gitlab_issue)
    link_intent_github = subparsers.add_parser("link-intent-from-github-issue", help="Link a GitHub issue as the recorded source for G1 Intent")
    link_intent_github.add_argument("--root", default=".")
    link_intent_github.add_argument("--task-id", required=True)
    link_intent_github.add_argument("--role", choices=sorted(AUTHORITY_ROLES), required=True, help="Authority role recording the link")
    link_intent_github.add_argument("--repo", required=True, help="GitHub repository in owner/name form")
    link_intent_github.add_argument("--issue-number", type=int, required=True, help="Issue number")
    link_intent_github.set_defaults(handler=link_intent_from_github_issue)
    link_requirements_github = subparsers.add_parser("link-requirements-from-github-issue", help="Link a GitHub issue as the recorded source for G2 Requirements Baseline")
    link_requirements_github.add_argument("--root", default=".")
    link_requirements_github.add_argument("--task-id", required=True)
    link_requirements_github.add_argument("--role", choices=sorted(AUTHORITY_ROLES), required=True, help="Authority role recording the link")
    link_requirements_github.add_argument("--repo", required=True, help="GitHub repository in owner/name form")
    link_requirements_github.add_argument("--issue-number", type=int, required=True, help="Issue number")
    link_requirements_github.set_defaults(handler=link_requirements_from_github_issue)
    create_gate_issues = subparsers.add_parser(
        "create-gate-issues", help="Publish GitLab gate/approval tracking issues for a task's lifecycle gates"
    )
    create_gate_issues.add_argument("--root", default=".")
    create_gate_issues.add_argument("--task-id", required=True)
    create_gate_issues.add_argument("--project-path", required=True, help="GitLab project path (namespace/project)")
    create_gate_issues.add_argument(
        "--as-bot", required=True, help="Required GitLab bot/machine username; verified via `glab api user`"
    )
    create_gate_issues.add_argument(
        "--gates", default=None, help="Comma-separated gate ids, e.g. G1,G3,G9; default = all eligible gates"
    )
    gate_issues_mode = create_gate_issues.add_mutually_exclusive_group()
    gate_issues_mode.add_argument("--dry-run", dest="apply", action="store_false", help="Default: print the plan digest only")
    gate_issues_mode.add_argument("--apply", dest="apply", action="store_true", help="Actually create/reuse issues")
    create_gate_issues.set_defaults(apply=False)
    create_gate_issues.add_argument("--plan-digest", default=None, help="Required with --apply (from a prior --dry-run)")
    create_gate_issues.add_argument(
        "--allow-classification", default=None,
        help="Must exactly match the task's run-record classification -- no default",
    )
    create_gate_issues.add_argument(
        "--link-type", choices=["relates_to"], default=None,
        help="Opt-in: also call the GitLab Issue Links API; fails closed (exit 2) if unavailable",
    )
    create_gate_issues.add_argument(
        "--include-scope", action="store_true", help="Add a sanitized scope line to gate issue descriptions (default off)"
    )
    create_gate_issues.add_argument(
        "--reconcile-assignees", action="store_true",
        help="Overwrite GitLab's assignee to match authorities.json on drift (default: report only)",
    )
    create_gate_issues.add_argument("--break-lock", action="store_true", help="Explicitly override a held lock file")
    create_gate_issues.add_argument(
        "--i-know-this-is-mocked", action="store_true",
        help="Required alongside --apply whenever AGENTIC_SDLC_TEST_ISSUE_CREATE_FILE is set",
    )
    create_gate_issues.set_defaults(handler=cmd_create_gate_issues)
    list_gate_issues = subparsers.add_parser("list-gate-issues", help="Print the gate-issues sidecar ledger for a task")
    list_gate_issues.add_argument("--root", default=".")
    list_gate_issues.add_argument("--task-id", required=True)
    list_gate_issues.set_defaults(handler=cmd_list_gate_issues)
    create_github_gate_issues = subparsers.add_parser(
        "create-github-gate-issues", help="Publish GitHub gate/approval tracking issues for a task's lifecycle gates"
    )
    create_github_gate_issues.add_argument("--root", default=".")
    create_github_gate_issues.add_argument("--task-id", required=True)
    create_github_gate_issues.add_argument("--repo", required=True, help="GitHub repository (owner/name)")
    create_github_gate_issues.add_argument(
        "--as-bot", required=True, help="Required GitHub bot/machine login; verified via `gh api user`"
    )
    create_github_gate_issues.add_argument(
        "--gates", default=None, help="Comma-separated gate ids, e.g. G1,G3,G9; default = all eligible gates"
    )
    gate_issues_github_mode = create_github_gate_issues.add_mutually_exclusive_group()
    gate_issues_github_mode.add_argument("--dry-run", dest="apply", action="store_false", help="Default: print the plan digest only")
    gate_issues_github_mode.add_argument("--apply", dest="apply", action="store_true", help="Actually create/reuse issues")
    create_github_gate_issues.set_defaults(apply=False)
    create_github_gate_issues.add_argument("--plan-digest", default=None, help="Required with --apply (from a prior --dry-run)")
    create_github_gate_issues.add_argument(
        "--allow-classification", default=None,
        help="Must exactly match the task's run-record classification -- no default",
    )
    create_github_gate_issues.add_argument(
        "--include-scope", action="store_true", help="Add a sanitized scope line to gate issue descriptions (default off)"
    )
    create_github_gate_issues.add_argument(
        "--reconcile-assignees", action="store_true",
        help="Overwrite GitHub's assignee to match authorities.json on drift (default: report only)",
    )
    create_github_gate_issues.add_argument(
        "--allow-public-repo", action="store_true",
        help="Required to proceed if the target repository is public (GitHub issues have no per-issue "
        "confidential flag; see gate_issues_github.py)",
    )
    create_github_gate_issues.add_argument("--break-lock", action="store_true", help="Explicitly override a held lock file")
    create_github_gate_issues.add_argument(
        "--i-know-this-is-mocked", action="store_true",
        help="Required alongside --apply whenever AGENTIC_SDLC_TEST_GITHUB_READ_FILE or "
        "AGENTIC_SDLC_TEST_GITHUB_ISSUE_FILE is set",
    )
    create_github_gate_issues.set_defaults(handler=cmd_create_github_gate_issues)
    list_github_gate_issues = subparsers.add_parser(
        "list-github-gate-issues", help="Print the GitHub gate-issues sidecar ledger for a task"
    )
    list_github_gate_issues.add_argument("--root", default=".")
    list_github_gate_issues.add_argument("--task-id", required=True)
    list_github_gate_issues.set_defaults(handler=cmd_list_github_gate_issues)
    publish_gate_status = subparsers.add_parser(
        "publish-gate-status",
        help="Post or update a one-way, read-only gate-status summary comment on a task's GitHub PR or GitLab MR",
    )
    publish_gate_status.add_argument("--root", default=".")
    publish_gate_status.add_argument("--task-id", required=True)
    publish_gate_status.add_argument("--forge", choices=["github", "gitlab"], required=True, help="Never inferred")
    publish_gate_status.add_argument("--repo", default=None, help="GitHub repository in owner/name form; required iff --forge github")
    publish_gate_status.add_argument("--pr", type=int, default=None, help="Pull request number; required iff --forge github")
    publish_gate_status.add_argument("--project-path", default=None, help="GitLab project path (namespace/project); required iff --forge gitlab")
    publish_gate_status.add_argument("--mr-iid", type=int, default=None, help="Merge request internal ID (iid); required iff --forge gitlab")
    publish_gate_status.add_argument(
        "--as-bot", required=True, help="Required bot/machine username; verified via `gh api user` / `glab api user`"
    )
    publish_gate_status.add_argument(
        "--allow-classification", default=None,
        help="Must exactly match the task's run-record classification -- no default",
    )
    gate_status_mode = publish_gate_status.add_mutually_exclusive_group()
    gate_status_mode.add_argument("--dry-run", dest="apply", action="store_false", help="Default: print the body and resolved action only")
    gate_status_mode.add_argument("--apply", dest="apply", action="store_true", help="Actually create/update the comment")
    publish_gate_status.set_defaults(apply=False)
    publish_gate_status.add_argument("--break-lock", action="store_true", help="Explicitly override a held lock file")
    publish_gate_status.add_argument(
        "--i-know-this-is-mocked", action="store_true",
        help="Required alongside --apply whenever a mock backend env var is set",
    )
    publish_gate_status.set_defaults(handler=cmd_publish_gate_status)
    list_gate_status = subparsers.add_parser(
        "list-gate-status", help="Print the gate-status sidecar ledger(s) for a task (both forges, zero network)"
    )
    list_gate_status.add_argument("--root", default=".")
    list_gate_status.add_argument("--task-id", required=True)
    list_gate_status.set_defaults(handler=cmd_list_gate_status)
    request_gate_reviewers = subparsers.add_parser(
        "request-gate-reviewers",
        help=(
            "Report which GitHub logins would be requested as PR reviewers for a task's lifecycle gates "
            "-- read-only/reporting only in this version, never posts a review request"
        ),
    )
    request_gate_reviewers.add_argument("--root", default=".")
    request_gate_reviewers.add_argument("--task-id", required=True)
    request_gate_reviewers.add_argument("--repo", required=True, help="GitHub repository in owner/name form")
    request_gate_reviewers.add_argument("--pr", type=int, required=True, help="Pull request number; never auto-discovered")
    request_gate_reviewers.add_argument(
        "--as-bot", required=True, help="Required GitHub bot/machine login; verified via `gh api user`"
    )
    request_gate_reviewers.add_argument(
        "--gates", default=None, help="Comma-separated gate ids, e.g. G1,G3,G9; default = all eligible gates"
    )
    request_gate_reviewers.add_argument(
        "--allow-classification", default=None,
        help="Must exactly match the task's run-record classification -- no default",
    )
    request_gate_reviewers.set_defaults(handler=cmd_request_gate_reviewers)
    request_gate_reviewers_gitlab = subparsers.add_parser(
        "request-gate-reviewers-gitlab",
        help=(
            "Report which GitLab usernames would be set as MR reviewers for a task's lifecycle gates "
            "-- read-only/reporting only, never sets reviewer_ids"
        ),
    )
    request_gate_reviewers_gitlab.add_argument("--root", default=".")
    request_gate_reviewers_gitlab.add_argument("--task-id", required=True)
    request_gate_reviewers_gitlab.add_argument(
        "--project-path", required=True, help="GitLab project path (namespace/project)"
    )
    request_gate_reviewers_gitlab.add_argument(
        "--mr-iid", type=int, required=True, help="Merge request internal ID (iid); never auto-discovered"
    )
    request_gate_reviewers_gitlab.add_argument(
        "--as-bot", required=True, help="Required GitLab bot/machine username; verified via `glab api user`"
    )
    request_gate_reviewers_gitlab.add_argument(
        "--gates", default=None, help="Comma-separated gate ids, e.g. G1,G3,G9; default = all eligible gates"
    )
    request_gate_reviewers_gitlab.add_argument(
        "--allow-classification", default=None,
        help="Must exactly match the task's run-record classification -- no default",
    )
    request_gate_reviewers_gitlab.set_defaults(handler=cmd_request_gate_reviewers_gitlab)
    decide = subparsers.add_parser("decide", help="Record a human decision (approved/rejected/request-changes) for a lifecycle gate")
    decide.add_argument("--root", default=".")
    decide.add_argument("--task-id", required=True)
    decide.add_argument("--gate", choices=GATE_IDS, required=True)
    decide.add_argument("--role", choices=sorted(AUTHORITY_ROLES), required=True)
    decide.add_argument("--decision", choices=["approved", "rejected", "request-changes"], required=True)
    decide.add_argument("--actor-id", required=True, help="Identity recording the decision; must match the assigned authority for --role")
    decide.add_argument("--evidence-uri", required=True, help="External evidence reference for this decision")
    decide.add_argument("--note", help="Optional human-readable rationale")
    decide.add_argument("--decided-at", help="RFC 3339 timestamp; defaults to now")
    decide.set_defaults(handler=decide_gate)

    invalid = subparsers.add_parser("invalidate", help="Invalidate the earliest affected gate and all downstream gates")
    invalid.add_argument("--root", default=".")
    invalid.add_argument("--task-id", required=True)
    invalid.add_argument("--earliest-gate", choices=GATE_IDS, required=True)
    invalid.add_argument("--reason", required=True)
    invalid.add_argument("--actor", required=True, help="Accountable identity recording the invalidation")
    invalid.set_defaults(handler=invalidate)
    reentry = subparsers.add_parser("reenter", help="Prepare an invalidated run for explicit re-entry")
    reentry.add_argument("--root", default=".")
    reentry.add_argument("--task-id", required=True)
    reentry.add_argument("--earliest-gate", choices=GATE_IDS, required=True)
    reentry.add_argument("--reason", required=True)
    reentry.add_argument("--actor", required=True)
    reentry.set_defaults(handler=reenter)
    upgrade_parser = subparsers.add_parser("upgrade", help="Check or apply a non-destructive kernel lock upgrade")
    upgrade_parser.add_argument("--root", default=".")
    mode = upgrade_parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--check", action="store_true")
    mode.add_argument("--apply", action="store_true")
    upgrade_parser.set_defaults(handler=upgrade)
    publish_reviewer_nudge = subparsers.add_parser(
        "publish-reviewer-nudge",
        help=(
            "Post or update an advisory GitHub PR comment suggesting reviewers, based on "
            "request-gate-reviewers's classification -- never a review request, never notifies anyone"
        ),
    )
    publish_reviewer_nudge.add_argument("--root", default=".")
    publish_reviewer_nudge.add_argument("--task-id", required=True)
    publish_reviewer_nudge.add_argument("--repo", required=True, help="GitHub repository in owner/name form")
    publish_reviewer_nudge.add_argument("--pr", type=int, required=True, help="Pull request number; never auto-discovered")
    publish_reviewer_nudge.add_argument(
        "--as-bot", required=True, help="Required GitHub bot/machine login; verified via `gh api user`"
    )
    publish_reviewer_nudge.add_argument(
        "--gates", default=None, help="Comma-separated gate ids, e.g. G1,G3,G9; default = all eligible gates"
    )
    publish_reviewer_nudge.add_argument(
        "--allow-classification", default=None,
        help="Must exactly match the task's run-record classification -- no default",
    )
    reviewer_nudge_mode = publish_reviewer_nudge.add_mutually_exclusive_group()
    reviewer_nudge_mode.add_argument("--dry-run", dest="apply", action="store_false", help="Default: print the body and resolved action only")
    reviewer_nudge_mode.add_argument("--apply", dest="apply", action="store_true", help="Actually create/update the comment")
    publish_reviewer_nudge.set_defaults(apply=False)
    publish_reviewer_nudge.add_argument("--break-lock", action="store_true", help="Explicitly override a held lock file")
    publish_reviewer_nudge.add_argument(
        "--i-know-this-is-mocked", action="store_true",
        help="Required alongside --apply whenever a mock backend env var is set",
    )
    publish_reviewer_nudge.set_defaults(handler=cmd_publish_reviewer_nudge)
    list_reviewer_nudge = subparsers.add_parser(
        "list-reviewer-nudge", help="Print the reviewer-nudge sidecar ledger for a task (GitHub only, zero network)"
    )
    list_reviewer_nudge.add_argument("--root", default=".")
    list_reviewer_nudge.add_argument("--task-id", required=True)
    list_reviewer_nudge.set_defaults(handler=cmd_list_reviewer_nudge)
    for kind in ("provider", "profile", "extension"):
        group = subparsers.add_parser(kind, help=f"Inspect loaded {kind} resources")
        group.add_argument("action", choices=["list", "inspect"] if kind == "provider" else ["list"])
        group.add_argument("--root", default=".", help=argparse.SUPPRESS)
        group.set_defaults(resource_kind=kind)
        if kind == "provider":
            group.add_argument("provider_id", nargs="?")
        group.set_defaults(handler=lambda args, kind=kind: provider_introspection(args))
    contract = subparsers.add_parser("show-contract", help="Print a bundled lifecycle contract as JSON")
    contract.add_argument(
        "name",
        choices=["artifact.schema", "agent-catalog.schema", "dispatch-bindings.schema", "extension.schema", "lifecycle-gates", "mutation-gates", "profile.schema", "provider.schema", "run-record.schema", "selection.schema"],
    )
    contract.set_defaults(
        handler=lambda args: (
            print((CONTRACTS / f"{args.name}.json").read_text(encoding="utf-8").rstrip()) or 0
        )
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    try:
        args = build_parser().parse_args(argv)
        for provider in args.provider:
            load_provider(provider)
        return int(args.handler(args))
    except (OSError, ValueError, json.JSONDecodeError) as error:
        print(json.dumps({"error": str(error)}, indent=2), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
