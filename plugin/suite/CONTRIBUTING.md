<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

# Contributing

This repository is hosted on GitHub. Contributions are reviewed through GitHub
pull requests and validated by the repository's GitHub Actions checks. The
Secure Cloud target profile may describe GitLab-based customer environments;
that does not change this repository's contribution workflow.

## Before changing files

Read:

1. [AGENTS.md](AGENTS.md) for repository rules.
2. [IDENTITY.md](IDENTITY.md) for the suite's informational orientation.
3. [roster/README.md](roster/README.md) for the source layout.
4. [roster/RUNBOOK.md](roster/RUNBOOK.md) for orchestration and handoffs.

Keep role definitions and `roster/catalog.yaml` synchronized. Preserve the
separation between authors, independent reviewers, human approvers, evidence
curators, and release operators.

Every role's metadata (`phase`, `capability`, `model`, `codex_model`,
`reasoning_effort`, `knowledge_focus`) lives in its own `AGENT.md`'s
`---`-delimited frontmatter (see `internal/generators/frontmatter.go`
and the `agent-authoring` skill). `roster/catalog.yaml` and
`roster/orchestration/routing.json`'s `knowledge_focus` block are purely
generated output derived from that frontmatter -- never hand-edit them.
Edit a role's frontmatter, then regenerate `roster/catalog.yaml` and
`roster/orchestration/routing.json` with `cadre generate-role-metadata`
(`... --check` to validate without writing).

`generate-role-metadata` is the first of several regeneration steps, not the
whole procedure: the packaged plugin (`cadre generate-plugin --output plugin`)
and the Cline mirror (`python3 plugin/tools/port_cline_agents.py --root
cline-plugins --source plugin`) each have their own CI guard, and stopping
after the first step is the usual way to leave a pull request red. `git add`
new files before regenerating — untracked files are silently skipped. See
`roster/RUNBOOK.md` §17, "Regenerating derived output", for the full sequence,
the required ordering, and what each guard catches.

This repository's own catalog/plugin feature and roadmap work is tracked
through GitHub Issues/PRs for discussion and triage; this repository does not
run its own `.agentic-sdlc/` overlay.

## Typical change flow

```text
understand scope -> make a focused change -> run relevant checks
-> regenerate packaged artifacts when required -> inspect the diff
-> open a GitHub pull request -> obtain independent review -> merge
```

Do not commit or push any of the prohibited content listed in
[AGENTS.md's Commit & Merge Request
Guidelines](AGENTS.md#commit--merge-request-guidelines). Do not make
persistent-environment or production changes as part of repository
validation.

## Documentation changes

For documentation-only work:

- use approved implementation, policy, and runbook sources;
- identify the intended audience and prerequisites;
- preserve security warnings and authority boundaries;
- link to the canonical source rather than duplicating long procedures;
- avoid documenting proposals as current behavior;
- check local Markdown links and command names before opening the PR.

Edit canonical role and policy documentation under `roster/`. Files under
`provider/` may be generated artifacts. When a source change
requires regeneration, follow the repository command in `AGENTS.md`, inspect
the generated diff, and include the reason in the pull request.

## Pull request checklist

- [ ] The scope and affected decisions are described.
- [ ] Security, authority, lifecycle, and generated-artifact implications are
      called out.
- [ ] Relevant tests or documentation checks were run.
- [ ] Role definitions and catalog entries remain synchronized when applicable.
- [ ] Independent review is assigned for implementation or policy changes.
- [ ] No secrets or sensitive source material are included.
- [ ] Human-only approvals remain explicit and are not inferred from agent work.
