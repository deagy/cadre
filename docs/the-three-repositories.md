# cadre, the kernel, and recall

Three repositories, and nothing in any of them said how they fit together
until this page existed. This is that.

## What each one is for

**[cadre](https://github.com/deagy/cadre)** — the agent suite, and the only one
you install directly. It holds the role definitions, decides which specialists
a task needs, produces a reviewable dispatch plan, and dispatches work through
whichever runner you use (Claude Code, Codex, Cline). If you are adopting one
thing, it is this.

**[cadre-kernel](https://github.com/deagy/cadre-kernel)** — the lifecycle
kernel, shipped as the `agentic-sdlc` binary. It owns the G1–G10 gate
contracts and the schemas for run records, agent catalogs, profiles and
providers. **It records and validates; it does not drive.** It answers what a
gate requires and whether a run record is valid; deciding to move a task
through a gate is the caller's job. Optional — most projects never need it.

**[recall](https://github.com/deagy/recall)** — a Go library and CLI for
retrieval-augmented generation: chunking, embeddings, hybrid vector and BM25
search, HNSW indexing, SQLite persistence. cadre uses it as the storage engine
behind `cadre knowledge`. Useful on its own, and the only one of the three
with no opinion about agents at all.

## How they actually connect

Both connections run one way, out of cadre, and they are different kinds of thing.

```
   you ──install──▶ cadre
                      │
                      ├── imports github.com/deagy/recall   (a Go dependency)
                      │     └── storage behind `cadre knowledge`
                      │
                      └── shells out to `agentic-sdlc`      (a separate binary)
                            └── asks: does this gate pass?
```

**cadre → recall is a module dependency.** `go.mod` pins it, and
`internal/retrieval` builds on `recall/govern`. You do not install recall
separately to use `cadre knowledge`; it is compiled in.

**cadre → the kernel is a subprocess.** cadre finds `agentic-sdlc` through
`AGENTIC_SDLC_BIN`, on `PATH`, or in the shim the lifecycle plugin packages,
and shells out to it — `internal/cli/sdlc.go` and
`internal/orchestration/kernel_probe.go` are where.

**That boundary is enforced by construction rather than by a test.** The kernel
is a separate Go module and is not in cadre's `go.mod`, so cadre cannot import
it even by accident; the only thing it can do is execute a binary. Roster asks;
the kernel answers. Before the two repositories were split apart this was a
convention held by a guard, and it is now a property of the build.

**Nothing flows the other way.** The kernel does not know cadre exists beyond
accepting a provider bundle from it. recall knows nothing about either.

## Adopting them, in order

1. **Install cadre.** `curl -fsSL https://raw.githubusercontent.com/deagy/cadre/main/install.sh | sh`, then authenticate your runner. Read [Installing Cadre](INSTALL.md) first — it lists the prerequisites a bare machine does not have, and the authentication step, both of which stop people.
2. **Do one task with it.** `cadre select` produces a dispatch plan. Nothing below is needed to get value from this.
3. **Add lifecycle governance, if you want gates.** `./install.sh --with-lifecycle` installs the kernel alongside. Skip unless G1–G10 gate tracking is something you actually want; it is not the default and most projects do not need it.
4. **Add a knowledge store, if you want retrieval.** `cadre knowledge` works once configured; recall is already compiled in. Installing recall's own CLI is separate and only needed if you want to use it directly.

## The two words that mean different things in different places

Worth stating plainly, because both collide across these repositories and
nothing warns you.

**provider.** In cadre and the kernel, a *provider* is a bundle supplying
roles, profiles and extensions to the kernel — cadre is one. In recall, a
*provider* is an embedding backend (OpenAI, Cohere, Ollama, a local ONNX
model). In gloop, it is an LLM backend. Three unrelated concepts, one word.

**kernel.** It names the repository `cadre-kernel`; a subdirectory *inside*
that repository holding only the contract schemas and a README, while the
implementation sits beside it; and — until `11eefd47` deleted it — a directory
inside cadre. Every document still claiming that third one has been corrected,
and a test now fails if another appears.

## Glossary

| Term | Means |
|---|---|
| **roster** | A directory of role definitions (`AGENT.md`), an agent catalog (`catalog.yaml`) and a routing table. cadre owns and produces rosters; `roster/` is where they live. |
| **catalog** | `roster/catalog.yaml`, the machine-readable inventory of role IDs and their metadata. |
| **routing** | The table mapping a task's shape — matched paths, keywords, risk rules — to the roles that should handle it. `roster/orchestration/routing.json`. |
| **dispatch plan** | A reviewable `cadre select` output: the matched routes and risks, the primary, reviewer and support roles, the workflow, and the required and human gates. It is data, not an instruction — you read it before anything runs. |
| **gate** | A lifecycle checkpoint requiring defined criteria and evidence before a task progresses. Ten of them, G1 to G10. |
| **G1–G10** | Intent, Requirements Baseline, Architecture, Governance and Data, Security and Crypto, Verification and Test, Evidence, Release Readiness, Deployment Authorization (human-only), Runtime Conformance — the contract's own names, read from `agentic-sdlc show-contract lifecycle-gates`, which is normative if this table ever drifts. |
| **provider** | See above — the word is overloaded. In cadre and the kernel: a bundle of roles, profiles and extensions. |
| **overlay** | The `.agentic-sdlc/` directory the kernel writes into *your* project, holding its authorities, profile and run records. Your project owns it; installing or upgrading the tooling never takes it over. |
| **kernel** | The lifecycle kernel: the `agentic-sdlc` binary and the repository that produces it. Overloaded — see above. |
| **human gate** | A gate a person must clear. G9 Deployment Authorization is always one. Agents may prepare evidence for it and may not clear it. |

## What none of them do

No amount of this dispatches work you did not ask for, approves its own gates,
or decides anything a human is supposed to decide. cadre prepares scoped
changes and evidence; production, persistent infrastructure, destructive
actions, policy exceptions, privileged access and risk acceptance all require
a human. That is the point of the gates, and it is why the kernel refuses to
drive.
