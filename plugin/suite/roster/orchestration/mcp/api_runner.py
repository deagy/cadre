"""The `api` dispatch runner: drive a role against an OpenAI-compatible
chat endpoint directly, with no coding-CLI child process.

Motivation. The `codex` and `claude-code` runners in `dispatch_core.py` both
work by spawning a coding CLI, which supplies the agent loop *and* an
OS-level sandbox (Landlock/seccomp, Seatbelt) that `dispatch_core` narrows
through a `--sandbox`/`--permission-mode` flag. That is unavailable when the
operator has no such CLI installed, or wants to drive a self-hosted
`llama-server` (or any other OpenAI-compatible endpoint) headlessly. This
module supplies both missing halves itself.

READ THIS BEFORE EXTENDING. Supplying the sandbox ourselves is the entire
risk of this runner, and it is strictly weaker than what the CLI runners get
for free:

  - File tools are confined **in-process**, by path resolution and
    `dispatch_core._ensure_contained`. That is not kernel enforcement. It
    holds against a model that asks for a path outside the project; it would
    not hold against arbitrary code execution inside this process.
  - `run_command` is gated by an operator-configured allowlist of bare
    command names. That allowlist is **advisory, not enforced**, in the
    precise sense `roster/orchestration/SECURITY-CONTROLS.md` defines: the
    dispatched agent chooses the *arguments*, and every command an operator
    would realistically allowlist (`pytest`, `go`, `npm`) executes
    repository-controlled code by design. It raises the bar against casual
    misuse; it is not a containment boundary.
  - Consequently the write-capable path is opt-in
    (`runners.api_allow_writes`, default false) and `run_command` is
    unavailable until `runners.api_command_allowlist` is non-empty.

What this runner keeps from the existing dispatch pipeline, unchanged:
`dispatch_core`'s classification checks, sandbox narrowing, confirmation
gate, concurrency limiter, audit trail, brief fencing
(`fence_untrusted_brief`), and untrusted-output wrapping. `run_command`
additionally routes through `dispatch_core.spawn_and_wait` and
`build_child_env`, which restores process-group isolation, group-kill on
timeout, output caps, the deny-by-default environment allowlist, and the
re-dispatch depth counter for that one path.

Recursive dispatch is *narrowed*, not structurally prevented -- an earlier
version of this docstring claimed the latter and was wrong. No tool exposes
dispatch; `run_command` refuses the agent-launching binaries by name (see
`_REFUSED_COMMANDS`); and `_sanitized_child_path()` removes their directories
from the child's PATH. But an allowlisted general-purpose interpreter can
still exec one by absolute path, so `MAX_DISPATCH_DEPTH` remains the actual
backstop. See SECURITY-CONTROLS.md's "API runner" section.

Stdlib only, deliberately: `pyproject.toml` declares `dependencies = []`, and
every HTTP client already in this repository (`gitlab_core.py`,
`knowledge-store/src/embeddings.py`, `src/role_fidelity.py`) is built on
`urllib.request`. The request/response shape and error taxonomy below follow
`role_fidelity.ChatClient`; the redirect and response-cap handling follows
`embeddings.py`. Neither is imported -- they raise their own module-specific
exception types and carry unrelated dependencies.
"""

from __future__ import annotations

import fnmatch
import json
import os
import re
import shutil
import stat
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any, Callable

_MODULE_DIR = Path(__file__).resolve().parent
if str(_MODULE_DIR) not in sys.path:
    sys.path.insert(0, str(_MODULE_DIR))

import dispatch_core as core  # noqa: E402  (sys.path set above)
import settings  # noqa: E402  (dispatch_core appends roster/shared/src to sys.path)

# --------------------------------------------------------------------------
# Bounds. Every one of these exists because this runner has no child process
# whose own limits could apply -- `spawn_and_wait`'s timeout/output caps
# cover only the run_command path.
# --------------------------------------------------------------------------

# One HTTP response body. Matches embeddings.py's cap for the same reason:
# a misbehaving or hostile endpoint must not be able to exhaust memory.
MAX_RESPONSE_BYTES = 4 * 1024 * 1024

# Turns of the agent loop (one model call plus its tool results per turn).
# Bounds both cost and the blast radius of a model that loops on a failing
# tool call.
MAX_TOOL_ITERATIONS = 24

# One tool result, as returned to the model. Larger reads are truncated with
# an explicit marker rather than silently trimmed.
MAX_TOOL_RESULT_BYTES = 64 * 1024

# One `write_file` payload.
MAX_WRITE_BYTES = 1 * 1024 * 1024

# One file `read_file`/`search` will open.
MAX_READ_BYTES = 2 * 1024 * 1024

# Files `search`/`list_files` will consider before giving up, so a glob over
# a huge tree cannot stall the dispatch.
MAX_FILES_SCANNED = 20000

MAX_SEARCH_MATCHES = 200

# Ceiling for a single HTTP request. The effective per-call timeout is the
# *smaller* of this and whatever remains of the caller's whole-dispatch
# deadline -- see `run_api_dispatch`'s `client.complete(..., timeout=...)`
# call, which computes that each turn exactly as `_tool_run_command` does for
# its own child.
#
# An earlier revision passed this value unconditionally and checked the
# deadline only *between* turns, which let a slow endpoint overrun the
# caller's `timeout_seconds` by up to this much on the final in-flight call.
# The whole surrounding pipeline (spawn_and_wait's group-kill, the
# concurrency limiter, job/team TTLs) is built on that deadline being real.
DEFAULT_REQUEST_TIMEOUT_SECONDS = 120.0

