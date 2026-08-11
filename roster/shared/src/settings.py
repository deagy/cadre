#!/usr/bin/env python3
"""Unified operator-settings resolver.

Replaces scattered `os.environ.get(...)` calls across this repository's
tooling with a single, per-field precedence chain:

    env var > project-local config file > user-global config file >
    static default > computed default > interactive prompt (writes the
    file) > fail-closed error

Stdlib-only at import time -- PyYAML is lazily imported only when a `.yaml`
config file actually exists on disk (`resolve._require_yaml()`'s pattern,
reused directly rather than reimplemented).

Config file locations (dual `.yaml`/`.json` accepted at each location; both
present at once at the same tier is an error):

  - project-local: `.agents/cadre.yaml` (or `.json`), discovered by walking
    up from the current directory to the nearest `.git` boundary via
    `resolve.find_file_at_project_root`.
  - user-global: `${XDG_CONFIG_HOME:-~/.config}/cadre/config.yaml` (or
    `.json`).

Trust scope is security-critical: fields marked `global_only` below may
never be set from the project-local file, because that file is untrusted,
clone-able repository content and these fields select executables or
exfiltration-sensitive endpoints. A project-local file that sets a
`global_only` field raises `SettingsError`, never silently ignored.

Secrets (service tokens, embedding API keys) are never read from or written
to a config file this module manages -- see `_looks_like_secret_key`. This
module also never itself introduces a new secret; `resolve_token()`-style
functions elsewhere stay exactly as they are.
"""

from __future__ import annotations

import contextlib
import ipaddress
import json
import os
import re
import shutil
import socket
import sys
import tempfile
import urllib.parse
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable

SRC_DIR = Path(__file__).resolve().parent
if str(SRC_DIR) not in sys.path:
    # Appended, never inserted at sys.path[0]: every consumer of this module
    # (dispatch_core.py, gitlab_core.py, config.py, agentic_sdlc_contracts.py,
    # bin/cadre.py) documents and relies on that same discipline so a
    # caller's own same-named module is never shadowed -- this module
    # holding itself to a different rule would defeat all four of those
    # comments the moment any consumer's own path computation doesn't
    # happen to already match this one exactly (e.g. an unresolved or
    # symlinked path), since whichever import runs first would win sys.path[0].
    sys.path.append(str(SRC_DIR))

from resolve import (  # noqa: E402  (sys.path set above)
    MAXIMUM_WALK_DEPTH,
    _is_same_or_descendant,
    _require_yaml,
    _resolve_existing_ancestor,
    find_file_at_project_root,
)

PROJECT_CONFIG_BASENAME = "cadre"
PROJECT_CONFIG_DIR = Path(".agents")
GLOBAL_CONFIG_APP_DIR = "cadre"
GLOBAL_CONFIG_BASENAME = "config"

INTERACTIVE_ENV_VAR = "CADRE_INTERACTIVE"

SCOPE_GLOBAL_ONLY = "global_only"
SCOPE_PROJECT_OR_GLOBAL = "project_or_global"

_UNSET = object()


class SettingsError(Exception):
    """A setting could not be resolved, or a config file/value is invalid."""


class SettingsScopeError(SettingsError):
    """A project-local file set a `global_only` field.

    This is a security event (untrusted, clonable repository content trying
    to steer an executable path, a data-store location, or an exfiltration
    endpoint), not an ordinary "value unavailable" outcome -- it must never
    be silently swallowed, including by resolve_optional(), which otherwise
    treats every other SettingsError as "field simply isn't configured."
    """


@dataclass(frozen=True)
class Resolved:
    key: str
    value: Any
    origin: str  # "env" | "project-file" | "global-file" | "default" | "computed" | "prompt"
    origin_path: Path | None


# ---------------------------------------------------------------------------
# Secret-shaped key rejection -- never read from, or written to, a config
# file this module manages.
# ---------------------------------------------------------------------------

_SECRET_LEAF_PATTERNS = (
    re.compile(r"^token$"),
    re.compile(r".*_token$"),
    re.compile(r"^api_key$"),
    re.compile(r"^password$"),
    re.compile(r"^secret$"),
    re.compile(r"^svc_token$"),
)


def _looks_like_secret_key(leaf_name: str) -> bool:
    return any(pattern.match(leaf_name) for pattern in _SECRET_LEAF_PATTERNS)


def _reject_secret_shaped_keys(data: dict[str, Any], path_prefix: str, file_path: Path) -> None:
    for key, value in data.items():
        if key == "schema_version":
            continue
        dotted = f"{path_prefix}.{key}" if path_prefix else key
        if _looks_like_secret_key(key):
            raise SettingsError(
                f"{file_path}: key {dotted!r} looks like a secret (matches a *_token/*.token/"
                "*.api_key/*.password/*.secret pattern) and must never be stored in a cadre "
                "config file; secrets are always read from an environment variable"
            )
        _reject_secret_shaped_keys_in_value(value, dotted, file_path)


def _reject_secret_shaped_keys_in_value(value: Any, dotted: str, file_path: Path) -> None:
    """Walk into both mappings and sequences.

    The dict-only walk this replaces meant a secret-shaped key nested under
    a list (`gitlab:\\n  extra:\\n    - token: "..."`) was never scanned. No
    registered field is list-shaped today, so such a key could not be
    *resolved* -- but `write_setting`'s "preserve unknown keys" merge would
    have round-tripped it into every subsequent rewrite of the file,
    silently persisting a pasted credential the module's own docstring
    promises is never stored. Strings are deliberately not descended into
    (they are values, not key containers).
    """
    if isinstance(value, dict):
        _reject_secret_shaped_keys(value, dotted, file_path)
        return
    if isinstance(value, (list, tuple)):
        for index, item in enumerate(value):
            _reject_secret_shaped_keys_in_value(item, f"{dotted}[{index}]", file_path)


# ---------------------------------------------------------------------------
# Field registry.
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class FieldSpec:
    key: str
    env_var: str | None
    scope: str
    kind: str
    required: bool = False
    default_static: Any = _UNSET
    default_computed: Callable[[], Any] | None = None
    secret: bool = False


def _validate_string(value: Any, spec: FieldSpec) -> str:
    if isinstance(value, bool) or not isinstance(value, str):
        raise SettingsError(
            f"{spec.key}: expected a string, got {type(value).__name__} ({value!r}); "
            "quote the value if it came from a YAML file"
        )
    stripped = value.strip()
    if not stripped:
        raise SettingsError(f"{spec.key}: value is empty/whitespace-only")
    return stripped


def _validate_gitlab_base_url(value: Any, spec: FieldSpec) -> str:
    stripped = _validate_string(value, spec)
    if not stripped.lower().startswith("https://"):
        raise SettingsError(f"{spec.env_var} must start with https://: {stripped!r}")
    if "@" in urllib.parse.urlparse(stripped).netloc:
        raise SettingsError(
            f"{spec.env_var} must not contain URL userinfo (an '@' in the host "
            f"component): {stripped!r}"
        )
    return stripped.rstrip("/")


def _validate_project_id(value: Any, spec: FieldSpec) -> str:
    if isinstance(value, bool) or not isinstance(value, str):
        raise SettingsError(
            f"{spec.key} must be a string (got {type(value).__name__} {value!r}); quote "
            "numeric-looking project ids in YAML, e.g. \"007\""
        )
    stripped = value.strip()
    if not stripped:
        raise SettingsError(f"{spec.key}: value is empty/whitespace-only")
    return stripped


