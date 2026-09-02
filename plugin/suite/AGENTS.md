<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

# Repository Guidelines

## Project Structure & Module Organization

Agent roles, policies, workflows, orchestration, testing, support/escalation, and the knowledge store live under `roster/`; publishable repository skills live under `.agents/skills/`, with thin per-skill pointer files under `.claude/skills/` (Claude Code). A single general pointer file under `.clinerules/` (Cline CLI) references `AGENTS.md`/`roster/RUNBOOK.md` directly — it is unrelated to the per-skill pointer mechanism. The installable plugin distributions live in this same repository, under `plugin/`: the packaged Claude Code / Codex plugin generated from `roster/catalog.yaml`, `roster/`, `.agents/skills/` and this repository's `provider/` bundle, plus the three lifecycle plugin distributions (`cadre-lifecycle-core`/`-github`/`-gitlab`), plus a hand-authored TypeScript Cline CLI plugin under `cline-plugins/cline/`. The portable lifecycle kernel itself lives in this repository too, under `kernel/` (with the LangGraph orchestration engine that drives it under `engine/`).

Read `roster/RUNBOOK.md` for orchestration and any project-local `AGENTS.md` before product changes. Keep role definitions and `roster/catalog.yaml` synchronized.

## Build, Test, and Development Commands

Resolve Python 3.10+ as documented in the runbook. From each internal-tool component, run:

```sh
go test ./...                                                    # the CLI, kernel, stores, generators
go test ./internal/generators/                                   # repo-health guards
```

After changing `roster/catalog.yaml`, `roster/`, or `.agents/skills/`, regenerate derived output before committing. `git add` any new files **first** — the generator copies git-tracked files and silently skips untracked ones, so regenerating before staging ships a package referencing a file it does not contain:

```sh
./bin/cadre generate-authority-aides   # only when editing roster/authority/aides.yaml or _template.md.tmpl
./bin/cadre generate-role-metadata     # roster/catalog.yaml, routing's knowledge_focus, generated half of provider/
./bin/cadre generate-plugin --output plugin
./bin/cadre port-cline-agents --root cline-plugins --source plugin
```

The order is load-bearing, `generate-plugin` never touches `cline-plugins/`, and this applies to code and to this file itself — `plugin/suite/` bundles `roster/` and `AGENTS.md`, so a new module under `roster/*/src/` is part of the packaged CLI. Then re-run the regeneration guard, which is **`./bin/cadre generate-plugin --check --output plugin`** — not a Go test. `go test ./internal/generators/` checks that the generator is deterministic against what it just wrote in a temp directory; only `--check` against the committed tree catches a hand-edit to `plugin/`. That distinction has cost a red run: the whole Go suite passed locally while `validate.yml` failed on one word of difference between `.agents/skills/lifecycle-onboarding/SKILL.md` and its generated copy. Also run `go test ./...` if you touched anything the Cline mirror reads, since its byte-for-byte guard is a Go test. The `plugin/tools` suite is **not** a regeneration guard: what is left there covers the workspace-mutation hook, its parity with the TypeScript Cline implementation, and the kernel bootstrap. Lifecycle integration tests run against the kernel resolved from `AGENTIC_SDLC_BIN`, `PATH`, or the packaged shim — there is no in-tree `kernel/`, which was deleted at `11eefd47`.

**`roster/RUNBOOK.md` §17, "Regenerating derived output", is the canonical version** — it explains why each step exists, why the order matters, what each guard catches, and the `git add` gotcha in both of its directions. Extend it there rather than restating it here.

For Go services, use `gofmt`, `go tool goimports`, `go vet ./...`, `go test ./...`, `go test -race ./...`, and `go tool golangci-lint run ./...`. For React frontends, use the project-pinned package manager for install, test, typecheck, and build commands. Podman, PostgreSQL migrations, Helm, and OpenTofu remain disposable or validation-only unless a project has explicit production approval; follow the component README and never target a persistent environment without approval.

The only Node/TypeScript source in this repository is the Cline CLI plugin under `cline-plugins/`, a separate npm workspace kept out of `plugin/` deliberately (see `README.md`'s repository layout notes).

## Coding Style & Naming Conventions

Use four-space indentation and snake_case for Python. Format Go with `gofmt` and `goimports`; lint with the committed `golangci-lint` config. Keep Go packages lowercase and errors safe for callers. Use two spaces, strict TypeScript, semantic React markup, CSS Modules, and lowercase kebab-case directories. Prefer the Go libraries and tools in `roster/shared/library-standards.yaml`; pin and justify every added dependency.

## Testing Guidelines

Use `unittest` for internal Python tools, Go `testing` plus Testify for services, and Vitest/Testing Library for React. Express integration and regression behavior in Gherkin/Godog. Cover authorization, negative paths, state transitions, accessibility, failure recovery, migrations, and sensitive-data exclusion. Use synthetic fixtures only.

## Commit & Merge Request Guidelines

Use short imperative commit subjects and focused changes. This GitHub-hosted
repository uses GitHub pull requests and GitHub Actions; each pull request must
describe scope, security implications, validation, affected decisions, and
linked issues. The Secure Cloud target profile may use GitLab merge requests
for downstream projects, but that is not this repository's contribution
workflow. Include CLI or UI evidence when behavior changes.

Never commit secrets, raw chat exports, real documents, local environment files, databases, object data, generated credentials, OpenTofu/Terraform state, or rendered secrets. Preserve independent review and human gates for persistent mutations, production, risk acceptance, and release.

## Agentic SDLC boundary

This repository is a provider/plugin distribution — it supplies provider
resources and dispatch inputs to *other* consuming projects, which own their
own `.agentic-sdlc/` overlays, run records, gate approvals, and lifecycle
decisions. This repository does not run its own `.agentic-sdlc/` overlay.

Do not copy lifecycle schemas, run-record validators, gate authorities, or
kernel authority into this repository. Never infer gate approval,
production/destructive authority, risk acceptance, or compliance applicability
for another project. Artifact authors must remain separate from independent
reviewers and human approvers.
