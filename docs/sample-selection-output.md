# Sample `cadre select` output

This walks through one real, committed `cadre select` plan so a reader can see
what the selector actually produces before running it themselves. The
authoritative shape is [`roster/orchestration/selection.schema.json`](../roster/orchestration/selection.schema.json)
(`schema_version: 8`); if this page and the schema ever disagree, the schema
wins.

See the [glossary](terminology.md) for definitions of the terms used below
(route, risk rule, team recipe, dispatch plan, quality gate, human gate,
knowledge focus, provenance, ...).

The example below is `GOLDEN-CROSS-STACK-1` from the golden-corpus regression
fixtures ([`roster/orchestration/test/fixtures/selection_golden_corpus.json`](../roster/orchestration/test/fixtures/selection_golden_corpus.json),
case id `CROSS-STACK-1`). It was chosen because it is a realistic, non-edge-case
task that touches two stacks at once (frontend and backend) and triggers a
named team recipe, which exercises most of the fields a reader will encounter
in practice. It is also asserted byte-for-byte against `build_dispatch_plan()`
in `internal/selector/golden_corpus_test.go`, so this page cannot silently drift from
selector behavior without that test failing first.

## Reproduce it

```sh
cadre select \
  --task "Add a React upload form backed by a PostgreSQL API" \
  --files frontend/src/Upload.tsx,services/upload/main.go \
  --task-id GOLDEN-CROSS-STACK-1 \
  --classification internal
```

## The output

`repository_root`, `generated_at`, `source_filter`, `provenance`, and
`dispatch_fingerprint` are derived from the environment the selector runs in
(working tree path, wall-clock time, the `deagy/cadre` origin remote of this
checkout, the checkout's commit and uncommitted paths, and a hash over the
rest of the plan, respectively) — expect different values in your own
checkout. `lifecycle_tracking.status` and `required_quality_gates[].reason`
also depend on your environment: this capture shows
`lifecycle_tracking.status: "integrated"` and gate-specific reasons because a
standalone Agentic SDLC executable was present on `PATH` when it ran; without
one, `lifecycle_tracking.status` reads `"standalone"` with a `reason`, and
every `required_quality_gates[].reason` instead reads "Required by routing
configuration (Agentic SDLC unavailable; gate detail omitted)." (see
`internal/selector/plan.go`). The `matched_routes` route
ids, `agents`, `teams`, the *set* of gate ids in `required_quality_gates`, and
`human_gates` stay identical either way, and are pinned by the golden-corpus
test referenced above (which compares route ids, not each entry's `reasons` —
reason content is pinned by `internal/selector's tests` instead) (that test forces standalone mode so the
corpus is reproducible without the executable — see the fixture file's
comment).

