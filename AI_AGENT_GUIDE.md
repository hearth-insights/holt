# AI Agent Navigation Guide

**Purpose**: A machine-readable index for AI agents to efficiently find relevant documentation and manage context windows.
**Scope**: Essential - read first for any development task.

---

## 1. Core Context Documents

For any non-trivial task, load this core set of documents to understand the project's high-level architecture, goals, and current state.

- **`README.md`** (~2,000 tokens) - Project overview, quick start, and links to key documents.
- **`ROADMAP.md`** (~500 tokens) - The official project roadmap.
- **`docs/reference/architecture.md`** (~1,500 tokens) - The project's core philosophy, architectural principles, and component design.
- **`docs/reference/cli.md`** (~1,000 tokens) - Essential CLI command reference and usage.

---

## 2. Task-Specific Reading Lists

To supplement the core context, select from the lists below based on your specific task.

### **Topic: Independent Holt Deployment (No Source Code)**
- **`docs/independent-holt-clan-creation/`** - Complete guide for users who want to create Holt configurations and agents WITHOUT the full source code
  - `README.md` - Overview and getting started
  - `agent_interface.md` - The pup contract (stdin/stdout, logging)
  - `agent_definition.md` - How to define agents in holt.yml
  - `best_practices.md` - Agent design patterns and anti-patterns
  - `cli_reference.md` - Complete CLI command reference
  - `examples/` - Reference agent implementations

### **Topic: Onboarding & Contributing**
- **`CONTRIBUTING.md`** (~1,000 tokens) - How to set up the environment and contribute.
- **`docs/reference/development-process.md`** (~2,000 tokens) - The mandatory design-first development lifecycle.

### **Topic: Agent Development**
- **`docs/guides/agent-development.md`** - General guide for building agents.
- **`docs/guides/logging.md`** - (M4.10) Guide to the FD 3 Return architecture for agent logging.
- **`docs/guides/debugging.md`** - Strategies for debugging agents and workflows.

### **Topic: Advanced Coordination**
- **`docs/guides/fan-in-synchronization.md`** - (M5.1) Guide to fan-in workflows and synchronizer agents.

### **Topic: Enterprise & Compliance Features**
- **`docs/compliance/ENTERPRISE.md`** (~1,000 tokens) - Explains built-in security, data sovereignty, and air-gap capabilities.
- **`docs/compliance/CONTROLS_MAP.md`** (~1,500 tokens) - Maps Holt features to HIPAA, SOC 2, and ISO 27001 technical controls.
- **`docs/compliance/AUDIT_DEFENSE.md`** (~1,200 tokens) - Articulates the forensic argument for Internal Audit.
- **`docs/reference/system-spine.md`** - (M4.7) Documentation on Configuration Drift Detection and the SystemManifest.

### **Topic: Learning by Example (Tutorials)**
- **`docs/tutorials/simple-agent.md`** - Walkthrough of the basic file-creation agent.
- **`docs/tutorials/multi-agent-workflow.md`** - Deep dive into the collaborative recipe-generator demo.
- **`docs/tutorials/iac-agent.md`** - Guide to the advanced Terraform agent.

### **Topic: Core Architecture Deep Dive**
- **`docs/reference/architecture.md`** - The complete technical architecture.
- **`docs/reference/cryptography.md`** - Technical specification of the Merkle DAG and verification.
- **`docs/reference/system-spine.md`** - System integrity and configuration ledger details.

### **Topic: Designing a New Feature**
- **`docs/reference/development-process.md`** (~2,000 tokens) - The three-stage development lifecycle.
- **`docs/reference/architecture.md`** - Reference the current system architecture before designing changes.

---

## 3. Common Navigation Patterns

- **Goal: "Understand the project's security and compliance features."**
  - → `docs/compliance/ENTERPRISE.md`
  - → `docs/compliance/CONTROLS_MAP.md`
  - → `docs/compliance/AUDIT_DEFENSE.md`
  - → `docs/reference/system-spine.md`

- **Goal: "Learn how to build my first agent."**
  - → `docs/tutorials/simple-agent.md`
  - → `docs/guides/logging.md` (Crucial for output handling)
  - → `docs/reference/cli.md`

- **Goal: "Design a new feature."**
  - → `ROADMAP.md` (to understand current phase goals)
  - → `docs/reference/development-process.md` (to understand the standardized process)

- **Goal: "Fix a bug in the Orchestrator."**
  - → `docs/reference/architecture.md` (to understand the logic)
  - → `docs/guides/debugging.md`
