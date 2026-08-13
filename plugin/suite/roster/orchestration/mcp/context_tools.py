"""Context-store operations for the MCP dispatch server.

This module is a **subprocess adapter**, not an import wrapper. Every operation
shells out to `roster/context-store/src/cli.py` and parses its JSON, rather than
importing the store's modules into this process. Three reasons, in descending
order of importance:

1. **Flat module names.** Both stores name their modules `config`, `database`,
   `service`, and `cli`, and this server already inserts
   `roster/orchestration/src/` at `sys.path[0]`. Importing a store here would
   make those names resolve by `sys.path` order in a long-lived process that
   has no reason to care about either store's internals -- exactly the silent
   shadowing `roster/orchestration/test/test_context_boundary.py` refuses
   *between* the stores. Not creating the hazard beats detecting it.

2. **One behaviour, two surfaces.** The MCP tools and the CLI a human runs are
   now the same code path, so they cannot drift. A scope rule fixed in one is
   fixed in both, and a test of the CLI is a test of the tool.

3. **Precedent and containment.** `roster/orchestration/src/
   agentic_sdlc_contracts.py` already shells out to the kernel CLI rather than
   importing it, for the same "ask, don't re-implement" reason. A crash in the
   store cannot take down a long-lived dispatch server.

What these tools add over running the CLI by hand is *ambient identity*:
`task_id` and `dispatch_id` come from the dispatch environment rather than from
tool arguments, and `classification` is validated against the session's parent
classification so a caller cannot write or read above its own ceiling.

`agent` is **not** ambient and is honestly a caller-asserted parameter -- there
is no role-id environment variable in the dispatch protocol today. See
`ROLE_ID_ENV_VAR` below.

`source` is the same honest gap, for the same reason. `classification` gets a
real ceiling here because the dispatch server tracks a parent classification
per session (`core.PARENT_CLASSIFICATION_ENV_VAR`); no equivalent ambient
project/repository identity exists to check `source` against. The server's own
working directory is not a usable substitute -- `dispatch_server.py` disables
project-tier settings resolution specifically because an MCP server's cwd is
wherever the host CLI happened to launch it, not reliably the target project
root. So `source` is forwarded to the CLI exactly as asserted, and the CLI's
own `_enforce_scope()` is the only check it gets -- identical to what a human
running `cadre context` by hand would get. See
`roster/context-store/SECURITY.md`'s "What is not enforced" section.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import subprocess
import sys
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

_MODULE_DIR = Path(__file__).resolve().parent
if str(_MODULE_DIR) not in sys.path:
    sys.path.insert(0, str(_MODULE_DIR))

import dispatch_core as core  # noqa: E402  (sys.path set above)

CONTEXT_CLI = core.REPOSITORY_ROOT / "roster" / "context-store" / "src" / "cli.py"

# Read if present, so a dispatched child's context writes are attributed to the
# role it is actually running as. Nothing in the dispatch protocol sets it
# today -- `build_child_env()` does not pass a role id -- so in practice the
# `agent` tool parameter is what gets used, and the tool docstrings say so
# rather than implying an assurance that does not exist. Wiring this into
# `build_child_env()` would make agent identity genuinely ambient for
# dispatched children, but it changes safety-relevant dispatch code and
# belongs in its own review, not smuggled in behind a storage feature.
ROLE_ID_ENV_VAR = "SECURE_CLOUD_AGENTS_ROLE_ID"

# The store resolves `~/.agents/context-store` and the user-global settings
# file, so the child needs HOME and the two location overrides on top of
# dispatch_core's ordinary allowlist. Deliberately a small, explicit addition
# to that list rather than passing os.environ through.
_EXTRA_ENV = ("CONTEXT_STORE_HOME", "XDG_CONFIG_HOME")

MAX_OUTPUT_BYTES = 4 * 1024 * 1024
DEFAULT_TIMEOUT_SECONDS = 30
FINAL_HANDOFF_SCHEMA_VERSION = 1
MAX_FINAL_HANDOFF_BYTES = 64 * 1024
MAX_HANDOFF_TEXT_BYTES = 4 * 1024
MAX_HANDOFF_LIST_ITEMS = 32
_CONTEXT_HANDLE_PATTERN = re.compile(r"^ctx_[0-9a-f]{32}$")
_FINAL_HANDOFF_KEYS = {
    "summary", "disposition", "findings", "assumptions", "unresolved_questions",
    "next_action", "context_handles", "knowledge_steward_handoffs",
}
_ARTIFACT_KEYS = {"id", "kind", "revision", "digest", "uri"}
_HANDOFF_DISPOSITIONS = {"complete", "approve", "request-changes", "needs-information", "blocked"}
_FINDING_KEYS = {"id", "title", "severity", "summary", "evidence", "owner"}
_KNOWLEDGE_HANDOFF_KEYS = {
    "title", "summary", "evidence", "origin", "proposed_classification", "source_scope",
    "sensitivity_notes", "conflicts_or_staleness", "untrusted_instruction_risk", "recommended_action",
}


class ContextToolError(RuntimeError):
    """A context operation failed in a way the caller should see."""


def _child_env() -> dict[str, str]:
    env = {name: os.environ[name] for name in core.ENV_ALLOWLIST if name in os.environ}
    for name in _EXTRA_ENV:
        if name in os.environ:
            env[name] = os.environ[name]
    env.setdefault("PATH", "/usr/bin:/bin")
    return env


def _run(args: list[str], *, stdin_text: str | None = None) -> dict[str, Any]:
    if not CONTEXT_CLI.is_file():
        raise ContextToolError(f"Context store CLI not found at {CONTEXT_CLI}")
    try:
        completed = subprocess.run(
            [sys.executable, str(CONTEXT_CLI), *args],
            input=(stdin_text or "").encode("utf-8"),
            capture_output=True,
            env=_child_env(),
            timeout=DEFAULT_TIMEOUT_SECONDS,
            check=False,
        )
    except subprocess.TimeoutExpired as error:
        raise ContextToolError("Context store command timed out") from error
    except OSError as error:
        raise ContextToolError(f"Could not run the context store CLI: {error}") from error

    if len(completed.stdout) > MAX_OUTPUT_BYTES:
        raise ContextToolError("Context store returned more output than the tool will relay")
    if completed.returncode != 0:
        # The CLI's own error text already names the remediation; relaying it
        # verbatim beats inventing a second vocabulary for the same failure.
        message = completed.stderr.decode("utf-8", errors="replace").strip()
        raise ContextToolError(message or f"Context store command failed ({completed.returncode})")
    try:
        return json.loads(completed.stdout.decode("utf-8"))
    except (json.JSONDecodeError, UnicodeDecodeError) as error:
        raise ContextToolError("Context store returned output that was not valid JSON") from error


def _resolved_agent(agent: str | None) -> str:
    """Ambient role identity wins over the asserted parameter when it exists."""
    ambient = os.environ.get(ROLE_ID_ENV_VAR)
    resolved = ambient or agent
    if not resolved:
        raise ContextToolError(
            "agent is required: pass the role id you are acting as, or set "
            f"{ROLE_ID_ENV_VAR} in the dispatch environment."
        )
    return resolved


def _resolved_task_id(task_id: str | None) -> str:
    if not task_id:
        raise ContextToolError(
            "No task identifier is available: set SECURE_CLOUD_AGENTS_TASK_ID in this "
            "session before using the context store, so every entry is attributable."
        )
    return task_id


def _checked_classification(classification: str, parent_classification: str | None) -> str:
    """Narrow-only, reusing the dispatch server's own rule.

    A context entry written at a classification above the session's ceiling
    would be exactly the labelling error the dispatch path already refuses, so
    it is refused the same way rather than with a second, weaker check.
    """
    if not parent_classification:
        raise ContextToolError(
            f"This server must set {core.PARENT_CLASSIFICATION_ENV_VAR} before the "
            "context store is usable, so a caller cannot assert a classification "
            "ceiling for itself."
        )
    try:
        return core.validate_classification(classification, parent_classification)
    except core.DispatchDenied as error:
        raise ContextToolError(str(error)) from error


def _audit(operation: str, **fields: Any) -> None:
    """Best-effort audit line, mirroring the dispatch path's own records.

    The context store keeps its own `access_runs` table; this is the *dispatch
    server's* record that a session reached for the store at all, which is a
    different question from what the store itself saw. Never carries content,
    query text, or a label -- `build_audit_record` rejects the forbidden key
    set outright, and nothing here goes near it.
    """
    core._write_audit_record_best_effort(
        core.build_audit_record(event=f"context_{operation}", **fields), path=None
    )


def _scope_args(source: str | None) -> list[str]:
    return ["--source", source] if source else []


def _dispatch_source(project_root: Path) -> str:
    """A stable, dispatch-derived source partition; never a child assertion."""
    digest = hashlib.sha256(str(project_root.resolve()).encode("utf-8")).hexdigest()[:12]
    name = re.sub(r"[^a-z0-9._-]+", "-", project_root.name.lower()).strip("-") or "project"
    return f"dispatch-{name}-{digest}"


def _short_text(value: Any, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ContextToolError(f"{field} must be a non-empty string")
    if len(value.encode("utf-8")) > MAX_HANDOFF_TEXT_BYTES or value.count("\n") > 40:
        raise ContextToolError(f"{field} exceeds the bounded final-handoff text limit")
    return value


def _short_text_list(value: Any, field: str, *, limit: int = MAX_HANDOFF_LIST_ITEMS) -> list[str]:
    if not isinstance(value, list) or len(value) > limit:
        raise ContextToolError(f"{field} must be a list of at most {limit} strings")
    return [_short_text(item, field) for item in value]


def _validate_artifact_identifier(value: Any, field: str, *, uri: bool = False) -> str:
    text = _short_text(value, field)
    if "?" in text or text.startswith(("/", "\\")) or re.match(r"^[A-Za-z]:[\\/]", text):
        raise ContextToolError(f"{field} may not be an absolute path or query-bearing identifier")
    parsed = urlsplit(text)
    if parsed.scheme.lower() == "file":
        raise ContextToolError(f"{field} may not use a file URI")
    if uri and parsed.scheme:
        if parsed.scheme.lower() != "https" or not parsed.netloc:
            raise ContextToolError(f"{field} may use only an https URI or a repository-relative identifier")
    return text


def _validate_finding(value: Any) -> dict[str, Any]:
    if not isinstance(value, dict) or not value or set(value) - _FINDING_KEYS:
        raise ContextToolError("final_handoff.handoff.findings entries must use the structured finding fields only")
    checked: dict[str, Any] = {}
    for field, item in value.items():
        checked[field] = _short_text_list(item, f"finding.{field}", limit=16) if field == "evidence" else _short_text(
            item, f"finding.{field}"
        )
    return checked


def _validate_knowledge_handoff(value: Any) -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != _KNOWLEDGE_HANDOFF_KEYS:
        raise ContextToolError("knowledge_steward_handoffs entries must use the required structured fields")
    checked: dict[str, Any] = {}
    for field, item in value.items():
        if field == "evidence":
            checked[field] = _short_text_list(item, f"knowledge_steward_handoffs.{field}", limit=16)
        elif field == "untrusted_instruction_risk":
            if item not in {True, False, "unknown"}:
                raise ContextToolError("knowledge_steward_handoffs.untrusted_instruction_risk is invalid")
            checked[field] = item
        else:
            checked[field] = _short_text(item, f"knowledge_steward_handoffs.{field}")
    return checked


def _validate_handoff(handoff: Any) -> dict[str, Any]:
    if not isinstance(handoff, dict) or not handoff or set(handoff) - _FINAL_HANDOFF_KEYS:
        raise ContextToolError("final_handoff.handoff must use only the documented structured handoff fields")
    checked: dict[str, Any] = {}
    for field, value in handoff.items():
        if field == "disposition":
            if value not in _HANDOFF_DISPOSITIONS:
                raise ContextToolError("final_handoff.handoff.disposition is invalid")
            checked[field] = value
        elif field in {"summary", "next_action"}:
            checked[field] = _short_text(value, f"final_handoff.handoff.{field}")
        elif field in {"assumptions", "unresolved_questions"}:
            checked[field] = _short_text_list(value, f"final_handoff.handoff.{field}")
        elif field == "findings":
            if not isinstance(value, list) or len(value) > MAX_HANDOFF_LIST_ITEMS:
                raise ContextToolError("final_handoff.handoff.findings must be a bounded list")
            checked[field] = [_validate_finding(item) for item in value]
        elif field == "knowledge_steward_handoffs":
            if not isinstance(value, list) or len(value) > MAX_HANDOFF_LIST_ITEMS:
                raise ContextToolError("final_handoff.handoff.knowledge_steward_handoffs must be a bounded list")
            checked[field] = [_validate_knowledge_handoff(item) for item in value]
        elif field == "context_handles":
            if not isinstance(value, list) or not all(
                isinstance(handle, str) and _CONTEXT_HANDLE_PATTERN.fullmatch(handle) for handle in value
            ):
                raise ContextToolError("final_handoff.handoff.context_handles must contain context handles only")
            checked[field] = value
    return checked


def _validated_final_handoff(envelope: Any) -> dict[str, Any]:
    """Validate the narrow machine-result protocol used for automatic capture.

    This intentionally does not inspect child stdout. A runner must return this
    object as its separate ``final_handoff`` result field; arbitrary prose,
    transcripts, and tool results are never inferred to be a handoff.
    """
    if not isinstance(envelope, dict):
        raise ContextToolError("final_handoff must be a structured object")
    if envelope.get("kind") != "cadre-final-handoff" or envelope.get("schema_version") != FINAL_HANDOFF_SCHEMA_VERSION:
        raise ContextToolError("final_handoff must declare kind='cadre-final-handoff' and schema_version=1")
    if set(envelope) != {"kind", "schema_version", "handoff", "artifacts", "derived_from"}:
        raise ContextToolError("final_handoff has unsupported top-level fields")
    handoff = _validate_handoff(envelope.get("handoff"))
    artifacts = envelope.get("artifacts")
    derived_from = envelope.get("derived_from")
    if not isinstance(artifacts, list) or len(artifacts) > 64:
        raise ContextToolError("final_handoff.artifacts must be a manifest of at most 64 entries")
    if not isinstance(derived_from, list) or not all(isinstance(item, str) for item in derived_from):
        raise ContextToolError("final_handoff.derived_from must be a list of provenance references")
    for reference in derived_from:
        if not (_CONTEXT_HANDLE_PATTERN.fullmatch(reference) or reference.startswith("ks:untrusted:")):
            raise ContextToolError("final_handoff.derived_from may contain only context handles or ks:untrusted markers")
    for artifact in artifacts:
        if not isinstance(artifact, dict) or not artifact.get("id") or set(artifact) - _ARTIFACT_KEYS:
            raise ContextToolError("final_handoff.artifacts must be identifier-only manifest entries")
        artifact["id"] = _validate_artifact_identifier(artifact["id"], "final_handoff.artifacts.id")
        for field in ("kind", "revision", "digest"):
            if field in artifact:
                artifact[field] = _validate_artifact_identifier(artifact[field], f"final_handoff.artifacts.{field}")
        if "uri" in artifact:
            artifact["uri"] = _validate_artifact_identifier(artifact["uri"], "final_handoff.artifacts.uri", uri=True)
    referenced_handles = handoff.get("context_handles", [])
    if not set(referenced_handles).issubset(set(derived_from)):
        raise ContextToolError("every final_handoff context handle must also be listed in derived_from")
    captured = {
        "kind": "cadre-final-handoff",
        "schema_version": FINAL_HANDOFF_SCHEMA_VERSION,
        "handoff": handoff,
        "artifacts": artifacts,
        "derived_from": derived_from,
    }
    try:
        encoded = json.dumps(captured, sort_keys=True).encode("utf-8")
    except (TypeError, ValueError) as error:
        raise ContextToolError("final_handoff must be JSON-serializable") from error
    if len(encoded) > MAX_FINAL_HANDOFF_BYTES:
        raise ContextToolError(f"final_handoff exceeds the {MAX_FINAL_HANDOFF_BYTES}-byte capture cap")
    return captured


def capture_final_handoff(
    *,
    envelope: Any,
    role_id: str,
    task_id: str | None,
    dispatch_id: str | None,
    parent_classification: str | None,
    classification: str,
    project_root: Path,
) -> dict[str, Any]:
    """Best-effort automatic capture for an explicit final-handoff envelope.

    Identity, scope, classification, source, and TTL are dispatch-owned. The
    child supplies neither a source nor a scope, and artifacts are recorded as
    a manifest only: this adapter never reads artifact paths.
    """
    try:
        captured = _validated_final_handoff(envelope)
        source = _dispatch_source(project_root)
        result = put(
            label=f"final handoff: {role_id}",
            content=json.dumps(captured, sort_keys=True),
            agent=role_id,
            task_id=task_id,
            dispatch_id=dispatch_id,
            parent_classification=parent_classification,
            classification=classification,
            scope="dispatch",
            tags=["automatic", "final-handoff"],
            derived_from=captured["derived_from"],
            source=source,
            enforce_agent=True,
        )
        return {"status": "captured", "handle": result["handle"], "expires_at": result["expires_at"],
                "untrusted_inputs": result["untrusted_inputs"], "source": source}
    except ContextToolError as error:
        return {"status": "not_captured", "reason": str(error)}


def put(
    *,
    label: str,
    content: str,
    agent: str | None,
    task_id: str | None,
    dispatch_id: str | None,
    parent_classification: str | None,
    classification: str = "internal",
    scope: str = "agent",
    tags: list[str] | None = None,
    derived_from: list[str] | None = None,
    ttl_days: int | None = None,
    source: str | None = None,
    enforce_agent: bool = False,
) -> dict[str, Any]:
    # Automatic final-handoff capture is attributed to the already-resolved
    # dispatch role, never an ambient environment variable from this server.
    resolved_agent = agent if enforce_agent and agent else _resolved_agent(agent)
    resolved_task = _resolved_task_id(task_id)
    checked = _checked_classification(classification, parent_classification)
    args = [
        "put", "--label", label, "--scope", scope,
        "--agent", resolved_agent, "--task-id", resolved_task,
        "--classification", checked, *_scope_args(source),
    ]
    if scope == "dispatch":
        if not dispatch_id:
            raise ContextToolError(
                "scope 'dispatch' needs a dispatch identity: set "
                "SECURE_CLOUD_AGENTS_SESSION_ID in this session."
            )
        args += ["--dispatch-id", dispatch_id]
    for tag in tags or []:
        args += ["--tag", tag]
    for reference in derived_from or []:
        args += ["--derived-from", reference]
    if ttl_days is not None:
        args += ["--ttl-days", str(ttl_days)]

    result = _run(args, stdin_text=content)
    _audit(
        "put", agent=resolved_agent, task_id=resolved_task, classification=checked,
        scope=scope, handle=result.get("handle"), byte_length=result.get("byte_length"),
        untrusted_inputs=result.get("untrusted_inputs"),
    )
    return result


def _fence(bundle: dict[str, Any]) -> dict[str, Any]:
    """Wrap every returned entry's content in the untrusted-output fence.

    Stored content returns to the parent model as this tool call's result --
    the same position a dispatched child's stdout occupies, and it gets the
    same treatment for the same reason: a random per-call token the content
    cannot forge, so text engineered to look like a closing fence followed by
    "trusted instructions resume here" cannot escape its framing.

    Written by an agent is not the same as trustworthy. An entry may be a
    faithful summary of a file that was itself hostile, which is precisely
    what `untrusted_inputs` records and why it is surfaced beside the content.
    """
    for result in bundle.get("results", []):
        if isinstance(result.get("content"), str):
            result["content"] = core.wrap_untrusted_output(result["content"])
    return bundle


def get(
    *,
    handle: str,
    agent: str | None,
    task_id: str | None,
    dispatch_id: str | None,
    parent_classification: str | None,
    classification: str = "internal",
    source: str | None = None,
) -> dict[str, Any]:
    resolved_agent = _resolved_agent(agent)
    resolved_task = _resolved_task_id(task_id)
    checked = _checked_classification(classification, parent_classification)
    args = [
        "get", "--handle", handle, "--agent", resolved_agent,
        "--task-id", resolved_task, "--classification", checked, *_scope_args(source),
    ]
    if dispatch_id:
        args += ["--dispatch-id", dispatch_id]
    bundle = _run(args)
    _audit(
        "get", agent=resolved_agent, task_id=resolved_task, classification=checked,
        handle=handle, result_count=len(bundle.get("results", [])),
    )
    return _fence(bundle)


def listing(
    *,
    agent: str | None,
    task_id: str | None,
    dispatch_id: str | None,
    parent_classification: str | None,
    classification: str = "internal",
    scope: str | None = None,
    tags: list[str] | None = None,
    top: int | None = None,
    source: str | None = None,
) -> dict[str, Any]:
    resolved_agent = _resolved_agent(agent)
    resolved_task = _resolved_task_id(task_id)
    checked = _checked_classification(classification, parent_classification)
    args = [
        "list", "--agent", resolved_agent, "--task-id", resolved_task,
        "--classification", checked, *_scope_args(source),
    ]
    if scope:
        args += ["--scope", scope]
    if dispatch_id:
        args += ["--dispatch-id", dispatch_id]
    for tag in tags or []:
        args += ["--tag", tag]
    if top is not None:
        args += ["--top", str(top)]
    bundle = _run(args)
    _audit(
        "list", agent=resolved_agent, task_id=resolved_task, classification=checked,
        scope=scope, result_count=len(bundle.get("results", [])),
    )
    # No fencing needed: `list` returns metadata only, never content.
    return bundle


def search(
    *,
    query: str,
    agent: str | None,
    task_id: str | None,
    dispatch_id: str | None,
    parent_classification: str | None,
    classification: str = "internal",
    scope: str | None = None,
    top: int | None = None,
    source: str | None = None,
) -> dict[str, Any]:
    resolved_agent = _resolved_agent(agent)
    resolved_task = _resolved_task_id(task_id)
    checked = _checked_classification(classification, parent_classification)
    args = [
        "search", "--query", query, "--agent", resolved_agent,
        "--task-id", resolved_task, "--classification", checked, *_scope_args(source),
    ]
    if scope:
        args += ["--scope", scope]
    if dispatch_id:
        args += ["--dispatch-id", dispatch_id]
    if top is not None:
        args += ["--top", str(top)]
    bundle = _run(args)
    _audit(
        "search", agent=resolved_agent, task_id=resolved_task, classification=checked,
        scope=scope, query_id=bundle.get("query_id"), result_count=len(bundle.get("results", [])),
    )
    return _fence(bundle)