# Never runnable through `run_command`, whatever the operator allowlisted.
# These are the binaries that would start another agent.
#
# READ THE LIMIT OF THIS, and do not restate it more strongly elsewhere: this
# is a check on the literal `command` argument only. It says nothing about
# what an allowlisted command then does, so an operator who allowlists a
# general-purpose interpreter (`python`, `bash`, `node`, `make`, `npm`) has
# given the dispatched agent a way to exec one of these as a *grandchild*,
# which this set never sees. `_command_child_path()` below narrows that, and
# `MAX_DISPATCH_DEPTH` still backstops it, but the honest description is
# "narrower attack surface", not "structurally impossible". An earlier
# revision of SECURITY-CONTROLS.md claimed the latter; that was wrong and has
# been corrected.
#
# Compared casefolded, so a `Codex` entry on a case-insensitive filesystem
# cannot slip past a lowercase-only comparison.
_REFUSED_COMMANDS = frozenset({"cadre", "codex", "claude", "cline", "agentic-sdlc"})
_REFUSED_COMMANDS_FOLDED = frozenset(name.casefold() for name in _REFUSED_COMMANDS)


class ApiRunnerError(core.DispatchUnavailable):
    """An endpoint/transport failure: unreachable, malformed, or misbehaving.

    Subclasses DispatchUnavailable rather than DispatchDenied because none of
    these is a policy decision -- they are infrastructure faults, and
    `dispatch_core`'s existing handling for that distinction (never fall back
    to a less-enforced mechanism on a *denial*) is the behavior we want.
    """


class ToolDenied(Exception):
    """A tool call refused by policy (path escape, denied command, write
    without authorization).

    Deliberately NOT a `DispatchDenied`: it is reported back to the model as
    a tool result so it can correct a mistaken path and continue, exactly as
    a coding CLI reports a refused tool call. The refused operation never
    happens either way, so surfacing it in-band weakens nothing -- and
    aborting the whole dispatch on the first mistyped path would make the
    runner unusable.
    """


# --------------------------------------------------------------------------
# HTTP
# --------------------------------------------------------------------------


class _RejectRedirects(urllib.request.HTTPRedirectHandler):
    """Follows embeddings.py: a redirect from a configured inference endpoint
    is never legitimate, and following one would let that endpoint move the
    request (and its Authorization header) to a host the operator never
    configured."""

    def redirect_request(self, request: Any, file_pointer: Any, code: int, message: str, headers: Any, new_url: str) -> None:
        return None


def _open_request(request: urllib.request.Request, timeout: float) -> Any:
    return urllib.request.build_opener(_RejectRedirects()).open(request, timeout=timeout)


class ChatEndpoint:
    """Minimal OpenAI-compatible `/chat/completions` client with tool calling.

    Modeled on `roster/orchestration/src/role_fidelity.py`'s `ChatClient`,
    including its error taxonomy, extended with the `tools`/`tool_calls`
    fields this runner needs.
    """

    def __init__(
        self,
        base_url: str,
        model: str,
        api_key: str | None = None,
        request_timeout: float = DEFAULT_REQUEST_TIMEOUT_SECONDS,
    ) -> None:
        self.base_url = base_url.rstrip("/")
        self.model = model
        self.api_key = api_key
        self.request_timeout = request_timeout

    def complete(
        self,
        messages: list[dict[str, Any]],
        tools: list[dict[str, Any]] | None,
        temperature: float = 0.0,
        timeout: float | None = None,
    ) -> dict[str, Any]:
        """`timeout` overrides this endpoint's default for one call, so the
        caller can bound a request by the dispatch budget still remaining
        rather than by a fixed ceiling."""
        url = f"{self.base_url}/chat/completions"
        payload: dict[str, Any] = {
            "model": self.model,
            "temperature": temperature,
            "messages": messages,
        }
        if tools:
            payload["tools"] = tools
            payload["tool_choice"] = "auto"
        data = json.dumps(payload).encode("utf-8")
        headers = {"Content-Type": "application/json"}
        if self.api_key:
            headers["Authorization"] = f"Bearer {self.api_key}"
        request = urllib.request.Request(url, data=data, headers=headers, method="POST")
        try:
            effective_timeout = self.request_timeout if timeout is None else min(timeout, self.request_timeout)
            with _open_request(request, effective_timeout) as response:
                raw = response.read(MAX_RESPONSE_BYTES + 1)
        except urllib.error.HTTPError as error:
            detail = error.read().decode("utf-8", "replace")[:500]
            raise ApiRunnerError(f"{url}: HTTP {error.code}: {detail}") from error
        except urllib.error.URLError as error:
            raise ApiRunnerError(f"{url}: cannot reach endpoint: {error.reason}") from error
        except (TimeoutError, OSError) as error:
            raise ApiRunnerError(f"{url}: request failed: {error}") from error
        if len(raw) > MAX_RESPONSE_BYTES:
            raise ApiRunnerError(f"{url}: response exceeds {MAX_RESPONSE_BYTES}-byte cap")
        try:
            body = json.loads(raw.decode("utf-8"))
        except ValueError as error:
            # A non-JSON 200 -- an intercepting proxy's HTML error page, or an
            # endpoint streaming SSE because it ignored the non-streaming
            # request. Same class as a timeout: the endpoint misbehaving.
            raise ApiRunnerError(f"{url}: unreadable response: {error}") from error
        try:
            message = body["choices"][0]["message"]
        except (KeyError, IndexError, TypeError) as error:
            raise ApiRunnerError(f"{url}: unexpected response shape: {json.dumps(body)[:500]}") from error
        if not isinstance(message, dict):
            raise ApiRunnerError(f"{url}: choices[0].message is not an object")
        return message


