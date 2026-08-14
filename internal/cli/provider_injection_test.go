package cli

import "testing"

// TestResolveProviderInjection exercises the five-case matrix from
// bin/cadre.py's _resolve_provider_injection() docstring, reproduced in
// CADRE_CLI_GO_ARCHITECTURE.md §5.4. Case names match the acceptance
// criteria's "provider injection test matrix" requirement (5 cases).
func TestResolveProviderInjection(t *testing.T) {
	tests := []struct {
		name           string
		input          []string
		wantForwarded  []string
		wantSuppressed bool
	}{
		{
			name:           "case1_no_provider_flag",
			input:          []string{"plan", "--task", "foo"},
			wantForwarded:  []string{"plan", "--task", "foo"},
			wantSuppressed: false,
		},
		{
			name:           "case2_provider_space_separated",
			input:          []string{"--provider", "/path/to/other", "plan"},
			wantForwarded:  []string{"--provider", "/path/to/other", "plan"},
			wantSuppressed: true,
		},
		{
			name:           "case3_provider_equals_form",
			input:          []string{"--provider=/path/to/other", "plan"},
			wantForwarded:  []string{"--provider", "/path/to/other", "plan"},
			wantSuppressed: true,
		},
		{
			name:           "case4_no_default_provider",
			input:          []string{"--no-default-provider", "plan"},
			wantForwarded:  []string{"plan"},
			wantSuppressed: true,
		},
		{
			name:           "case5_multiple_provider_flags",
			input:          []string{"--provider", "a", "--provider", "b", "plan"},
			wantForwarded:  []string{"--provider", "a", "--provider", "b", "plan"},
			wantSuppressed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarded, suppress := ResolveProviderInjection(tt.input)
			if suppress != tt.wantSuppressed {
				t.Errorf("suppress = %v, want %v", suppress, tt.wantSuppressed)
			}
			if !equalStrings(forwarded, tt.wantForwarded) {
				t.Errorf("forwarded = %v, want %v", forwarded, tt.wantForwarded)
			}
		})
	}
}

func TestResolveProviderInjection_EmptyInput(t *testing.T) {
	forwarded, suppress := ResolveProviderInjection(nil)
	if suppress {
		t.Errorf("suppress = true, want false for empty input")
	}
	if len(forwarded) != 0 {
		t.Errorf("forwarded = %v, want empty", forwarded)
	}
}

func TestResolveProviderInjection_TrailingBareProviderIsMalformed(t *testing.T) {
	// A trailing bare --provider with no value is what the kernel's own
	// argparse would reject; the wrapper must forward it untouched rather
	// than guessing, per _resolve_provider_injection's documented fallback.
	input := []string{"plan", "--provider"}
	forwarded, suppress := ResolveProviderInjection(input)
	if suppress {
		t.Errorf("suppress = true, want false for malformed argv")
	}
	if !equalStrings(forwarded, input) {
		t.Errorf("forwarded = %v, want unmodified input %v", forwarded, input)
	}
}

func TestResolveProviderInjection_NoDefaultProviderWithValueIsMalformed(t *testing.T) {
	input := []string{"--no-default-provider=true", "plan"}
	forwarded, suppress := ResolveProviderInjection(input)
	if suppress {
		t.Errorf("suppress = true, want false for malformed argv")
	}
	if !equalStrings(forwarded, input) {
		t.Errorf("forwarded = %v, want unmodified input %v", forwarded, input)
	}
}

func TestResolveProviderInjection_UnrecognizedFlagsPassThrough(t *testing.T) {
	// parse_known_args tolerates unrecognized flags; they belong in
	// remainder, not treated as an error.
	forwarded, suppress := ResolveProviderInjection([]string{"--unknown-flag", "value", "plan"})
	if suppress {
		t.Errorf("suppress = true, want false")
	}
	want := []string{"--unknown-flag", "value", "plan"}
	if !equalStrings(forwarded, want) {
		t.Errorf("forwarded = %v, want %v", forwarded, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
