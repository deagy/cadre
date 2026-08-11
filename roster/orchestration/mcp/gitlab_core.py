"""Pure-Python core logic for the GitLab-evidence MCP server.

Exposes a small, deliberately create-only set of GitLab operations (a
review-subtask issue, a wiki page, an evidence comment) so any agent -- in
this repo or a consuming project -- can record human-reviewable evidence
against a single, pre-configured, docs-only GitLab project without ever being
able to transition an issue's state (close/reopen/resolve/relabel-away-from
open-review). This module mirrors `dispatch_core.py`'s architecture exactly:

- Zero dependency on the optional `mcp` package (or anything else outside the
  standard library plus this module's own sibling `dispatch_core.py`), so it
  can be imported and unit tested with all HTTP calls mocked, with no live
  GitLab instance and no third-party HTTP client. `gitlab_server.py` is the
  thin protocol adapter that depends on `mcp`; this module is the reviewable
  safety core.
- Reuses `dispatch_core.ConfirmationGate` verbatim (not a reimplementation)
  for `write_wiki_page`'s mandatory human-confirmation gate. `ConfirmationGate`
  is already fully generic -- its five bound fields (`role_id`, `brief`,
  `mode`, `classification`, `effective_sandbox`) are only ever hashed and
  compared for exact equality, never validated against dispatch-specific
  vocabularies -- so this module binds them to GitLab-shaped values (tool
  name, a hash of the write's arguments, a fixed "human_approval" mode
  string, a fixed "internal" classification, and a fixed "wiki-write" sandbox
  label) instead of inventing a second confirmation mechanism.
- Reuses `dispatch_core.wrap_untrusted_output`'s marker-token pattern (via
  `_wrap_untrusted_gitlab_payload` below) for every piece of GitLab-retrieved
  content handed back in a tool result, so retrieved issue/wiki/comment text
  can never be mistaken for an instruction by the calling model.
- Reuses `dispatch_core.build_audit_record`/`write_audit_record` verbatim
  (not a reimplementation) for a structured, append-only JSON-lines audit
  trail of every call to the three tools in this module -- every
  confirmation-requested / confirmed / denied / unavailable / ok outcome,
  not only final success -- written to its own file
  (`~/.agents/mcp-gitlab/audit.jsonl`, see `GITLAB_AUDIT_LOG_PATH` below),
  separate from `dispatch_core`'s own `~/.agents/mcp-dispatch/audit.jsonl`.
  Records never contain the GitLab token, wiki/comment/issue body content,
  or a confirmation token value -- only identifiers, hashes/lengths, and the
  decision -- matching `dispatch_core`'s own forbidden-audit-key discipline
  (`_FORBIDDEN_AUDIT_KEYS`), which `build_audit_record` enforces for this
  module too since it is the same function.

Deliberate, hard, structural invariant: this module never implements any
function that closes, reopens, resolves, or relabels-away-from-open-review
an issue, and never calls such a function on any caller's behalf. Search
this file for the string "STATE TRANSITION" if you are looking for the one
comment that exists specifically to keep that invariant visible to a future
editor; there is no such function to find beyond that comment.

Deliberate, stated scope boundary (not a silent omission): unlike
`dispatch_core.py`'s tools, none of this module's three tools accepts or
enforces a `classification` parameter on the GitLab write it performs. The
human-accepted residual-risk decision for this integration is that scope
containment is achieved operationally -- a dedicated, docs-only GitLab
project and a least-privilege service token scoped to only that project --
rather than by an in-code classification check here. See
`../SECURITY-CONTROLS.md`'s GitLab section for the same statement.
"""

from __future__ import annotations

import dataclasses
import hashlib
import json
import os
import random
import re
import socket
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

_MODULE_DIR = Path(__file__).resolve().parent
if str(_MODULE_DIR) not in sys.path:
    sys.path.insert(0, str(_MODULE_DIR))

import dispatch_core as _dispatch_core  # noqa: E402  (sys.path set above)

# roster/shared/src is appended (never inserted at index 0): this module's
# own directory already holds nothing named "settings", but appending keeps
# the discipline consistent with every other call site that reuses this
# module across the repository, so a caller's own same-named module is never
# shadowed by this one.
_SHARED_SRC_DIR = _MODULE_DIR.parent.parent / "shared" / "src"
if str(_SHARED_SRC_DIR) not in sys.path:
    sys.path.append(str(_SHARED_SRC_DIR))

import settings as _settings  # noqa: E402  (sys.path set above)

# ---------------------------------------------------------------------------
# Configuration: exactly three required env vars, no aliases.
# ---------------------------------------------------------------------------

# Settled decision: exactly this name. GL_SVC_TOKEN / GITLAB_SERVICE_TOKEN are
# explicitly rejected, not merely deprioritized -- there is no alias-lookup
# code anywhere in this module, so a caller relying on either alias fails
# closed with a message naming *this* variable, never silently picking up a
# same-shaped alias.
GITLAB_TOKEN_ENV_VAR = "GITLAB_SVC_TOKEN"
# The plain (non-secret) three are sourced from settings.FIELDS rather than
# hardcoded here, so this name and the one actually consulted by
# resolve_config() (via settings.resolve_many) cannot drift apart.
GITLAB_BASE_URL_ENV_VAR = _settings.FIELDS["gitlab.base_url"].env_var
GITLAB_PROJECT_ID_ENV_VAR = _settings.FIELDS["gitlab.project_id"].env_var
GITLAB_HIERARCHY_ENV_VAR = _settings.FIELDS["gitlab.supports_work_item_hierarchy"].env_var

MAX_EVIDENCE_COMMENT_BYTES = 1 * 1024 * 1024  # 1 MiB, UTF-8 encoded content
# Wiki pages are documented as this integration's home for durable,
# structured documentation (design records, architecture decisions), which
# can reasonably be larger than a single evidence comment -- a 2 MiB cap
# (2x the evidence-comment cap) rather than reusing MAX_EVIDENCE_COMMENT_BYTES
# outright, but still a hard reject-not-truncate cap rather than no cap at
# all, and kept comfortably under MAX_RESPONSE_BYTES below (GitLab's wiki
# write response typically echoes the written content back, plus metadata
# overhead). Recorded here as a deliberate, revisitable choice, not a
# silent gap.
MAX_WIKI_PAGE_CONTENT_BYTES = 2 * 1024 * 1024  # 2 MiB, UTF-8 encoded content
MAX_RESPONSE_BYTES = 4 * 1024 * 1024  # defensive cap on any single API response body

DEFAULT_TIMEOUT_SECONDS = 15.0
MAX_RETRY_ATTEMPTS = 5
MAX_RETRY_ELAPSED_SECONDS = 30.0
BASE_BACKOFF_SECONDS = 0.5
MAX_BACKOFF_SECONDS = 8.0

PERMANENT_STATUS_CODES = {401, 403, 404}

REVIEW_SUBTASK_LABEL = "review-subtask"
_EVIDENCE_KEY_LABEL_PREFIX = "evidence-key:"