def parse_tool_calls(message: dict[str, Any]) -> list[dict[str, Any]]:
    """Normalize `message.tool_calls` into [{id, name, arguments: dict}].

    Tolerates two real-world deviations from the OpenAI schema rather than
    failing the dispatch over either:

      - `function.arguments` arriving as an already-parsed JSON *object*
        instead of a JSON string. llama.cpp's server does this (ggml-org/
        llama.cpp issue #20198); accepting both costs nothing.
      - a missing `id`. Some servers omit it; a synthesized index-based id
        keeps the tool-result correlation well-formed on the way back.

    Anything else malformed raises, because guessing what the model meant to
    call is worse than surfacing an endpoint problem.
    """
    raw_calls = message.get("tool_calls") or []
    if not isinstance(raw_calls, list):
        raise ApiRunnerError(f"tool_calls must be a list, got {type(raw_calls).__name__}")
    calls: list[dict[str, Any]] = []
    for index, raw in enumerate(raw_calls):
        if not isinstance(raw, dict):
            raise ApiRunnerError(f"tool_calls[{index}] must be an object")
        function = raw.get("function")
        if not isinstance(function, dict):
            raise ApiRunnerError(f"tool_calls[{index}].function must be an object")
        name = function.get("name")
        if not isinstance(name, str) or not name:
            raise ApiRunnerError(f"tool_calls[{index}].function.name must be a non-empty string")
        raw_arguments = function.get("arguments")
        if raw_arguments is None or raw_arguments == "":
            arguments: dict[str, Any] = {}
        elif isinstance(raw_arguments, dict):
            arguments = raw_arguments
        elif isinstance(raw_arguments, str):
            try:
                arguments = json.loads(raw_arguments)
            except ValueError as error:
                raise ApiRunnerError(
                    f"tool_calls[{index}].function.arguments is not valid JSON: {error}"
                ) from error
        else:
            raise ApiRunnerError(
                f"tool_calls[{index}].function.arguments must be a JSON string or object, "
                f"got {type(raw_arguments).__name__}"
            )
        if not isinstance(arguments, dict):
            raise ApiRunnerError(f"tool_calls[{index}].function.arguments must decode to an object")
        call_id = raw.get("id")
        calls.append(
            {
                "id": call_id if isinstance(call_id, str) and call_id else f"call_{index}",
                "name": name,
                "arguments": arguments,
            }
        )
    return calls


# --------------------------------------------------------------------------
# Path confinement
# --------------------------------------------------------------------------


def resolve_within_project(project_root: Path, raw: Any) -> Path:
    """Resolve a model-supplied path and prove it lands inside the project.

    Resolution is done with `os.path.realpath` *before* the containment
    check, so a symlink pointing out of the tree is caught by the check
    rather than by trusting the literal path. Containment itself reuses
    `dispatch_core._ensure_contained`, the same helper the role-file
    resolver uses, so both paths share one implementation of the rule.
    `.git` is refused outright: rewriting history or hooks is not editing
    the project, and a hook is code that runs later outside this loop.
    """
    if not isinstance(raw, str) or not raw.strip():
        raise ToolDenied("path must be a non-empty string")
    if "\x00" in raw:
        raise ToolDenied("path must not contain NUL")
    candidate = Path(raw)
    base = Path(os.path.realpath(project_root))
    resolved = Path(os.path.realpath(candidate if candidate.is_absolute() else base / candidate))
    try:
        core._ensure_contained(resolved, base)
    except core.DispatchDenied as error:
        raise ToolDenied(f"path escapes the project root: {raw!r}") from error
    try:
        relative = resolved.relative_to(base)
    except ValueError as error:  # pragma: no cover - _ensure_contained already proved this
        raise ToolDenied(f"path escapes the project root: {raw!r}") from error
    if any(part == ".git" for part in relative.parts):
        raise ToolDenied(f"refusing to touch the git directory: {raw!r}")
    return resolved


def _read_text_capped(path: Path) -> str:
    """Read a regular file, refusing symlinks and non-regular files.

    Same O_NOFOLLOW-open-then-fstat discipline as
    `dispatch_core._read_role_file_capped`: the final component being a
    symlink is refused at the kernel level rather than checked-then-opened.
    """
    try:
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    except OSError as error:
        raise ToolDenied(f"cannot open {path.name}: {error.strerror or error}") from error
    try:
        file_stat = os.fstat(descriptor)
        if not stat.S_ISREG(file_stat.st_mode):
            raise ToolDenied(f"not a regular file: {path.name}")
        if file_stat.st_size > MAX_READ_BYTES:
            raise ToolDenied(f"file exceeds the {MAX_READ_BYTES}-byte read cap: {path.name}")
        chunks: list[bytes] = []
        while True:
            chunk = os.read(descriptor, 65536)
            if not chunk:
                break
            chunks.append(chunk)
    finally:
        os.close(descriptor)
    return b"".join(chunks).decode("utf-8", errors="replace")


