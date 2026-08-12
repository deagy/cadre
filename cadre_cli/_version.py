"""Version marker for the `cadre` pip/pipx distribution (`pyproject.toml`'s
`[tool.hatch.version]` reads this file). This is a separate version line from
`provider/provider.json`'s `version` (that one is the Agentic SDLC
provider-manifest version, consumed by the kernel's compatibility check) and
from the packaged Claude/Codex plugin's own version -- this file versions
only the pip distribution channel added by this task.
"""

VERSION = "0.4.0"