def _validate_tristate_bool(value: Any, spec: FieldSpec) -> bool | None:
    if value is None:
        return None
    if isinstance(value, bool):
        return value
    if isinstance(value, str):
        normalized = value.strip().lower()
        if normalized == "true":
            return True
        if normalized == "false":
            return False
        raise SettingsError(f"{spec.env_var or spec.key} must be 'true' or 'false' if set: {value!r}")
    raise SettingsError(
        f"{spec.key} must be a boolean or 'true'/'false' string, got {type(value).__name__} ({value!r})"
    )


def _reject_control_characters(value: str, spec: FieldSpec) -> None:
    """Reject embedded control characters (newline, tab, NUL, ...).

    None is legitimate in an executable name or a filesystem path, and a
    newline in particular breaks `cadre config resolve`'s contract of
    printing exactly one value on stdout for a shell caller to capture --
    the packaged `bin/cadre` wrapper reads it with `$(...)`, so an embedded
    newline would silently produce a multi-line value. Reported by position
    and repr rather than echoed raw, so an invisible character is
    identifiable in the message.
    """
    for index, character in enumerate(value):
        if character.isprintable():
            continue
        raise SettingsError(
            f"{spec.key} must not contain control characters; found {character!r} "
            f"at position {index} of {value!r}"
        )


def _has_path_separator(value: str) -> bool:
    if "/" in value:
        return True
    if os.sep != "/" and os.sep in value:
        return True
    return False


def _validate_executable(value: Any, spec: FieldSpec) -> str:
    stripped = _validate_string(value, spec)
    if _has_path_separator(stripped) and not os.path.isabs(stripped):
        raise SettingsError(
            f"{spec.key} must be an absolute path or a bare executable name found on PATH, "
            f"not a relative path: {stripped!r}"
        )
    if stripped.startswith("-"):
        # A leading '-' is read as an option by the thing that runs this
        # value, not as a program name. Verified under bash:
        # `bin="-a"; exec "$bin" --provider p.json --version` makes exec
        # consume `--provider` as -a's argv[0] argument and then attempt to
        # execute `p.json` -- i.e. a value that looks inert silently
        # reinterprets the rest of the command line. dash's exec has no -a
        # and merely fails, but the packaged wrapper is `#!/bin/sh`, which
        # is bash on some systems. No legitimate executable name or path
        # begins with '-'; use './-name' or an absolute path if one ever
        # does.
        raise SettingsError(
            f"{spec.key} must not begin with '-' (it would be parsed as an option by the "
            f"program that runs it, not as a command): {stripped!r}"
        )
    _reject_control_characters(stripped, spec)
    # Internal spaces are deliberately allowed: '/opt/My Tools/bin/x' is a
    # legitimate path, every consumer quotes the value ("$sdlc_bin",
    # subprocess list form), and rejecting them would break real installs
    # for no security gain.
    return stripped


def _validate_path(value: Any, spec: FieldSpec) -> str:
    stripped = _validate_string(value, spec)
    # Same reasoning as the executable validator: a newline here would also
    # break `cadre config resolve`'s one-value-per-line stdout contract.
    # Internal spaces stay legal -- paths routinely contain them.
    _reject_control_characters(stripped, spec)
    return stripped


_IDENTIFIER_PATTERN = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:/+-]*$")


def _validate_identifier(value: Any, spec: FieldSpec) -> str:
    """A model name or a Codex config-profile name.

    Deliberately *not* `_validate_executable`: these values are passed as the
    argument of a `--model`/`--profile` flag, never executed, so a path
    separator is legal (`qwen3-coder:30b`, `org/model-name`) while whitespace
    and shell metacharacters are not. Anchored allowlist rather than a
    metacharacter denylist, so a character nobody thought about is refused
    rather than admitted. The leading character may not be '-', for the same
    reason `_validate_executable` refuses one: a value beginning with '-' is
    read as another option by the CLI that receives it.
    """
    stripped = _validate_string(value, spec)
    if not _IDENTIFIER_PATTERN.match(stripped):
        raise SettingsError(
            f"{spec.key} must start with a letter or digit and contain only letters, digits, "
            f"and the characters . _ : / + - (no whitespace or shell metacharacters): {stripped!r}"
        )
    return stripped


def _is_private_host(host: str) -> bool:
    """True when `host` is a loopback/link-local/private address, or resolves
    only to such addresses.

    A bare hostname is resolved once, here at validation time, and every
    address it maps to must be private -- a name resolving to a mix of
    private and public addresses is treated as public (fails closed). Name
    resolution can of course change after this check, so this is a
    configuration-time guard against a typo shipping a plaintext credential
    to the open internet, not a DNS-rebinding defence.
    """
    literal = host.strip("[]")
    try:
        address = ipaddress.ip_address(literal)
    except ValueError:
        pass
    else:
        return address.is_loopback or address.is_private or address.is_link_local
    try:
        resolved = socket.getaddrinfo(host, None)
    except (socket.gaierror, UnicodeError, OSError):
        return False
    if not resolved:
        return False
    for entry in resolved:
        try:
            address = ipaddress.ip_address(entry[4][0])
        except ValueError:
            return False
        if not (address.is_loopback or address.is_private or address.is_link_local):
            return False
    return True


def _validate_endpoint_url(value: Any, spec: FieldSpec) -> str:
    """An OpenAI-compatible API base URL, e.g. `http://10.0.0.5:8080/v1`.

    Distinct from `_validate_gitlab_base_url`, which mandates https:// --
    that rule is right for a hosted GitLab and wrong for a self-hosted
    inference server on a LAN, which is the entire point of this field. The
    compromise: https:// is accepted for any host, while plaintext http:// is
    accepted *only* toward a loopback/private/link-local address, so a
    mistyped or attacker-suggested public endpoint can never receive an API
    key in the clear. URL userinfo is refused exactly as it is for GitLab --
    credentials belong in the env var named by `runners.api_key_env`, never
    in a URL that lands in config files and error messages.
    """
    stripped = _validate_string(value, spec)
    _reject_control_characters(stripped, spec)
    parsed = urllib.parse.urlparse(stripped)
    if parsed.scheme not in ("http", "https"):
        raise SettingsError(f"{spec.key} must be an http:// or https:// URL: {stripped!r}")
    if "@" in parsed.netloc:
        raise SettingsError(
            f"{spec.key} must not contain URL userinfo (an '@' in the host component); put "
            f"the credential in the variable named by runners.api_key_env: {stripped!r}"
        )
    host = parsed.hostname
    if not host:
        raise SettingsError(f"{spec.key} must include a host: {stripped!r}")
    if parsed.scheme == "http" and not _is_private_host(host):
        raise SettingsError(
            f"{spec.key}: plaintext http:// is only allowed toward a loopback or private-network "
            f"host (this is a self-hosted-endpoint escape hatch, not a general one); use https:// "
            f"for {host!r}"
        )
    return stripped.rstrip("/")


_ENV_VAR_NAME_PATTERN = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")


def _validate_env_var_name(value: Any, spec: FieldSpec) -> str:
    """The *name* of an environment variable, never its value.

    This is how a credential reaches this tooling without ever being stored
    in a config file -- `_looks_like_secret_key` would reject an `api_key`
    leaf outright, so the field holds the pointer instead. The name itself is
    not sensitive.
    """
    stripped = _validate_string(value, spec)
    if not _ENV_VAR_NAME_PATTERN.match(stripped):
        raise SettingsError(
            f"{spec.key} must be an environment variable *name* (letters, digits, underscore; "
            f"not starting with a digit), not its value: {stripped!r}"
        )
    return stripped


