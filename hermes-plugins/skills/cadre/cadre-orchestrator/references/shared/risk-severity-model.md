# Risk Severity Model

| Severity | Meaning | Required disposition |
|---|---|---|
| Critical | Credible path to broad compromise, major data exposure, destructive production impact, or regulatory breach | Block immediately; human security owner must review |
| High | Material confidentiality, integrity, availability, or compliance impact with a plausible exploitation path | Block release until fixed or formally excepted |
| Medium | Meaningful weakness with constrained impact or prerequisites | Remediate before release when practical; otherwise assign owner and due date |
| Low | Defense-in-depth, maintainability, or minor policy issue | Track and prioritize |
| Informational | Observation or improvement without a demonstrated risk | Optional follow-up |

Assess severity from impact, likelihood, exposure, exploitability, affected data, blast radius, and existing compensating controls. Do not reduce severity solely because exploitation has not yet been observed.
