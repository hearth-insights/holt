# Bug Fix: M4.3 E2E Test Helper Functions

**Date**: 2025-11-13
**Status**: ✅ Fixed
**Affected Test**: `TestE2E_M4_3_ContextCachingFullLifecycle`
**Root Cause**: Inadequate test helper function for scenarios with multiple artefact versions

---

## Problem Summary

The E2E test for M4.3 Context Caching was failing with the assertion:
```
Knowledge should be attached to the work thread
```

### Root Cause Discovery

Through comprehensive diagnostic testing, we discovered:

1. **The checkpoint mechanism works perfectly** - All unit tests for `CreateOrVersionKnowledge` passed
2. **Knowledge artefacts WERE being created and attached** - Just not to the thread we were checking
3. **The real issue**: The test helper `WaitForArtefactOfType()` returns the **FIRST** artefact it finds with matching type, regardless of version or relevance

**The orchestrator was creating 50+ DesignSpec artefacts** (all v1, different logical_ids), each correctly triggering a checkpoint. The test was finding a **random** DesignSpec, while the Knowledge checkpoint was attached to a **different** DesignSpec's thread_context.

---

## The Fix

### 1. Enhanced Test Helper Documentation

Added comprehensive warnings to `WaitForArtefactOfType()` about its limitations when multiple artefacts exist.

### 2. New Helper Functions

Added three new helper functions to `/app/internal/testutil/e2e.go`:

- **`FindAllArtefactsOfType()`** - Returns all artefacts of a specific type
- **`WaitForArtefactWithContext()`** - Finds artefacts based on their relationship to other artefacts via thread_context
- **`WaitForArtefactVersion()`** - Enhanced documentation for existing function

### 3. Updated M4.3 Test

Changed from finding any DesignSpec to finding the specific DesignSpec that produced the Knowledge checkpoint.

---

## Test Results

✅ **All tests now pass**:
- M4.3 E2E test: PASS
- All E2E tests: PASS  
- Unit tests: PASS

---

## Key Insight

The checkpoint mechanism was working correctly all along! The bug was in how the test was selecting which artefact to verify.
