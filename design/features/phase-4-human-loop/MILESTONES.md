# Phase 4: "Human-in-the-Loop" - Implementation Milestones

**Phase Goal**: To deliver a full-featured system with robust human oversight, production-grade data persistence, and advanced interactive capabilities, culminating in a powerful "dogfooding" demonstration.

**Phase Success Criteria**:
- Complex workflows can be paused for and resumed by human or agent-based clarification.
- Humans can interactively intercept and control workflows at predefined breakpoints.
- Holt instances can be stopped without data loss and permanently destroyed when required.
- The entire development lifecycle of a Holt feature can be demonstrated using Holt itself.

---

## **Milestone Overview**

Phase 4 focuses on adding sophisticated human-in-the-loop interaction, production-level data management, and proving the power of the platform by using it to develop itself.

### **M4.1: Advanced Question/Answer System**
**Status**: Not Started
**Dependencies**: Phase 3 completion

**Goal**: Enable agents to ask clarifying questions to other agents or escalate to a human when necessary.

**Scope**:
- **Agent-to-Agent Questions**: Implement a mechanism for an agent working on a claim to create a `Question` artefact that is directed at the `produced_by_role` of a source artefact. The target agent can then produce an `Answer` artefact, unblocking the original claim.
- **Agent-to-Human Escalation**: If a question cannot be answered by another agent (or is directed to a `user` role), it should be escalated to the human operator.
- **CLI Tooling**: Implement `holt questions [--wait]` and `holt answer <question-id> "response"`.
- **Orchestrator Logic**: The orchestrator must manage the new `Question`/`Answer` state, pausing and unblocking claims as appropriate.

**Design Document**: TBD

---

### **M4.2: Interactive Debugging & Control**
**Status**: Not Started
**Dependencies**: M4.1

**Goal**: Allow a human operator to proactively intercept a workflow at critical points for inspection and manual intervention.

**Scope**:
- **Breakpoint System**: Introduce a new configuration section in `holt.yml` or a CLI command that allows a user to set "breakpoints" that trigger on the creation of artefacts with a specific `type` (e.g., `CodeCommit`).
- **Workflow Pause**: When a breakpoint is hit, the orchestrator will pause all new claim grants for that workflow thread.
- **Manual Intervention**: While paused, the human operator can use the CLI to inspect the state and manually create a `Review` artefact (approving or rejecting the work), forcing the system down a specific path.
- **Resume Workflow**: A new CLI command to `resume` a paused workflow.

**Design Document**: TBD

---

### **M4.3: Production-Grade State Management**
**Status**: Not Started
**Dependencies**: None

**Goal**: Ensure that Holt instances are persistent by default and can be explicitly destroyed when no longer needed.

**Scope**:
- **Persistent Redis Storage**: Modify the `holt up` command to create and use a persistent data volume for the Redis container, likely in a local `~/.holt/instances/<instance_name>/` directory. This path should be configurable.
- **Clarify `holt down`**: The `holt down` command will stop the service containers but will **not** delete the persistent Redis data volume.
- **Implement `holt destroy`**: Create the `holt destroy --name <instance>` command. This command will stop the instance (if running) and permanently delete the associated Redis data volume from the host machine.
- **Safety Features**: The `destroy` command must include safety checks, such as requiring the instance name to be typed out for confirmation, as specified in the original design.

**Design Document**: TBD

---

### **M4.4: The Holt Development Lifecycle Demo**
**Status**: Not Started
**Dependencies**: M4.1, M4.2, M4.3

**Goal**: Create the ultimate "dogfooding" demo, showcasing a team of Holt agents building a new feature for Holt itself.

**Scope**:
- **Demo Scenario**: The goal will be to "add a new `holt stats` command that shows the number of artefacts and claims".
- **New Agents**: Create a clan of agents for the demo:
    - `designer-agent`: Creates a feature design document.
    - `go-coder-agent`: Implements the Go code for the CLI command and tests.
    - `reviewer-agent`: Reviews code and design documents.
    - `test-runner-agent`: A tool-based agent that runs `make test` and reports results.
- **Workflow**: The demo will showcase the full lifecycle: design -> review -> implementation -> testing -> human approval, including the potential for agent-to-agent questions and human breakpoints.

**Design Document**: TBD

---

### **M4.5: Production Documentation**
**Status**: Not Started
**Dependencies**: All other M4 milestones.

**Goal**: Document all new Phase 4 features for end-users.

**Scope**:
- Create user guides for the Q&A system and interactive debugging features.
- Document the new persistent state model and the `holt down` vs. `holt destroy` behavior.
- Create a new "How-To" guide walking through the new `holt-development-lifecycle-demo`.
- Update all relevant sections of the `README.md`, `QUICK_REFERENCE.md`, etc.