def _split_comma_list(value: Any, spec: FieldSpec) -> list[str]:
    if value is None or value == "":
        return []
    if isinstance(value, (list, tuple)):
        items = [str(item).strip() for item in value]
    else:
        items = [item.strip() for item in _validate_string(value, spec).split(",")]
    return [item for item in items if item]


def _validate_env_var_name_list(value: Any, spec: FieldSpec) -> list[str]:
    """A comma-separated list of exact environment variable names.

    Deliberately exact names only -- no wildcards and no prefix matching. A
    pattern here would let one config line widen the dispatch child's
    environment by an unbounded amount, which is precisely what
    `dispatch_core.ENV_ALLOWLIST` exists to prevent.
    """
    items = _split_comma_list(value, spec)
    for item in items:
        if not _ENV_VAR_NAME_PATTERN.match(item):
            raise SettingsError(
                f"{spec.key} must be a comma-separated list of exact environment variable names "
                f"(no wildcards or prefixes): {item!r}"
            )
    return items


def _validate_command_allowlist(value: Any, spec: FieldSpec) -> list[str]:
    """A comma-separated list of bare command names the `api` runner's
    `run_command` tool may execute.

    Bare names only: an entry containing a path separator is refused, so this
    list can never be used to reach an arbitrary binary outside PATH. The
    dispatch runner resolves each name through `shutil.which` and pins the
    absolute result at call time.
    """
    items = _split_comma_list(value, spec)
    for item in items:
        if _has_path_separator(item) or item.startswith("-"):
            raise SettingsError(
                f"{spec.key} entries must be bare command names, not paths or options: {item!r}"
            )
        _reject_control_characters(item, spec)
    return items


_VALIDATORS: dict[str, Callable[[Any, FieldSpec], Any]] = {
    "gitlab_base_url": _validate_gitlab_base_url,
    "project_id": _validate_project_id,
    "tristate_bool": _validate_tristate_bool,
    "executable": _validate_executable,
    "path": _validate_path,
    "string": _validate_string,
    "identifier": _validate_identifier,
    "endpoint_url": _validate_endpoint_url,
    "env_var_name": _validate_env_var_name,
    "env_var_name_list": _validate_env_var_name_list,
    "command_allowlist": _validate_command_allowlist,
}


def _validate(spec: FieldSpec, value: Any) -> Any:
    return _VALIDATORS[spec.kind](value, spec)


def validate_tristate_bool(value: Any, key: str) -> bool | None:
    """Public entry point for a tri-state-bool field's validation rule
    (accepts None/absent, a native bool, or a case-insensitive 'true'/
    'false' string; rejects anything else). Exposed so `gitlab_core.py`'s
    `_parse_hierarchy_flag` wrapper reuses this single implementation
    instead of a second copy of the same rule."""
    return _validate_tristate_bool(value, _spec(key))


FIELDS: dict[str, FieldSpec] = {
    "gitlab.base_url": FieldSpec(
        key="gitlab.base_url",
        env_var="GITLAB_BASE_URL",
        scope=SCOPE_GLOBAL_ONLY,
        kind="gitlab_base_url",
        required=True,
    ),
    "gitlab.project_id": FieldSpec(
        # global_only, not project_or_global: roster/orchestration/
        # SECURITY-CONTROLS.md records a human-accepted residual-risk
        # control for this integration -- GitLab write scope is contained
        # operationally by pointing GITLAB_BASE_URL *and*
        # GITLAB_DOCS_PROJECT_ID at one dedicated, docs-only project with a
        # least-privilege service token, since the module itself performs
        # no classification check. Letting an untrusted project-local file
        # set the destination project would let a cloned repo redirect
        # every evidence-comment/wiki write to a project of its choosing,
        # silently weakening that recorded control.
        key="gitlab.project_id",
        env_var="GITLAB_DOCS_PROJECT_ID",
        scope=SCOPE_GLOBAL_ONLY,
        kind="project_id",
        required=True,
    ),
    "gitlab.supports_work_item_hierarchy": FieldSpec(
        key="gitlab.supports_work_item_hierarchy",
        env_var="GITLAB_SUPPORTS_WORK_ITEM_HIERARCHY",
        scope=SCOPE_PROJECT_OR_GLOBAL,
        kind="tristate_bool",
        required=False,
        default_static=None,
    ),
    "runners.claude_bin": FieldSpec(
        key="runners.claude_bin",
        env_var="SECURE_CLOUD_AGENTS_CLAUDE_BIN",
        scope=SCOPE_GLOBAL_ONLY,
        kind="executable",
        required=False,
        default_static="claude",
    ),
    "runners.codex_bin": FieldSpec(
        key="runners.codex_bin",
        env_var="SECURE_CLOUD_AGENTS_CODEX_BIN",
        scope=SCOPE_GLOBAL_ONLY,
        kind="executable",
        required=False,
        default_static="codex",
    ),
    # --- Self-hosted / local-provider dispatch -----------------------------
    #
    # Every field below is global_only for the reason in this module's
    # docstring: they select an executable's model, a config profile that can
    # redirect where a dispatched agent's prompt is sent, an endpoint, or the
    # set of environment variables a child inherits. All four are
    # exfiltration-sensitive, and the project-local file is untrusted,
    # clone-able repository content.
    #
    # None of them holds a credential. `runners.api_key_env` holds the *name*
    # of the variable that does; see `_validate_env_var_name`.
    "runners.codex_profile": FieldSpec(
        # Layered by `codex exec --profile <name>` from
        # $CODEX_HOME/<name>.config.toml, where the operator declares their
        # own [model_providers.*] block (base_url / wire_api / env_key). That
        # file is Codex's to own, not this repository's -- which is precisely
        # why no base_url or credential for the codex runner appears here.
        key="runners.codex_profile",
        env_var="SECURE_CLOUD_AGENTS_CODEX_PROFILE",
        scope=SCOPE_GLOBAL_ONLY,
        kind="identifier",
        required=False,
        default_static=None,
    ),
    # One key per catalog model tier, rather than one map-valued key: this
    # registry is flat and scalar by construction, and the shape matches the
    # existing Cline precedent (CLINE_AGENTS_MODEL_<TIER>) exactly. A tier
    # left unset keeps the wrapper's own vendor identifier.
    "runners.local_model_opus": FieldSpec(
        key="runners.local_model_opus",
        env_var="SECURE_CLOUD_AGENTS_LOCAL_MODEL_OPUS",
        scope=SCOPE_GLOBAL_ONLY,
        kind="identifier",
        required=False,
        default_static=None,
    ),
    "runners.local_model_sonnet": FieldSpec(
        key="runners.local_model_sonnet",
        env_var="SECURE_CLOUD_AGENTS_LOCAL_MODEL_SONNET",
        scope=SCOPE_GLOBAL_ONLY,
        kind="identifier",
        required=False,
        default_static=None,
    ),
    "runners.local_model_haiku": FieldSpec(
        key="runners.local_model_haiku",
        env_var="SECURE_CLOUD_AGENTS_LOCAL_MODEL_HAIKU",
        scope=SCOPE_GLOBAL_ONLY,
        kind="identifier",
        required=False,
        default_static=None,
    ),
    "runners.forward_env": FieldSpec(
        # Narrow, explicit extension of dispatch_core.ENV_ALLOWLIST, for the
        # single case that genuinely needs it: a Codex [model_providers.*]
        # block declaring `env_key = "SOME_VAR"` cannot authenticate unless
        # SOME_VAR reaches the child. Empty by default, so the deny-by-default
        # posture is unchanged for every operator who does not opt in.
        key="runners.forward_env",
        env_var="SECURE_CLOUD_AGENTS_FORWARD_ENV",
        scope=SCOPE_GLOBAL_ONLY,
        kind="env_var_name_list",
        required=False,
        default_static=None,
    ),
    "runners.api_base_url": FieldSpec(
        key="runners.api_base_url",
        env_var="SECURE_CLOUD_AGENTS_API_BASE_URL",
        scope=SCOPE_GLOBAL_ONLY,
        kind="endpoint_url",
        required=False,
        default_static=None,
    ),
    "runners.api_key_env": FieldSpec(
        key="runners.api_key_env",
        env_var="SECURE_CLOUD_AGENTS_API_KEY_ENV",
        scope=SCOPE_GLOBAL_ONLY,
        kind="env_var_name",
        required=False,
        default_static=None,
    ),
    "runners.api_allow_writes": FieldSpec(
        # The api runner has no OS-level sandbox to delegate to (see
        # roster/orchestration/SECURITY-CONTROLS.md's "API runner" section),
        # so its write-capable path is opt-in and off by default. This is one
        # of four independent conditions a write must satisfy, not a
        # standalone switch.
        key="runners.api_allow_writes",
        env_var="SECURE_CLOUD_AGENTS_API_ALLOW_WRITES",
        scope=SCOPE_GLOBAL_ONLY,
        kind="tristate_bool",
        required=False,
        default_static=False,
    ),
    "runners.api_command_allowlist": FieldSpec(
        # Empty by default, which means the api runner offers no command-
        # execution tool at all. Non-empty enables it for exactly these
        # command names. Read the "advisory, not enforced" note in
        # SECURITY-CONTROLS.md before setting this: the dispatched agent
        # chooses the *arguments*, and test runners execute repository-
        # controlled code by design.
        key="runners.api_command_allowlist",
        env_var="SECURE_CLOUD_AGENTS_API_COMMAND_ALLOWLIST",
        scope=SCOPE_GLOBAL_ONLY,
        kind="command_allowlist",
        required=False,
        default_static=None,
    ),
    "agentic_sdlc.bin_path": FieldSpec(
        key="agentic_sdlc.bin_path",
        env_var="AGENTIC_SDLC_BIN",
        scope=SCOPE_GLOBAL_ONLY,
        kind="executable",
        required=False,
        default_computed=lambda: shutil.which("agentic-sdlc"),
    ),
    "knowledge_store.home": FieldSpec(
        key="knowledge_store.home",
        env_var="KNOWLEDGE_STORE_HOME",
        scope=SCOPE_GLOBAL_ONLY,
        kind="path",
        required=False,
        default_static=None,
    ),
}