```json
{
  "schema_version": 8,
  "task_id": "GOLDEN-CROSS-STACK-1",
  "generated_at": "2026-08-10T18:57:46.976Z",
  "status": "ready",
  "workflow": "new-service",
  "inputs": {
    "task": "Add a React upload form backed by a PostgreSQL API",
    "repository_root": "/path/to/your/checkout",
    "base": null,
    "changed_file_source": "explicit",
    "changed_files": [
      "frontend/src/Upload.tsx",
      "services/upload/main.go"
    ],
    "classification": "internal",
    "source_filter": ["deagy/cadre", "proposed-knowledge"]
  },
  "matched_routes": [
    {
      "id": "frontend",
      "reasons": {
        "keywords": [
          "react"
        ],
        "keyword_groups": [],
        "paths": [
          {
            "pattern": "frontend/**",
            "file": "frontend/src/Upload.tsx"
          },
          {
            "pattern": "**/*.tsx",
            "file": "frontend/src/Upload.tsx"
          }
        ]
      }
    },
    {
      "id": "backend",
      "reasons": {
        "keywords": [
          "api",
          "postgresql"
        ],
        "keyword_groups": [],
        "paths": [
          {
            "pattern": "services/**",
            "file": "services/upload/main.go"
          },
          {
            "pattern": "**/*.go",
            "file": "services/upload/main.go"
          }
        ]
      }
    },
    {
      "id": "go-service-execution",
      "reasons": {
        "keywords": [],
        "keyword_groups": [],
        "paths": [
          {
            "pattern": "**/*.go",
            "file": "services/upload/main.go"
          }
        ]
      }
    }
  ],
  "matched_risks": [],
  "context_packs": [],
  "agents": {
    "primary": [
      "frontend-engineer",
      "backend-engineer",
      "go-service-implementer"
    ],
    "reviewers": [
      "test-engineer",
      "code-reviewer",
      "accessibility-reviewer"
    ],
    "support": [
      "interaction-designer"
    ]
  },
  "dispatch_disposition": {
    "status": "staffed",
    "reason": "A primary and/or reviewer role was selected and can be dispatched as an accountable executor or independent reviewer."
  },
  "teams": [
    {
      "id": "cross-stack-build",
      "type": "fixed",
      "members": [
        "frontend-engineer",
        "backend-engineer"
      ],
      "trigger_reason": {
        "routes": [
          "backend",
          "frontend"
        ]
      },
      "communication_mode": "peer",
      "fallback": "orchestrator-relayed",
      "description": "Cross-stack implementers coordinating shared contracts for a change spanning 2 or more stack layers."
    }
  ],
  "lifecycle_tracking": {
    "status": "integrated"
  },
  "required_quality_gates": [
    {
      "id": "G1",
      "required": true,
      "reason": "Required by the standalone lifecycle gate sequence.",
      "contributing_routes": [
        "lifecycle-sequence"
      ]
    },
    {
      "id": "G2",
      "required": true,
      "reason": "Required by the standalone lifecycle gate sequence.",
      "contributing_routes": [
        "lifecycle-sequence"
      ]
    },
    {
      "id": "G3",
      "required": true,
      "reason": "Architecture lifecycle gate (architecture phase).",
      "contributing_routes": [
        "frontend",
        "backend",
        "go-service-execution"
      ]
    },
    {
      "id": "G4",
      "required": true,
      "reason": "Governance and Data lifecycle gate (governance-data phase).",
      "contributing_routes": [
        "backend",
        "go-service-execution"
      ]
    },
    {
      "id": "G5",
      "required": true,
      "reason": "Security and Crypto lifecycle gate (security-crypto phase).",
      "contributing_routes": [
        "frontend",
        "backend",
        "go-service-execution"
      ]
    },
    {
      "id": "G6",
      "required": true,
      "reason": "Verification and Test lifecycle gate (verify phase).",
      "contributing_routes": [
        "frontend",
        "backend",
        "go-service-execution"
      ]
    },
    {
      "id": "G7",
      "required": true,
      "reason": "Evidence lifecycle gate (evidence phase).",
      "contributing_routes": [
        "frontend",
        "backend",
        "go-service-execution"
      ]
    }
  ],
  "ignored_quality_gates": [],
  "human_gates": [],
  "knowledge_context": {
    "status": "planned",
    "classification": "internal",
    "source_filter": ["deagy/cadre", "proposed-knowledge"],
    "requests": [
      {
        "agent": "interaction-designer",
        "query": "Task: Add a React upload form backed by a PostgreSQL API. Retrieve prior UX decisions, interaction patterns, accessibility findings, and user journey/flow history.",
        "invocation": {
          "launcher": {
            "runtime": "cadre",
            "minimum_version": "0.5.0",
            "resolution": "platform-anchored"
          },
          "args": [
            "/path/to/your/checkout/bin/cadre",
            "knowledge",
            "search",
            "--agent",
            "interaction-designer",
            "--task-id",
            "GOLDEN-CROSS-STACK-1",
            "--classification",
            "internal",
            "--top",
            "5",
            "--json",
            "--source",
            "deagy/cadre",
            "--source",
            "proposed-knowledge",
            "Task: Add a React upload form backed by a PostgreSQL API. Retrieve prior UX decisions, interaction patterns, accessibility findings, and user journey/flow history."
          ]
        }
      },
      {
        "agent": "frontend-engineer",
        "query": "Task: Add a React upload form backed by a PostgreSQL API. Retrieve frontend implementation patterns, UX decisions, accessibility behavior, API contracts, browser security, and approved React or TypeScript conventions.",
        "invocation": {
          "launcher": {
            "runtime": "cadre",
            "minimum_version": "0.5.0",
            "resolution": "platform-anchored"
          },
          "args": [
            "/path/to/your/checkout/bin/cadre",
            "knowledge",
            "search",
            "--agent",
            "frontend-engineer",
            "--task-id",
            "GOLDEN-CROSS-STACK-1",
            "--classification",
            "internal",
            "--top",
            "5",
            "--json",
            "--source",
            "deagy/cadre",
            "--source",
            "proposed-knowledge",
            "Task: Add a React upload form backed by a PostgreSQL API. Retrieve frontend implementation patterns, UX decisions, accessibility behavior, API contracts, browser security, and approved React or TypeScript conventions."
          ]
        }
      },
      {
        "agent": "backend-engineer",
        "query": "Task: Add a React upload form backed by a PostgreSQL API. Retrieve backend service patterns, datastore decisions, schemas, migrations, APIs, operational lessons, and approved Go or PostgreSQL conventions.",
        "invocation": {
          "launcher": {
            "runtime": "cadre",
            "minimum_version": "0.5.0",
            "resolution": "platform-anchored"
          },
          "args": [
            "/path/to/your/checkout/bin/cadre",
            "knowledge",
            "search",
            "--agent",
            "backend-engineer",
            "--task-id",
            "GOLDEN-CROSS-STACK-1",
            "--classification",
            "internal",
            "--top",
            "5",
            "--json",
            "--source",
            "deagy/cadre",
            "--source",
            "proposed-knowledge",
            "Task: Add a React upload form backed by a PostgreSQL API. Retrieve backend service patterns, datastore decisions, schemas, migrations, APIs, operational lessons, and approved Go or PostgreSQL conventions."
          ]
        }
      },
      {
        "agent": "go-service-implementer",
        "query": "Task: Add a React upload form backed by a PostgreSQL API. Retrieve Go service patterns, safe concurrency, interfaces, tests, and approved library conventions.",
        "invocation": {
          "launcher": {
            "runtime": "cadre",
            "minimum_version": "0.5.0",
            "resolution": "platform-anchored"
          },
          "args": [
            "/path/to/your/checkout/bin/cadre",
            "knowledge",
            "search",
            "--agent",
            "go-service-implementer",
            "--task-id",
            "GOLDEN-CROSS-STACK-1",
            "--classification",
            "internal",
            "--top",
            "5",
            "--json",
            "--source",
            "deagy/cadre",
            "--source",
            "proposed-knowledge",
            "Task: Add a React upload form backed by a PostgreSQL API. Retrieve Go service patterns, safe concurrency, interfaces, tests, and approved library conventions."
          ]
        }
      },
      {
        "agent": "test-engineer",
        "query": "Task: Add a React upload form backed by a PostgreSQL API. Retrieve Gherkin scenarios, regressions, failure cases, and quality history.",
        "invocation": {
          "launcher": {
            "runtime": "cadre",
            "minimum_version": "0.5.0",
            "resolution": "platform-anchored"
          },
          "args": [
            "/path/to/your/checkout/bin/cadre",
            "knowledge",
            "search",
            "--agent",
            "test-engineer",
            "--task-id",
            "GOLDEN-CROSS-STACK-1",
            "--classification",
            "internal",
            "--top",
            "5",
            "--json",
            "--source",
            "deagy/cadre",
            "--source",
            "proposed-knowledge",
            "Task: Add a React upload form backed by a PostgreSQL API. Retrieve Gherkin scenarios, regressions, failure cases, and quality history."
          ]
        }
      },
      {
        "agent": "code-reviewer",
        "query": "Task: Add a React upload form backed by a PostgreSQL API. Retrieve prior defects, coding conventions, exceptions, and relevant findings.",
        "invocation": {
          "launcher": {
            "runtime": "cadre",
            "minimum_version": "0.5.0",
            "resolution": "platform-anchored"
          },
          "args": [
            "/path/to/your/checkout/bin/cadre",
            "knowledge",
            "search",
            "--agent",
            "code-reviewer",
            "--task-id",
            "GOLDEN-CROSS-STACK-1",
            "--classification",
            "internal",
            "--top",
            "5",
            "--json",
            "--source",
            "deagy/cadre",
            "--source",
            "proposed-knowledge",
            "Task: Add a React upload form backed by a PostgreSQL API. Retrieve prior defects, coding conventions, exceptions, and relevant findings."
          ]
        }
      },
      {
        "agent": "accessibility-reviewer",
        "query": "Task: Add a React upload form backed by a PostgreSQL API. Retrieve prior accessibility findings, conformance target decisions, affected journeys, and assistive-technology constraints.",
        "invocation": {
          "launcher": {
            "runtime": "cadre",
            "minimum_version": "0.5.0",
            "resolution": "platform-anchored"
          },
          "args": [
            "/path/to/your/checkout/bin/cadre",
            "knowledge",
            "search",
            "--agent",
            "accessibility-reviewer",
            "--task-id",
            "GOLDEN-CROSS-STACK-1",
            "--classification",
            "internal",
            "--top",
            "5",
            "--json",
            "--source",
            "deagy/cadre",
            "--source",
            "proposed-knowledge",
            "Task: Add a React upload form backed by a PostgreSQL API. Retrieve prior accessibility findings, conformance target decisions, affected journeys, and assistive-technology constraints."
          ]
        }
      }
    ]
  },
  "provenance": {
    "catalog_content_hash": "sha256:6782e141a789a569cd16c7d029aa9346e09dead6d501a0b913cf83ed0d1a57b5",
    "routing_content_hash": "sha256:0cc2e34802a9edec336d950d5f98cb2a0e98d8230075a6366e523cf76815c13a",
    "git_commit_sha": "d17c47c25658ac9d86ddd17a04aa2d46e097bd1b",
    "git_dirty_paths": [],
    "agentic_sdlc_contract_version": 2
  },
  "dispatch_fingerprint": "sha256:5cbc395c71ece85feda27529422361eaa1f7193072f5bd4a6201e7d597a0369b"
}
```

