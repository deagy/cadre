"""Version marker for the `cadre` CLI.

`pyproject.toml`'s `[tool.hatch.version]` reads this file, and so does the Go
CLI (`internal/cli/version.go`'s `CLIVersion()`, which parses the single
literal assignment below without executing any Python) -- one version line for
both distribution channels.

This is a separate version line from `provider/provider.json`'s `version`
(that one is the Agentic SDLC provider-manifest version, consumed by the
kernel's compatibility check) and from the packaged Claude/Codex plugin's own
version, which `plugin/tools/plugin_version.py` sets across its 8 manifests.

Keep the assignment on one line, top level, with a plain string literal:
`internal/cli/version.go`'s regex and hatchling's `pattern` both match exactly
that shape.
"""

VERSION = "0.5.0"
