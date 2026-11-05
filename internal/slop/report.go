package slop

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Output is what `--format json` prints: the report plus anything the scan
// declined to read.
type Output struct {
	*Report
	Skipped []Skipped `json:"skipped"`
}

// WriteJSON renders the report as indented JSON.
func WriteJSON(w io.Writer, report *Report, skipped []Skipped) error {
	if skipped == nil {
		skipped = []Skipped{}
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(Output{Report: report, Skipped: skipped})
}

// WriteText renders the report for a person. Clear units get one line; a unit
// with tells lists each hit with its rule, rationale and context.
func WriteText(w io.Writer, report *Report, skipped []Skipped) error {
	var b strings.Builder
	for _, unit := range report.Units {
		label := unitLabel(unit)
		switch {
		case unit.Exempt != nil:
			fmt.Fprintf(&b, "%s: exempt (line %d, slop-ok: %s)\n", label, unit.Exempt.Line, unit.Exempt.Reason)
		case unit.Failed():
			fmt.Fprintf(&b, "%s: %d tell(s)\n", label, len(unit.Hits))
			for _, hit := range unit.Hits {
				fmt.Fprintf(&b, "  %s:%d:%d [%s/%s] %s\n", hitFile(hit), hit.Line, hit.Column,
					hit.Class, hit.RuleID, hit.Span)
				fmt.Fprintf(&b, "      why: %s\n", hit.Rationale)
				fmt.Fprintf(&b, "      in:  %s\n", hit.Context)
			}
			if unit.CountedExceeded {
				fmt.Fprintf(&b, "  counted tells: %d, limit %d\n", unit.CountedTotal, report.CountedLimit)
			}
		default:
			fmt.Fprintf(&b, "%s: clear", label)
			if unit.CountedTotal > 0 {
				fmt.Fprintf(&b, " (%d of %d counted tells allowed)", unit.CountedTotal, report.CountedLimit)
			}
			b.WriteString("\n")
		}
	}

	if exemptions := report.Exemptions(); len(exemptions) > 0 {
		b.WriteString("\nExemptions (slop-ok):\n")
		for _, exemption := range exemptions {
			fmt.Fprintf(&b, "  %s line %d: %s\n", exemptionLabel(exemption), exemption.Line, exemption.Reason)
		}
	}
	if len(report.Disabled) > 0 {
		b.WriteString("\nDisabled rules:\n")
		for _, disabled := range report.Disabled {
			fmt.Fprintf(&b, "  %s: %s\n", disabled.ID, disabled.Reason)
		}
	}
	if len(skipped) > 0 {
		b.WriteString("\nSkipped:\n")
		for _, skip := range skipped {
			fmt.Fprintf(&b, "  %s: %s\n", skip.Path, skip.Reason)
		}
	}

	status := "clear"
	if report.Failed {
		status = fmt.Sprintf("%d tell(s) found", report.TotalHits)
	}
	fmt.Fprintf(&b, "\nrules v%d, %d unit(s): %s\n%s\n", report.RulesVersion, len(report.Units), status, report.Limitation)
	_, err := io.WriteString(w, b.String())
	return err
}

func unitLabel(unit UnitResult) string {
	if unit.Commit != "" {
		return "commit " + shortSHA(unit.Commit)
	}
	return unit.File
}

func hitFile(hit Hit) string {
	if hit.Commit != "" {
		return shortSHA(hit.Commit)
	}
	return hit.File
}

func exemptionLabel(exemption Exemption) string {
	if exemption.Commit != "" {
		return "commit " + shortSHA(exemption.Commit)
	}
	return exemption.File
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