## Reading the fields

- **`status` / `workflow`** — `status: "ready"` means the task matched at
  least one route; `"needs-triage"` means no route matched and the plan
  should not be treated as reviewable guidance. `workflow` is the single
  matched high-level shape (here `new-service`, since this task combines the
  `frontend` and `backend` routes).
- **`matched_routes`** — the `roster/orchestration/routing.json` routes whose
  paths or keywords matched this task's files/description, each as an `id`
  plus the `reasons` it matched: the literal `keywords`, conjunctive
  `keyword_groups`, and `paths` (each a `pattern`/`file` pair) that fired.
  Each route carries its own primary/reviewer/support role list; `agents.*`
  below is the union across every matched route. Read `reasons` when a route
  matched that you did not expect — it names the trigger without requiring a
  read of `routing.json`. Above, `frontend` matched on both the keyword
  `react` and two path patterns, while `backend` matched on `api` and
  `postgresql` plus its own two patterns.
- **`matched_risks`** — routing.json `risk_rules` (for example `production`
  or `destructive`) that matched, in the same `{id, reasons}` shape as
  `matched_routes`. Empty here because this task is neither.
- **`context_packs`** — the non-authoring reference packs
  ([`roster/context-packs/`](../roster/context-packs/)) selected alongside the
  roles, each as an `id`/`version`/`definition`/`content_hash`. They supply
  bounded vendor and platform context; they are never dispatched as agents and
  never approve work. Empty here because no pack matched this task.