def _spec(key: str) -> FieldSpec:
    try:
        return FIELDS[key]
    except KeyError as error:
        raise SettingsError(f"unknown settings key: {key!r}") from error


def known_keys() -> list[str]:
    return sorted(FIELDS)


# ---------------------------------------------------------------------------
# Config file discovery + loading, cached per process.
# ---------------------------------------------------------------------------

_FILE_CACHE: dict[str, dict[str, Any]] = {}
_INTERACTIVE_DISABLED = False
# See disable_project_tier_cwd_fallback(). Only suppresses the *implicit*
# cwd anchor; an explicit start= is always honored.
_PROJECT_TIER_CWD_FALLBACK_DISABLED = False


def reset_cache() -> None:
    """Clear the per-process config-file cache. Call after `write_setting`,
    and at the start of any test that needs isolation from a prior test's
    resolved config-file state."""
    _FILE_CACHE.clear()


def disable_interactive() -> None:
    """Hard opt-out for interactive prompting for the remaining lifetime of
    this process. Call unconditionally at the top of any stdio-transport
    entry point (e.g. the MCP dispatch server), where stdin is a protocol
    channel and prompting would corrupt it."""
    global _INTERACTIVE_DISABLED
    _INTERACTIVE_DISABLED = True


def disable_project_tier_cwd_fallback() -> None:
    """Stop treating this process's working directory as a project anchor.

    When `start=` is not passed, the project tier is discovered by walking
    up from `Path.cwd()`. That is right for a CLI a human ran inside a
    project, and wrong for a long-lived, project-agnostic process -- an
    stdio MCP server's cwd is wherever its host CLI happened to be
    launched, which has no relationship to the project a given tool call
    is about. Resolving a `project_or_global` field from that incidental
    directory means an unrelated checkout's `.agents/cadre.yaml` can steer
    a call it has nothing to do with.

    After this is called, a resolution with no explicit `start=` skips the
    project tier entirely rather than guessing from cwd. An explicit
    `start=<path>` is still honored -- it is a validated anchor the caller
    supplied on purpose (e.g. `dispatch_core`'s `project_root`), not an
    ambient one -- so a server can still resolve correctly for the project
    a tool call actually names.

    Mirrors `disable_interactive()`: call it unconditionally at an entry
    point, never from shared core logic, since the same core is also
    imported by CLIs where cwd *is* the right anchor.
    """
    global _PROJECT_TIER_CWD_FALLBACK_DISABLED
    _PROJECT_TIER_CWD_FALLBACK_DISABLED = True


def _global_config_dir(env: dict[str, str]) -> Path:
    xdg = env.get("XDG_CONFIG_HOME") or os.environ.get("XDG_CONFIG_HOME")
    base = Path(xdg).expanduser() if xdg else Path.home() / ".config"
    return base / GLOBAL_CONFIG_APP_DIR


def _reject_symlink_escape_on_read(candidate: Path) -> Path:
    """Guard the read path the same way `write_setting` already guards the
    write path: `find_file_at_project_root`'s `candidate.is_file()` check
    follows symlinks, so a malicious `.agents/cadre.yaml` (or a symlinked
    `.agents` directory) shipped in an untrusted, clonable project can point
    outside the project entirely. Reject that before the file is ever
    opened/parsed, rather than silently reading (and, on a parse failure,
    risking an error message that quotes) whatever it resolves to."""
    # relative_path passed to find_file_at_project_root is always exactly
    # PROJECT_CONFIG_DIR / "<basename>.<ext>" (two components), so the
    # directory this candidate was actually discovered under -- before any
    # symlink is followed -- is candidate.parent.parent.
    root = candidate.parent.parent.resolve(strict=False)
    resolved_candidate = candidate.resolve(strict=False)
    if not _is_same_or_descendant(resolved_candidate, root):
        raise SettingsError(
            f"{candidate} resolves outside of {root} (via a symlink); a project-local "
            "cadre config file/directory may not point outside the project it was found in"
        )
    return candidate


def _project_config_candidates(start: Path | None) -> tuple[Path | None, Path | None]:
    # No explicit anchor, and this process has declared its cwd meaningless
    # (see disable_project_tier_cwd_fallback) -- skip the project tier
    # rather than walking up from an incidental directory.
    if start is None and _PROJECT_TIER_CWD_FALLBACK_DISABLED:
        return None, None
    yaml_path = find_file_at_project_root(PROJECT_CONFIG_DIR / f"{PROJECT_CONFIG_BASENAME}.yaml", start)
    json_path = find_file_at_project_root(PROJECT_CONFIG_DIR / f"{PROJECT_CONFIG_BASENAME}.json", start)
    if yaml_path is not None:
        yaml_path = _reject_symlink_escape_on_read(yaml_path)
    if json_path is not None:
        json_path = _reject_symlink_escape_on_read(json_path)
    return yaml_path, json_path


