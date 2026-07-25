"""Build the stable version 2 agent dispatch-plan document."""

from __future__ import annotations

import hashlib
import json
import re
from datetime import datetime, timezone
from functools import lru_cache
from pathlib import Path
from typing import Any, Iterable

from risk_classifier import apply_cross_stack, classify_risks
from routing import _keyword_matches, match_routes
from agentic_sdlc_contracts import require_lifecycle_contract, try_lifecycle_contract

CLASSIFICATIONS = {"public", "internal", "confidential", "restricted"}
MAXIMUM_KNOWLEDGE_TOP = 20
KNOWLEDGE_STORE_ROOT = Path(__file__).resolve().parents[2] / "knowledge-store"
DEFAULT_KNOWLEDGE_SOURCE = "secure-cloud-agents"
STANDALONE_REASON = "Agentic SDLC executable not found; team dispatch is unaffected."
# Agentic SDLC's lifecycle-gates contract only names each gate's
# required_contributions *slots* (e.g. "intent", "architecture-design"); it
# does not itself carry per-gate agents/tasks/artifacts. Those live in this
# repo's own provider profile, hand-maintained (not generated), keyed by
# gate id -> contributions -> slot. "secure-cloud" extends "generic" but
# currently leaves gate_bindings empty, so generic's bindings are
# authoritative; if secure-cloud ever adds its own, merge it in here too.
# This module runs from two different locations, so the plugin root has to
# be found relative to whichever one is currently loaded: the canonical
# source (agents/orchestration/src/) or the packaged, self-contained mirror
# (plugins/secure-cloud-agents/suite/agents/orchestration/src/).
_AGENTS_ROOT = Path(__file__).resolve().parents[2]
_PLUGIN_ROOT = (
    _AGENTS_ROOT.parent.parent
    if _AGENTS_ROOT.parent.name == "suite"
    else _AGENTS_ROOT.parent / "plugins" / "secure-cloud-agents"
)
GATE_BINDINGS_PROFILE_PATH = _PLUGIN_ROOT / "profiles" / "generic" / "profile.json"


def _default_knowledge_source() -> str:
    return DEFAULT_KNOWLEDGE_SOURCE


@lru_cache(maxsize=1)
def _gate_bindings() -> dict[str, Any]:
    profile = json.loads(GATE_BINDINGS_PROFILE_PATH.read_text(encoding="utf-8"))
    return profile.get("gate_bindings", {})


def _gate_contribution_totals(
    gate_id: str, contract: dict[str, Any], key: str
) -> list[str]:
    """Union of one field (agents/tasks/artifacts) across a gate's bound contribution slots."""
    contributions = _gate_bindings().get(gate_id, {}).get("contributions", {})
    return _unique(
        value
        for slot in contract.get("required_contributions", [])
        for value in contributions.get(slot, {}).get(key, [])
    )


def _lifecycle_gates(require_sdlc: bool) -> list[dict[str, Any]] | None:
    contract = require_lifecycle_contract() if require_sdlc else try_lifecycle_contract()
    if contract is None:
        return None
    gates = contract.get("gates", [])
    if not gates or any(not isinstance(gate, dict) or not gate.get("id") for gate in gates):
        raise ValueError("Agentic SDLC lifecycle contract must contain identified gates")
    return gates


def _gate_order(gates: list[dict[str, Any]] | None) -> list[str] | None:
    return None if gates is None else [gate["id"] for gate in gates]


def _unique(values: Iterable[str]) -> list[str]:
    return list(dict.fromkeys(values))


