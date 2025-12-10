---
name: 🐛 Anomaly Report
about: Report a deviation from deterministic behaviour
title: ''
labels: bug, anomaly
assignees: ''
---

**Forensic Context**
A precise description of the deviation.

**Reproduction Path**
1. Initialize deterministic state: `holt init`
2. Execute command: `...`
3. Observe deviation

**Deterministic Expectation**
What should have happened according to the specification.

**Evidence (Logs & State)**
* Orchestrator Logs: `holt logs orchestrator`
* Relevant Artefact ID: (e.g., `Claim:12345`)

**Environment**
* OS/Arch: [e.g. macOS/ARM64]
* Holt Version: [output of `holt version`]
