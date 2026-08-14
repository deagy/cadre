package cli

import (
	"fmt"
	"strings"

	"github.com/deagy/cadre/cli/internal/orchestration"
)

func renderStaticFidelity(r orchestration.StaticFidelityReport) string {
	var b strings.Builder
	b.WriteString("Role payload vs context budget\n")
	b.WriteString(strings.Repeat("=", 60) + "\n")
	fmt.Fprintf(&b, "Roles analyzed:        %d\n", r.RoleCount)
	fmt.Fprintf(&b, "Context budget:        %d tokens\n", r.ContextBudgetTokens)
	fmt.Fprintf(&b, "Reserved (task/reply): %d tokens\n", r.ReserveTokens)
	fmt.Fprintf(&b, "Usable for the brief:  %d tokens\n", r.UsableTokens)
	fmt.Fprintf(&b, "Estimate basis:        ~%v chars/token (approximate)\n", r.CharsPerToken)
	b.WriteString("\n")
	fmt.Fprintf(&b, "Largest brief:  %d tokens (%s)\n", r.MaxEstimatedTokens, r.LargestRole)
	fmt.Fprintf(&b, "Median brief:   %d tokens\n", r.MedianEstimatedTokens)
	fmt.Fprintf(&b, "Over budget:    %d of %d\n", r.OverBudgetCount, r.RoleCount)
	b.WriteString("\n")
	b.WriteString("Composition of a median brief:\n")
	fmt.Fprintf(&b, "  role-specific:  %7d tokens\n", r.MedianRoleSpecificTokens)
	fmt.Fprintf(&b, "  shared policy:  %7d tokens (embedded verbatim in every role)\n", r.MedianSharedPolicyTokens)
	share := 0.0
	if r.SharedPolicyShareOfTotal != nil {
		share = *r.SharedPolicyShareOfTotal * 100
	}
	fmt.Fprintf(&b, "  shared policy is %.0f%% of all payload tokens across the catalog\n", share)
	fmt.Fprintf(&b, "  roles whose *role-specific* part alone exceeds the budget: %d of %d\n",
		r.RoleSpecificOverBudgetCount, r.RoleCount)
	b.WriteString("\n")
	fmt.Fprintf(&b, "%-44s %-6s %8s %9s  fits\n", "role", "tier", "~tokens", "% usable")
	b.WriteString(strings.Repeat("-", 78) + "\n")

	limit := 15
	for i, row := range r.Roles {
		if i >= limit {
			fmt.Fprintf(&b, "... %d more (use --json for all)\n", len(r.Roles)-limit)
			break
		}
		pct := "n/a"
		if row.PercentOfUsable != nil {
			pct = fmt.Sprintf("%.1f%%", *row.PercentOfUsable)
		}
		fits := "yes"
		if !row.Fits {
			fits = "NO"
		}
		name := row.Role
		if len(name) > 44 {
			name = name[:44]
		}
		fmt.Fprintf(&b, "%-44s %-6s %8d %9s  %s\n", name, row.Tier, row.EstimatedTokens, pct, fits)
	}
	b.WriteString("\nToken counts are estimates from a chars-per-token divisor, not a real\n")
	b.WriteString("tokenizer. Treat a role near the limit as over it.")
	return b.String()
}