# Idempotency search pagination: a sufficiently large per_page (GitLab's own
# max) plus a bounded page-count cap, rather than trusting a single
# unpaginated page. In practice, because the search is now also narrowed by
# an exact-match evidence-key label (see _evidence_key_label below), a
# legitimate (task_id, gate_id) pair should only ever match 0 or 1 open
# issues on the first page; the loop exists for defensive completeness
# rather than an expected common case.
_ISSUE_SEARCH_PAGE_SIZE = 100
_ISSUE_SEARCH_MAX_PAGES = 20

_GATE_ID_PATTERN = re.compile(r"^[A-Za-z0-9_.:-]+$")
_TASK_ID_PATTERN = re.compile(r"^[A-Za-z0-9_.:-]+$")

# GitLab interprets any line whose first non-whitespace characters match this
# shape as a "quick action" attempt (e.g. /close, /unlabel, /relabel,
# /confidential, /lock, /reopen, /label), executed server-side as coming from
# the note/issue author -- which, for this integration, is always this
# module's own service-account token, never the human the content actually
# came from. Quick-action matching is case-insensitive server-side (verified
# against GitLab's own `lib/gitlab/quick_actions/extractor.rb`), so this
# pattern is too (`re.IGNORECASE`) -- `/Close`, `/CLOSE`, and `/cLoSe` are all
# executed identically to `/close`. This is deliberately broader than
# GitLab's real, finite command list (built server-side from
# `Regexp.union(names)` over actual registered quick-action names, so
# `/notacommand` is never interpreted) rather than an exhaustive keyword
# list, so this module never needs to track GitLab's exact, version-specific
# command set to stay safe -- a false-positive reject on a line that merely
# *looks* like a quick action is an accepted, deliberate cost of that margin
# (see ../SECURITY-CONTROLS.md's noted over-rejection follow-up). A `/`
# appearing mid-line (a file path, a URL fragment) never matches, since
# `re.MULTILINE` anchors `^` to line starts only.
_QUICK_ACTION_LINE_PATTERN = re.compile(r"^\s*/[a-z][a-z_]*\b", re.MULTILINE | re.IGNORECASE)


def _reject_quick_action_syntax(value: str, *, field_name: str) -> None:
    """Raise `GitLabValidationError` if any line of caller-supplied `value`
    is shaped like a GitLab quick action (see `_QUICK_ACTION_LINE_PATTERN`).
    Rejects outright -- never strips or truncates the offending line -- and
    never echoes the rejected line/content back in the error message,
    consistent with this module's existing discipline of not folding
    untrusted content into error text it doesn't control. Callers must run
    this against caller-supplied text only, *before* any of this module's
    own trusted, deliberate quick-action lines (e.g. `create_review_subtask`'s
    own `/relate #<iid>`) are appended -- this check has no way to
    distinguish "this module wrote this" from "the caller wrote this" once
    the two are concatenated."""
    if _QUICK_ACTION_LINE_PATTERN.search(value):
        raise GitLabValidationError(
            f"{field_name} contains a line shaped like a GitLab quick action "
            "(a line starting with '/' followed by a letter and then letters/"
            "underscores, matched case-insensitively since GitLab itself matches "
            "quick actions case-insensitively), which GitLab would interpret and "
            "execute server-side as this integration's own service-account token "
            "-- rejected rather than sent to GitLab; remove or reword the "
            "offending line (e.g. escape the leading slash or add leading text "
            "before it)"
        )

# ---------------------------------------------------------------------------
# Audit trail: same mechanism as dispatch_core.py's own audit log
# (build_audit_record/write_audit_record, including its forbidden-key
# check), written to a dedicated file so this module's records never mix
# with dispatch_core's own. Never contains the GitLab token, wiki/comment/
# issue body content, or a raw confirmation-token value.
# ---------------------------------------------------------------------------

GITLAB_AUDIT_LOG_DIR = Path.home() / ".agents" / "mcp-gitlab"
GITLAB_AUDIT_LOG_PATH = GITLAB_AUDIT_LOG_DIR / "audit.jsonl"


def _write_gitlab_audit_record(
    *,
    tool: str,
    task_id: str | None,
    decision: str,
    audit_path: Path | None = None,
    **extra: Any,
) -> None:
    """Build and append one audit record for a GitLab tool call. `extra`
    must never include the token, confirmation-token value, or raw
    issue/wiki/comment body content -- callers pass identifiers, hashes, and
    lengths instead; `dispatch_core.build_audit_record`'s forbidden-key
    check (`_FORBIDDEN_AUDIT_KEYS`) rejects an accidental `token`/
    `confirmation_token`/`content` key outright rather than silently
    allowing it through."""
    record = _dispatch_core.build_audit_record(tool=tool, task_id=task_id, decision=decision, **extra)
    _dispatch_core.write_audit_record(record, path=audit_path or GITLAB_AUDIT_LOG_PATH)


class GitLabError(Exception):
    """Base class for structured GitLab tool failures."""

    kind = "error"


class GitLabConfigError(GitLabError):
    """Missing/invalid env var configuration. Always fails closed, never
    silently proceeds with a guessed default. Maps to status="unavailable"
    (an operator/deployment problem, not a policy denial)."""

    kind = "unavailable"


class GitLabValidationError(GitLabError):
    """Caller-supplied argument failed a structural check (bad gate_id shape,
    over-cap comment, etc). Maps to status="denied": this is the caller's
    input being rejected, not an infrastructure problem."""

    kind = "denied"


class GitLabPermanentError(GitLabError):
    """401/403/404, or any other non-retryable HTTP status. Never retried.
    Maps to status="denied".

    `message` (and therefore `str(error)`) may embed a snippet of GitLab's
    own raw response body -- useful for a human debugging a caller-facing
    result, which is why it stays in `message` -- but that snippet must
    never reach the audit trail. `audit_reason` carries a body-free variant
    for that purpose (defaults to `message` itself for direct-construction
    call sites, e.g. in tests, that never had a raw body to begin with);
    `response_body_sha256`/`response_body_length` optionally carry a hash/
    length of the raw body so the audit record still records *that* an error
    body existed and how large it was, without ever recording its content.
    """

    kind = "denied"

    def __init__(
        self,
        message: str,
        *,
        status_code: int | None = None,
        audit_reason: str | None = None,
        response_body_sha256: str | None = None,
        response_body_length: int | None = None,
    ) -> None:
        super().__init__(message)
        self.status_code = status_code
        self.audit_reason = audit_reason if audit_reason is not None else message
        self.response_body_sha256 = response_body_sha256
        self.response_body_length = response_body_length


class GitLabRetryableExhaustedError(GitLabError):
    """429/5xx/timeout/network error that exhausted the bounded retry budget
    (attempt count or elapsed time) without succeeding. Maps to
    status="unavailable": the caller never observes a false success here --
    this is only ever raised, never swallowed into a fabricated ok result."""

    kind = "unavailable"


# ---------------------------------------------------------------------------
# Token resolution: lazy, fail-closed, never logged.
# ---------------------------------------------------------------------------