def _gate_dispatch(
    configured: list[str], ignored: list[str], gates: list[dict[str, Any]] | None
) -> tuple[list[str], list[dict[str, Any]], list[str]]:
    if not configured:
        return [], [], []
    if gates is None:
        ignored_set = set(ignored).intersection(configured)
        effective = [gate_id for gate_id in configured if gate_id not in ignored_set]
        ignored_list = [gate_id for gate_id in configured if gate_id in ignored_set]
        return effective, [], ignored_list
    gate_ids = _gate_order(gates)
    unknown = set(ignored) - set(gate_ids)
    if unknown:
        raise ValueError(f"ignored_gates contains unknown lifecycle gates: {sorted(unknown)}")
    unknown = set(configured) - set(gate_ids)
    if unknown:
        raise ValueError(f"routing references unknown lifecycle gates: {sorted(unknown)}")
    sequence = gate_ids[: max(gate_ids.index(gate_id) for gate_id in configured) + 1]
    ignored_set = set(ignored).intersection(sequence)
    contracts = {gate["id"]: gate for gate in gates}
    dispatch = []
    for gate_id in sequence:
        contract = contracts[gate_id]
        dispatch.append({
            "gate_id": gate_id,
            "status": "ignored" if gate_id in ignored_set else "required",
            "agents": _gate_contribution_totals(gate_id, contract, "agents"),
            "tasks": _gate_contribution_totals(gate_id, contract, "tasks"),
            "artifacts": _gate_contribution_totals(gate_id, contract, "artifacts"),
        })
    return [gate_id for gate_id in sequence if gate_id not in ignored_set], dispatch, sorted(ignored_set, key=gate_ids.index)


def _gate_agents(configured: list[str], ignored: list[str], gates: list[dict[str, Any]] | None) -> list[str]:
    if gates is None or not configured:
        return []
    gate_ids = _gate_order(gates)
    unknown = set(ignored) - set(gate_ids)
    if unknown:
        raise ValueError(f"ignored_gates contains unknown lifecycle gates: {sorted(unknown)}")
    unknown = set(configured) - set(gate_ids)
    if unknown:
        raise ValueError(f"routing references unknown lifecycle gates: {sorted(unknown)}")
    sequence = gate_ids[: max(gate_ids.index(gate_id) for gate_id in configured) + 1]
    ignored_set = set(ignored).intersection(sequence)
    contracts = {gate["id"]: gate for gate in gates}
    return _unique(
        agent
        for gate_id in sequence
        if gate_id not in ignored_set
        for agent in _gate_contribution_totals(gate_id, contracts[gate_id], "agents")
    )


def _ordered(values: Iterable[str], catalog: list[str]) -> list[str]:
    positions = {agent: index for index, agent in enumerate(catalog)}
    return sorted(_unique(values), key=lambda agent: positions.get(agent, len(catalog)))


def _reasons(match: dict[str, Any]) -> dict[str, Any]:
    return {
        "keywords": match["reasons"]["keywords"],
        "paths": match["reasons"]["paths"],
    }


def _select_workflow(route_ids: list[str], risk_ids: list[str], has_agents: bool) -> str:
    if not has_agents:
        return "needs-triage"
    if "production" in risk_ids:
        return "production-release"
    if "support" in route_ids or "incident-response" in route_ids:
        return "support-escalation"
    if "runtime-assurance" in route_ids:
        return "runtime-assurance"
    if "knowledge-store" in route_ids and all(
        route_id in {"knowledge-store", "documentation", "testing"} for route_id in route_ids
    ):
        return "knowledge-ingestion"
    if "agent-suite-governance" in route_ids:
        return "debugging"
    if "debugging" in route_ids:
        return "debugging"
    product_intake_routes = {
        "product-intent",
        "requirements-baseline",
        "documentation",
        "testing",
    }
    if (
        any(route_id in {"product-intent", "requirements-baseline"} for route_id in route_ids)
        and all(route_id in product_intake_routes for route_id in route_ids)
        and "architecture-change" not in risk_ids
    ):
        return "product-intake"
    if "infrastructure" in route_ids and not any(
        route_id in {"frontend", "backend", "pipeline"} for route_id in route_ids
    ):
        return "infrastructure-change"
    if "pipeline" in route_ids and not any(
        route_id in {"frontend", "backend", "infrastructure"} for route_id in route_ids
    ):
        return "pipeline-change"
    return "new-service"


def _build_human_gates(risks: list[dict[str, Any]]) -> list[dict[str, Any]]:
    descriptions = {
        "persistent-database-migration": "An authorized human must approve persistent database migrations.",
        "production-change": "An authorized human must approve the exact production change and target.",
        "destructive-action": "An authorized human must approve the exact destructive action and recovery plan.",
        "accountable-human-escalation": "An accountable human owner or approval group must make the requested decision.",
        "privileged-identity-change": "An authorized human must approve privileged identity, credential, or break-glass changes.",
    }
    gate_ids = _unique(
        risk["rule"].get("human_gate")
        for risk in risks
        if risk["rule"].get("human_gate")
    )
    return [
        {
            "id": gate_id,
            "required": True,
            "reason": descriptions.get(gate_id, "An authorized human decision is required."),
        }
        for gate_id in gate_ids
    ]