func renderProbeFidelity(r orchestration.FidelityProbeReport) string {
	var b strings.Builder
	if r.DryRun {
		b.WriteString("Fidelity probe -- DRY RUN (nothing sent)\n")
		b.WriteString(strings.Repeat("=", 60) + "\n")
		fmt.Fprintf(&b, "Would run %d probe(s) across %d role(s).\n\n", len(r.Results), r.RoleCount)
		var degenerate []orchestration.FidelityProbeRecord
		for _, rec := range r.Results {
			if len(rec.DegenerateKeywords) > 0 {
				degenerate = append(degenerate, rec)
			}
		}
		if len(degenerate) > 0 {
			fmt.Fprintf(&b, "%d probe/role pair(s) have keywords already in the brief:\n", len(degenerate))
			for i, rec := range degenerate {
				if i >= 20 {
					break
				}
				fmt.Fprintf(&b, "  %s / %s: %s\n", rec.Role, rec.Probe, strings.Join(rec.DegenerateKeywords, ", "))
			}
			b.WriteString("\n")
		}
		if len(r.ZeroMatchProbes) > 0 {
			fmt.Fprintf(&b, "WARNING: %d probe(s) would match zero preset(s) and would never be sent:\n", len(r.ZeroMatchProbes))
			b.WriteString("  " + strings.Join(r.ZeroMatchProbes, ", ") + "\n\n")
		}
		b.WriteString("Re-run without --dry-run to execute.")
		return b.String()
	}

	b.WriteString("Role fidelity probe\n")
	b.WriteString(strings.Repeat("=", 60) + "\n")
	fmt.Fprintf(&b, "Model:      %s\n", r.Model)
	fmt.Fprintf(&b, "Endpoint:   %s\n", r.BaseURL)
	fmt.Fprintf(&b, "Answered:   %d probe run(s) across %d role(s)\n", r.Answered, r.RoleCount)
	fmt.Fprintf(&b, "Passed:     %d\n", r.Passed)
	fmt.Fprintf(&b, "Failed:     %d\n", r.Failed)
	fmt.Fprintf(&b, "Pass rate:  %s  (over scored, reliable probes only)\n", formatFloatPtr(r.PassRate))
	if r.Errored > 0 {
		fmt.Fprintf(&b, "Unanswered: %d (transport error -- NOT a fidelity result)\n", r.Errored)
		fmt.Fprintf(&b, "Coverage:   %s\n", formatFloatPtr(r.Coverage))
	}
	if r.Unreliable > 0 {
		fmt.Fprintf(&b, "Unreliable: %d (verdict-scored, but the same role failed %s in this run -- "+
			"NOT a policy finding; excluded from pass rate)\n", r.Unreliable, orchestration.RetentionProbeID)
	}
	b.WriteString("\nBy tier:\n")
	for _, tier := range sortedKeys(r.ByTier) {
		counts := r.ByTier[tier]
		total := counts["passed"] + counts["failed"]
		rate := 0.0
		if total > 0 {
			rate = 100.0 * float64(counts["passed"]) / float64(total)
		}
		fmt.Fprintf(&b, "  %-6s %4d/%-4d (%.1f%%)\n", tier, counts["passed"], total, rate)
	}

	var failures []orchestration.FidelityProbeRecord
	for _, rec := range r.Results {
		if rec.Passed != nil && !*rec.Passed && (rec.VerdictReliable == nil || *rec.VerdictReliable) {
			failures = append(failures, rec)
		}
	}
	if len(failures) > 0 {
		fmt.Fprintf(&b, "\nFailures (%d):\n%s\n", len(failures), strings.Repeat("-", 60))
		for i, rec := range failures {
			if i >= 20 {
				fmt.Fprintf(&b, "  ... %d more (use --json for all)\n", len(failures)-20)
				break
			}
			fmt.Fprintf(&b, "  %s / %s: %s\n", rec.Role, rec.Probe, strings.Join(rec.Failures, "; "))
		}
	}

	var unreliable []orchestration.FidelityProbeRecord
	for _, rec := range r.Results {
		if rec.VerdictReliable != nil && !*rec.VerdictReliable {
			unreliable = append(unreliable, rec)
		}
	}
	if len(unreliable) > 0 {
		fmt.Fprintf(&b, "\nUnreliable (%d) -- role failed %s in this run, so a verdict-format result "+
			"here says nothing about policy adherence:\n%s\n", len(unreliable), orchestration.RetentionProbeID, strings.Repeat("-", 60))
		for i, rec := range unreliable {
			if i >= 20 {
				fmt.Fprintf(&b, "  ... %d more (use --json for all)\n", len(unreliable)-20)
				break
			}
			verdict := "<none>"
			if rec.Verdict != nil {
				verdict = *rec.Verdict
			}
			fmt.Fprintf(&b, "  %s / %s: verdict=%s (%s)\n", rec.Role, rec.Probe, verdict, rec.VerdictOutcome)
		}
	}

	var errored []orchestration.FidelityProbeRecord
	for _, rec := range r.Results {
		if rec.Errored {
			errored = append(errored, rec)
		}
	}
	if len(errored) > 0 {
		fmt.Fprintf(&b, "\nUnanswered (%d) -- endpoint problems, not fidelity findings:\n%s\n", len(errored), strings.Repeat("-", 60))
		for i, rec := range errored {
			if i >= 20 {
				fmt.Fprintf(&b, "  ... %d more (use --json for all)\n", len(errored)-20)
				break
			}
			fmt.Fprintf(&b, "  %s / %s: %s\n", rec.Role, rec.Probe, rec.Error)
		}
	}

	var degenerate []orchestration.FidelityProbeRecord
	for _, rec := range r.Results {
		if len(rec.DegenerateKeywords) > 0 {
			degenerate = append(degenerate, rec)
		}
	}
	if len(degenerate) > 0 {
		fmt.Fprintf(&b, "\nNOTE: %d probe/role pair(s) assert keywords the brief already\ncontains, "+
			"so a pass there may be copying rather than compliance.\n", len(degenerate))
	}

	if len(r.ZeroMatchProbes) > 0 {
		fmt.Fprintf(&b, "\nWARNING: %d probe(s) matched zero preset(s) in this run and were never sent:\n", len(r.ZeroMatchProbes))
		b.WriteString("  " + strings.Join(r.ZeroMatchProbes, ", ") + "\n")
		b.WriteString("  Check --role/--presets-dir selection and each probe's applies_to/applies_to_tiers.\n")
	}
	b.WriteString("\nA pass means the payload still shapes the reply. It is not a judgement\n")
	b.WriteString("that the role behaved correctly -- read a sample of replies in the JSON\n")
	b.WriteString("report before drawing a conclusion.")
	return b.String()
}