def resolve_token() -> str:
    """Resolve `GITLAB_SVC_TOKEN` lazily -- called only from inside a tool
    function that actually needs it, never at import or server-startup time.
    Fails closed on unset/empty/whitespace-only, naming the env var checked
    but never any value. Callers must never place the returned token in a
    log line, exception message, audit record, or generated artifact.

    Deliberately NOT routed through `settings.py`: secrets are always
    environment-variable-only and are never read from, or written to, a
    cadre config file (`settings.py` actively rejects a secret-shaped key
    found in one) -- this function stays a direct `os.environ` read so the
    token can never accidentally land in a config file's resolution path.
    """
    raw = os.environ.get(GITLAB_TOKEN_ENV_VAR)
    if raw is None or not raw.strip():
        raise GitLabConfigError(f"{GITLAB_TOKEN_ENV_VAR} is not set or is empty/whitespace-only")
    return raw


# ---------------------------------------------------------------------------
# Target-project configuration.
# ---------------------------------------------------------------------------


@dataclasses.dataclass(frozen=True)
class GitLabConfig:
    base_url: str  # always https://..., no trailing slash
    project_id: str  # numeric id or "namespace/project" path, URL-encoded on use
    supports_work_item_hierarchy: bool | None  # None = unset/undetectable -> fallback


def _parse_hierarchy_flag(raw: str | None) -> bool | None:
    """Thin wrapper kept for existing tests/call sites -- the canonical
    validation logic (must also accept a native YAML bool, not only the
    'true'/'false' strings this env-var-shaped signature implies) now lives
    once, in `settings.py`, as the tri-state validator for
    `gitlab.supports_work_item_hierarchy`."""
    try:
        return _settings.validate_tristate_bool(raw, "gitlab.supports_work_item_hierarchy")
    except _settings.SettingsError as error:
        raise GitLabConfigError(str(error)) from error


def resolve_config() -> GitLabConfig:
    """Resolve the three (well, two required + one optional) target-project
    settings via `settings.py`'s unified resolver (env var > project-local
    file, when the field's trust scope allows it > user-global file >
    default), mirroring `resolve_token()`'s fail-closed discipline. Both
    `gitlab.base_url` and `gitlab.project_id` are `global_only` (never
    settable from a project-local file -- see settings.py's trust-scope
    table): `base_url` because it selects the exfiltration-sensitive
    network endpoint every write in this module talks to, and
    `project_id` because `../SECURITY-CONTROLS.md` records a human-accepted
    residual-risk control for this integration that depends on both fields
    being operator-fixed (a single dedicated, docs-only project + a
    least-privilege token). `gitlab.supports_work_item_hierarchy` is the
    only field here that may come from either tier. Requires HTTPS -- there
    is no flag anywhere in this module that can accept an http:// base URL
    or disable TLS certificate verification (enforced inside settings.py's
    `gitlab.base_url` validator, which also rejects URL userinfo
    host-confusion, e.g. "https://gitlab.example.com@attacker.com/", the
    same way this function always has).

    Failures are re-raised as `GitLabConfigError` wrapping whatever message
    `settings.py`'s resolver produced. The `https://`/URL-userinfo/
    hierarchy-flag validation messages match this function's pre-`settings.py`
    wording exactly (settings.py's validators were written to match them).
    The unset/empty-env-var and totally-unresolved messages do NOT match
    the pre-`settings.py` wording verbatim -- they now read
    "<key> is not configured" with "checked: <source> -> <state>" lines,
    rather than "<ENV_VAR> is not set or is empty/whitespace-only". Existing
    tests assert on exception *type* plus specific substrings (e.g.
    "userinfo"), not full-message equality, so this doesn't break anything,
    but don't assume unset/empty message text is stable across this
    refactor if you're about to add a new assertion on it.
    """
    try:
        values = _settings.resolve_many(
            ["gitlab.base_url", "gitlab.project_id", "gitlab.supports_work_item_hierarchy"]
        )
    except _settings.SettingsError as error:
        raise GitLabConfigError(str(error)) from error

    return GitLabConfig(
        base_url=values["gitlab.base_url"],
        project_id=values["gitlab.project_id"],
        supports_work_item_hierarchy=values["gitlab.supports_work_item_hierarchy"],
    )


# ---------------------------------------------------------------------------
# HTTP transport: stdlib urllib only, PRIVATE-TOKEN header (never a query
# param), no cross-host redirects, no TLS verification bypass, bounded
# jittered exponential-backoff retry for 429/5xx/timeout, permanent failure
# for 401/403/404.
# ---------------------------------------------------------------------------


