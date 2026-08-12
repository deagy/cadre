# Pipeline Change Workflow

```mermaid
flowchart LR
    Document --> Test
    Test --> Review["Pipeline Security Reviewer"]
    Review --> SecGov{"Identity scope, production access, or regulated workflow change?"}
    SecGov -->|yes| G5["G5: Security Lead"]
    SecGov -->|no| Verify["G6: Product Owner + Engineering Lead"]
    G5 --> Verify
    Verify --> Approve["G7/G8: Release Owner (Release Engineer confirms)"]
    Approve --> Enable["Enable for Production"]
```

Gate set cross-checked against `routing.json`'s `pipeline` route (`G5, G6, G7, G8`) and `roster/authority/aides.yaml`.

1. CI/CD engineer documents the execution graph, trust boundaries, runner types, triggers, permissions, secret exposure, artifact flow, environments, and rollback behavior.
2. Test changes with non-production identities and synthetic inputs. Include untrusted merge-request/fork scenarios where applicable.
3. Pipeline security reviewer independently checks injection paths, token scope, runner persistence, cache/artifact poisoning, mutable dependencies, provenance, signatures, protections, approvals, and audit evidence.
4. Code or infrastructure reviewers join when scripts, build logic, deployment modules, runners, or cloud resources change.
5. Security/compliance review is required when identity scope, production access, evidence generation, retention, or regulated workflows change.
6. Release engineer confirms protections and approvals before enabling the pipeline for production.

Never expose production secrets to untrusted code, reuse a build identity for deployment, promote a different artifact than the reviewed build, or bypass gates by changing trigger context.
