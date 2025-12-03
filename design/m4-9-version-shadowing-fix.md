# **Feature design: M4.9 Version Shadowing Fix**

**Purpose**: Fix the "Version Shadowing" issue in context assembly where newer versions lose links to original inputs.
**Scope**: `pup` context assembly logic and `blackboard` client.
**Estimated tokens**: ~1,500 tokens
**Read when**: Understanding how `pup` handles artefact versioning and context graph traversal.

Associated phase: Coordination
Status: Draft

***Template purpose:*** *This document is a blueprint for a single, implementable milestone. Its purpose is to provide an unambiguous specification for a developer (human or AI) to build a feature that is consistent with Holt's architecture and guiding principles.*

## **1. The 'why': goal and success criteria**

### **1.1. Goal statement**
Ensure that when an agent works on a reworked artefact (v2+), the context assembly algorithm preserves the original input dependencies (from v1) even if intermediate versions do not explicitly link to them.

### **1.2. User story**
As an agent developer, I want my agent to have access to the original `ClinicalTerms` (v1 inputs) even after multiple rounds of rework (`HPOMappingResult` v3), so that the agent doesn't crash due to missing context.

### **1.3. Success criteria**
*   **Reproduction**: A test case with `Grandparent -> ParentV1`, `ParentV2` (no link), and `Target -> ParentV2` fails to include `Grandparent` in the context.
*   **Fix**: The same test case passes with the fix, including `Grandparent` in the context.
*   **Direct V1 Linking**: The fix explicitly fetches V1 of the parent artefact and merges its links, ignoring intermediate versions (V2).

### **1.4. Non-goals**
*   We are not changing how `pup` determines the "latest version" (it still prefers the newest).
*   We are not changing the graph structure in Redis (artefacts remain immutable).

## **2. The 'what': component impact analysis**

### **2.1. Blackboard changes**
*   **New method**: `GetFirstVersion(ctx, logicalID)`
    *   Uses `ZRangeWithScores` on the thread ZSET with limit 0, 0 (lowest score).
    *   Returns the artefact ID of the first version (v1).

### **2.2. Orchestrator changes**
*   No changes.

### **2.3. Agent pup changes**
*   **Modified logic**: `assembleContext` in `internal/pup/context.go`.
    *   Current logic: Discovers artefact -> Resolves to Latest Version -> Adds Latest's sources to queue.
    *   **New logic**:
        1.  Discovers artefact -> Resolves to Latest Version (e.g., v3).
        2.  If Latest Version > 1:
            *   Call `GetFirstVersion` to find v1.
            *   Fetch v1 artefact.
            *   **Merge** v1's `SourceArtefacts` into Latest Version's `SourceArtefacts`.
        3.  Add merged sources to BFS queue.
        4.  Add Latest Version (with merged sources) to context map.

### **2.4. CLI changes**
*   No changes.

## **3. The 'how': implementation & testing plan**

### **3.1. Key design decisions & risks**
*   **Decision**: "Direct V1 Link" strategy. Instead of traversing V3 -> V2 -> V1, we jump straight from V3 to V1 to get the original inputs.
    *   **Justification**: Intermediate versions (V2) in a rework loop often only link to the "Review" that triggered them, not the original inputs. V1 is the source of truth for the original task inputs.
*   **Risk**: If V1 is deleted or expired (TTL), we might fail to fetch it. (Mitigation: Redis persistence, long TTLs).

### **3.2. Implementation steps**
1.  **[Blackboard]** Implement `GetFirstVersion` in `pkg/blackboard/client.go`.
2.  **[Pup]** Update `assembleContext` in `internal/pup/context.go` to implement the merge logic.

### **3.3. Performance & resource considerations**
*   **Redis Calls**: Adds 1 extra round-trip (`GetFirstVersion` + `GetArtefact`) for every dependency that is not V1. This is acceptable given the context depth limit (10) and typical graph size.

### **3.4. Testing strategy**
*   **Unit tests**:
    *   `TestGetFirstVersion`: Verify retrieving v1 from a thread.
    *   `TestAssembleContext_VersionShadowing_DirectV1`:
        *   Setup: `Grandparent` -> `ParentV1`.
        *   `ParentV2` (no link).
        *   `Target` -> `ParentV2`.
        *   Assert: Context includes `Grandparent`.

## **4. Principle compliance check**

### **4.1. YAGNI**
No new dependencies.

### **4.2. Auditability**
No new artefacts created, but improves auditability of agent decisions by ensuring they see the full context.

### **4.3. Small, single-purpose components**
Logic is contained within `pup`'s context assembly.

### **4.4. Security considerations**
No new security risks.

### **4.5. Backward compatibility**
Fully backward compatible. Old agents will simply start seeing better context.

### **4.6. Dependency impact**
No impact.

## **5. Definition of done**
*   [ ] `GetFirstVersion` implemented and tested.
*   [ ] `assembleContext` updated.
*   [ ] Shadowing reproduction test passes.
*   [ ] No regressions in existing tests.

## **6. Error scenarios & edge cases**

### **6.1. Failure modes**
*   **V1 not found**: If `GetFirstVersion` returns nothing (e.g. thread corruption), log warning and proceed with Latest Version only.
*   **Redis error**: Log warning and proceed.

### **6.2. Concurrency considerations**
*   Safe (read-only operations).

### **6.3. Edge case handling**
*   **V1 is the Latest**: Logic skips the extra fetch (optimization).