def _global_config_candidates(env: dict[str, str]) -> tuple[Path, Path]:
    directory = _global_config_dir(env)
    return directory / f"{GLOBAL_CONFIG_BASENAME}.yaml", directory / f"{GLOBAL_CONFIG_BASENAME}.json"


def _select_existing(yaml_path: Path | None, json_path: Path | None, tier_label: str) -> Path | None:
    yaml_exists = yaml_path is not None and yaml_path.is_file()
    json_exists = json_path is not None and json_path.is_file()
    if yaml_exists and json_exists:
        raise SettingsError(
            f"both {yaml_path} and {json_path} exist; only one {tier_label} cadre config file "
            "may exist at a time -- remove one"
        )
    if yaml_exists:
        return yaml_path
    if json_exists:
        return json_path
    return None


def _load_config_file(path: Path) -> dict[str, Any]:
    cache_key = str(path)
    if cache_key in _FILE_CACHE:
        return _FILE_CACHE[cache_key]
    text = path.read_text(encoding="utf-8")
    if not text.strip():
        data: Any = {}
    elif path.suffix.lower() == ".json":
        try:
            data = json.loads(text)
        except json.JSONDecodeError as error:
            # Deliberately never includes `error`'s own message text below
            # the path: a project-local config path may resolve through a
            # symlink to a file this process has no business quoting back
            # to the caller, and JSONDecodeError.msg/.doc can echo a
            # snippet of the parsed content.
            raise SettingsError(f"{path}: not valid JSON") from error
    else:
        try:
            yaml = _require_yaml()
        except RuntimeError as error:
            raise SettingsError(f"{path}: {error}") from error
        try:
            data = yaml.safe_load(text)
        except yaml.YAMLError as error:
            # Same rationale as the JSON branch above: never echo the
            # parser's own message, which can quote a snippet of the
            # offending (possibly symlink-redirected) file's content.
            raise SettingsError(f"{path}: not valid YAML") from error
        if data is None:
            data = {}
    if not isinstance(data, dict):
        raise SettingsError(f"{path}: root of a cadre config file must be a mapping")
    _reject_secret_shaped_keys(data, "", path)
    _FILE_CACHE[cache_key] = data
    return data


def project_config_path(start: Path | None = None) -> Path | None:
    """The resolved project-local config file path, or None if absent."""
    yaml_path, json_path = _project_config_candidates(start)
    return _select_existing(yaml_path, json_path, "project-local")


def global_config_path(env: dict[str, str] | None = None) -> Path:
    """The resolved (existing-or-not) user-global config file path.
    Returns the `.yaml` candidate when neither file exists, since that is
    where a fresh write goes by default."""
    env = env if env is not None else os.environ
    yaml_path, json_path = _global_config_candidates(env)
    existing = _select_existing(yaml_path, json_path, "user-global")
    return existing if existing is not None else yaml_path


def _lookup_nested(data: dict[str, Any], key: str) -> tuple[bool, Any]:
    node: Any = data
    for part in key.split("."):
        if not isinstance(node, dict) or part not in node:
            return False, None
        node = node[part]
    return True, node


# ---------------------------------------------------------------------------
# Resolution.
# ---------------------------------------------------------------------------


def _display_default(spec: FieldSpec) -> Any:
    if spec.default_static is not _UNSET:
        return spec.default_static
    if spec.default_computed is not None:
        try:
            return spec.default_computed()
        except Exception:  # noqa: BLE001 - best-effort display only
            return None
    return None


def _fail_closed_message(spec: FieldSpec, checks: list[str], global_path: Path) -> str:
    lines = [f"{spec.key} is not configured."]
    lines.extend(f"  checked: {line}" for line in checks)
    hint_env = f"Set {spec.env_var} or " if spec.env_var else ""
    lines.append(
        f"{hint_env}add `{spec.key.split('.')[-1]}` under `{spec.key.split('.')[0]}:` in "
        f"{global_path}, or re-run with `cadre --interactive ...`."
    )
    return "\n".join(lines)


# Set only by _cmd_resolve's own tty-bound prompt path (see _open_tty_io
# below), never by any other caller. `cadre config resolve <key>` -- the
# packaged shell wrapper's only way to consult this resolver -- is always
# invoked as `x=$(... resolve key)`, a command substitution whose child
# process's stdout is unconditionally a pipe, never a tty, regardless of
# the real controlling terminal or CADRE_INTERACTIVE=1. Gating solely on
# `sys.stdout.isatty()` would make --interactive prompting through that
# subcommand permanently unreachable. When _cmd_resolve has independently
# confirmed a controlling terminal exists (by successfully opening
# /dev/tty) and has rebound the prompt's own input/output to that terminal
# (never to the piped stdout/stdin), this override lets the gate open on
# stdin-tty alone for that one call -- every other caller (in-process
# Python callers whose real stdout is what a human is looking at) is
# unaffected and still requires both.
_STDOUT_TTY_OVERRIDE: bool | None = None


def _interactive_gate_open(env: dict[str, str]) -> bool:
    if _INTERACTIVE_DISABLED:
        return False
    if env.get(INTERACTIVE_ENV_VAR) != "1":
        return False
    try:
        if not sys.stdin.isatty():
            return False
        stdout_ok = sys.stdout.isatty() if _STDOUT_TTY_OVERRIDE is None else _STDOUT_TTY_OVERRIDE
        if not stdout_ok:
            return False
    except Exception:  # noqa: BLE001 - a non-stream stdin/stdout is not a tty
        return False
    return True


@contextlib.contextmanager
def _stdout_tty_override(value: bool):
    global _STDOUT_TTY_OVERRIDE
    previous = _STDOUT_TTY_OVERRIDE
    _STDOUT_TTY_OVERRIDE = value
    try:
        yield
    finally:
        _STDOUT_TTY_OVERRIDE = previous


def _open_tty_io() -> tuple[Callable[[str], str], Callable[[str], None]] | None:
    """Best-effort open of the controlling terminal for prompt I/O, used
    only by `_cmd_resolve` below. `/dev/tty` (POSIX) refers to whatever
    terminal is actually controlling this process, independent of this
    process's own stdin/stdout redirection -- exactly the property needed
    when stdout is a shell command-substitution pipe capturing a resolved
    value, not a place prompt text can go. Returns None (not an exception)
    if there is no controlling terminal (e.g. truly non-interactive), so
    the caller falls through to the ordinary non-prompting path."""
    if sys.platform == "win32":
        return None
    # Two separate handles rather than one "r+": a buffered read-write text
    # stream requires a seekable file, and /dev/tty is a character device,
    # so open("/dev/tty", "r+") raises io.UnsupportedOperation ("File or
    # stream is not seekable") on modern CPython. Opening read and write
    # sides independently avoids the seekability requirement entirely.
    reader = writer = None
    try:
        reader = open("/dev/tty", "r", encoding="utf-8")  # noqa: SIM115 - held for the process lifetime
        writer = open("/dev/tty", "w", encoding="utf-8")  # noqa: SIM115 - held for the process lifetime
    except (OSError, ValueError):
        for handle in (reader, writer):
            if handle is not None:
                with contextlib.suppress(Exception):
                    handle.close()
        return None

    def input_func(prompt: str) -> str:
        writer.write(prompt)
        writer.flush()
        line = reader.readline()
        if not line:
            raise EOFError("controlling terminal closed during prompt")
        return line.rstrip("\n")

    def output_func(text: str) -> None:
        writer.write(text + "\n")
        writer.flush()

    return input_func, output_func