func renderCondensedFidelityComparison(r orchestration.FidelityCondensedComparisonReport) string {
	var b strings.Builder
	if r.DryRun {
		b.WriteString("Fidelity probe -- full vs condensed DRY RUN (nothing sent)\n")
		b.WriteString(strings.Repeat("=", 60) + "\n")
		fmt.Fprintf(&b, "Would run %d full-brief probe/role pair(s) and %d condensed pair(s).\n\n",
			len(r.Full.Results), len(r.Condensed.Results))
		b.WriteString("Re-run without --dry-run to execute.")
		return b.String()
	}

	b.WriteString("Role fidelity: full brief vs condensed brief\n")
	b.WriteString(strings.Repeat("=", 60) + "\n")
	fmt.Fprintf(&b, "Full brief:      %d/%d passed (pass rate %s)\n", r.Full.Passed, r.Full.Scored, formatFloatPtr(r.Full.PassRate))
	fmt.Fprintf(&b, "Condensed brief: %d/%d passed (pass rate %s)\n", r.Condensed.Passed, r.Condensed.Scored, formatFloatPtr(r.Condensed.PassRate))
	b.WriteString("\nBy role (delta = condensed pass rate - full pass rate;\n")
	b.WriteString("negative means the condensed brief lost fidelity the full brief kept):\n")

	pairs := make([]roleDeltaPair, 0, len(r.ByRoleDelta))
	for role, delta := range r.ByRoleDelta {
		pairs = append(pairs, roleDeltaPair{role, delta})
	}
	sortRoleDeltas(pairs)
	limit := 15
	for i, p := range pairs {
		if i >= limit {
			fmt.Fprintf(&b, "  ... %d more (use --json for all)\n", len(pairs)-limit)
			break
		}
		name := p.role
		if len(name) > 40 {
			name = name[:40]
		}
		fmt.Fprintf(&b, "  %-40s full=%s  condensed=%s  delta=%s\n",
			name, formatFloatPtr(p.delta.FullPassRate), formatFloatPtr(p.delta.CondensedPassRate), formatFloatPtr(p.delta.Delta))
	}

	for _, arm := range []struct {
		label  string
		report orchestration.FidelityProbeReport
	}{{"full", r.Full}, {"condensed", r.Condensed}} {
		if len(arm.report.ZeroMatchProbes) > 0 {
			fmt.Fprintf(&b, "\nWARNING (%s arm): %d probe(s) matched zero preset(s): %s\n",
				arm.label, len(arm.report.ZeroMatchProbes), strings.Join(arm.report.ZeroMatchProbes, ", "))
		}
	}

	b.WriteString("\nThis is still a screening instrument, run on both arms of the same\n")
	b.WriteString("probes -- not a verdict on which brief shape governs the model better.\n")
	b.WriteString("Read a sample of both arms' transcripts in the JSON report before\n")
	b.WriteString("drawing that conclusion.")
	return b.String()
}

type roleDeltaPair struct {
	role  string
	delta orchestration.FidelityRoleDelta
}

func sortRoleDeltas(pairs []roleDeltaPair) {
	// Ascending by delta (nil treated as 0.0), matching Python's sort key.
	for i := 1; i < len(pairs); i++ {
		for j := i; j > 0; j-- {
			a := valueOrZero(pairs[j-1].delta.Delta)
			b := valueOrZero(pairs[j].delta.Delta)
			if a <= b {
				break
			}
			pairs[j-1], pairs[j] = pairs[j], pairs[j-1]
		}
	}
}

func valueOrZero(v *float64) float64 {
	if v == nil {
		return 0.0
	}
	return *v
}

func formatFloatPtr(v *float64) string {
	if v == nil {
		return "None"
	}
	return fmt.Sprintf("%v", *v)
}

func sortedKeys(m map[string]map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