def _write_bytes_nofollow(path: Path, payload: bytes) -> None:
    """Write `payload` to `path`, refusing a symlink at the final component.

    The read path has always done this (`_read_text_capped`); the write path
    originally used `Path.write_bytes`/`Path.write_text`, which call plain
    `open()` and therefore *follow* a symlink at the final component. That
    left containment for writes resting entirely on the `realpath` snapshot
    taken by `resolve_within_project()` before the write -- a check-then-open
    gap a symlink appearing in between would defeat, redirecting a write to
    any path this process can reach. `dispatch_team()` runs members
    concurrently against one project root, so that is not a theoretical
    interleaving.

    O_NOFOLLOW closes the gap at the kernel level: the open itself fails with
    ELOOP if the final component is a symlink, whatever changed since the
    containment check. The post-open `fstat`/`S_ISREG` check additionally
    refuses a FIFO or device node that appeared at the same path, matching
    `_read_text_capped`'s discipline exactly.

    O_TRUNC (not O_EXCL) because `write_file` is documented as "create or
    overwrite"; O_EXCL would break every legitimate overwrite.
    """
    flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(path, flags, 0o644)
    except OSError as error:
        # ELOOP here means the final component is a symlink -- report it as a
        # policy refusal, not an I/O error, because that is what it is.
        raise ToolDenied(f"cannot write {path.name}: {error.strerror or error}") from error
    try:
        file_stat = os.fstat(descriptor)
        if not stat.S_ISREG(file_stat.st_mode):
            raise ToolDenied(f"refusing to write a non-regular file: {path.name}")
        written = 0
        while written < len(payload):
            written += os.write(descriptor, payload[written:])
    finally:
        os.close(descriptor)


def _iter_project_files(base: Path, pattern: str) -> list[Path]:
    """Walk the project for files matching a glob, skipping `.git` and any
    symlinked directory (which could otherwise walk out of the tree)."""
    matches: list[Path] = []
    scanned = 0
    for directory, subdirectories, filenames in os.walk(base):
        subdirectories[:] = [
            name
            for name in subdirectories
            if name != ".git" and not os.path.islink(os.path.join(directory, name))
        ]
        for filename in filenames:
            scanned += 1
            if scanned > MAX_FILES_SCANNED:
                return matches
            full = Path(directory) / filename
            relative = full.relative_to(base).as_posix()
            if fnmatch.fnmatch(relative, pattern) or fnmatch.fnmatch(filename, pattern):
                matches.append(full)
    return matches


# --------------------------------------------------------------------------
# Tool definitions and execution
# --------------------------------------------------------------------------

_READ_TOOLS = ("read_file", "list_files", "search")
_WRITE_TOOLS = ("write_file", "edit_file")

_TOOL_SCHEMAS: dict[str, dict[str, Any]] = {
    "read_file": {
        "description": "Read a UTF-8 text file from the project. Paths are relative to the project root.",
        "parameters": {
            "type": "object",
            "properties": {"path": {"type": "string", "description": "Path relative to the project root."}},
            "required": ["path"],
        },
    },
    "list_files": {
        "description": "List project files matching a glob pattern, e.g. 'src/**/*.py' or '*.md'.",
        "parameters": {
            "type": "object",
            "properties": {"pattern": {"type": "string", "description": "Glob pattern."}},
            "required": ["pattern"],
        },
    },
    "search": {
        "description": "Search project file contents for a regular expression, optionally limited to files matching a glob.",
        "parameters": {
            "type": "object",
            "properties": {
                "pattern": {"type": "string", "description": "Python regular expression."},
                "glob": {"type": "string", "description": "Optional glob limiting which files are searched."},
            },
            "required": ["pattern"],
        },
    },
    "write_file": {
        "description": "Create or overwrite a project file with the given UTF-8 content.",
        "parameters": {
            "type": "object",
            "properties": {
                "path": {"type": "string", "description": "Path relative to the project root."},
                "content": {"type": "string", "description": "Full new file content."},
            },
            "required": ["path", "content"],
        },
    },
    "edit_file": {
        "description": "Replace one exact occurrence of old_string with new_string in a project file.",
        "parameters": {
            "type": "object",
            "properties": {
                "path": {"type": "string", "description": "Path relative to the project root."},
                "old_string": {"type": "string", "description": "Exact text to replace; must occur exactly once."},
                "new_string": {"type": "string", "description": "Replacement text."},
            },
            "required": ["path", "old_string", "new_string"],
        },
    },
    "run_command": {
        "description": "Run one allowlisted command in the project root and return its combined output.",
        "parameters": {
            "type": "object",
            "properties": {
                "command": {"type": "string", "description": "Command name; must be operator-allowlisted."},
                "args": {
                    "type": "array",
                    "items": {"type": "string"},
                    "description": "Arguments. Not shell-interpreted.",
                },
            },
            "required": ["command"],
        },
    },
}


