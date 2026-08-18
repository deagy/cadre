# Routing doctrine: when a base route may claim a generic path glob

`roster/orchestration/routing.json` ships as the **base ruleset** to every
consuming project. `internal/selector/overlay.go` lets a consumer *widen* a base route
(add paths, keywords, team members) but never narrow one — see
`roster/RUNBOOK.md`'s "Customize routing.json with a project-local overlay"
section for the full per-construct merge rule. That non-narrowing property is
real and matters, but it is not, by itself, the test for whether a route may
claim a generic filename glob (`**/go.mod`, `**/pyproject.toml`, and similar).
[#201](https://github.com/deagy/cadre/issues/201) corrected an earlier,
incomplete restatement of this rule — see that issue and the
`[Unreleased]` `CHANGELOG.md` entry it produced for the full history.

## The rule as it was stated, and why it was wrong

Earlier `routing.json` history (`#195`, `#196`, `#197`) rejected claiming root
`pyproject.toml` on the reasoning that "a generic file present in arbitrary
downstream projects is unclaimable in the base ruleset because
`internal/selector/overlay.go` can widen but never narrow." Read literally, that rule
disqualifies most of the ruleset as it exists today: roughly a dozen routes
already claim generic `**` globs base-wide, including `supply-chain`
(`**/go.mod`, `**/package.json`, `**/*.lock`, `**/Dockerfile`,
`**/charts/Chart.yaml`), `frontend` (`**/*.ts`, `**/*.tsx`, `**/*.css`,
`**/*.scss`), `backend` (`**/*.go`, `**/*.sql`, `**/migrations/**`),
`infrastructure` (`**/*.tf`, `**/compose.yaml`), `pipeline`
(`.github/workflows/**`, `**/.gitlab-ci.yml`), `testing` (`**/*_test.go`,
`**/*.spec.ts`), `black-box-testing` (`**/e2e/**`, `**/playwright/**`),
`cost-capacity` (`**/values*.yaml`), `api-contract` (`**/openapi.yaml`,
`**/*.proto`, `**/schema.graphql`), `documentation` (`**/*.md`),
`secrets-identity` (`**/serviceaccount*.yaml`), `database-reliability`
(`**/migrations/**`, `**/*.sql`), `architecture-design` (`**/architecture/**`,
scoped by `exclude_paths: ["roster/**"]` — see `CHANGELOG.md`'s `#162` entry),
plus `visual-system` and `ai-feature`. Every
one of these is exactly the kind of file the `pyproject.toml` rejection
argument describes — generic, present in arbitrary downstream projects, and
non-narrowable via overlay. The rule was never actually applied consistently;
it was only invoked when someone tried to *add* a generic claim, never
audited against what had already shipped.

Genericness of the filename alone was never the real, operative test.

## The actual two-part test

A base route may claim a generic path glob (one likely to exist, unrelated to
Cadre, in an arbitrary consuming project) only when **both** parts hold:

**(i) Domain-generality of the route's own design intent, not of the roles'
abstract skill.** This condition is about what the *route itself* was built
to cover — its full path set, its keyword list, and the concern it exists to
catch — not about whether the staffed roles are individually competent
generalists. A generically-skilled role (e.g. `application-engineer`) can
review almost anything competently in isolation; that is not sufficient. The
question is whether the route as a whole reflects a domain-general concern
that applies the same way regardless of which repository the matched file
lives in, or whether it was intentionally scoped and keyworded around *this*
repository's own tooling, such that a match in an unrelated consumer project
is a scoping miss rather than a legitimate hit.

- `supply-chain` exists to catch supply-chain risk (compromised dependency,
  license drift, mutable base-image digest) in a dependency manifest. That
  concern, and the route's keywords and paths, are the same regardless of
  whose `go.mod` or `package.json` it is — nothing about the route's design
  references this repository specifically. This condition holds.
- `packaging` was purpose-built around this repository's own
  plugin-distribution tooling: its keywords are `plugin version bump`,
  `plugin changelog entry`, `plugin install script`, `kernel shim`,
  `port cline agents` (`CHANGELOG.md`'s `#189` entry), and its paths are
  `plugin/tools/**` plus one Cadre-specific `supply-chain` addition. The
  route's own scoping — not the general competence of the roles it
  staffs — targets this repository, so a stranger's `pyproject.toml` match
  would be a scoping miss: the route firing on a file its own design was
  never built to reach. This condition fails.

**(ii) False-positive vs. false-negative cost asymmetry.** Compare what
happens when the route fires on a file it shouldn't (false positive) against
what happens when it fails to fire on a file it should have caught (false
negative).

- For `supply-chain`, a false positive is a cheap extra reviewer look at a
  trivial version bump. A false negative is exactly the harm the route
  exists to catch. Default-on is the correct bias.
- For `packaging`, a false positive produces actively wrong review output —
  a Cadre-packaging-specialized team confidently reviewing a target project
  it knows nothing about — which is a worse failure mode than no automatic
  routing at all (falling through to `needs-triage`, where a human or a
  differently-scoped route picks it up).

Both conditions must hold for a base route to claim a generic glob.
`supply-chain`'s dependency-manifest globs satisfy both and are the
**intentional counter-example** that proves genericness alone was never
disqualifying. `packaging` fails condition (i) and is the case this test
correctly excludes from claiming generic globs like root `pyproject.toml` or
`packaging/**`.

**A third worked example: `architecture-design`'s `**/architecture/**`.** This
route claims a generic directory-name glob base-wide, and both conditions
hold the same way `supply-chain`'s do — reviewing architecture documentation
for design-quality/completeness concerns is the same judgment regardless of
whose repository it lives in, and a false positive (an extra architecture
review) is cheap next to the false negative of missed design review. It also
shows a third shape this test's outcome can take: rather than staying
unclaimed (the `pyproject.toml` disposition) or being claimed unconditionally
(`supply-chain`), `**/architecture/**` is claimed *and* carved back with
`exclude_paths: ["roster/**"]`, because this repository's own
`roster/architecture/` role definitions would otherwise false-positive on a
Cadre-specific path even though the route's underlying concern is genuinely
domain-general (`CHANGELOG.md`'s `#162` entry has the full before/after).
`exclude_paths` is therefore a live tool for narrowing a route's own
false-positive surface without failing either prong — distinct from the
narrowing this document restricts below, which is about relinquishing
security-relevant coverage, not carving out a repository's own paths from an
otherwise-valid domain-general route.

## What this test does not authorize

The two-part test above is **one-directional**. It governs whether a base
route may *newly claim* a generic glob it does not already have. It is not a
license to *relinquish* one a route already claims, and re-running the two
prongs is, by itself, explicitly insufficient justification for narrowing or
dropping an existing generic glob.

The reason this needs to be said explicitly: prong (ii) is a cost-asymmetry
judgment, and judgments can be re-argued. Nothing about the test's wording
stops a future PR from re-running both prongs against, say, `**/*.lock` or
`**/Dockerfile` under `supply-chain`, or `**/serviceaccount*.yaml` under
`secrets-identity`, and concluding the noise isn't worth it anymore — while
missing that the asymmetry those routes were built on (a false negative is
the exact harm the route exists to catch) does not become less true just
because someone re-litigates it. A test built to justify claiming new
coverage is not automatically safe to run in reverse against coverage that
already exists and that other work may already depend on.

A change that narrows or removes an existing `paths` glob (via `exclude_paths`
or outright removal — not `exclude_paths` used the `architecture-design` way,
to carve a route's own repository out of its own domain-general concern; see
above) on any route whose current matches route to `security-reviewer`,
`supply-chain-security-reviewer`, `secrets-identity-engineer`, or
`compliance-reviewer` must clear a higher bar than this document's two-part
test: explicit sign-off from the relevant reviewer role named above, plus
**evidence, not assertion,** that the false-negative risk has genuinely
changed (e.g. measured false-positive rate, a documented replacement control,
or a scoping error this document itself would recognize as a mistake).
Re-deriving the same cost-asymmetry conclusion from the armchair is not that
evidence.

## The overlay runs in the selection path

`internal/selector` resolves
`.agents/orchestration/routing-overlay.json` through
`routing_overlay.resolve_effective_routing()` before building a plan, so the
configuration the selector dispatches against is the effective (merged) one,
discovered by walking up from the repository under selection. With no
overlay present the base configuration is used unchanged.

"the consumer can widen but not narrow" therefore describes live dispatch,
not just a static file merge rule. The widen-only semantics are enforced at
selection time: an overlay that narrows a base entry now fails the
`cadre select` run outright rather than being silently ignored.

Two consequences worth stating plainly:

- **A consumer's overlay changes their reviewer routing.** Getting each base
  route's own default right still matters — an overlay may only widen, so a
  base route that over-claims cannot be narrowed away by a consumer — but
  the mechanism a consumer is pointed at is no longer inert.
- **An applied overlay is recorded in the plan.** `provenance` carries
  `overlay_applied`, `overlay_path`, and `overlay_content_hash`, and
  `routing_content_hash` continues to name the *base* file, so an auditor
  has both halves needed to reproduce the merge. Those fields are absent
  when no overlay was discovered.

This closes [#202](https://github.com/deagy/cadre/issues/202), which
recorded the disconnection between the documented mechanism and the live
selection path.

## Where this leaves `pyproject.toml`

The Python dependency-manifest gap tracked by `#189`/`#195`/`#196`/`#197`
remains open, but on a corrected basis. It was never blocked by genericness
of the filename — that was never the real test, per the correction above.

**Stated plainly, this is a present-tense coverage gap, not a hypothetical
one.** Today, a change to a Python dependency manifest (root `pyproject.toml`,
`kernel/pyproject.toml`, `engine/pyproject.toml`) gets zero default
`supply-chain-security-reviewer`/`security-reviewer` routing — it falls
through to `needs-triage` unless task wording happens to hit an unrelated
route's keywords. An otherwise-identical change to a `go.mod`, `go.sum`,
`package.json`, `package-lock.json`, or `Dockerfile` gets both reviewers
automatically, every time. Leaving this gap open is therefore an accepted,
informed risk — not a passive default — and it remains open because:

- The two-part test has not yet been formally applied and decided for
  `**/pyproject.toml` under `supply-chain` in a reviewed change.
- The selector still cannot express a path-and-intent or
  repository-identity-aware predicate, which was the *other* half of what
  `#196` originally gestured at as a possible resolution path.

A future PR adding `**/pyproject.toml` (and the two nested
`kernel/pyproject.toml` / `engine/pyproject.toml` manifests) to
`supply-chain` would be **consistent with the two-part test above** —
`supply-chain-security-reviewer` and `security-reviewer` reviewing a Python
dependency-pin change is exactly the same kind of domain-general,
default-on-favorable judgment as reviewing a `go.mod` or `package.json`
change. This document does not forbid that PR. It is a scope decision
pending explicit review and a stated basis at the time it's proposed, not a
standing objection.

See also `CHANGELOG.md`'s `[Unreleased]` entry for the corrected record of
this decision, and `internal/selector's tests`'s
`test_generic_pyproject_manifests_remain_unclaimed_by_path` and
`test_root_pyproject_toml_alone_does_not_route_to_packaging` for the pinned
regression coverage this document explains.