- **`agents.primary` / `.reviewers` / `.support`** — the deduplicated role ids
  selected across all matched routes: who implements, who independently
  reviews, and who supports without owning the change.
- **`dispatch_disposition`** — `"staffed"` when `agents.primary`/`.reviewers`
  hold an accountable executor or independent reviewer; `"advisory-only"` when
  only `agents.support` was populated with nothing else selected;
  `"no-agents-selected"` otherwise. An orchestrator must not treat
  `"advisory-only"` as authorization to do the work itself.
- **`teams`** — deterministic `team_recipes` (routing.json) triggered by the
  matched route combination; never adds a role that wasn't already in
  `agents.*`. This example's `cross-stack-build` recipe fires because both
  `frontend` and `backend` matched. `communication_mode: "peer"` /
  `fallback: "orchestrator-relayed"` describe what's actually possible per
  runner — see
  [runner-adapters.md](../.agents/skills/run-agent-orchestration/references/runner-adapters.md).
- **`lifecycle_tracking`** — `"integrated"` when the standalone Agentic SDLC
  executable is on `PATH` and recognizes this repository's lifecycle
  contract; `"standalone"` (with a `reason`) otherwise. This does not mean
  gates were run — only whether the kernel is present to track them.
- **`required_quality_gates` / `ignored_quality_gates`** — the G1–G10 gates
  this task's matched routes and lifecycle phase require, each with the
  route(s) that contributed it, versus any gates explicitly ignored by
  `routing.json`'s `ignored_gates`.
- **`human_gates`** — gates requiring an accountable human decision (risk
  acceptance, production authorization, policy exception); empty here because
  this task reaches no such gate. Each entry also carries a
  `kernel_mutation_gate_id`, cross-referencing the Agentic SDLC kernel's own
  `contracts/mutation-gates.json` id where one exists — the kernel stays the
  authoritative definition, this is a pointer to it, not a duplicate.
