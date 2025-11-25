# Audit defense theory: The "glass box" architecture
**Classification**: Technical Whitepaper
**Version**: 1.1

## 1. Executive summary: The audit paradox
Financial institutions face a critical conflict: the need for "agentic AI" efficiency versus the regulatory requirement for "deterministic explainability."

Legacy automation was rule-based (*If X, then Y*). Modern AI is probabilistic (*Given X, likely Y*). This introduces **Non-Deterministic Risk**: distinct inputs may not always yield identical outputs.

Holt resolves this paradox via **Deterministic Orchestration**. While the *agent* inside the container may be probabilistic, the *harness* (the Holt engine) is a rigid, append-only state machine. This document outlines the theoretical basis for using Holt as a system of record for regulated decisioning.

## 2. Theoretical basis: The immutable blackboard
Traditional systems log *outputs*. Holt logs *state transitions*.

### 2.1 The event-sourced ledger
The "Blackboard" is not a temporary message queue; it is a chronological database of record.
* **Principle of non-repudiation**: Once an agent writes an `Artefact` (e.g., a drafted SAR), it cannot be deleted or modified. It can only be superseded by a new version.
* **Temporal ordering**: The Redis-backed sequence ensures that the "Chain of Events" is preserved. We can prove that *Event A* (Sanctions Check) happened before *Event B* (Transaction Clearance).

### 2.2 Forensic replayability
Because the state is immutable, the system supports "Forensic Time-Travel." An auditor can replay the exact state of the workflow as it existed at 14:03 PM on a specific date. This satisfies the regulatory requirement to reconstruct the decision environment long after the transaction has settled.

### 2.3 Configuration as evidence (The "Git" guarantee)
A decision is only auditable if the logic that produced it is versioned.
* **The commit hash as anchor**: Every execution in Holt is tied to a specific Git Commit Hash.
* **Prompt versioning**: Because agent prompts and logic are stored as code in the repository, Holt allows you to prove exactly which version of a System Prompt was active during any specific transaction.
* **Defense**: If a regulator asks, "Did your AI have the new sanctions rules on July 4th?", you can point to the specific Commit Hash active in the orchestrator logs on that day.

## 3. The "chain of thought" as evidence
In manual compliance, an analyst writes a report (the output). In Holt, the system captures the *process*.

* **Evidence artefacts**: The agent must link its decision (`Grant_Loan`) to specific source artefacts (`Credit_Report_v3`, `Income_Statement_v1`).
* **Graph traversal**: This creates a traversable graph from the final decision back to the raw ingestion data.
* **Defense strategy**: When a regulator asks "Why?", you do not show them opaque model weights. You show them the **Artefact Chain**: *Input -> Reasoning Step 1 -> Reasoning Step 2 -> Output*.

## 4. The "pup" constraint model
To mitigate "Hallucination Risk," Holt wraps every AI model in a deterministic binary called the "Pup."

### 4.1 The contract (schema enforcement)
The Pup enforces a strict JSON schema contract. If the AI hallucinates a field that doesn't exist or violates a type constraint, the Pup rejects the output *before* it reaches the Blackboard. This acts as a circuit breaker for model drift.

### 4.2 The "human gate" (Article 14 compliance)
For high-risk decisions, the "Pup" can be configured to require a `Grant` from a human operator.
* **The "four eyes" principle**: The AI performs the labour (gathering data, drafting the narrative), but the human provides the authority.
* **The audit record**: The human's intervention is recorded as a distinct `Grant` event on the Blackboard, distinguishing clearly between "Machine Reasoning" and "Human Approval."

## 5. Conclusion: From "black box" to "glass box"
Holt transforms the opacity of agentic AI into a transparent, auditable workflow. By treating **Compliance as Code** and **Workflow as Evidence**, it provides the forensic defensibility required for Tier-1 financial operations.