def _prompt_tier_choice(spec: FieldSpec, input_func: Callable[[str], str], output_func: Callable[[str], None]) -> str | None:
    allow_project = spec.scope == SCOPE_PROJECT_OR_GLOBAL
    choices = "project/global/skip" if allow_project else "global/skip"
    output_func(f"Save {spec.key} to which tier? ({choices}, default: global)")
    raw = (input_func("> ") or "").strip().lower()
    if not raw or raw == "global":
        return "global"
    if raw == "skip":
        return None
    if raw == "project" and allow_project:
        return "project"
    output_func(f"  unrecognized choice {raw!r}; defaulting to global")
    return "global"


def _prompt_for(
    spec: FieldSpec,
    *,
    input_func: Callable[[str], str] | None,
    output_func: Callable[[str], None] | None,
    start: Path | None,
) -> Resolved | None:
    if spec.secret:
        return None
    raw_input_func = input_func or input
    output_func = output_func or (lambda text: sys.stdout.write(text + "\n"))

    def input_func(prompt: str) -> str:
        # Ctrl-D, or a controlling terminal that closes mid-prompt, is an
        # ordinary way to abandon an interactive prompt -- not something
        # that should escape as a raw traceback. Both the builtin input()
        # and _open_tty_io()'s reader raise EOFError there; convert it to
        # this module's usual fail-closed SettingsError so every caller's
        # existing `except SettingsError` handling (including
        # `cadre config resolve`'s clean stderr message + exit 1) applies
        # uniformly. KeyboardInterrupt is deliberately NOT caught: Ctrl-C
        # should stay an interrupt, not become a settings error.
        try:
            return raw_input_func(prompt)
        except EOFError as error:
            raise SettingsError(
                f"{spec.key}: input stream closed before a value was entered "
                "(prompt cancelled); set it via the environment or a config file instead"
            ) from error

    default_value = _display_default(spec)
    output_func(f"{spec.key} is not configured.")
    if spec.env_var:
        output_func(f"  env var: {spec.env_var}")
    output_func(f"  default: {default_value!r}" if default_value is not None else "  default: (none)")
    for _attempt in range(3):
        raw = input_func(f"Enter value for {spec.key} (blank = default): ")
        raw = raw if raw is not None else ""
        if not raw.strip():
            if default_value is not None:
                value = default_value
            else:
                output_func("  no default available; please enter a value")
                continue
        else:
            try:
                value = _validate(spec, raw)
            except SettingsError as error:
                output_func(f"  invalid: {error}")
                continue
        tier = _prompt_tier_choice(spec, input_func, output_func)
        origin_path = None
        if tier is not None:
            origin_path = write_setting(spec.key, value, tier=tier, start=start)
            reset_cache()
        return Resolved(spec.key, value, "prompt", origin_path)
    raise SettingsError(f"{spec.key}: no valid value entered after 3 attempts")


def _resolve_core(
    key: str,
    *,
    start: Path | None,
    env: dict[str, str] | None,
    input_func: Callable[[str], str] | None,
    output_func: Callable[[str], None] | None,
    allow_prompt: bool,
) -> Resolved | None:
    spec = _spec(key)
    env = env if env is not None else os.environ

    checks: list[str] = []

    # 1. Environment variable.
    if spec.env_var:
        raw = env.get(spec.env_var)
        if raw is not None:
            if not raw.strip():
                raise SettingsError(f"{spec.env_var} is set but empty/whitespace-only")
            return Resolved(spec.key, _validate(spec, raw), "env", None)
        checks.append(f"environment {spec.env_var} -> not set")

    # 2. Project-local file.
    project_path = project_config_path(start)
    if project_path is not None:
        data = _load_config_file(project_path)
        found, raw_value = _lookup_nested(data, key)
        # Deliberately fires on `found` alone, not `found and raw_value is not
        # None`: an explicit `null` still means this key was placed in an
        # untrusted project-local file for a field that may never be set
        # there, and the module's own "never silently ignored" invariant
        # applies to the key's presence, not just a non-null value -- a
        # project file cannot use `null` to probe/no-op past this check.
        if found and spec.scope == SCOPE_GLOBAL_ONLY:
            raise SettingsScopeError(
                f"{key} may only be set via {spec.env_var or 'the environment'} or the "
                f"user-global config file, never the project-local file ({project_path}); "
                "project-local configuration is untrusted, clonable repository content -- "
                "remove this key from there"
            )
        if found and raw_value is not None:
            return Resolved(spec.key, _validate(spec, raw_value), "project-file", project_path)
        if found:
            checks.append(f"{project_path} -> found, key explicitly null (not set at this tier)")
        else:
            checks.append(f"{project_path} -> found, key absent")
    elif start is None and _PROJECT_TIER_CWD_FALLBACK_DISABLED:
        # Don't name a cwd-derived path here -- this process declared its
        # working directory meaningless, so quoting it would send the
        # reader to a file that was never consulted and would not be read
        # even if they created it.
        checks.append(
            "project-local config -> not consulted (no project anchor supplied and this "
            "process does not treat its working directory as one)"
        )
    else:
        yaml_candidate, _json_candidate = _project_config_candidates(start)
        expected = yaml_candidate if yaml_candidate is not None else (
            (start or Path.cwd()).resolve() / PROJECT_CONFIG_DIR / f"{PROJECT_CONFIG_BASENAME}.yaml"
        )
        checks.append(f"{expected} -> not found")

    # 3. User-global file.
    global_yaml, global_json = _global_config_candidates(env)
    global_selected = _select_existing(global_yaml, global_json, "user-global")
    if global_selected is not None:
        data = _load_config_file(global_selected)
        found, raw_value = _lookup_nested(data, key)
        if found and raw_value is not None:
            return Resolved(spec.key, _validate(spec, raw_value), "global-file", global_selected)
        if found:
            checks.append(f"{global_selected} -> found, key explicitly null (not set at this tier)")
        else:
            checks.append(f"{global_selected} -> found, key absent")
    else:
        checks.append(f"{global_yaml} -> not found")

    # 4. Static default.
    if spec.default_static is not _UNSET:
        return Resolved(spec.key, spec.default_static, "default", None)

    # 5. Computed default.
    if spec.default_computed is not None:
        computed = spec.default_computed()
        if computed is not None:
            return Resolved(spec.key, computed, "computed", None)

    # 6. Interactive prompt.
    if allow_prompt and _interactive_gate_open(env):
        prompted = _prompt_for(spec, input_func=input_func, output_func=output_func, start=start)
        if prompted is not None:
            return prompted

    # 7. Fail closed (only for required fields; optional fields resolve to
    # an absent value instead).
    if spec.required:
        raise SettingsError(_fail_closed_message(spec, checks, global_yaml))
    return None