- **`knowledge_context`** — one retrieval request per selected agent, each
  with the exact CLI invocation to run against the knowledge store
  (one `--source` per source in `source_filter` — this repository's origin
  remote plus `proposed-knowledge`, where steward-accepted findings are
  ingested — and `--classification` matched to the task). The second source
  appears here because the checkout this was captured from has its own
  `.agents/knowledge-store/config.json`; that file is gitignored, so a fresh
  clone with no partition of its own plans a single-source retrieval instead
  (`proposed-knowledge` is refused against the shared global-fallback store,
  and the refusal rejects the whole call). `status: "planned"`
  means retrieval is proposed, not performed — `cadre select` never executes
  retrieval itself.
- **`provenance`** — binds the plan to the exact inputs that produced it:
  content hashes over `catalog.yaml` and the routing configuration, plus
  best-effort `git_commit_sha` / `git_dirty_paths` for the checkout and, when
  `lifecycle_tracking.status` is `"integrated"`, the lifecycle contract
  version Cadre read. It is verifiable without trusting the process that
  generated the plan, and is absent entirely when no on-disk catalog/routing
  file backed the run.
- **`dispatch_fingerprint`** — a `sha256:`-prefixed hash over the rest of the
  plan (excluding `generated_at` and `provenance`), useful for detecting
  whether a plan changed between two runs.

The selector only produces this plan. It does not execute agents, retrieve
knowledge, approve gates, deploy, mutate infrastructure, merge, or push
changes — see the [orchestration guide](orchestration.md) for what happens
next with a plan like this.

## Diagnosing near-misses with `--explain`

`matched_routes[].reasons` (above) answers "why did this route match?" It
cannot answer the opposite question — "why did *this* route not match, and
how close did it come?" — which matters most at task-authoring time, when a
route you expected didn't fire. `cadre select --explain` answers that,
printed to **stderr**, separate from the JSON plan on stdout:

```sh
cadre select --task "improve cross-runner UX documentation" --explain
```

```
--explain: no near-miss routes for this task -- no unmatched route had a
partially satisfied keyword_groups entry (see internal/selector/nearmiss.go's relevance
threshold; most routes in the current routing.json use plain keywords/paths,
which have no partial-match state to report).
```

That "no near misses" answer is correct and expected here: `pipeline` did
match this task (on the `runner` keyword — see `matched_routes` above), and
as of this writing no route in `routing.json` declares a `keyword_groups`
entry (only `risk_rules` do), so there is currently nothing conjunctive for
any route to be *partially* close on. When a route does define
`keyword_groups`, an unmatched near-miss looks like this (shown here against
a synthetic route, since none of today's routes have one to demonstrate
against):

```
--explain: near-miss routes (did not match, but came close)

production-runbook-change:
  keyword_groups[1]: matched 1 of 3 required keywords (production); missing: prod, live environment
  keyword_groups[2]: matched 1 of 3 required keywords (runbook); missing: deployment, rollout
```

**Relevance threshold.** A route is only surfaced when at least one of its
`keyword_groups` entries is *partially* satisfied — some but not all of a
conjunctive (AND) group's keywords present in the task text. Plain
`keywords` and `paths` are disjunctive (OR) triggers: `match_rule()`
(`internal/selector/match.go`) already matches a route the moment
any one of them fires, so an *unmatched* route's plain-keyword/path overlap
is always exactly zero — there is no partial state to report, and printing
"0 of N keywords matched" for every one of the dozens of unmatched routes on
every call would be noise, not signal. Routes below this threshold are
omitted from the output entirely, not listed with an empty reasoning block.

**Diagnostic only, and purely descriptive.** `--explain` never changes the
JSON plan on stdout or `--output` — run the same command with and without
the flag and the plan (aside from `generated_at`) is identical; the schema
(`selection.schema.json`) and `schema_version` are untouched by this
feature. The output also never carries a numeric score, percentage,
confidence value, or cross-route ranking, under any field name: it states
which literal keywords are present or absent per group and nothing more,
preserving this repository's deterministic-selection invariant (selection is
a fixed rule match, never agent judgment) end to end. See
`internal/selector/nearmiss.go` (and `internal/selector/nearmiss.go`,
its differential counterpart) for the full mechanism and its tests. Deliberately
not a link: the packaged plugin ships neither implementation, only the data the
binary reads, so a relative link here resolves in a checkout and dangles in the
package.