def available_tool_names(capability_tools: list[str] | None, writes_allowed: bool, command_allowlist: list[str]) -> list[str]:
    """Decide which tools this dispatch offers.

    `capability_tools` is the role's entry from `runner-capabilities.json`'s
    `capability_tiers` -- the same manifest the wrapper generators read, now
    consulted at dispatch time. A read-only tier declares no Edit/Write, so
    it never reaches the write tools regardless of `writes_allowed`; a
    write-capable tier still needs `writes_allowed`, which carries the
    caller's mode, the narrowed sandbox, the consumed confirmation token and
    the operator's `runners.api_allow_writes` opt-in.

    `run_command` is offered only when the operator allowlisted at least one
    command AND the tier declares Bash AND writes are allowed -- a read-only
    review role does not get to execute anything.
    """
    declared = set(capability_tools or [])
    names = [name for name in _READ_TOOLS]
    tier_can_write = bool(declared & {"Edit", "Write"})
    if writes_allowed and tier_can_write:
        names.extend(_WRITE_TOOLS)
        if command_allowlist and "Bash" in declared:
            names.append("run_command")
    return names


def build_tool_schemas(names: list[str], command_allowlist: list[str]) -> list[dict[str, Any]]:
    schemas: list[dict[str, Any]] = []
    for name in names:
        spec = dict(_TOOL_SCHEMAS[name])
        description = spec["description"]
        if name == "run_command":
            description = f"{description} Allowed commands: {', '.join(sorted(command_allowlist))}."
        schemas.append(
            {
                "type": "function",
                "function": {
                    "name": name,
                    "description": description,
                    "parameters": spec["parameters"],
                },
            }
        )
    return schemas


class Toolbox:
    """Executes tool calls against one project root, under one policy.

    Every filesystem entry point goes through `resolve_within_project`; there
    is deliberately no path handling anywhere else in this class.
    """

    def __init__(
        self,
        project_root: Path,
        allowed: list[str],
        command_allowlist: list[str],
        dispatch_depth: int,
        deadline: float,
    ) -> None:
        self.project_root = Path(os.path.realpath(project_root))
        self.allowed = set(allowed)
        self.command_allowlist = list(command_allowlist)
        self.dispatch_depth = dispatch_depth
        self.deadline = deadline
        self.files_written: list[str] = []
        self.commands_run: list[str] = []
        self.denied_calls = 0

    def execute(self, name: str, arguments: dict[str, Any]) -> str:
        if name not in self.allowed:
            self.denied_calls += 1
            return f"ERROR: tool {name!r} is not available for this dispatch"
        handler: Callable[[dict[str, Any]], str] = getattr(self, f"_tool_{name}")
        try:
            return _truncate(handler(arguments))
        except ToolDenied as error:
            self.denied_calls += 1
            return f"ERROR: {error}"
        except OSError as error:
            return f"ERROR: {error.strerror or error}"

    # -- read -------------------------------------------------------------

    def _tool_read_file(self, arguments: dict[str, Any]) -> str:
        path = resolve_within_project(self.project_root, arguments.get("path"))
        return _read_text_capped(path)

    def _tool_list_files(self, arguments: dict[str, Any]) -> str:
        pattern = arguments.get("pattern")
        if not isinstance(pattern, str) or not pattern:
            raise ToolDenied("pattern must be a non-empty string")
        matches = _iter_project_files(self.project_root, pattern)
        if not matches:
            return "(no files matched)"
        return "\n".join(sorted(match.relative_to(self.project_root).as_posix() for match in matches))

    def _tool_search(self, arguments: dict[str, Any]) -> str:
        raw_pattern = arguments.get("pattern")
        if not isinstance(raw_pattern, str) or not raw_pattern:
            raise ToolDenied("pattern must be a non-empty string")
        try:
            expression = re.compile(raw_pattern)
        except re.error as error:
            raise ToolDenied(f"invalid regular expression: {error}") from error
        glob = arguments.get("glob")
        candidates = _iter_project_files(self.project_root, glob if isinstance(glob, str) and glob else "*")
        lines: list[str] = []
        for candidate in sorted(candidates):
            try:
                text = _read_text_capped(candidate)
            except ToolDenied:
                continue
            relative = candidate.relative_to(self.project_root).as_posix()
            for number, line in enumerate(text.splitlines(), start=1):
                if expression.search(line):
                    lines.append(f"{relative}:{number}:{line.strip()}")
                    if len(lines) >= MAX_SEARCH_MATCHES:
                        lines.append(f"(stopped at {MAX_SEARCH_MATCHES} matches)")
                        return "\n".join(lines)
        return "\n".join(lines) if lines else "(no matches)"

    # -- write ------------------------------------------------------------

    def _tool_write_file(self, arguments: dict[str, Any]) -> str:
        path = resolve_within_project(self.project_root, arguments.get("path"))
        content = arguments.get("content")
        if not isinstance(content, str):
            raise ToolDenied("content must be a string")
        encoded = content.encode("utf-8")
        if len(encoded) > MAX_WRITE_BYTES:
            raise ToolDenied(f"content exceeds the {MAX_WRITE_BYTES}-byte write cap")
        path.parent.mkdir(parents=True, exist_ok=True)
        _write_bytes_nofollow(path, encoded)
        relative = path.relative_to(self.project_root).as_posix()
        if relative not in self.files_written:
            self.files_written.append(relative)
        return f"wrote {len(encoded)} bytes to {relative}"

    def _tool_edit_file(self, arguments: dict[str, Any]) -> str:
        path = resolve_within_project(self.project_root, arguments.get("path"))
        old_string = arguments.get("old_string")
        new_string = arguments.get("new_string")
        if not isinstance(old_string, str) or not isinstance(new_string, str):
            raise ToolDenied("old_string and new_string must both be strings")
        if not old_string:
            raise ToolDenied("old_string must not be empty; use write_file to create a file")
        text = _read_text_capped(path)
        occurrences = text.count(old_string)
        if occurrences == 0:
            raise ToolDenied("old_string was not found in the file")
        if occurrences > 1:
            # Same rule as this suite's own editing tools: an ambiguous match
            # means the model has not identified a unique site, and guessing
            # which one it meant is how an edit lands in the wrong place.
            raise ToolDenied(f"old_string occurs {occurrences} times; it must occur exactly once")
        _write_bytes_nofollow(path, text.replace(old_string, new_string, 1).encode("utf-8"))
        relative = path.relative_to(self.project_root).as_posix()
        if relative not in self.files_written:
            self.files_written.append(relative)
        return f"edited {relative}"

    # -- execute ----------------------------------------------------------

    def _tool_run_command(self, arguments: dict[str, Any]) -> str:
        command = arguments.get("command")
        if not isinstance(command, str) or not command:
            raise ToolDenied("command must be a non-empty string")
        if command.casefold() in _REFUSED_COMMANDS_FOLDED:
            raise ToolDenied(
                f"{command!r} is never runnable from a dispatched role (it would start another agent)"
            )
        if command not in self.command_allowlist:
            raise ToolDenied(
                f"{command!r} is not in the operator's runners.api_command_allowlist "
                f"({', '.join(sorted(self.command_allowlist)) or 'empty'})"
            )
        raw_args = arguments.get("args") or []
        if not isinstance(raw_args, list) or not all(isinstance(item, str) for item in raw_args):
            raise ToolDenied("args must be a list of strings")
        resolved = shutil.which(command)
        if not resolved:
            raise ToolDenied(f"{command!r} is allowlisted but not found on PATH")
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise ToolDenied("the dispatch deadline has passed; not starting a new command")
        # Routed through the existing child-spawn helper on purpose: it is
        # what gives this one tool the process-group isolation, group-kill on
        # timeout, output cap and deny-by-default environment that the rest
        # of this runner cannot inherit from a child CLI. shell=False is
        # implicit -- spawn_and_wait takes an argv list and never a string.
        child_env = core.build_child_env(self.dispatch_depth, self.project_root)
        child_env["PATH"] = _sanitized_child_path(child_env.get("PATH", ""))
        result = core.spawn_and_wait(
            [resolved, *raw_args],
            prompt="",
            cwd=self.project_root,
            env=child_env,
            timeout_seconds=min(remaining, core.DEFAULT_TIMEOUT_SECONDS),
        )
        self.commands_run.append(command)
        status = "timed out" if result["timed_out"] else f"exit {result['exit_code']}"
        return f"[{command} {status}]\n{result['stdout_text']}"


