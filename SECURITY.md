# Security Policy

## Supported Versions

Holt is currently in active development. Security updates are provided only for the `main` branch and the most recently built `latest` Docker images. Older builds or arbitrary git revisions are not officially supported for security updates.

When Holt reaches a stable `v1.0.0` release, this policy will be updated to reflect long-term support for specific versions.

| Version | Supported          |
| ------- | ------------------ |
| main    | :white_check_mark: |
| all others | :x: |

## Reporting a Vulnerability

Holt is designed for **high-security, air-gapped environments**. We treat any breach of isolation or audit integrity as a critical failure.

**Do not open a public issue.**

If you discover a vulnerability, particularly regarding:
* **Container Escape:** Agents breaking out of the pup constraint model.
* **Ledger Corruption:** Circumventing the immutable blackboard.
* **Egress Leaks:** Unauthorized data transmission.

Please contact the maintainers directly. We will prioritize your report.
