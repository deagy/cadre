# Platform Guardrails

- Separate production and non-production through appropriate Proxmox resource pools, networks, Talos/Kubernetes clusters, namespaces, identities, state, runners, secrets, and deployment environments.
- Isolate Proxmox, Talos, Kubernetes, GitLab runner, registry, storage, and backup management planes from workload and user networks.
- Use short-lived, workload-specific identities where supported; prohibit routine use of shared administrator credentials or long-lived deployment keys.
- Restrict Proxmox roles, Talos API access, Kubernetes RBAC, GitLab tokens, and OpenTofu credentials; require strong authentication and log privileged activity.
- Keep services private unless public exposure is documented, threat-modeled, tested, and approved.
- Centralize tamper-resistant audit logs and security telemetry from Proxmox, Talos, Kubernetes, workloads, GitLab, runners, registries, network controls, and storage with tested alerting.
- Encrypt storage, backups, databases, registries, OpenTofu/Terraform state, Kubernetes secrets at rest where supported, and all management/data traffic appropriate to classification.
- Apply network segmentation, ingress/egress controls, Kubernetes network policy, private management access, and DNS protections appropriate to risk.
- Enable vulnerability management, configuration monitoring, OpenTofu drift detection, image scanning, backups, and recovery testing.
- Require ownership, environment, data classification, service, lifecycle, and recovery metadata on managed resources and workloads.
- Protect OpenTofu/Terraform state with encryption, locking, versioning, restricted access, audit logging, tested backup, and documented recovery.
- Preserve Talos control-plane quorum and recovery material; protect machine secrets, bootstrap credentials, cluster certificates, and encryption keys.
- Pin OpenTofu providers/modules, Helm charts, container images, Talos versions, and GitLab CI dependencies according to approved version policy.