def _build_teams(
    config: dict[str, Any],
    matched_routes: list[dict[str, Any]],
    selected_agents: list[str],
    task_text: str,
) -> list[dict[str, Any]]:
    """Deterministically form named teams from the same signals routing already matched.

    Fixed-type recipes only ever surface agents already selected by routing/risk
    rules; a team never pulls in an agent that wouldn't otherwise be dispatched.
    """
    selected = set(selected_agents)
    matched_route_ids = {route["id"] for route in matched_routes}
    teams: list[dict[str, Any]] = []
    for recipe in config.get("team_recipes", []):
        if recipe["type"] == "fixed":
            triggering_routes = sorted(matched_route_ids & set(recipe["route_ids"]))
            if len(triggering_routes) < recipe["minimum_matches"]:
                continue
            members = [agent for agent in recipe["members"] if agent in selected]
            if len(members) < recipe.get("minimum_members_selected", 2):
                continue
            teams.append({
                "id": recipe["id"],
                "type": "fixed",
                "members": members,
                "trigger_reason": {"routes": triggering_routes},
                "communication_mode": recipe["communication_mode"],
                "fallback": recipe["fallback"],
                "description": recipe["description"],
            })
        elif recipe["type"] == "dynamic":
            if recipe["role"] not in selected:
                continue
            if recipe.get("requires_route") and recipe["requires_route"] not in matched_route_ids:
                continue
            matched_keywords = [
                keyword for keyword in recipe.get("keywords", []) if _keyword_matches(task_text.lower(), keyword)
            ]
            if not matched_keywords:
                continue
            teams.append({
                "id": recipe["id"],
                "type": "dynamic",
                "role": recipe["role"],
                "instances": recipe["instances"],
                "trigger_reason": {"keywords": matched_keywords},
                "communication_mode": recipe["communication_mode"],
                "fallback": recipe["fallback"],
                "description": recipe["description"],
            })
    return teams


def _build_quality_gates(
    config: dict[str, Any],
    routes: list[dict[str, Any]],
    risks: list[dict[str, Any]],
    gates: list[dict[str, Any]] | None,
) -> list[dict[str, Any]]:
    """Aggregate provider applicability without defining lifecycle semantics."""
    contracts = {gate["id"]: gate for gate in gates} if gates is not None else {}
    contributors: dict[str, list[str]] = {}
    for match in [*routes, *risks]:
        for gate_id in match["rule"].get("quality_gates", []):
            if gates is not None and gate_id not in contracts:
                raise ValueError(f"Routing references an unknown lifecycle gate: {gate_id}")
            contributors.setdefault(gate_id, []).append(match["id"])

    if gates is not None:
        gate_ids = [gate_id for gate_id in _gate_order(gates) if gate_id in contributors]
    else:
        gate_ids = list(contributors)

    return [
        {
            "id": gate_id,
            "required": True,
            "reason": (
                f"{contracts[gate_id].get('name', gate_id)} lifecycle gate ({contracts[gate_id].get('phase', 'unspecified')} phase)."
                if gates is not None
                else "Required by routing configuration (Agentic SDLC unavailable; gate detail omitted)."
            ),
            "contributing_routes": _unique(contributors[gate_id]),
        }
        for gate_id in gate_ids
    ]


