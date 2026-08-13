package cli

// ResolveProviderInjection decides whether to inject Cadre's provider bundle
// (PP-FR-4) when delegating to `sdlc`.
//
// This is an exact behavioral replica of bin/cadre.py's
// _resolve_provider_injection(). Returns the argv to forward, and whether to
// suppress the injection.
//
// Two ways to suppress it, and the first is why this exists at all:
//
//  1. The caller supplied their own --provider. Injecting Cadre's
//     alongside it is what made `cadre sdlc --provider <other> provider list`
//     fail with `duplicates profile ids: ['generic']` -- the foreign manifest
//     loaded correctly and was then rejected for colliding with a bundle the
//     caller never asked for. A caller naming a provider has expressed which
//     one they want.
//  2. --no-default-provider, for suppressing Cadre's without supplying a
//     replacement. Consumed here; never forwarded.
//
// The kernel's own --provider is action="append" in argparse terms: it may
// repeat, accepts both `--provider X` and `--provider=X`, and list order is
// the caller's stated precedence. This function reproduces exactly that
// parsing rather than a looser scan, for the same reason the Python
// implementation uses argparse rather than string matching: a wrapper that
// tokenizes differently from the delegate it is wrapping is the actual
// hazard this exists to avoid.
//
// Malformed argv (e.g. a bare trailing `--provider` with no value) is not
// rejected here -- it is forwarded untouched, exactly as the Python
// implementation's _Quiet parser falls back to on a parse error, so the
// kernel reports it in the kernel's own wording about the command the
// caller actually invoked.
func ResolveProviderInjection(rest []string) (forwarded []string, suppressDefault bool) {
	// A malformed argv is the kernel's error to report, in the kernel's own
	// wording about the command the caller actually invoked -- printing a
	// usage block for a wrapper parser they never called would just be a
	// second, more confusing error. Mirrors the Python _Quiet parser's
	// error() raising SystemExit(2), caught by the caller to fall back to
	// "forward rest unmodified, inject as before".
	//
	// argparse, with `--provider` as action="append" (expects exactly one
	// value) and `--no-default-provider` as action="store_true" (expects
	// none) and allow_abbrev=False, can only fail to parse this argv in two
	// ways: a trailing bare `--provider` with nothing after it, or an
	// explicit `--no-default-provider=<value>` (store_true rejects an
	// explicit argument). Unrecognized flags are not errors here --
	// parse_known_args tolerates them, forwarding them through `remainder`.
	if isMalformedProviderArgv(rest) {
		return append([]string(nil), rest...), false
	}

	var noDefaultProvider bool
	var providerSupplied []string
	var remainder []string

	for i := 0; i < len(rest); i++ {
		arg := rest[i]

		switch {
		case arg == "--no-default-provider":
			noDefaultProvider = true

		case arg == "--provider":
			providerSupplied = append(providerSupplied, rest[i+1])
			i++

		case len(arg) > len("--provider=") && arg[:len("--provider=")] == "--provider=":
			providerSupplied = append(providerSupplied, arg[len("--provider="):])

		default:
			remainder = append(remainder, arg)
		}
	}

	// Order preserved: an earlier value stays before a later one, matching
	// argparse's action="append" semantics for repeated --provider flags.
	forwarded = make([]string, 0, len(providerSupplied)*2+len(remainder))
	for _, value := range providerSupplied {
		forwarded = append(forwarded, "--provider", value)
	}
	forwarded = append(forwarded, remainder...)

	suppressDefault = noDefaultProvider || len(providerSupplied) > 0
	return forwarded, suppressDefault
}

// isMalformedProviderArgv reports the two ways argparse would raise on this
// specific two-flag parser (see ResolveProviderInjection's doc comment).
func isMalformedProviderArgv(rest []string) bool {
	for i, arg := range rest {
		if arg == "--provider" && i == len(rest)-1 {
			return true
		}
		if len(arg) > len("--no-default-provider=") && arg[:len("--no-default-provider=")] == "--no-default-provider=" {
			return true
		}
	}
	return false
}