def resolve_with_origin(
    key: str,
    *,
    start: Path | None = None,
    env: dict[str, str] | None = None,
    input_func: Callable[[str], str] | None = None,
    output_func: Callable[[str], None] | None = None,
) -> Resolved:
    resolved = _resolve_core(
        key, start=start, env=env, input_func=input_func, output_func=output_func, allow_prompt=True
    )
    if resolved is None:
        # Only reachable for a non-required field that resolved to "unset";
        # represent that explicitly rather than returning None from a
        # function documented to always return a Resolved.
        return Resolved(key, None, "default", None)
    return resolved


def resolve_setting(
    key: str,
    *,
    start: Path | None = None,
    env: dict[str, str] | None = None,
    input_func: Callable[[str], str] | None = None,
    output_func: Callable[[str], None] | None = None,
) -> Any:
    return resolve_with_origin(
        key, start=start, env=env, input_func=input_func, output_func=output_func
    ).value


def resolve_optional(
    key: str,
    *,
    start: Path | None = None,
    env: dict[str, str] | None = None,
    input_func: Callable[[str], str] | None = None,
    output_func: Callable[[str], None] | None = None,
) -> Any:
    # Unlike resolve_setting, this is documented to never raise for an
    # ordinary "field simply isn't configured" outcome -- it resolves to
    # None instead, so callers like
    # agentic_sdlc_contracts.try_lifecycle_contract() can keep their
    # documented graceful "unavailable" fallback rather than crashing.
    # SettingsScopeError is deliberately NOT caught here: a project-local
    # file setting a global_only field is a security event (untrusted,
    # clonable repository content trying to steer an executable path or a
    # data-store location), and per this module's own trust-scope
    # invariant it must never be silently ignored, even by the "optional"
    # resolver.
    try:
        return resolve_setting(
            key, start=start, env=env, input_func=input_func, output_func=output_func
        )
    except SettingsScopeError:
        raise
    except SettingsError:
        return None


def resolve_many(
    keys: list[str],
    *,
    start: Path | None = None,
    env: dict[str, str] | None = None,
    input_func: Callable[[str], str] | None = None,
    output_func: Callable[[str], None] | None = None,
) -> dict[str, Any]:
    return {
        key: resolve_setting(key, start=start, env=env, input_func=input_func, output_func=output_func)
        for key in keys
    }


def effective_settings(
    *, start: Path | None = None, env: dict[str, str] | None = None
) -> list[Resolved]:
    """Non-interactive, never-raising snapshot of every known setting's
    resolved value, origin, and source path -- backs `cadre config show`.
    Secret-classified fields (none currently registered) would be excluded
    here rather than resolved."""
    results: list[Resolved] = []
    for key in known_keys():
        spec = FIELDS[key]
        if spec.secret:
            continue
        try:
            resolved = _resolve_core(
                key, start=start, env=env, input_func=None, output_func=None, allow_prompt=False
            )
        except SettingsError as error:
            results.append(Resolved(key, None, "unresolved", None))
            _ = error  # detail intentionally not embedded in the Resolved record
            continue
        results.append(resolved if resolved is not None else Resolved(key, None, "unset", None))
    return results


# ---------------------------------------------------------------------------
# Writing.
# ---------------------------------------------------------------------------

_HEADER_COMMENT = """\
# Generated by cadre's settings resolver (roster/shared/src/settings.py).
#
# Resolution precedence per field: environment variable > this file's
# project-local counterpart (.agents/cadre.yaml, only for fields not marked
# global-only) > this user-global file > built-in default > interactive
# prompt.
#
# Secrets (service tokens, API keys) are never read from or written to this
# file -- they are always environment-variable only.
#
# Known keys and their environment-variable equivalents:
#   gitlab.base_url                        GITLAB_BASE_URL           (global-only)
#   gitlab.project_id                      GITLAB_DOCS_PROJECT_ID
#   gitlab.supports_work_item_hierarchy    GITLAB_SUPPORTS_WORK_ITEM_HIERARCHY
#   runners.claude_bin                     SECURE_CLOUD_AGENTS_CLAUDE_BIN (global-only)
#   runners.codex_bin                      SECURE_CLOUD_AGENTS_CODEX_BIN  (global-only)
#   runners.codex_profile                  SECURE_CLOUD_AGENTS_CODEX_PROFILE          (global-only)
#   runners.local_model_opus               SECURE_CLOUD_AGENTS_LOCAL_MODEL_OPUS       (global-only)
#   runners.local_model_sonnet             SECURE_CLOUD_AGENTS_LOCAL_MODEL_SONNET     (global-only)
#   runners.local_model_haiku              SECURE_CLOUD_AGENTS_LOCAL_MODEL_HAIKU      (global-only)
#   runners.forward_env                    SECURE_CLOUD_AGENTS_FORWARD_ENV            (global-only)
#   runners.api_base_url                   SECURE_CLOUD_AGENTS_API_BASE_URL           (global-only)
#   runners.api_key_env                    SECURE_CLOUD_AGENTS_API_KEY_ENV            (global-only)
#   runners.api_allow_writes               SECURE_CLOUD_AGENTS_API_ALLOW_WRITES       (global-only)
#   runners.api_command_allowlist          SECURE_CLOUD_AGENTS_API_COMMAND_ALLOWLIST  (global-only)
#   agentic_sdlc.bin_path                  AGENTIC_SDLC_BIN               (global-only)
#   knowledge_store.home                   KNOWLEDGE_STORE_HOME           (global-only)
#
# runners.api_key_env holds the *name* of the variable containing the
# self-hosted endpoint's API key, never the key itself.
"""


def _set_nested(data: dict[str, Any], key: str, value: Any) -> dict[str, Any]:
    parts = key.split(".")
    node = data
    for part in parts[:-1]:
        existing = node.get(part)
        if not isinstance(existing, dict):
            existing = {}
            node[part] = existing
        node = existing
    node[parts[-1]] = value
    return data


class _QuotedString(str):
    """Marker subclass so the YAML dumper always emits string scalars with
    explicit quoting, closing the `007`/`~`/`yes` YAML-scalar-hazard class
    of bug on read-back."""


def _quote_all_strings(node: Any) -> Any:
    if isinstance(node, dict):
        return {key: _quote_all_strings(value) for key, value in node.items()}
    if isinstance(node, list):
        return [_quote_all_strings(item) for item in node]
    if isinstance(node, str):
        return _QuotedString(node)
    return node


def _dump_yaml_with_quoted_strings(data: dict[str, Any]) -> str:
    yaml = _require_yaml()

    def _quoted_str_representer(dumper, value):  # noqa: ANN001 - PyYAML callback signature
        return dumper.represent_scalar("tag:yaml.org,2002:str", str(value), style='"')

    class _Dumper(yaml.SafeDumper):
        pass

    _Dumper.add_representer(_QuotedString, _quoted_str_representer)
    return yaml.dump(_quote_all_strings(data), Dumper=_Dumper, sort_keys=False)


def _find_project_git_root(start: Path | None) -> Path | None:
    current = (start or Path.cwd()).resolve()
    for _ in range(MAXIMUM_WALK_DEPTH):
        if (current / ".git").exists():
            return current
        parent = current.parent
        if parent == current:
            return None
        current = parent
    return None


def _write_atomic(path: Path, content: str, *, mode: int) -> None:
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    try:
        os.chmod(path.parent, 0o700)
    except OSError:
        pass
    fd, tmp_name = tempfile.mkstemp(dir=str(path.parent), prefix=f".{path.name}.", suffix=".tmp")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            handle.write(content)
        os.chmod(tmp_name, mode)
        os.replace(tmp_name, path)
    except BaseException:
        try:
            os.unlink(tmp_name)
        except OSError:
            pass
        raise