def _build_knowledge_context(
    config: dict[str, Any], selected_agents: list[str], input_data: dict[str, Any]
) -> dict[str, Any]:
    if not selected_agents:
        return {"status": "not-applicable", "requests": []}
    classification = input_data.get("classification")
    if not classification:
        return {
            "status": "authorization-required",
            "reason": "Provide an authorized classification and scope before retrieval.",
            "requests": [],
        }
    if classification not in CLASSIFICATIONS:
        raise ValueError(f"Invalid classification: {classification}")
    try:
        top = int(input_data.get("top", 5))
    except (TypeError, ValueError) as error:
        raise ValueError("Knowledge top must be an integer from 1 through 20") from error
    if not 1 <= top <= MAXIMUM_KNOWLEDGE_TOP:
        raise ValueError("Knowledge top must be an integer from 1 through 20")

    requests = []
    normalized_task = " ".join(input_data["task"].split())
    for agent in selected_agents:
        focus = config["knowledge_focus"].get(agent)
        if not focus:
            raise ValueError(f"Missing knowledge focus for selected agent: {agent}")
        query = f"Task: {normalized_task}. Retrieve {focus}."
        args = [
            str(KNOWLEDGE_STORE_ROOT / "src" / "cli.py"),
            "context",
            "--agent",
            agent,
            "--task-id",
            input_data["task_id"],
            "--query",
            query,
            "--classification",
            classification,
            "--top",
            str(top),
            "--source",
            input_data.get("source") or _default_knowledge_source(),
        ]
        requests.append(
            {
                "agent": agent,
                "query": query,
                "invocation": {
                    "launcher": {
                        "runtime": "python",
                        "minimum_version": "3.10",
                        "resolution": "runner-probed",
                    },
                    "args": args,
                },
            }
        )
    return {
        "status": "planned",
        "classification": classification,
        "source_filter": input_data.get("source") or _default_knowledge_source(),
        "requests": requests,
    }


def _matches_change_intake(config: dict[str, Any], task: str) -> bool:
    """Identify implementation/change work that must start with intent and requirements."""
    normalized = task.lower()
    return any(
        re.search(rf"(^|[^a-z0-9]){re.escape(keyword.lower())}([^a-z0-9]|$)", normalized)
        for keyword in config.get("change_intake", {}).get("keywords", [])
    )


def _validate_agents(groups: dict[str, list[str]], catalog: list[str]) -> None:
    known = set(catalog)
    for agent in [*groups["primary"], *groups["reviewers"], *groups["support"]]:
        if agent not in known:
            raise ValueError(f"Routing selected an unknown agent: {agent}")