def _sanitized_child_path(path_value: str) -> str:
    """Drop from PATH every directory containing a refused agent binary.

    Defense in depth for the gap `_REFUSED_COMMANDS` cannot close on its own:
    that check sees only the literal `command` argument, so an allowlisted
    interpreter could `exec codex` as a grandchild. Removing the directories
    where those binaries actually live means a bare-name lookup inside the
    child fails.

    HONEST LIMIT -- do not describe this as containment. It defeats a
    bare-name `exec`, nothing more. A child that hardcodes an absolute path,
    or reconstructs one, is unaffected, and any allowlisted interpreter can
    do that trivially. It narrows the surface; `MAX_DISPATCH_DEPTH` remains
    the actual backstop, and that guard is itself documented as advisory
    against an adversarial child.

    Never returns an empty PATH: if stripping would remove everything, the
    original is kept rather than handing the child a PATH so broken that
    legitimate allowlisted commands fail in a way that looks like a bug
    rather than a policy decision.
    """
    directories = [entry for entry in path_value.split(os.pathsep) if entry]
    kept = [
        directory
        for directory in directories
        if not any(
            os.path.exists(os.path.join(directory, name)) for name in _REFUSED_COMMANDS
        )
    ]
    return os.pathsep.join(kept) if kept else path_value


def _truncate(text: str) -> str:
    encoded = text.encode("utf-8")
    if len(encoded) <= MAX_TOOL_RESULT_BYTES:
        return text
    return encoded[:MAX_TOOL_RESULT_BYTES].decode("utf-8", errors="ignore") + "\n... (truncated)"


# --------------------------------------------------------------------------
# Configuration
# --------------------------------------------------------------------------


def resolve_endpoint(project_root: Path, model: str) -> ChatEndpoint:
    """Build the endpoint client from operator settings.

    The API key is read from the environment variable *named* by
    `runners.api_key_env`, never from a settings file -- `settings.py`
    structurally refuses secret-shaped keys. Note the trust boundary this
    creates and that `SECURITY-CONTROLS.md` records: the key is read from
    *this server process's own* environment, which is exactly what the child
    environment allowlist exists to keep out of a dispatched child. There is
    no contradiction (this path spawns no child, so there is no child
    environment for it to leak into), but it is a genuinely new boundary.
    """
    base_url = settings.resolve_setting("runners.api_base_url", start=project_root)
    if not base_url:
        raise ApiRunnerError(
            "runner='api' requires runners.api_base_url "
            f"({settings.FIELDS['runners.api_base_url'].env_var}) to be configured"
        )
    key_env = settings.resolve_setting("runners.api_key_env", start=project_root)
    api_key = os.environ.get(key_env) if key_env else None
    if key_env and not api_key:
        raise ApiRunnerError(
            f"runners.api_key_env names {key_env!r}, but that variable is unset in this "
            "server process's environment"
        )
    return ChatEndpoint(base_url=base_url, model=model, api_key=api_key)


