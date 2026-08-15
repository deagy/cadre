#!/usr/bin/env python3
"""Differential probe for the API runner.

    python3 roster/orchestration/test/probe_api_runner_parity.py

Runs a corpus of scripted dispatches through both implementations and reports
every disagreement.

## What is compared, and what deliberately is not

The API runner has no byte-exact output contract the way `cadre select` does,
and its behaviour depends on what a model says. So this compares **effects
and decisions**:

- which files exist afterwards, and with what contents
- which files the runner *reports* writing, and which commands it ran
- how the dispatch was classified: unavailable, or completed with an exit code

It does **not** compare error prose. Go and Python word the same refusal
differently without disagreeing about it, and pinning the wording would make
this fail on a rephrasing while missing a real divergence. The tree-after
comparison is what catches a runner that reports the right accounting while
writing the wrong bytes.

## How a scenario works

Each side stands up its own HTTP server replaying the same canned response
list, so neither depends on the other's ordering. Running past the end of the
script makes the endpoint fail, which is how the endpoint-failure scenarios
are expressed without a second mechanism.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(REPOSITORY_ROOT / "roster" / "orchestration" / "mcp"))
sys.path.insert(0, str(REPOSITORY_ROOT / "roster" / "orchestration" / "src"))
sys.path.insert(0, str(REPOSITORY_ROOT / "roster" / "shared" / "src"))

import api_runner  # noqa: E402
import dispatch_core as core  # noqa: E402


def tool_call(name: str, arguments: dict) -> str:
    """One assistant turn asking for a single tool call."""
    return json.dumps(
        {
            "choices": [
                {
                    "message": {
                        "content": "",
                        "tool_calls": [
                            {
                                "id": "c1",
                                "type": "function",
                                "function": {"name": name, "arguments": json.dumps(arguments)},
                            }
                        ],
                    }
                }
            ]
        }
    )


PLAIN = json.dumps({"choices": [{"message": {"content": "done"}}]})


def build_corpus() -> list[dict]:
    """Every scenario, each with why it is here.

    Weighted towards refusals: those are the decisions that matter, and the
    ones where two implementations most plausibly disagree while both looking
    correct in isolation.
    """
    seed = {"src/app.go": "package main\n", "README.md": "# project\n"}
    return [
        {
            "name": "plain-completion",
            "why": "No tool calls at all: the dispatch completes on the first reply.",
            "responses": [PLAIN],
            "files": seed,
            "writes_allowed": False,
            "capability_tools": ["Read"],
            "command_allowlist": [],
        },
        {
            "name": "read-inside-project",
            "why": "The ordinary case. If this diverges, nothing else is meaningful.",
            "responses": [tool_call("read_file", {"path": "src/app.go"}), PLAIN],
            "files": seed,
            "writes_allowed": False,
            "capability_tools": ["Read"],
            "command_allowlist": [],
        },
        {
            "name": "read-escapes-project",
            "why": "A relative path climbing out. Both must refuse and continue.",
            "responses": [tool_call("read_file", {"path": "../../etc/passwd"}), PLAIN],
            "files": seed,
            "writes_allowed": False,
            "capability_tools": ["Read"],
            "command_allowlist": [],
        },
        {
            "name": "read-absolute-outside",
            "why": "An absolute path outside the project, not merely a climbing one.",
            "responses": [tool_call("read_file", {"path": "/etc/passwd"}), PLAIN],
            "files": seed,
            "writes_allowed": False,
            "capability_tools": ["Read"],
            "command_allowlist": [],
        },
        {
            "name": "write-authorized",
            "why": "An authorized write must land on disk with the same bytes on both sides.",
            "responses": [
                tool_call("write_file", {"path": "notes.txt", "content": "hello\n"}),
                PLAIN,
            ],
            "files": seed,
            "writes_allowed": True,
            "capability_tools": ["Read", "Edit", "Write", "Bash"],
            "command_allowlist": [],
        },
        {
            "name": "write-unauthorized",
            "why": "Writes off: the file must not appear, and the refusal must not "
            "count as a mutation.",
            "responses": [
                tool_call("write_file", {"path": "notes.txt", "content": "hello\n"}),
                PLAIN,
            ],
            "files": seed,
            "writes_allowed": False,
            "capability_tools": ["Read"],
            "command_allowlist": [],
        },
        {
            "name": "write-escapes-project",
            "why": "An authorized write is still confined. This is the one that "
            "would be catastrophic to get wrong in only one implementation.",
            "responses": [
                tool_call("write_file", {"path": "../escaped.txt", "content": "x\n"}),
                PLAIN,
            ],
            "files": seed,
            "writes_allowed": True,
            "capability_tools": ["Read", "Edit", "Write", "Bash"],
            "command_allowlist": [],
        },
        {
            "name": "write-into-git",
            "why": "A hook is code that runs later, outside the loop and outside "
            "every limit it applies.",
            "responses": [
                tool_call("write_file", {"path": ".git/hooks/pre-commit", "content": "#!/bin/sh\n"}),
                PLAIN,
            ],
            "files": seed,
            "writes_allowed": True,
            "capability_tools": ["Read", "Edit", "Write", "Bash"],
            "command_allowlist": [],
        },
        {
            "name": "command-not-allowlisted",
            "why": "An empty allowlist permits nothing.",
            "responses": [tool_call("run_command", {"command": "rm", "args": ["-rf", "."]}), PLAIN],
            "files": seed,
            "writes_allowed": True,
            "capability_tools": ["Read", "Edit", "Write", "Bash"],
            "command_allowlist": [],
        },
        {
            "name": "command-agent-starting",
            "why": "Allowlisted *and* agent-starting: the refusal that cannot be "
            "configured away, because a role that can start an agent escapes "
            "every limit by starting one without them.",
            "responses": [tool_call("run_command", {"command": "codex", "args": []}), PLAIN],
            "files": seed,
            "writes_allowed": True,
            "capability_tools": ["Read", "Edit", "Write", "Bash"],
            "command_allowlist": ["codex"],
        },
        {
            "name": "write-allowed-but-tier-declares-none",
            "why": "The gate this probe found missing from the Go port. The "
            "operator allowed writes, but the role's declared capability tier "
            "has no Edit/Write -- so a read-only role never reaches the write "
            "tools however things are configured. Without a scenario where "
            "writes_allowed and the tier disagree, removing the gate passes.",
            "responses": [
                tool_call("write_file", {"path": "notes.txt", "content": "hello\n"}),
                PLAIN,
            ],
            "files": seed,
            "writes_allowed": True,
            "capability_tools": ["Read"],
            "command_allowlist": [],
        },
        {
            "name": "command-allowlisted-but-tier-declares-no-bash",
            "why": "run_command needs a declared Bash capability as well as an "
            "allowlist -- a review role that may edit still does not execute.",
            "responses": [tool_call("run_command", {"command": "ls", "args": []}), PLAIN],
            "files": seed,
            "writes_allowed": True,
            "capability_tools": ["Read", "Edit", "Write"],
            "command_allowlist": ["ls"],
        },
        {
            "name": "endpoint-fails-before-any-write",
            "why": "Nothing mutated, so the honest classification is an "
            "infrastructure failure -- unavailable, not a dispatch that ran.",
            "responses": [],
            "files": seed,
            "writes_allowed": True,
            "capability_tools": ["Read", "Edit", "Write", "Bash"],
            "command_allowlist": [],
        },
        {
            "name": "endpoint-fails-after-a-write",
            "why": "The audit case. The workspace HAS been mutated, so reporting "
            "unavailable would hide a real write from the audit trail.",
            "responses": [
                tool_call("write_file", {"path": "partial.txt", "content": "written\n"})
            ],
            "files": seed,
            "writes_allowed": True,
            "capability_tools": ["Read", "Edit", "Write", "Bash"],
            "command_allowlist": [],
        },
        {
            "name": "malformed-tool-arguments",
            "why": "A malformed argument blob is one refused tool call, not a "
            "failed dispatch.",
            "responses": [
                json.dumps(
                    {
                        "choices": [
                            {
                                "message": {
                                    "content": "",
                                    "tool_calls": [
                                        {
                                            "id": "c1",
                                            "type": "function",
                                            "function": {
                                                "name": "read_file",
                                                "arguments": "not valid json",
                                            },
                                        }
                                    ],
                                }
                            }
                        ]
                    }
                ),
                PLAIN,
            ],
            "files": seed,
            "writes_allowed": False,
            "capability_tools": ["Read"],
            "command_allowlist": [],
        },
        {
            "name": "unknown-tool",
            "why": "A tool the runner does not implement must be refused, not "
            "crash the dispatch.",
            "responses": [tool_call("delete_everything", {"path": "."}), PLAIN],
            "files": seed,
            "writes_allowed": True,
            "capability_tools": ["Read", "Edit", "Write", "Bash"],
            "command_allowlist": [],
        },
    ]


class _ScriptedHandler(BaseHTTPRequestHandler):
    responses: list[str] = []
    index = 0

    def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler's spelling
        length = int(self.headers.get("Content-Length", "0"))
        self.rfile.read(length)
        cls = type(self)
        if cls.index >= len(cls.responses):
            body = json.dumps({"error": "scripted endpoint exhausted"}).encode("utf-8")
            self.send_response(500)
        else:
            body = cls.responses[cls.index].encode("utf-8")
            cls.index += 1
            self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args) -> None:
        """Silence the default stderr access log."""


def read_tree(root: Path) -> dict[str, str]:
    tree = {}
    for path in sorted(root.rglob("*")):
        if path.is_file():
            try:
                tree[str(path.relative_to(root)).replace(os.sep, "/")] = path.read_text(
                    encoding="utf-8"
                )
            except (OSError, UnicodeDecodeError):
                pass
    return tree


def run_python_scenario(scenario: dict) -> dict:
    handler = type("Handler", (_ScriptedHandler,), {"responses": list(scenario["responses"]), "index": 0})
    server = HTTPServer(("127.0.0.1", 0), handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()

    workspace = Path(tempfile.mkdtemp(prefix="apirunner-py-")).resolve()
    try:
        for relative, content in scenario["files"].items():
            path = workspace / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content, encoding="utf-8")

        endpoint = api_runner.ChatEndpoint(
            base_url=f"http://127.0.0.1:{server.server_port}", model="probe-model", api_key=None
        )
        toolbox = api_runner.Toolbox(
            workspace,
            api_runner.available_tool_names(
                scenario.get("capability_tools"), scenario["writes_allowed"], scenario["command_allowlist"]
            ),
            scenario["command_allowlist"],
            0,
            __import__("time").monotonic() + scenario.get("timeout_seconds", 30),
        )
        messages = [
            {"role": "system", "content": "you are a probe"},
            {"role": "user", "content": "do the thing"},
        ]
        outcome = {"name": scenario["name"], "unavailable": False, "exit_code": 0,
                   "files_written": [], "commands_run": [], "tool_calls": 0}

        # The loop, mirrored from run_api_dispatch's own shape so this compares
        # the same decisions rather than a re-imagined control flow.
        try:
            completed = False
            for _ in range(api_runner.MAX_TOOL_ITERATIONS):
                try:
                    message = endpoint.complete(messages, None)
                except api_runner.ApiRunnerError:
                    if not (toolbox.files_written or toolbox.commands_run):
                        outcome["unavailable"] = True
                        raise
                    outcome["exit_code"] = 1
                    break
                try:
                    calls = api_runner.parse_tool_calls(message)
                except api_runner.ApiRunnerError:
                    # parse_tool_calls raises rather than guessing what the
                    # model meant. run_api_dispatch lets that propagate, so
                    # dispatch_core reports it as unavailable -- classified
                    # here the same way an endpoint failure is, since with
                    # nothing mutated it is the same kind of event.
                    if not (toolbox.files_written or toolbox.commands_run):
                        outcome["unavailable"] = True
                        raise
                    outcome["exit_code"] = 1
                    break
                if not calls:
                    completed = True
                    break
                messages.append({"role": "assistant", "content": message.get("content") or "",
                                 "tool_calls": [
                                     {"id": c["id"], "type": "function",
                                      "function": {"name": c["name"],
                                                   "arguments": json.dumps(c["arguments"])}}
                                     for c in calls]})
                for call in calls:
                    outcome["tool_calls"] += 1
                    result = toolbox.execute(call["name"], call["arguments"])
                    messages.append({"role": "tool", "tool_call_id": call["id"], "content": result})
            _ = completed
        except api_runner.ApiRunnerError:
            pass

        if not outcome["unavailable"]:
            outcome["files_written"] = sorted(str(p) for p in toolbox.files_written)
            outcome["commands_run"] = sorted(toolbox.commands_run)
        outcome["tree_after"] = read_tree(workspace)
        return outcome
    finally:
        server.shutdown()
        shutil.rmtree(workspace, ignore_errors=True)


def run_go(scenarios: list[dict], workspace: Path) -> list[dict]:
    input_path = workspace / "scenarios.json"
    output_path = workspace / "outcomes.json"
    input_path.write_text(json.dumps(scenarios), encoding="utf-8")

    result = subprocess.run(
        ["go", "test", "./internal/orchestration/", "-run", "TestAPIRunnerParityProbe", "-count=1", "-v"],
        cwd=REPOSITORY_ROOT, capture_output=True, text=True,
        env={**os.environ, "CGO_ENABLED": "1",
             "CADRE_APIRUNNER_PROBE_IN": str(input_path),
             "CADRE_APIRUNNER_PROBE_OUT": str(output_path)},
    )
    if result.returncode != 0:
        sys.stderr.write(result.stdout + result.stderr)
        raise SystemExit("go probe failed")
    return json.loads(output_path.read_text(encoding="utf-8"))


COMPARED_KEYS = ("unavailable", "exit_code", "files_written", "commands_run", "tool_calls", "tree_after")


def main() -> int:
    corpus = build_corpus()
    print(f"corpus: {len(corpus)} scenarios")

    with tempfile.TemporaryDirectory() as workspace:
        go_outcomes = {o["name"]: o for o in run_go(corpus, Path(workspace))}

    differing = 0
    for scenario in corpus:
        expected = run_python_scenario(scenario)
        actual = go_outcomes.get(scenario["name"])
        if actual is None:
            print(f"\n  MISSING [{scenario['name']}] the Go probe produced no outcome")
            differing += 1
            continue

        mismatches = [key for key in COMPARED_KEYS if expected.get(key) != actual.get(key)]
        if not mismatches:
            continue
        differing += 1
        print(f"\n  DIFFERS [{scenario['name']}] {scenario['why']}")
        for key in mismatches:
            print(f"    {key}:")
            print(f"      python: {json.dumps(expected.get(key))[:200]}")
            print(f"      go:     {json.dumps(actual.get(key))[:200]}")

    print(f"\n  {len(corpus)} scenarios, {len(corpus) - differing} identical, {differing} differing")
    if differing:
        print("\nFAIL")
        return 1
    print("\nOK")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
