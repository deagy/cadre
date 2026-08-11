# Follow-up experiment protocol: condensed briefs and the quantization confound

**Status:** designed, not run. No results exist yet.
**Motivates:** `fidelity-baseline.md` §5 (condensed-brief variant untested) and
§6 (weight quantization changed alongside model family and was never isolated).
**Instrument:** `cadre role-fidelity`, current probe file.

This protocol is written *before* the run and fixes what each outcome means,
because the two questions it settles both have a comfortable answer and an
inconvenient one. The record it extends already had to correct a confidently
wrong printed summary twice; pre-registering the interpretation is the cheapest
defence against doing that a third time in the other direction.

Whoever runs this owns a local endpoint. Nothing here can be run from this
repository's CI, and no result in it may stand in for a human review, a gate
approval, or a risk acceptance.

## 1. The two questions

**Q1 — does a shorter brief govern a weakly-steerable model better?**
`fidelity-baseline.md` §5 originally concluded no condensed variant was
justified. It has been corrected to *untested*, because no shortened brief was
ever put in front of the failing model. The transcripts argue both ways: `Sage`
obeys short, front-loaded, safety-adjacent instructions crisply (5–39 word
refusals) and ignored the handoff rule buried in ~15k tokens of shared policy.
That is equally consistent with "this model is not steerable by a brief" and
with "this model does not attend to instructions this deep in a prompt", and
those two have opposite fixes.

**Q2 — was the family comparison confounded by weight precision?**
`Scout` ran Q8_K_XL weights, `Sage` ran Q4_K_M. KV-cache quantization was held
constant at `q8_0`/`q8_0` and the record explains why it bothered; weight
precision was never named as a variable. "Llama-3.3-70B fails role-scope
discipline" and "4-bit weight quantization degrades steerability" are equally
consistent with what was run.

## 2. Arms

Four arms. A and B come from a single `--compare-condensed` invocation, which
is the point of that flag: both variants, identical probes, one run.

| Arm | Model | Weights | Brief | Answers |
|---|---|---|---|---|
| A | Llama-3.3-70B-Instruct | Q4_K_M | full | baseline, re-established under the verdict probes |
| B | Llama-3.3-70B-Instruct | Q4_K_M | condensed | Q1 |
| C | Llama-family | **Q8** | full + condensed | Q2 |
| D | Qwen3.6-27B-MTP | Q8_K_XL | full + condensed | comparison point, re-established |

**A and D are not optional.** The verdict-token conversion changed the
instrument, so the committed 36/45 and 45/45 figures cannot be re-scored — 27
of 45 stored results now report "no verdict found" rather than a verdict that
disagrees. There is no baseline to compare against until A and D are re-run.

For arm C, prefer the same Llama-3.3-70B-Instruct at Q8 if it fits the
hardware. If it does not, a smaller Llama-family model at Q8 still answers Q2,
because Q2 is about precision-versus-family, not size — but record which was
used and treat "70B at Q8" as the stronger test.

## 3. Held constant across every arm

| Setting | Value |
|---|---|
| Context window | 32,768 |
| KV cache | `q8_0` / `q8_0` |
| Temperature | 0 |
| `parallel` | 1 |
| Roles | the same 9 as the original run (below) |
| Probes | all 5, current probe file |
| Repeats | **n ≥ 3 per role/probe** |

Roles, three per tier, unchanged from the original so the arms stay comparable:
`cloud-architect`, `threat-modeler`, `cryptographic-assurance-engineer` (high);
`code-reviewer`, `backend-engineer`, `test-engineer` (mid);
`knowledge-store-steward`, `support-triage-agent`, `evidence-curator` (low).

The n ≥ 3 is not ceremony. §6 of the record concedes 43 of 45 original results
were single samples on hardware where replies are not reproducible verbatim
even at temperature 0 (speculative decoding plus flash-attention). A 0/9 built
from single samples cannot distinguish "always fails" from "usually fails", and
Q1 turns on exactly that difference.

## 4. Commands

`--role` is **repeatable, not comma-separated** — one flag per role. A comma
list is read as a single role name and fails with "no preset found".