class _NoCrossHostRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Follows same-host, same-scheme redirects (urllib's normal behavior)
    but refuses -- raises GitLabPermanentError, not a silent fallthrough --
    any redirect whose target host differs from the request's original
    host, *or* whose target scheme is not `https`. GitLab's own API does not
    ordinarily redirect same-request-shape calls, but this exists
    specifically so a compromised or misconfigured instance can never cause
    this client to replay the PRIVATE-TOKEN header at an attacker-controlled
    host (cross-host redirection) or in cleartext (a same-host
    https-to-http scheme downgrade)."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):  # noqa: N802 (urllib's own method name)
        original_host = urllib.parse.urlparse(req.full_url).hostname
        new_parsed = urllib.parse.urlparse(newurl)
        if new_parsed.hostname != original_host or new_parsed.scheme != "https":
            # audit_reason is set explicitly here even though these values
            # (hostname/scheme parsed from the response's own Location
            # header) are narrower than an arbitrary response body -- they
            # are still network-derived and in principle attacker/
            # compromised-instance-influenced, so this stays consistent with
            # every other GitLabPermanentError raise site rather than
            # silently falling back to the constructor's message-as-reason
            # default.
            raise GitLabPermanentError(
                f"Refusing redirect during GitLab API call (status {code}, from host "
                f"{original_host!r} to {new_parsed.hostname!r} scheme {new_parsed.scheme!r}): "
                "cross-host redirection and same-host https-to-http scheme downgrade are both refused",
                status_code=code,
                audit_reason=(
                    f"Refusing redirect during GitLab API call (status {code}): cross-host "
                    "redirection and same-host https-to-http scheme downgrade are both refused "
                    "(host/scheme values omitted from the audit trail; see the caller-facing "
                    "result for detail)"
                ),
            )
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def _build_opener() -> urllib.request.OpenerDirector:
    # No ssl.create_default_context(check_hostname=False, ...) escape hatch
    # anywhere in this module -- the default context (full verification) is
    # the only context ever constructed.
    import ssl

    https_handler = urllib.request.HTTPSHandler(context=ssl.create_default_context())
    # Explicit ProxyHandler({}) below is load-bearing, not decorative:
    # urllib.request.build_opener() only omits a *default* handler class
    # when an instance of that exact class is already among the handlers
    # passed in. Neither HTTPSHandler nor _NoCrossHostRedirectHandler is a
    # ProxyHandler, so without this, build_opener() silently adds its own
    # default ProxyHandler() -- which consults the ambient HTTPS_PROXY /
    # https_proxy / ALL_PROXY environment variables (via getproxies()) and,
    # if any is set, transparently routes every GitLab API call through
    # that proxy with no logging and no opt-out anywhere in this module.
    # Passing ProxyHandler({}) here means "use this exact proxy map (none)"
    # and disables proxying unconditionally, regardless of ambient
    # environment -- consistent with this module's "no escape hatch
    # anywhere" TLS/redirect-hardening discipline (see ../SECURITY-CONTROLS.md).
    return urllib.request.build_opener(
        https_handler,
        _NoCrossHostRedirectHandler(),
        urllib.request.ProxyHandler({}),
    )


def _api_url(config: GitLabConfig, path: str) -> str:
    return f"{config.base_url}/api/v4{path}"


def _quote_project_id(config: GitLabConfig) -> str:
    return urllib.parse.quote(config.project_id, safe="")


def _should_retry(attempt: int, started_monotonic: float) -> bool:
    if attempt >= MAX_RETRY_ATTEMPTS:
        return False
    return (time.monotonic() - started_monotonic) < MAX_RETRY_ELAPSED_SECONDS


def _sleep_backoff(attempt: int, *, sleep=time.sleep) -> None:
    base = min(MAX_BACKOFF_SECONDS, BASE_BACKOFF_SECONDS * (2 ** (attempt - 1)))
    jitter = random.uniform(0, base * 0.25)
    sleep(base + jitter)


def _safe_error_body(error: urllib.error.HTTPError) -> str:
    try:
        raw = error.read(4096)
    except Exception:  # noqa: BLE001 - best-effort diagnostic only
        return ""
    try:
        return raw.decode("utf-8", errors="replace")
    except Exception:  # noqa: BLE001
        return ""


def _error_body_meta(body_snippet: str) -> tuple[str | None, int | None]:
    """Hash/length of an error-response body snippet, computed once so the
    audit trail can record that a body existed (and how large it was)
    without ever recording its content -- mirrors how `write_wiki_page`
    already hashes wiki content for its own audit records rather than
    logging it raw. Returns (None, None) for an empty/unreadable body."""
    if not body_snippet:
        return None, None
    encoded = body_snippet.encode("utf-8")
    return hashlib.sha256(encoded).hexdigest(), len(encoded)


def _perform_request(method: str, url: str, token: str, json_body: Any, timeout: float) -> Any:
    headers = {"PRIVATE-TOKEN": token, "Accept": "application/json"}
    data = None
    if json_body is not None:
        data = json.dumps(json_body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    opener = _build_opener()
    with opener.open(request, timeout=timeout) as response:
        raw = response.read(MAX_RESPONSE_BYTES + 1)
        if len(raw) > MAX_RESPONSE_BYTES:
            # audit_reason set explicitly for the same consistency reason as
            # the redirect refusal above -- this message only ever embeds
            # the method/url/byte-cap, never response content, but stays
            # explicit rather than relying on the constructor default.
            raise GitLabPermanentError(
                f"GitLab API response for {method} {url} exceeded {MAX_RESPONSE_BYTES}-byte cap",
                audit_reason=f"GitLab API response exceeded the {MAX_RESPONSE_BYTES}-byte cap",
            )
        if not raw:
            return None
        return json.loads(raw.decode("utf-8"))


def request_json(
    method: str,
    path: str,
    config: GitLabConfig,
    token: str,
    *,
    query: dict[str, str] | None = None,
    json_body: Any = None,
    timeout: float = DEFAULT_TIMEOUT_SECONDS,
    sleep=time.sleep,
) -> Any:
    """Perform one logical GitLab API call, retrying 429/5xx/timeout/network
    errors with bounded jittered exponential backoff and raising immediately
    (no retry) on 401/403/404. Never returns a result unless the call
    actually succeeded -- every non-2xx/network outcome raises a
    GitLabError subclass rather than fabricating a success shape.

    Honest limitation, ported from this module's docstring: retrying a
    non-idempotent write (POST/PUT) on a 5xx or timeout is inherently unsafe
    if the prior attempt's request was actually processed server-side but
    the response was lost -- a possibility this stdlib-only client cannot
    distinguish from "never processed" without a server-side idempotency
    key, which the GitLab REST API this module targets does not expose. This
    module's settled design applies the same bounded-retry policy uniformly
    (per the task's settled failure-handling decision) and relies on
    caller-level idempotency checks (see `create_review_subtask`'s
    search-before-create) as the safety design for the one operation where a
    duplicate would be most visible; `write_evidence_comment` and
    `write_wiki_page` have no equivalent caller-level dedup and could in
    principle double-apply on this specific failure shape. Flagged here,
    not silently accepted.
    """
    url = _api_url(config, path)
    if query:
        url = f"{url}?{urllib.parse.urlencode(query)}"

    attempt = 0
    started = time.monotonic()
    while True:
        attempt += 1
        try:
            return _perform_request(method, url, token, json_body, timeout)
        except urllib.error.HTTPError as error:
            status = error.code
            body_snippet = _safe_error_body(error)
            body_sha256, body_length = _error_body_meta(body_snippet)
            if status in PERMANENT_STATUS_CODES:
                raise GitLabPermanentError(
                    f"GitLab API returned {status} for {method} {path}: {body_snippet}",
                    status_code=status,
                    audit_reason=(
                        f"GitLab API returned {status} for {method} {path} "
                        "(response body redacted from the audit trail; see "
                        "response_body_sha256/response_body_length)"
                    ),
                    response_body_sha256=body_sha256,
                    response_body_length=body_length,
                ) from None
            if status == 429 or 500 <= status < 600:
                if not _should_retry(attempt, started):
                    raise GitLabRetryableExhaustedError(
                        f"GitLab API call {method} {path} did not succeed after {attempt} attempt(s) "
                        f"over {time.monotonic() - started:.1f}s (last status {status}); giving up"
                    ) from error
                _sleep_backoff(attempt, sleep=sleep)
                continue
            raise GitLabPermanentError(
                f"GitLab API returned unexpected status {status} for {method} {path}: {body_snippet}",
                status_code=status,
                audit_reason=(
                    f"GitLab API returned unexpected status {status} for {method} {path} "
                    "(response body redacted from the audit trail; see "
                    "response_body_sha256/response_body_length)"
                ),
                response_body_sha256=body_sha256,
                response_body_length=body_length,
            ) from None
        except (urllib.error.URLError, socket.timeout, TimeoutError) as error:
            if not _should_retry(attempt, started):
                raise GitLabRetryableExhaustedError(
                    f"GitLab API call {method} {path} did not succeed after {attempt} attempt(s) "
                    f"over {time.monotonic() - started:.1f}s (network/timeout error); giving up"
                ) from error
            _sleep_backoff(attempt, sleep=sleep)
            continue


# ---------------------------------------------------------------------------
# Untrusted-output wrapping: reuse dispatch_core's marker-token pattern.
# ---------------------------------------------------------------------------


def wrap_untrusted_gitlab_payload(payload: Any) -> str:
    """Serialize `payload` (a dict/list returned by the GitLab API, e.g. an
    idempotency search result or a created issue/comment/wiki page) and wrap
    it with `dispatch_core.wrap_untrusted_output`'s exact marker-token
    scheme, so GitLab-retrieved text can never be mistaken by the calling
    model for an instruction, including text an attacker deliberately wrote
    into an issue title/description/wiki body to try to forge a fake
    trusted-instruction boundary."""
    return _dispatch_core.wrap_untrusted_output(json.dumps(payload, sort_keys=True))


# ---------------------------------------------------------------------------
# Structured result helpers (mirrors dispatch_core's status vocabulary:
# "denied" for permanent/policy rejections, "unavailable" for infra/config
# problems, never a bare exception escaping to the tool layer).
# ---------------------------------------------------------------------------


def _audit_safe_reason(error: GitLabError) -> str:
    """The `reason` text safe to write to the audit trail: this module's own
    generated wording, never a raw snippet of GitLab's response body.
    `GitLabPermanentError` sets `audit_reason` explicitly (see its
    docstring); every other error kind is generated entirely by this module
    already (validation/config failures), so `str(error)` is already safe
    and `audit_reason` (absent on those classes) falls back to it."""
    return getattr(error, "audit_reason", str(error))


def _audit_error_meta(error: GitLabError) -> dict[str, Any]:
    """Hash/length fields (never raw content) to fold into an audit record
    for an error that originated from an actual GitLab HTTP response."""
    meta: dict[str, Any] = {}
    body_sha256 = getattr(error, "response_body_sha256", None)
    body_length = getattr(error, "response_body_length", None)
    if body_sha256 is not None:
        meta["response_body_sha256"] = body_sha256
    if body_length is not None:
        meta["response_body_length"] = body_length
    return meta


def _error_result(error: GitLabError) -> dict[str, Any]:
    """Build the tool-caller-facing error result. For an error that
    originated from an actual GitLab HTTP response (`GitLabPermanentError`/
    `GitLabRetryableExhaustedError`), `str(error)` may embed a snippet of
    GitLab's own response body -- untrusted, potentially attacker-influenced
    text (e.g. a custom error message from a compromised/misconfigured
    instance) -- so it is wrapped with the same untrusted-output marker-token
    scheme every success-path payload already uses, rather than returned
    raw. A validation/config error's message is entirely this module's own
    generated wording and is returned unwrapped, matching existing test
    expectations that assert on its exact substring content."""
    reason_text = str(error)
    if isinstance(error, (GitLabPermanentError, GitLabRetryableExhaustedError)):
        reason_text = _dispatch_core.wrap_untrusted_output(reason_text)
    result: dict[str, Any] = {"status": error.kind, "reason": reason_text}
    status_code = getattr(error, "status_code", None)
    if status_code is not None:
        result["status_code"] = status_code
    return result


def _resolve_token_and_config() -> tuple[str, GitLabConfig] | dict[str, Any]:
    try:
        token = resolve_token()
        config = resolve_config()
    except GitLabConfigError as error:
        return _error_result(error)
    return token, config


# ---------------------------------------------------------------------------
# Idempotent create_review_subtask.
# ---------------------------------------------------------------------------


def _validate_label_component(value: str, *, field_name: str, pattern: re.Pattern[str]) -> None:
    if not isinstance(value, str) or not value or not pattern.match(value):
        raise GitLabValidationError(f"{field_name} must be a non-empty string matching {pattern.pattern!r}: {value!r}")


def _idempotency_key(task_id: str, gate_id: str) -> str:
    return f"task_id={task_id} gate_id={gate_id}"


def _evidence_key_label(task_id: str, gate_id: str, parent_issue_iid: int) -> str:
    """A third label, in addition to `review-subtask` and `gate:<gate_id>`,
    encoding a hash of the (task_id, gate_id, parent_issue_iid) idempotency
    key. `parent_issue_iid` is folded into the hashed input -- not only
    `task_id`/`gate_id` -- specifically so this label also binds the subtask
    to the parent issue it was requested against: without it, an open issue
    carrying the right three labels would be adopted as a match regardless of
    which parent it actually references, silently dropping the parent
    binding the old (pre-labels-only) description-based `Parent: #<iid>`
    substring check used to provide. Filtering by all three labels
    server-side (GitLab's issues API ANDs comma-separated `labels` values) is
    what actually makes the idempotency search exact-match rather than
    relying on an unauthenticated substring match against untrusted issue
    description text -- a decoy issue would need to carry this exact label
    combination, which requires the same permission tier (e.g. GitLab
    Reporter+ on this project) the legitimate create flow already assumes,
    not the same identity/credential."""
    digest = hashlib.sha256(
        f"task_id={task_id} gate_id={gate_id} parent={parent_issue_iid}".encode("utf-8")
    ).hexdigest()
    return f"{_EVIDENCE_KEY_LABEL_PREFIX}{digest}"


def _find_existing_subtask(
    config: GitLabConfig, token: str, parent_issue_iid: int, gate_id: str, task_id: str
) -> dict[str, Any] | None:
    """Search the configured project's *open* issues for one already
    carrying the exact three-label combination (`review-subtask`,
    `gate:<gate_id>`, and a hash-based `evidence-key:<hash>` label derived
    from (task_id, gate_id)) -- all filtered server-side, paginated, never
    matched via an unauthenticated substring scan of issue description text.
    This is the search-before-create step that makes `create_review_subtask`
    idempotent; see `request_json`'s docstring for why this matters beyond
    ordinary dedup (it is also this module's stated safety design for
    retrying a POST on 429/5xx/timeout).

    `state=opened` is included in the query filter so a closed (already
    resolved) issue is never silently adopted as satisfying a fresh review
    request -- a fresh call after the prior subtask was closed intentionally
    creates a new subtask rather than reusing history. The three-label match
    is re-verified locally against each candidate's own `labels` field (cheap
    structural comparison of GitLab-controlled metadata, not the untrusted
    free-text description) as a defense-in-depth backstop in case a server-
    side label filter ever behaves more loosely than documented; the
    `Parent: #<iid>` reference is still written into the description for
    human readability only and is never used for matching -- the parent
    binding is instead folded structurally into the evidence-key label hash
    itself (see `_evidence_key_label`'s docstring), so a match still requires
    the correct parent without reading untrusted free text."""
    expected_labels = {
        REVIEW_SUBTASK_LABEL,
        f"gate:{gate_id}",
        _evidence_key_label(task_id, gate_id, parent_issue_iid),
    }
    labels_filter = ",".join(sorted(expected_labels))
    page = 1
    while page <= _ISSUE_SEARCH_MAX_PAGES:
        candidates = request_json(
            "GET",
            f"/projects/{_quote_project_id(config)}/issues",
            config,
            token,
            query={
                "labels": labels_filter,
                "state": "opened",
                "per_page": str(_ISSUE_SEARCH_PAGE_SIZE),
                "page": str(page),
            },
        )
        if not isinstance(candidates, list):
            return None
        for issue in candidates:
            if not isinstance(issue, dict):
                continue
            if issue.get("state") != "opened":
                continue
            if expected_labels.issubset(set(issue.get("labels") or [])):
                return issue
        if len(candidates) < _ISSUE_SEARCH_PAGE_SIZE:
            return None
        page += 1
    return None


def create_review_subtask(
    parent_issue_iid: int,
    title: str,
    description: str,
    gate_id: str,
    task_id: str,
    *,
    audit_path: Path | None = None,
) -> dict[str, Any]:
    """Create (or, if one already exists, return) a GitLab issue linked to
    `parent_issue_iid` as a review subtask, labeled `review-subtask` and
    `gate:<gate_id>`.

    STATE TRANSITION: this function never closes, reopens, resolves, or
    relabels any issue away from its open review state, and calls no
    function anywhere in this module or its imports that does. It only ever
    reads issues (for the idempotency search) and creates at most one new
    issue. This is a Python-call-graph guarantee only; it does not by itself
    prevent GitLab's own server-side quick-action interpretation of body
    text from causing a state transition (see the quick-action neutralization
    note below, and `../SECURITY-CONTROLS.md`'s GitLab section for the two-layer
    explanation).

    Quick-action neutralization: `description` is checked with
    `_reject_quick_action_syntax()` before this function's own trusted
    "Parent: #<iid>" / "/relate #<iid>" lines are added around it, so no
    caller-supplied line shaped like a GitLab quick action (e.g. `/close`,
    `/unlabel ~"review-subtask"`) ever reaches GitLab in the issue body.
    Without this, GitLab would interpret such a line as coming from this
    module's own service-account note author and execute it server-side --
    an effect-level state transition no Python-level structural check here
    can observe, since it never goes through this module's own HTTP call
    shapes.

    Idempotency-search race, disclosed honestly rather than implied away by
    the word "idempotent": the search-then-create sequence below (see
    `_find_existing_subtask`) is not atomic. Two genuinely concurrent calls
    with the same `(task_id, gate_id, parent_issue_iid)` can both observe "no
    existing issue" via their own GET before either has POSTed, and both then
    POST, producing two open issues carrying the identical evidence-key
    label. This module has no distributed-lock or server-side compare-and-
    swap primitive to close this gap, and none is implemented in this round;
    a caller relying on strict single-issue-per-key semantics under genuine
    concurrent callers should be aware this is a best-effort dedup, not a
    hard uniqueness guarantee.

    Hierarchy note (deviation from the ideal design, recorded here rather
    than silently guessed): the settled design allows detecting work-item
    hierarchy support "via a GraphQL feature probe or an explicit config
    flag ... default to the fallback if unset/undetectable". This
    implementation only honors the explicit `GITLAB_SUPPORTS_WORK_ITEM_HIERARCHY`
    flag as *informational* -- it is threaded through to the result so a
    caller can see whether the instance is believed to support it -- but
    does not attempt the GraphQL work-item-hierarchy mutation even when the
    flag is `true`. That mutation's exact schema (namespace-scoped work-item
    type IDs, hierarchy widget shape) could not be verified against a real
    GitLab instance or its GraphQL docs in this environment, and shipping an
    unverified mutation guess risked a worse outcome (a confidently wrong
    hierarchy link) than always using the documented fallback (an explicit
    "Parent: #<iid>" reference plus a `/relate` quick action in the
    description, which is what every call currently uses regardless of the
    flag). Extending this to a verified GraphQL hierarchy path is tracked as
    follow-up work, not done here.
    """
    common_audit_fields = {
        "tool": "create_review_subtask",
        "task_id": task_id,
        "gate_id": gate_id,
        "parent_issue_iid": parent_issue_iid,
        "audit_path": audit_path,
    }

    try:
        if not isinstance(parent_issue_iid, int) or isinstance(parent_issue_iid, bool) or parent_issue_iid <= 0:
            raise GitLabValidationError(f"parent_issue_iid must be a positive integer: {parent_issue_iid!r}")
        if not isinstance(title, str) or not title.strip():
            raise GitLabValidationError("title must be a non-empty string")
        if not isinstance(description, str):
            raise GitLabValidationError("description must be a string")
        # Checked on the raw caller-supplied description, before this
        # function's own trusted "Parent: #<iid>" / "/relate #<iid>"
        # lines are appended below -- see _reject_quick_action_syntax's
        # docstring for why the ordering matters.
        _reject_quick_action_syntax(description, field_name="description")
        _validate_label_component(gate_id, field_name="gate_id", pattern=_GATE_ID_PATTERN)
        _validate_label_component(task_id, field_name="task_id", pattern=_TASK_ID_PATTERN)
    except GitLabValidationError as error:
        _write_gitlab_audit_record(**common_audit_fields, decision="denied", reason=_audit_safe_reason(error))
        return _error_result(error)

    resolved = _resolve_token_and_config()
    if isinstance(resolved, dict):
        _write_gitlab_audit_record(
            **common_audit_fields, decision="unavailable", reason=resolved.get("reason", "config unavailable")
        )
        return resolved
    token, config = resolved

    try:
        existing = _find_existing_subtask(config, token, parent_issue_iid, gate_id, task_id)
        if existing is not None:
            _write_gitlab_audit_record(
                **common_audit_fields,
                decision="ok",
                created=False,
                issue_iid=existing.get("iid") if isinstance(existing, dict) else None,
            )
            return {
                "status": "ok",
                "created": False,
                "hierarchy_supported": config.supports_work_item_hierarchy,
                # Surfaced at top level (not only nested inside the wrapped
                # issue payload) so a caller can see the matched issue's
                # state without unwrapping untrusted content -- always
                # "opened" here since _find_existing_subtask only ever
                # returns an open match, but kept explicit rather than
                # assumed.
                "state": existing.get("state") if isinstance(existing, dict) else None,
                "issue": wrap_untrusted_gitlab_payload(existing),
            }

        key = _idempotency_key(task_id, gate_id)
        full_description = (
            f"Parent: #{parent_issue_iid}\n\n"
            f"{description}\n\n"
            f"<!-- {key} -->\n\n"
            f"/relate #{parent_issue_iid}\n"
        )
        payload = {
            "title": title,
            "description": full_description,
            "labels": [
                REVIEW_SUBTASK_LABEL,
                f"gate:{gate_id}",
                _evidence_key_label(task_id, gate_id, parent_issue_iid),
            ],
        }
        created = request_json(
            "POST",
            f"/projects/{_quote_project_id(config)}/issues",
            config,
            token,
            json_body=payload,
        )
        _write_gitlab_audit_record(
            **common_audit_fields,
            decision="ok",
            created=True,
            issue_iid=created.get("iid") if isinstance(created, dict) else None,
        )
        return {
            "status": "ok",
            "created": True,
            "hierarchy_supported": config.supports_work_item_hierarchy,
            "state": created.get("state") if isinstance(created, dict) else None,
            "issue": wrap_untrusted_gitlab_payload(created),
        }
    except GitLabError as error:
        _write_gitlab_audit_record(
            **common_audit_fields,
            decision=error.kind,
            reason=_audit_safe_reason(error),
            status_code=getattr(error, "status_code", None),
            **_audit_error_meta(error),
        )
        return _error_result(error)


# ---------------------------------------------------------------------------
# write_wiki_page: mandatory confirmation gate, every invocation, no
# exceptions.
# ---------------------------------------------------------------------------

# A dedicated ConfirmationGate instance, reused verbatim from dispatch_core --
# not a new confirmation mechanism. Module-level singleton mirrors
# dispatch_core's own `_DEFAULT_GATE` pattern.
_WIKI_CONFIRMATION_GATE = _dispatch_core.ConfirmationGate()

# Fixed, non-dispatch-shaped values bound into ConfirmationGate's five
# generic fields for this tool. ConfirmationGate never validates these
# against dispatch's own MODES/CLASSIFICATIONS vocabularies -- it only
# hashes and exact-matches them -- so reusing the class this way is safe.
_WIKI_GATE_MODE = "human_approval"
_WIKI_GATE_CLASSIFICATION = "internal"
_WIKI_GATE_SANDBOX_LABEL = "wiki-write"


def _wiki_write_brief(slug: str, title: str, content: str, fmt: str) -> str:
    # Hash content *here* rather than passing raw content into
    # ConfirmationGate: the gate only ever needs to detect tampering between
    # the confirmation-request and confirmation-consume calls (exact-match
    # comparison of this brief string), so hashing up front means the gate's
    # in-memory pending-confirmation state never holds a second copy of the
    # raw wiki body -- ConfirmationGate hashes its own `brief` argument again
    # internally, but by then it is already just this compact digest, not
    # the original content.
    return json.dumps(
        {
            "slug": slug,
            "title": title,
            "format": fmt,
            "content_sha256": hashlib.sha256(content.encode("utf-8")).hexdigest(),
            "content_length_bytes": len(content.encode("utf-8")),
        },
        sort_keys=True,
    )


def _get_wiki_page(config: GitLabConfig, token: str, slug: str) -> dict[str, Any] | None:
    try:
        return request_json(
            "GET",
            f"/projects/{_quote_project_id(config)}/wikis/{urllib.parse.quote(slug, safe='')}",
            config,
            token,
        )
    except GitLabPermanentError as error:
        if error.status_code == 404:
            return None
        raise


def write_wiki_page(
    slug: str,
    title: str,
    content: str,
    format: str = "markdown",  # noqa: A002 (matches the settled public signature exactly)
    confirmation_token: str | None = None,
    *,
    audit_path: Path | None = None,
) -> dict[str, Any]:
    """Create or update (versioned, by GitLab's own wiki history) a wiki page
    in the configured project. The `human_approval`-tier tool: every call --
    with no exception for any caller -- must round-trip through
    `_WIKI_CONFIRMATION_GATE` exactly like a write-capable dispatch does in
    `dispatch_core.py`. A first call with no `confirmation_token` never
    writes anything; it returns `status="confirmation_required"` plus a
    token bound to the exact (slug, title, format, content hash) tuple, and
    a second call replaying that token is required before any GitLab write
    happens.

    Note: this tool's public signature has no `task_id` parameter (unlike
    `create_review_subtask`/`write_evidence_comment`), so its audit records
    carry `task_id=None`; that is a property of this tool's existing
    contract, not something this audit-logging change introduces.

    Quick-action scope note: unlike `create_review_subtask`'s `description`
    and `write_evidence_comment`'s `content`, `content` here is deliberately
    NOT run through `_reject_quick_action_syntax()`. GitLab wiki pages are
    plain Markdown/RDoc/AsciiDoc/Org rendering of a repository-like content
    blob, not an issue/note body -- GitLab's quick-action interpreter only
    ever parses issue descriptions and issue/MR/commit/epic notes, never wiki
    page content, so a `/close`-shaped line in a wiki page is rendered as
    literal text, not executed. If this is ever found to be inaccurate for a
    specific GitLab version/edition, this scope boundary must be revisited,
    not assumed to still hold.

    Stale-hint honesty note: `will_overwrite_existing` in the
    `confirmation_required` response (see below) is computed once, before the
    confirmation round trip begins, from whichever page state existed at that
    moment. Because the confirmation token's TTL is up to
    `dispatch_core.CONFIRMATION_TTL_SECONDS` (currently 300s), another actor
    could create or delete a page at the same `slug` during that window,
    making the disclosed hint stale by the time a human actually reads and
    approves it. The actual write path (`_get_wiki_page()` inside the
    confirmed branch below) always re-checks fresh at consume-time, so the
    create-vs-update *behavior* itself is never wrong -- only the
    informational hint shown to the approving human can lag reality.
    """
    # common_audit_fields never includes slug/title/content -- only the
    # content hash/length computed below once brief is available -- so a
    # validation failure that fires before `brief` exists (unknown slug/
    # title/content shape) is logged with those fields omitted rather than
    # raw content included.
    if not isinstance(slug, str) or not slug.strip():
        error = GitLabValidationError("slug must be a non-empty string")
        _write_gitlab_audit_record(
            tool="write_wiki_page", task_id=None, decision="denied", reason=_audit_safe_reason(error), audit_path=audit_path
        )
        return _error_result(error)
    if not isinstance(title, str) or not title.strip():
        error = GitLabValidationError("title must be a non-empty string")
        _write_gitlab_audit_record(
            tool="write_wiki_page", task_id=None, decision="denied", reason=_audit_safe_reason(error), slug=slug, audit_path=audit_path
        )
        return _error_result(error)
    if not isinstance(content, str):
        error = GitLabValidationError("content must be a string")
        _write_gitlab_audit_record(
            tool="write_wiki_page", task_id=None, decision="denied", reason=_audit_safe_reason(error), slug=slug, audit_path=audit_path
        )
        return _error_result(error)
    if format not in ("markdown", "rdoc", "asciidoc", "org"):
        error = GitLabValidationError(f"format must be one of markdown/rdoc/asciidoc/org: {format!r}")
        _write_gitlab_audit_record(
            tool="write_wiki_page", task_id=None, decision="denied", reason=_audit_safe_reason(error), slug=slug, audit_path=audit_path
        )
        return _error_result(error)
    encoded_content_length = len(content.encode("utf-8"))
    if encoded_content_length > MAX_WIKI_PAGE_CONTENT_BYTES:
        error = GitLabValidationError(
            f"content exceeds the {MAX_WIKI_PAGE_CONTENT_BYTES}-byte UTF-8-encoded cap for "
            f"write_wiki_page ({encoded_content_length} bytes); shorten the content, do not truncate it here"
        )
        _write_gitlab_audit_record(
            tool="write_wiki_page", task_id=None, decision="denied", reason=_audit_safe_reason(error), slug=slug, audit_path=audit_path
        )
        return _error_result(error)

    resolved = _resolve_token_and_config()
    if isinstance(resolved, dict):
        _write_gitlab_audit_record(
            tool="write_wiki_page",
            task_id=None,
            decision="unavailable",
            reason=resolved.get("reason", "config unavailable"),
            slug=slug,
            audit_path=audit_path,
        )
        return resolved
    token, config = resolved

    brief = _wiki_write_brief(slug, title, content, format)
    content_sha256 = hashlib.sha256(content.encode("utf-8")).hexdigest()
    content_length_bytes = len(content.encode("utf-8"))
    common_audit_fields = {
        "tool": "write_wiki_page",
        "task_id": None,
        "slug": slug,
        "format": format,
        "content_sha256": content_sha256,
        "content_length_bytes": content_length_bytes,
        "audit_path": audit_path,
    }

    if confirmation_token is None:
        # Check for an existing page *before* requesting confirmation, not
        # after, so the human approving the confirmation_required response
        # can see whether this write will create a new page or destructively
        # overwrite an existing one -- not folded into `brief` itself (which
        # stays a pure function of slug/title/content/format so the
        # confirmation gate's tamper-detecting exact-match on the second call
        # is unaffected by any change in page existence between the two
        # calls).
        try:
            existing_before_confirmation = _get_wiki_page(config, token, slug)
        except GitLabError as error:
            _write_gitlab_audit_record(
                **common_audit_fields,
                decision=error.kind,
                reason=_audit_safe_reason(error),
                status_code=getattr(error, "status_code", None),
                **_audit_error_meta(error),
            )
            return _error_result(error)
        will_overwrite_existing = existing_before_confirmation is not None

        issued = _WIKI_CONFIRMATION_GATE.request(
            "write_wiki_page", brief, _WIKI_GATE_MODE, _WIKI_GATE_CLASSIFICATION, _WIKI_GATE_SANDBOX_LABEL
        )
        _write_gitlab_audit_record(
            **common_audit_fields, decision="confirmation-required", will_overwrite_existing=will_overwrite_existing
        )
        return {
            "status": "confirmation_required",
            "confirmation_token": issued,
            "expires_in_seconds": _dispatch_core.CONFIRMATION_TTL_SECONDS,
            "will_overwrite_existing": will_overwrite_existing,
            "message": (
                "write_wiki_page requires human confirmation. Replay this call unchanged, "
                "adding confirmation_token, to actually write the wiki page."
            ),
        }

    try:
        _WIKI_CONFIRMATION_GATE.consume(
            confirmation_token, "write_wiki_page", brief, _WIKI_GATE_MODE, _WIKI_GATE_CLASSIFICATION, _WIKI_GATE_SANDBOX_LABEL
        )
    except _dispatch_core.DispatchDenied as error:
        # DispatchDenied is not a GitLabError, but it already carries the
        # same `.kind == "denied"` attribute _error_result() reads, and its
        # message is this module's/dispatch_core's own generated wording
        # (never raw GitLab response content), so routing it through the
        # same shared helper every other error path uses keeps this path
        # consistent rather than building an ad-hoc result dict here.
        _write_gitlab_audit_record(**common_audit_fields, decision="denied", reason=_audit_safe_reason(error))
        return _error_result(error)

    try:
        existing = _get_wiki_page(config, token, slug)
        payload = {"title": title, "content": content, "format": format}
        if existing is None:
            result = request_json(
                "POST", f"/projects/{_quote_project_id(config)}/wikis", config, token, json_body=payload
            )
            written = True
        else:
            result = request_json(
                "PUT",
                f"/projects/{_quote_project_id(config)}/wikis/{urllib.parse.quote(slug, safe='')}",
                config,
                token,
                json_body=payload,
            )
            written = False if result is None else True
        _write_gitlab_audit_record(
            **common_audit_fields,
            decision="ok",
            created=existing is None,
            written=written,
        )
        return {
            "status": "ok",
            "created": existing is None,
            "page": wrap_untrusted_gitlab_payload(result),
        }
    except GitLabError as error:
        _write_gitlab_audit_record(
            **common_audit_fields,
            decision=error.kind,
            reason=_audit_safe_reason(error),
            status_code=getattr(error, "status_code", None),
            **_audit_error_meta(error),
        )
        return _error_result(error)


# ---------------------------------------------------------------------------
# write_evidence_comment: hard size cap, no truncate-and-continue.
# ---------------------------------------------------------------------------


def write_evidence_comment(
    issue_iid: int, content: str, task_id: str, *, audit_path: Path | None = None
) -> dict[str, Any]:
    """Add a comment (GitLab "note") to an existing issue for small,
    structured per-task evidence. Rejects (does not truncate) content whose
    UTF-8 encoding exceeds `MAX_EVIDENCE_COMMENT_BYTES`."""
    common_audit_fields = {
        "tool": "write_evidence_comment",
        "task_id": task_id if isinstance(task_id, str) else None,
        "issue_iid": issue_iid,
        "content_length_bytes": len(content.encode("utf-8")) if isinstance(content, str) else None,
        "audit_path": audit_path,
    }

    if not isinstance(issue_iid, int) or isinstance(issue_iid, bool) or issue_iid <= 0:
        error = GitLabValidationError(f"issue_iid must be a positive integer: {issue_iid!r}")
        _write_gitlab_audit_record(**common_audit_fields, decision="denied", reason=_audit_safe_reason(error))
        return _error_result(error)
    if not isinstance(content, str):
        error = GitLabValidationError("content must be a string")
        _write_gitlab_audit_record(**common_audit_fields, decision="denied", reason=_audit_safe_reason(error))
        return _error_result(error)
    if not isinstance(task_id, str) or not task_id.strip():
        error = GitLabValidationError("task_id must be a non-empty string")
        _write_gitlab_audit_record(**common_audit_fields, decision="denied", reason=_audit_safe_reason(error))
        return _error_result(error)

    try:
        _reject_quick_action_syntax(content, field_name="content")
    except GitLabValidationError as error:
        _write_gitlab_audit_record(**common_audit_fields, decision="denied", reason=_audit_safe_reason(error))
        return _error_result(error)

    encoded_length = len(content.encode("utf-8"))
    if encoded_length > MAX_EVIDENCE_COMMENT_BYTES:
        error = GitLabValidationError(
            f"content exceeds the {MAX_EVIDENCE_COMMENT_BYTES}-byte UTF-8-encoded cap for "
            f"write_evidence_comment ({encoded_length} bytes); shorten the content, do not truncate it here"
        )
        _write_gitlab_audit_record(**common_audit_fields, decision="denied", reason=_audit_safe_reason(error))
        return _error_result(error)

    resolved = _resolve_token_and_config()
    if isinstance(resolved, dict):
        _write_gitlab_audit_record(
            **common_audit_fields, decision="unavailable", reason=resolved.get("reason", "config unavailable")
        )
        return resolved
    token, config = resolved

    try:
        created = request_json(
            "POST",
            f"/projects/{_quote_project_id(config)}/issues/{issue_iid}/notes",
            config,
            token,
            json_body={"body": content},
        )
        _write_gitlab_audit_record(
            **common_audit_fields,
            decision="ok",
            comment_id=created.get("id") if isinstance(created, dict) else None,
        )
        return {"status": "ok", "comment": wrap_untrusted_gitlab_payload(created)}
    except GitLabError as error:
        _write_gitlab_audit_record(
            **common_audit_fields,
            decision=error.kind,
            reason=_audit_safe_reason(error),
            status_code=getattr(error, "status_code", None),
            **_audit_error_meta(error),
        )
        return _error_result(error)


# ---------------------------------------------------------------------------
# Structural guarantee, enforced by this module's own public surface: no
# name resembling a state-transition operation exists here. See
# test_gitlab_integration.py's StructuralNoStateTransitionTests for the
# automated assertion this comment promises -- that test file does its own
# independent name/source scanning; there is no local pattern constant in
# this module that acts as the enforcement mechanism.
# ---------------------------------------------------------------------------
