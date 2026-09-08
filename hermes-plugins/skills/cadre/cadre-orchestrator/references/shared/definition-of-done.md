# Definition of Done

A change is done only when:

- The approved intent and requirements baselines are identified and every
  applicable G1-G10 progression gate has the required decision for the exact
  revision, artifact, target, and environment.
- Acceptance criteria and architecture constraints are satisfied.
- Tests and required scans pass with reproducible evidence.
- Code, infrastructure, pipeline, security, and compliance reviews required by the workflow are complete.
- Critical and high findings are resolved or covered by an approved, time-bound exception.
- Operational telemetry, alerts, runbooks, ownership, rollback, and recovery needs are addressed.
- Documentation and architecture decisions are current.
- Release artifacts are immutable, identifiable, and traceable to reviewed source.
- Production approval and post-deployment verification are recorded when applicable.
- Requirements, controls, implementation, tests, findings, decisions, and
  evidence are bidirectionally traceable with stable identifiers and integrity
  metadata.
- Every platform impact category and specialized BOM has explicit applicability;
  no item remains `unknown`, and no applicable item depends on undefined semantics.
- Material changes have invalidated and re-entered the earliest affected gate,
  and no author or material corrector approved its own artifact revision.
