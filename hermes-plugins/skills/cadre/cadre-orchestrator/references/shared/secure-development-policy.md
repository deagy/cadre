# Secure Development Policy

## Required practices

- Validate inputs and encode outputs at trust boundaries.
- Use centralized authentication and authorization; enforce authorization server-side.
- Store secrets in an approved secret manager and use short-lived credentials where possible.
- Encrypt sensitive data in transit and at rest using approved protocols and key-management services.
- Pin, scan, and routinely update dependencies and build images.
- Produce structured logs without credentials, tokens, or unnecessary sensitive data.
- Define timeouts, retries with backoff, resource limits, and safe failure behavior.
- Add tests for security-sensitive behavior and regression cases.
- Generate an SBOM and retain build provenance for releasable artifacts.

## Prohibited practices

- Hard-coded secrets or shared production credentials.
- Unreviewed public exposure, wildcard IAM grants, or disabled certificate validation.
- Dynamic execution of untrusted content.
- Logging authentication material or unredacted sensitive payloads.
- Bypassing a required gate to make a build or deployment pass.