```sh
# The nine roles, as shell arguments reused across every arm.
ROLES="--role cloud-architect --role threat-modeler \
--role cryptographic-assurance-engineer --role code-reviewer \
--role backend-engineer --role test-engineer \
--role knowledge-store-steward --role support-triage-agent \
--role evidence-curator"

# Arms A + B in one run: full and condensed, identical probes.
cadre role-fidelity --mode probe --compare-condensed $ROLES \
  --base-url http://<endpoint>/v1 --model "<llama-q4-model-string>" \
  --output sage-q4-full-vs-condensed-run1.json

# Arm C: same command, Q8 Llama endpoint/model string.
# Arm D: same command, Qwen endpoint/model string.
# Repeat each >=3 times, changing only the run number in --output.
```

Check `--dry-run` first on any new endpoint: it composes and reports the run
without sending anything, and flags degenerate probe/role pairs. Verified
against three roles, which reports `15 full-brief probe/role pair(s) and 15
condensed pair(s)` — 3 roles x 5 probes per arm. The nine-role run is 45 and
45, and at n >= 3 that is 270 generations per arm, so budget the wall clock:
single probes exceeded a 300s timeout on the 35B preset in the original run.

## 5. What each outcome means — fixed in advance

**Q1, read off `stays-in-remit` for arm B against arm A:**

- **B materially better than A** — a shorter brief governs this model where the
  full one does not. The control belongs in the *payload*, and a condensed
  variant for weakly-steerable models is warranted. This also weakens the case
  for any operator-side model gate, because the defect would be ours.
- **B ≈ A, both failing** — brief length is not the mechanism. §5's original
  conclusion is then supported by evidence rather than by absence of a test,
  and the condensed-variant question can be closed.
- **B worse than A** — the shared-policy block is doing real work that the
  role-specific text alone does not. Worth knowing; it argues against any
  future "trim the briefs" proposal.

**Q2, read off arms C and D:**

- **C behaves like D (holds)** — the original comparison was confounded. The
  operative variable is weight precision, not model family, and §5's "model
  choice matters more than size" must be restated as a claim about
  *deployment configuration*, of which the model name is only one part.
- **C behaves like A (fails)** — the family effect survives at Q8 and the
  confound, while real, was not load-bearing. Claim 1 stands as written.
- **C between the two** — both variables contribute. Report the split; do not
  round it to whichever story is tidier.

**A prior worth stating:** `Sage` passed `instruction-retention-under-load` 9/9
in the original run, so it can follow a short format instruction. If its
verdict results nonetheless come back `malformed` in bulk, that is a finding
about the verdict format itself, not about policy adherence — and the harness
will mark those results unreliable and exclude them from the pass rate rather
than counting them as refusals to comply.

## 6. Recording the result

Write each arm to its own `--output` JSON and commit them alongside a new
record in `roster/orchestration/runs/<task-id>/`. Do not overwrite this
directory: it is the pre-verdict baseline and its transcripts remain the
evidence for the `stays-in-remit` finding, which was established by reading
them rather than by any score.

State in the new record, at minimum: the exact model strings, **weight and KV
quantization for every arm**, context window, the harness commit, and the
number of repeats actually completed per role/probe. The confound this
experiment exists to break was introduced by omitting one of those fields.

## 7. Threats to validity, known in advance

- **Single-turn, no tool use.** Real dispatch is multi-turn with tool calls and
  possibly retrieved knowledge, all consuming the same window. Unchanged from
  the original run and still not exercised.
- **The condensed variant is a mechanical split**, not a rewritten brief. It
  keeps role-specific content and drops the embedded shared-policy block at the
  marker `PresetComposition` already uses. A brief *authored* to be short might
  do better than one *truncated* to be short; this experiment does not test
  that, and a null result on B does not rule it out.
- **9 of 159 roles**, chosen to span tiers, not randomly sampled.
- **Scores direct attention; transcripts are the finding.** Every prior
  correction to this record came from reading replies, not from a number.
  Read a sample from every arm before writing a conclusion.
