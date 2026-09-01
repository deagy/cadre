# Security

## Supply chain: the `agentic-sdlc` name on PyPI is not us

**This project is not published on PyPI.**

The distribution name `agentic-sdlc` on PyPI is owned by an unrelated
third-party project. Running:

```sh
pip install agentic-sdlc        # DO NOT DO THIS
```

installs *that* project — not this lifecycle kernel. It will install
successfully and look plausible, so the failure is silent: you get a working
package with the expected name that is not the software you intended to run.

There is no typo or malice required for this to happen. It is simply a name
collision, and it predates this project's use of the name.

### There is nothing to install from PyPI, and there never will be

The kernel is a Go binary now. It was a Python package until the port
finished; that package is deleted, and its PyPI name still belongs to
somebody else, so the warning above is if anything more pointed than before.
Nothing this project publishes will ever be installed by `pip`.

**The kernel is not in this repository either.** It moved to
[deagy/cadre-kernel](https://github.com/deagy/cadre-kernel), which publishes
it as platform binaries under `v*` tags — not the `kernel-v*` scheme this
document used to name, and not `./bin/agentic-sdlc`, a shim that was deleted
with the extraction.

```sh
# Verify a release you downloaded
gh release download v0.14.2 --repo deagy/cadre-kernel \
  --pattern "agentic-sdlc-v0.14.2-linux-amd64.tar.gz" --pattern SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
tar xzf agentic-sdlc-v0.14.2-linux-amd64.tar.gz
./agentic-sdlc --version        # prints a bare semver, e.g. 0.14.2
```

Past `kernel-v*` releases in *this* repository carry Python wheels and sdists.
They are the pre-release artifacts of a package that no longer exists; verify
them against their `SHA256SUMS` if you are inspecting history, and do not
install them.

### Verifying what you have

```sh
command -v -a agentic-sdlc      # every one on PATH, not just the first
agentic-sdlc --version          # a bare semver, e.g. 0.14.2
```

**Any `agentic-sdlc` that pip or pipx installed is the wrong one.** The kernel
is a Go binary and is not published to any Python index, so `pip show
agentic-sdlc` succeeding at all means you have somebody else's package or a
stale build of the pre-port Python kernel. Uninstall it before continuing:

```sh
pipx uninstall agentic-sdlc || pip uninstall agentic-sdlc
```

This is not hypothetical. A `pipx`-installed `agentic-sdlc 0.13.2` — a build
of the Python kernel from before the port — was found shadowing the released
Go binary on a developer machine, and it answered `agentic-sdlc` on `PATH`
while the real kernel was not installed at all. `command -v -a` is in the
snippet above for that reason: the first match is not the whole story.

### Automated installers

Each lifecycle plugin's `bin/agentic-sdlc` shim fetches this kernel from a
checksum-verified release asset, pinned at plugin-generation time to a
specific `kernel-v` release, for exactly this reason. It refuses on a checksum
mismatch rather than running what it downloaded.
If you write your own automation, do the same.

## Verifying a release

Release artifacts carry a SLSA provenance attestation: an ephemeral
certificate minted from the release workflow's OIDC identity and recorded in
the Rekor transparency log, so there is no long-lived signing key anywhere in
this project to be stolen or rotated.

```sh
gh release download kernel-v<version> --repo deagy/cadre
gh attestation verify agentic-sdlc --repo deagy/cadre
sha256sum -c SHA256SUMS
```

Each release also carries an SPDX SBOM: the kernel's records its resolved
Python dependency tree, and the plugin's records the Cline plugins' npm
tree, which is that distribution's only third-party surface.

### Verifying a tag

Release tags are signed with an SSH key. GitHub shows them as **Verified**,
which is the check most people will actually use — no tooling required.

To verify locally, tell git which key to trust:

```sh
echo "releases@cadre $(gh api repos/deagy/cadre/contents/.github/tag-signing-key.pub \
  --jq .content | base64 -d)" > /tmp/cadre-allowed-signers
git -c gpg.ssh.allowedSignersFile=/tmp/cadre-allowed-signers verify-tag plugin-v<version>
```

### Why tags use a key when artifacts do not

Artifacts are signed keylessly; tags are not. That inconsistency is
deliberate and was arrived at the hard way.

Keyless tag signing via [gitsign](https://github.com/sigstore/gitsign) was
implemented, shipped in `plugin-v0.12.2`, and reverted. It produced a
valid-looking signature on the tag object but created no Rekor entry, and a
keyless certificate is ephemeral — with nothing in the transparency log
there was nothing to verify against. It failed at signing time and still
failed hours later with the same gitsign version that produced it. In the
same workflow run, the artifact attestation logged its Rekor upload
normally; the tag signing logged none. `plugin-v0.12.2` still carries that
unverifiable signature.

A signature nobody can verify is worse than none, so tags now use a stored
SSH key: a long-lived private key in repository secrets, which is exactly
what the keyless posture exists to avoid, accepted here because it is the
option that demonstrably verifies.

The plugin distribution itself carries no artifact provenance, deliberately.
A marketplace installs it by cloning a git commit, so there is no downloaded
file to verify and integrity comes from git's content addressing. Signing a
tarball nobody installs from would prove something about a file no user
touches.

## Reporting a vulnerability

Open a
[security advisory](https://github.com/deagy/cadre/security/advisories/new)
rather than a public issue.

## Security-relevant invariants

These are load-bearing properties of the kernel, not incidental validation.
Treat a change that weakens any of them as a security regression:

- Human authorities start **unassigned**; conditional applicability starts
  `unknown`, and unknown-applicable requirements **block** the gate.
- No gate is ever approved by `init`, `detect`, `plan`, or `validate`.
- G9 (Deployment Authorization) is `human_only` — automation cannot grant it.
- Author, independent reviewer, and human approver must be distinct
  identities; `validate_repository()` and the engine's gate-decision nodes
  both reject configurations where they are not.
- Approval evidence must reference an external authoritative system. Evidence
  is never invented, inferred, or silently migrated.
- Provider resource paths must resolve inside the manifest's own directory;
  path escape, duplicate IDs, and kernel-version incompatibility all fail
  closed.