def write_setting(key: str, value: Any, *, tier: str, start: Path | None = None) -> Path:
    """Write `value` for `key` into the project-local or user-global config
    file, atomically, preserving unrecognized existing keys. Returns the
    written path."""
    spec = _spec(key)
    if tier not in ("project", "global"):
        raise SettingsError(f"tier must be 'project' or 'global', got {tier!r}")
    if tier == "project" and spec.scope == SCOPE_GLOBAL_ONLY:
        raise SettingsError(
            f"{key} is global-only and may not be written to the project-local config file"
        )

    validated = _validate(spec, value)

    if tier == "project":
        project_root = _find_project_git_root(start)
        if project_root is None:
            raise SettingsError("no project (.git boundary) found above the current directory to write into")
        existing_path = project_config_path(start)
        target = existing_path if existing_path is not None else (
            project_root / PROJECT_CONFIG_DIR / f"{PROJECT_CONFIG_BASENAME}.yaml"
        )
        resolved_root = project_root.resolve()
        resolved_target = target.resolve(strict=False)
        if not _is_same_or_descendant(resolved_target, resolved_root):
            raise SettingsError(f"refusing to write outside the project root (possible symlink escape): {target}")
        file_mode = 0o600
        dir_mode = 0o755
    else:
        env = os.environ
        target = global_config_path(env)
        global_dir = _global_config_dir(env)
        ancestor = _resolve_existing_ancestor(global_dir)
        if not _is_same_or_descendant(target, ancestor):
            raise SettingsError(f"refusing to write outside the global config directory: {target}")
        file_mode = 0o600
        dir_mode = 0o700

    existing: dict[str, Any] = {}
    if target.is_file():
        existing = dict(_load_config_file(target))

    _set_nested(existing, key, validated)
    existing["schema_version"] = 1

    if target.suffix.lower() == ".json":
        content = json.dumps(existing, indent=2, sort_keys=False) + "\n"
    else:
        content = _HEADER_COMMENT + "\n" + _dump_yaml_with_quoted_strings(existing)

    target.parent.mkdir(parents=True, exist_ok=True, mode=dir_mode)
    _write_atomic(target, content, mode=file_mode)
    reset_cache()
    return target


# ---------------------------------------------------------------------------
# CLI (`cadre config show` / `cadre config path`).
# ---------------------------------------------------------------------------

_SECRET_ENV_VARS = ("GITLAB_SVC_TOKEN", "KNOWLEDGE_EMBEDDING_API_KEY")


def _format_show_line(resolved: Resolved) -> str:
    source = resolved.origin_path if resolved.origin_path is not None else f"({resolved.origin})"
    return f"{resolved.key:<40} {resolved.value!r:<30} origin={resolved.origin:<12} source={source}"


def _cmd_show(args: list[str]) -> int:
    for resolved in effective_settings():
        print(_format_show_line(resolved))
    print("")
    print("env-only secrets (never read from or written to a config file):")
    for name in _SECRET_ENV_VARS:
        state = "set" if os.environ.get(name, "").strip() else "not set"
        print(f"  env-only: {name} ({state})")
    return 0


def _cmd_path(args: list[str]) -> int:
    project = project_config_path()
    print(f"project-local: {project if project is not None else 'not found'}")
    print(f"user-global:   {global_config_path()}")
    return 0


def _cmd_resolve(args: list[str]) -> int:
    """`cadre config resolve <key>` -- print a single non-secret setting's
    resolved value (or nothing, with exit 0, if it resolves to "unset") for
    a shell caller to consume via command substitution. Exists specifically
    so the packaged POSIX-sh `bin/cadre` wrapper (which cannot itself parse
    YAML/JSON or apply trust-scope rules) can resolve `agentic_sdlc.bin_path`
    through the exact same precedence chain as this repo's Python
    `bin/cadre.py` dispatcher, instead of the wrapper hand-rolling a second,
    env-var-and-`command -v`-only resolution that silently ignores a
    configured value. `CADRE_INTERACTIVE` is honored from the real process
    environment exactly as any other caller would see it -- the wrapper
    exports it before invoking this, never passes it as an argument here.
    On a `SettingsError` (including a `global_only` scope violation), the
    message is printed to stderr and this exits 1, matching every other
    fail-closed path in this module -- callers must propagate that exit
    code, not swallow it.

    If the field is unresolved and CADRE_INTERACTIVE=1, prompting still
    works even though this process's own stdout is a pipe (the caller's
    command substitution capturing the eventual resolved value): prompt
    input/output is rebound to /dev/tty via `_open_tty_io`, and
    `_stdout_tty_override` opens the interactive gate for the duration of
    that one resolution -- nothing prompt-related ever touches the real,
    captured stdout, only the final resolved value does."""
    if len(args) != 1:
        print("usage: cadre config resolve <key>", file=sys.stderr)
        return 2
    key = args[0]
    try:
        spec = _spec(key)
        if spec.secret:
            print(
                f"{key} is a secret-classified field and cannot be resolved via this command",
                file=sys.stderr,
            )
            return 2
        tty_io = _open_tty_io() if os.environ.get(INTERACTIVE_ENV_VAR) == "1" else None
        if tty_io is not None:
            input_func, output_func = tty_io
            with _stdout_tty_override(True):
                value = resolve_optional(key, input_func=input_func, output_func=output_func)
        else:
            value = resolve_optional(key)
    except SettingsError as error:
        print(str(error), file=sys.stderr)
        return 1
    if value is not None:
        print(value)
    return 0


def _cmd_set(argv: list[str]) -> int:
    """Write a setting, without requiring the human to hand-edit YAML.

    `write_setting()` has existed all along -- the interactive prompt uses it
    -- but nothing exposed it non-interactively, so the documented way to
    configure a project was to find the right file and edit it by hand. That
    is a poor answer for a value like a knowledge-store path, and an actively
    bad one for anything scope-restricted, where hand-editing the wrong file
    fails at use time with an error about trust scope.
    """
    tier = "project"
    positional: list[str] = []
    for argument in argv:
        if argument == "--global":
            tier = "global"
        elif argument == "--project":
            tier = "project"
        elif argument.startswith("-"):
            print(f"cadre config set: unknown option {argument}", file=sys.stderr)
            return 2
        else:
            positional.append(argument)

    if len(positional) != 2:
        print("usage: cadre config set [--project|--global] <key> <value>", file=sys.stderr)
        return 2

    key, raw = positional
    try:
        written = write_setting(key, raw, tier=tier)
    except SettingsError as error:
        # Includes the global-only scope violation, which is the case most
        # worth reporting clearly rather than as a traceback: that field
        # selects an executable, so a project-local file may never set it.
        print(f"cadre config set: {error}", file=sys.stderr)
        return 1

    print(f"{key} = {raw}")
    print(f"written to {written}")
    return 0


def main(argv: list[str] | None = None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    if not argv or argv[0] not in ("show", "path", "resolve", "set"):
        print("usage: cadre config <show|path|resolve KEY|set KEY VALUE>", file=sys.stderr)
        return 2
    if argv[0] == "show":
        return _cmd_show(argv[1:])
    if argv[0] == "resolve":
        return _cmd_resolve(argv[1:])
    if argv[0] == "set":
        return _cmd_set(argv[1:])
    return _cmd_path(argv[1:])


if __name__ == "__main__":
    raise SystemExit(main())