def resolve_model(role: core.ResolvedRole, project_root: Path) -> str:
    """The model this dispatch addresses.

    Always an operator setting, never the wrapper's `codex_model`: that field
    holds a vendor identifier a self-hosted endpoint has never heard of. A
    tier with no configured model is a configuration error, reported as such
    rather than silently falling back to an identifier that would 404.
    """
    model = core._local_model_for_tier(role.model_tier, project_root)
    if model:
        return model
    tier = role.model_tier or "unknown"
    raise ApiRunnerError(
        f"runner='api' has no model configured for the {tier!r} tier of role "
        f"{role.role_id!r}; set runners.local_model_{tier} "
        f"(SECURE_CLOUD_AGENTS_LOCAL_MODEL_{tier.upper()})"
    )


def writes_are_allowed(mode: str, effective_sandbox: str, project_root: Path) -> bool:
    """All of the conditions a write-capable api dispatch must satisfy.

    Three of these are already true by the time this runner is reached --
    `dispatch_core` will not have produced a write-capable
    `effective_sandbox` without the caller's mode and a consumed
    confirmation token. They are re-checked here anyway, cheaply and
    locally, so this module's write authorization does not depend on
    reading the caller's control flow correctly.
    """
    if mode != "scoped-repository-edit":
        return False
    if effective_sandbox not in core.WRITE_CAPABLE_SANDBOX_MODES:
        return False
    return bool(settings.resolve_setting("runners.api_allow_writes", start=project_root))


