---
name: agent-authoring
description: Create or update this repository's agent definitions, catalog entries, routing rules, knowledge focus, workflows, runbook examples, and selector tests. Use when adding a specialist agent, changing agent authority, or keeping orchestration dispatch behavior consistent.
canonicalSource: skills/agent-authoring/SKILL.md
---

> Cline packaging note: this skill's instructions describe this repository's own `roster/`-layout tooling in the abstract (the role catalog, routing configuration, and selector this plugin bundles) -- they are not literal paths to look up in an arbitrary target project. When dispatching, use `start_subagent`/`dispatch_selected_roles`/`bin/cadre select` rather than reading these files directly.


# Agent Authoring

Use this skill when an agent change must be publishable and selectable, not just a loose Markdown file.

## Required changes

For each new or changed agent:

1. Add or update its own role-definition file with role, inputs, outputs, required checks, authority, escalation, and completion criteria.
2. Add its id to the bundled role catalog's ordering file (dispatch-precedence order), if not already present.
3. Metadata (`phase`, `capability`, `model`, `codex_model`, `reasoning_effort`, `knowledge_focus`) lives in the `---`-delimited frontmatter at the top of the role's `AGENT.md` (see "Frontmatter-based roles" below): update the frontmatter, then run `cadre generate-role-metadata` to regenerate this repository's bundled role catalog and routing.json's `knowledge_focus` block. Do not hand-edit those generated regions -- this repository's bundled role catalog and this repository's bundled routing configuration's `knowledge_focus` block are fully generated output, never an input. Note: this repository's bundled role catalog's regenerated key order always exactly tracks the bundled role catalog's ordering file, but `routing.json`'s `knowledge_focus` block does not -- it never reorders an already-present role and always appends a newly-added role's entry at the very end, so don't expect a new role's `knowledge_focus` entry to land near related roles there.
4. Update or add workflow/runbook examples when the role changes dispatch behavior.
5. Add selector tests in `internal/selector's tests` for at least one representative path or keyword.
6. Regenerate the packaged plugin in-tree with `cadre generate-plugin --output plugin`, then port the Cline presets with `python3 plugin/tools/port_cline_agents.py --root cline-plugins --source plugin`, and commit both. The generated half of `plugin/` is committed here since the monorepo merge (`deagy/cadre-lifecycle` is archived); `validate.yml`'s `generated-content` job re-runs the same command with `--check`, so source and output only advance together in one commit.
7. Run the orchestration test suite and confirm catalog definition paths exist. Adding a role also means bumping `EXPECTED_ROLE_COUNT` in the bundled repository-health test suite, the two count assertions in the bundled Cline port's own test suite, the metadata-file count in `internal/generators/frontmatter_test.go`, `SOURCE_ROLE_COUNT`/`SOURCE_SKILL_COUNT` in `cline-plugins/cline-agents/test/presets.test.mts` (a TypeScript test, so the Python suites run green without it -- only CI's `cline-plugins` job catches it), and the hand-maintained "N roles" prose in `README.md`, `CLAUDE.md`, this repository's runbook, `docs/role-index.md`, `docs/capability-index.md`, `.claude-plugin/marketplace.json`, and `.agents/skills/role-discovery/SKILL.md`. Only some of those are test-enforced; the rest go stale silently.
8. Add a fixture to the bundled selector's golden-corpus fixtures for each new route. `test_corpus_covers_every_required_route_category` derives its required set from `routing.json` itself, so a route without a fixture fails. Every catalog role must also be referenced by some route, risk rule, or team recipe — `internal/orchestration/routing_health_test.go` reports an unreferenced role as an orphan.

### Frontmatter-based roles

Every role's `AGENT.md` starts with a `---`-delimited frontmatter block declaring `id`, `phase`, `capability`, `model`, `codex_model`, `reasoning_effort`, and `knowledge_focus` as flat scalar fields (see `internal/generators/frontmatter.go`). `definition` is never stored in frontmatter -- it is always derived from the file's own path under `roster/`. A role's metadata comes entirely from its frontmatter; there is no fallback to a legacy `catalog.yaml`/`routing.json` entry, so a missing required field fails the generator closed rather than silently inheriting a stale value. An `AGENT.md` that does not carry frontmatter is a generator error, not a supported transitional state. Regenerate with `cadre generate-role-metadata` (`internal/generators/catalog_generation.go`) after editing frontmatter, and validate with `cadre generate-role-metadata --check`.

### There is no docs-only change under `docs/`

`cadre generate-plugin` copies documentation into the packaged plugin, so
editing a Markdown file that looks like pure prose still makes the generated
tree stale and fails `validate.yml`'s `generated-content` job. What gets
packaged, from `internal/generators/plugin_generation.go`'s `documentation_paths`:

- everything under `docs/` recursively, **except `docs/kernel/`** (that documents the lifecycle kernel and the LangGraph engine, whose sibling directories the package does not ship -- copying it in would produce dangling links)
- `AGENTS.md`, `CONTRIBUTING.md`, and `IDENTITY.md`

`roster/` and `.agents/skills/` are copied only when **git-tracked**, from
`git ls-files`. A new file that has not been `git add`ed is skipped silently,
so a packaged file edited to reference it ships pointing at something absent --
stage new files before regenerating. The documentation paths above
(`AGENTS.md`, `CONTRIBUTING.md`, `IDENTITY.md`, `docs/`) and the `provider/`
bundle are selected by a filesystem walk instead, so an untracked one is copied
*in*; there the risk is committing the packaged copy while leaving the source
untracked. Staging first makes both branches behave the same. See
this repository's runbook §17, "Regenerating derived output", for the full sequence.

The same applies to `.agents/skills/`: adding or editing a skill is a
generated-content change, and a new skill also needs its
`agents/openai.yaml` and its `.claude/skills/<name>/SKILL.md` pointer, whose
`name`/`description` are compared byte-for-byte against the canonical file.

## Guardrails

- Do not let an implementation agent approve its own work.
- Keep human-only decisions behind explicit gates.
- Keep role authority narrow and environment-specific.
- Prefer adding a focused specialist only when existing agents would blur accountability or miss recurring work.