def build_dispatch_plan(
    config: dict[str, Any],
    catalog: list[str],
    input_data: dict[str, Any],
    require_sdlc: bool = False,
) -> dict[str, Any]:
    gates = _lifecycle_gates(require_sdlc)
    matched_routes = match_routes(config, input_data["task"], input_data["changed_files"])
    matched_risks = classify_risks(config, input_data["task"], input_data["changed_files"])
    primary = [agent for match in matched_routes for agent in match["rule"].get("primary", [])]
    reviewers = [agent for match in matched_routes for agent in match["rule"].get("reviewers", [])]
    support = [agent for match in matched_routes for agent in match["rule"].get("support", [])]
    for risk in matched_risks:
        primary.extend(risk["rule"].get("primary", []))
        reviewers.extend(risk["rule"].get("reviewers", []))
        support.extend(risk["rule"].get("support", []))
    support.extend(apply_cross_stack(config, matched_routes))

    change_intake = config.get("change_intake", {})
    if _matches_change_intake(config, input_data["task"]):
        support.extend(change_intake.get("agents", []))

    configured_gate_ids = [
        gate_id
        for match in [*matched_routes, *matched_risks]
        for gate_id in match["rule"].get("quality_gates", [])
    ]
    if _matches_change_intake(config, input_data["task"]):
        configured_gate_ids.extend(change_intake.get("quality_gates", []))
    support.extend(_gate_agents(configured_gate_ids, config.get("ignored_gates", []), gates))

    groups = {
        "primary": _ordered(primary, catalog),
        "reviewers": _ordered(reviewers, catalog),
        "support": _ordered(support, catalog),
    }
    groups["reviewers"] = [agent for agent in groups["reviewers"] if agent not in groups["primary"]]
    groups["support"] = [
        agent
        for agent in groups["support"]
        if agent not in groups["primary"] and agent not in groups["reviewers"]
    ]
    _validate_agents(groups, catalog)

    selected_agents = _ordered(
        [*groups["primary"], *groups["reviewers"], *groups["support"]], catalog
    )
    teams = _build_teams(config, matched_routes, selected_agents, input_data["task"])
    route_ids = [route["id"] for route in matched_routes]
    risk_ids = [risk["id"] for risk in matched_risks]
    task_id = input_data.get("task_id")
    if not task_id:
        changed_file_fingerprint = "\n".join(input_data["changed_files"])
        fingerprint = f"{input_data['task']}\n{changed_file_fingerprint}"
        task_id = f"local-{hashlib.sha256(fingerprint.encode('utf-8')).hexdigest()[:12]}"
    normalized_input = {**input_data, "task_id": task_id}
    generated_at = datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")
    required_quality_gates = _build_quality_gates(config, matched_routes, matched_risks, gates)
    existing_gate_ids = {gate["id"] for gate in required_quality_gates}
    if _matches_change_intake(config, input_data["task"]):
        change_intake_gate_ids = config.get("change_intake", {}).get("quality_gates", [])
        if gates is not None:
            contract_ids = set(_gate_order(gates))
            unknown = [gate_id for gate_id in change_intake_gate_ids if gate_id not in contract_ids]
            if unknown:
                raise ValueError(f"Change intake references unknown lifecycle gates: {unknown}")
            contracts = {gate["id"]: gate for gate in gates}
            required_quality_gates.extend(
                {
                    "id": gate_id,
                    "required": True,
                    "reason": f"{contracts[gate_id].get('name', gate_id)} lifecycle gate ({contracts[gate_id].get('phase', 'unspecified')} phase).",
                    "contributing_routes": ["change-intake"],
                }
                for gate_id in change_intake_gate_ids
                if gate_id not in existing_gate_ids
            )
        else:
            required_quality_gates.extend(
                {
                    "id": gate_id,
                    "required": True,
                    "reason": "Required by routing configuration (Agentic SDLC unavailable; gate detail omitted).",
                    "contributing_routes": ["change-intake"],
                }
                for gate_id in change_intake_gate_ids
                if gate_id not in existing_gate_ids
            )
    if gates is not None:
        gate_order = _gate_order(gates)
        required_quality_gates.sort(key=lambda gate: gate_order.index(gate["id"]))
    effective_gate_ids, gate_dispatch, ignored_quality_gates = _gate_dispatch(
        [gate["id"] for gate in required_quality_gates], config.get("ignored_gates", []), gates
    )
    existing = {gate["id"]: gate for gate in required_quality_gates}
    required_quality_gates = [
        existing.get(
            gate_id,
            {
                "id": gate_id,
                "required": True,
                "reason": "Required by the standalone lifecycle gate sequence.",
                "contributing_routes": ["lifecycle-sequence"],
            },
        )
        for gate_id in effective_gate_ids
    ]

    lifecycle_tracking = (
        {"status": "integrated"}
        if gates is not None
        else {"status": "standalone", "reason": STANDALONE_REASON}
    )

    dispatch = {
        "schema_version": 2,
        "task_id": task_id,
        "generated_at": generated_at,
        "status": "ready" if selected_agents else "needs-triage",
        "workflow": _select_workflow(route_ids, risk_ids, bool(selected_agents)),
        "inputs": {
            "task": input_data["task"],
            "base": input_data.get("base"),
            "changed_file_source": input_data["changed_file_source"],
            "changed_files": input_data["changed_files"],
            "classification": input_data.get("classification"),
            "source_filter": input_data.get("source"),
        },
        "matched_routes": [match["id"] for match in matched_routes],
        "matched_risks": [{"id": match["id"], "reasons": _reasons(match)} for match in matched_risks],
        "agents": groups,
        "teams": teams,
        "lifecycle_tracking": lifecycle_tracking,
        "required_quality_gates": required_quality_gates,
        "ignored_quality_gates": ignored_quality_gates,
        "gate_dispatch": gate_dispatch,
        "human_gates": _build_human_gates(matched_risks),
        "knowledge_context": _build_knowledge_context(config, selected_agents, normalized_input),
    }
    canonical = json.dumps(
        {key: value for key, value in dispatch.items() if key not in {"generated_at", "dispatch_fingerprint"}},
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")
    dispatch["dispatch_fingerprint"] = "sha256:" + hashlib.sha256(canonical).hexdigest()
    return dispatch
