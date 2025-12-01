# **Feature design: Holt Hoard Enhancements**

**Purpose**: Enhance `holt hoard` CLI for better debugging and observability  
**Scope**: CLI, Hoard Internal, Blackboard  
**Estimated tokens**: ~2,000 tokens  
**Read when**: Implementing `holt hoard` improvements  

Associated phase: Coordination  
Status: Draft  
***Template purpose:*** *This* document is a blueprint for a single, implementable milestone. Its purpose is to provide an unambiguous specification for a developer (human *or AI) to build a feature that is consistent with Holt's architecture and guiding principles.*

## **1\. The 'why': goal and success criteria**

### **1.1. Goal statement**

Enhance the `holt hoard` CLI to provide deeper system context (spine details), flexible data extraction (customizable JSON), and precise temporal filtering (absolute timestamps) to accelerate debugging and auditing.

### **1.2. User story**

As a developer or operator debugging a failed workflow, I want to:
1.  See which system configuration (spine/git commit) produced a specific artefact to rule out configuration drift.
2.  Extract specific fields of artefacts as JSON to pipe into other tools without parsing the full payload.
3.  Filter artefacts by an exact absolute time range (e.g., "during the incident between 10:00 and 11:00") to isolate relevant data.

### **1.3. Success criteria**

*   **Spine Context**: `holt hoard --with-spine` displays the Git commit and Config Hash of the SystemManifest anchored to each artefact.
*   **Custom JSON**: `holt hoard --json --fields=id,type,spine.git_commit` outputs a JSON array containing only the requested fields.
*   **Absolute Time**: `holt hoard --since="2023-10-27T10:00:00Z" --until="2023-10-27T11:00:00Z"` correctly filters artefacts within that absolute window.
*   **Verification**: A new E2E test validates that artefacts created in a specific window are returned, and spine details match the active system state.

### **1.4. Non-goals**

*   Interactive TUI for exploring artefacts (stick to CLI flags).
*   Modifying the `SystemManifest` structure itself (we only read it).

## **2\. The 'what': component impact analysis**

### **2.1. Blackboard changes**

*   No schema changes to `Artefact` or `Claim`.
*   We rely on the existing `SourceArtefacts` linkage to `SystemManifest` artefacts.

### **2.2. Orchestrator changes**

*   No changes.

### **2.3. Agent pup changes**

*   No changes.

### **2.4. CLI changes**

*   **New Flags**:
    *   `--with-spine`: Boolean, enables fetching and displaying spine context.
    *   `--fields`: String, comma-separated list of fields to include in JSON output (e.g., "id,type,payload").
    *   `--json`: Boolean, outputs a pretty-printed JSON array (alternative to default table and existing `jsonl`).
*   **Modified Behavior**:
    *   `--since` / `--until`: Ensure robust support for absolute timestamps (RFC3339) in addition to durations.

## **3\. The 'how': implementation & testing plan**

### **3.1. Key design decisions & risks**

*   **Spine Resolution**:
    *   *Decision*: To find the spine info, we will look for a `SourceArtefact` with `StructuralType == SystemManifest`.
    *   *Risk*: If an artefact has multiple parents or deep chains, finding the spine might be complex. We assume direct anchoring or a shallow search is sufficient.
    *   *Mitigation*: Implement a helper `GetSpineForArtefact(artefact)` that checks immediate parents. If not found, we might display "unknown".
*   **Performance (N+1 Problem)**:
    *   *Risk*: Fetching spine details for a list of 50 artefacts could trigger 50 extra Redis calls.
    *   *Decision*: Use a simple in-memory cache for `SystemManifest` artefacts within the CLI command execution, as there are usually very few unique manifests active in a short window.

### **3.2. Implementation steps**

*   [ ] **Timespec Update**: Verify `internal/timespec` supports absolute timestamps. Add unit tests if missing.
*   [ ] **Spine Resolution Logic**: Implement `ResolveSpine(artefact)` in `internal/hoard`.
*   [ ] **JSON Customization**: Implement a `SelectFields(artefact, fields []string) map[string]interface{}` helper.
*   [ ] **CLI Update**: Update `cmd/holt/commands/hoard.go` to handle new flags and wire up the logic.
*   [ ] **Output Formatting**: Update `internal/hoard/format.go` to support the new JSON array format and spine columns in table view.

### **3.3. Performance & resource considerations**

*   **Redis Load**: Listing with `--with-spine` will increase read ops. Caching unique spine IDs will mitigate this (O(1) manifests vs O(N) artefacts).
*   **Memory**: JSON output for large payloads could be heavy. We should stream if possible, but for "custom subset" it's likely fine.

### **3.4. Testing strategy**

*   **Unit Tests**:
    *   `timespec.Parse`: Test with various RFC3339 strings.
    *   `SelectFields`: Test with valid/invalid fields and nested paths (if supported, or just top-level).
*   **Integration Tests**:
    *   `TestHoardSpine`: Create a SystemManifest, create an anchored Artefact, run `holt hoard --with-spine` and verify output.
    *   `TestHoardTimeFilter`: Create artefacts at t1, t2, t3. Filter for [t1, t2] and verify t3 is excluded.

## **4\. Principle compliance check**

### **4.1. YAGNI**
*   We are adding features explicitly requested for debugging. No extra dependencies.

### **4.2. Auditability**
*   Enhances auditability by exposing the spine (provenance) of artefacts.

### **4.3. Small, single-purpose components**
*   Logic stays within `internal/hoard` and `cmd/holt`.

### **4.4. Security considerations**
*   No new privileges required. Read-only access to blackboard.

### **4.5. Backward compatibility**
*   Existing flags (`--output=jsonl`) remain unchanged. New flags are additive.

### **4.6. Dependency impact**
*   None.

## **5\. Definition of done**

*   [ ] `holt hoard --with-spine` shows git commit/config hash.
*   [ ] `holt hoard --json --fields=...` outputs correct subset.
*   [ ] `holt hoard --since="2023..." --until="2023..."` works accurately.
*   [ ] Unit and Integration tests passing.
*   [ ] Updated `docs/QUICK_REFERENCE.md` with new commands.

## **6\. Error scenarios & edge cases**

### **6.1. Failure modes**
*   **Spine Not Found**: If an artefact has no spine anchor, display "N/A" or "Detached" in the spine column/field.
*   **Invalid Field**: If user requests a non-existent field, ignore it or return null (don't crash).

### **6.2. Concurrency considerations**
*   CLI is read-only, safe to run concurrently.

### **6.3. Edge case handling**
*   **Malformed Timestamp**: Return clear error message showing expected format.
*   **Empty Result**: Handle gracefully (empty JSON array `[]`).

## **7\. Open questions & decisions**

*   *Question*: Should `--fields` support nested JSON paths (e.g. `payload.some_key`)?
    *   *Decision*: For V1, only top-level fields of the `Artefact` struct + `spine` virtual field. Parsing payload is too complex/schema-dependent.

## **8\. AI agent implementation guidance**

### **8.1. Development approach**
*   Start by verifying the `timespec` package.
*   Implement the `Spine` resolution helper next.
*   Then do the Output formatting changes.
*   Finally wire it all up in the CLI.

### **8.2. Common pitfalls to avoid**
*   Don't overcomplicate the field selector. Use reflection or a simple switch/map.
*   Remember that `Payload` is a string, not a map, so you can't easily select inside it without unmarshalling (which we want to avoid unless necessary).

### **8.3. Integration checklist**
*   [ ] Verify against a running instance with actual `SystemManifest` artefacts.