def load_capability_tools(role_id: str, project_root: Path) -> list[str] | None:
    """The role's declared tool list from `runner-capabilities.json`, via its
    `capability` in `catalog.yaml`.

    Returns None when either lookup fails, which `available_tool_names`
    treats as "no write capability declared" -- the fail-closed direction.
    """
    try:
        catalog_text = core.CATALOG_PATH.read_text(encoding="utf-8")
        manifest = json.loads(core.RUNNER_CAPABILITIES_PATH.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return None
    from routing import parse_catalog_entries  # local: keeps module import cheap

    entry = parse_catalog_entries(catalog_text).get(role_id) or {}
    capability = entry.get("capability")
    tier = (manifest.get("capability_tiers") or {}).get(capability) or {}
    tools = tier.get("tools")
    return list(tools) if isinstance(tools, list) else None


# --------------------------------------------------------------------------
# The dispatch itself
# --------------------------------------------------------------------------


def run_api_dispatch(
    role: core.ResolvedRole,
    brief: str,
    mode: str,
    effective_sandbox: str,
    project_root: Path,
    dispatch_depth: int,
    timeout_seconds: float = core.DEFAULT_TIMEOUT_SECONDS,
    endpoint: ChatEndpoint | None = None,
) -> dict[str, Any]:
    """Run one role to completion against the chat endpoint.

    Returns `spawn_and_wait`'s exact six-key result contract so every
    downstream consumer in `dispatch_core` -- the synchronous return, the
    async job store, team aggregation, and the audit record -- works
    unchanged. `pid` is None because there is no child process to identify
    (a fabricated integer would be worse than an honest null), and
    `exit_code` is 0 on a completed run, 1 otherwise. Three extra keys
    (`tool_calls`, `files_written`, `commands_run`) are added for the audit
    record; existing consumers ignore unknown keys.
    """
    started = time.monotonic()
    deadline = started + timeout_seconds

    model = resolve_model(role, project_root)
    client = endpoint or resolve_endpoint(project_root, model)

    command_allowlist = settings.resolve_setting("runners.api_command_allowlist", start=project_root) or []
    writes_allowed = writes_are_allowed(mode, effective_sandbox, project_root)
    tool_names = available_tool_names(
        load_capability_tools(role.role_id, project_root), writes_allowed, command_allowlist
    )
    tools = build_tool_schemas(tool_names, command_allowlist)
    toolbox = Toolbox(project_root, tool_names, command_allowlist, dispatch_depth, deadline)

    # System = the role's own trusted instructions. User = the brief, fenced
    # by dispatch_core's own helper, so the untrusted-data boundary is the
    # same code the CLI runners use rather than a second copy of the rule.
    messages: list[dict[str, Any]] = [
        {"role": "system", "content": role.developer_instructions},
        {"role": "user", "content": core.fence_untrusted_brief(brief)},
    ]

    transcript: list[str] = []
    tool_call_count = 0
    timed_out = False
    completed = False
    endpoint_failure: str | None = None

    for _ in range(MAX_TOOL_ITERATIONS):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            timed_out = True
            break
        try:
            # Bounded by whatever is left of the caller's deadline, not just
            # by the endpoint's own ceiling -- otherwise one slow response
            # can overrun a budget the rest of the pipeline treats as real.
            message = client.complete(messages, tools, timeout=remaining)
        except ApiRunnerError:
            if not (toolbox.files_written or toolbox.commands_run):
                # Nothing was mutated, so there is no accounting to preserve
                # and the honest classification is the original one: an
                # infrastructure failure, reported as `unavailable` by
                # dispatch_core's existing DispatchUnavailable handling.
                # Re-raised unchanged so a total endpoint outage does not
                # start masquerading as a dispatch that ran and failed.
                raise
            # Past this point the workspace HAS been mutated. Letting the
            # exception escape would discard that accounting and leave the
            # audit trail reporting an "unavailable" dispatch that in fact
            # wrote files -- the one outcome an auditor most needs to see.
            # Fall through to normal result assembly instead, which reports
            # exit_code=1 together with the partial
            # files_written/commands_run/tool_calls.
            endpoint_failure = str(sys.exc_info()[1])
            break
        content = message.get("content")
        if isinstance(content, str) and content.strip():
            transcript.append(content)
        calls = parse_tool_calls(message)
        if not calls:
            completed = True
            break
        # Echoed back verbatim (minus any provider-specific extras) so the
        # endpoint sees a well-formed assistant turn preceding the results.
        messages.append(
            {
                "role": "assistant",
                "content": content if isinstance(content, str) else "",
                "tool_calls": [
                    {
                        "id": call["id"],
                        "type": "function",
                        "function": {"name": call["name"], "arguments": json.dumps(call["arguments"])},
                    }
                    for call in calls
                ],
            }
        )
        for call in calls:
            tool_call_count += 1
            result = toolbox.execute(call["name"], call["arguments"])
            messages.append({"role": "tool", "tool_call_id": call["id"], "content": result})
    else:
        transcript.append(f"\n[dispatch stopped after {MAX_TOOL_ITERATIONS} tool iterations]")

    if timed_out:
        transcript.append(f"\n[dispatch stopped at the {timeout_seconds:.0f}s deadline]")

    if endpoint_failure is not None:
        # Surfaced in the transcript rather than only as an exception, so the
        # caller sees both that the endpoint failed *and* what was already
        # done before it did. The mutations below are real and on disk; the
        # audit record carries them via `files_written`/`commands_run`.
        transcript.append(f"\n[dispatch stopped: endpoint failure: {endpoint_failure}]")
        if toolbox.files_written or toolbox.commands_run:
            transcript.append(
                "[the endpoint failed partway through: this dispatch had already written "
                f"{len(toolbox.files_written)} file(s) and run {len(toolbox.commands_run)} "
                "command(s); those effects are on disk and are NOT rolled back]"
            )

    stdout_text = "\n\n".join(transcript).strip()
    encoded = stdout_text.encode("utf-8")
    truncated = len(encoded) > core.MAX_CHILD_OUTPUT_BYTES
    if truncated:
        stdout_text = encoded[: core.MAX_CHILD_OUTPUT_BYTES].decode("utf-8", errors="ignore")

    return {
        "pid": None,
        "exit_code": 0 if completed else 1,
        "timed_out": timed_out,
        "duration_seconds": round(time.monotonic() - started, 3),
        "stdout_truncated": truncated,
        "stdout_text": stdout_text,
        "tool_calls": tool_call_count,
        "files_written": list(toolbox.files_written),
        "commands_run": sorted(set(toolbox.commands_run)),
    }


def make_child_runner(
    role: core.ResolvedRole, mode: str, effective_sandbox: str, brief: str
) -> core.ChildRunner:
    """Adapt `run_api_dispatch` to the `ChildRunner` call signature.

    `dispatch_core` calls every runner as
    `child_runner(argv, prompt=..., cwd=..., env=..., timeout_seconds=...)`.
    That signature is depended on by the entire existing test suite, so it is
    not changed; instead the things an HTTP dispatch needs and an argv cannot
    carry -- the resolved role, the authorization context, and the raw brief
    -- are captured in this closure.

    Three of the call's parameters are deliberately unused:

      - `argv`: for this runner it is a short descriptive list, never executed.
      - `env`: there is no child process to give an environment to.
        `run_command` builds its own through `core.build_child_env`.
      - `prompt`: it arrives as `compose_prompt()` output, i.e. the role's
        instructions and the fenced brief already concatenated into one
        string for a CLI's stdin. A chat API has separate system and user
        slots, so `run_api_dispatch` composes those two messages itself from
        the role and the raw `brief` captured here -- reusing
        `core.fence_untrusted_brief` for the untrusted half. Re-fencing the
        composed prompt instead would both duplicate the instructions and
        nest one fence inside another.

    `dispatch_depth` still comes from `env`, because that is where the
    dispatch pipeline puts it and `run_command`'s own child must inherit it.
    """

    def child_runner(
        argv: list[str],
        *,
        prompt: str,
        cwd: Path,
        env: dict[str, str],
        timeout_seconds: float = core.DEFAULT_TIMEOUT_SECONDS,
    ) -> dict[str, Any]:
        return run_api_dispatch(
            role=role,
            brief=brief,
            mode=mode,
            effective_sandbox=effective_sandbox,
            project_root=Path(cwd),
            dispatch_depth=int(env.get(core.DEPTH_ENV_VAR, "1")),
            timeout_seconds=timeout_seconds,
        )

    return child_runner
