package blackboard

// M5.1: Lua scripts for atomic blackboard operations
//
// These scripts are embedded as Go string constants and executed via Redis EVAL.
// Lua scripts run atomically on the Redis server, ensuring consistency across
// multiple operations without race conditions.

// createArtefactScript performs atomic artefact creation with reverse index update.
//
// This script atomically executes 4 operations:
// 1. Create artefact hash (HSET)
// 2. Update thread tracking (ZADD)
// 3. Update reverse index for each parent (SADD)
// 4. Publish artefact event (PUBLISH)
//
// KEYS:
//
//	[1] artefact_key       - holt:{inst}:artefact:{uuid}
//	[2] thread_key         - holt:{inst}:thread:{logical_id}
//	[3] events_channel     - holt:{inst}:artefact_events
//
// ARGV:
//
//	[1]  artefact_id       - UUID
//	[2]  logical_id        - UUID
//	[3]  version           - integer
//	[4]  structural_type   - string
//	[5]  type              - string
//	[6]  payload           - string
//	[7]  source_artefacts  - JSON array string (e.g., ["uuid1","uuid2"])
//	[8]  produced_by_role  - string
//	[9]  created_at_ms     - int64 timestamp
//	[10] context_for_roles - JSON array string
//	[11] claim_id          - string
//	[12] metadata          - JSON object string (e.g., {"batch_size":"5"})
//	[13] artefact_json     - Full artefact JSON for Pub/Sub event
//
// Returns: artefact_id on success
//
// CRITICAL: This script MUST NOT fail partially. All operations are atomic.
const createArtefactScript = `
-- Extract keys and arguments
local artefact_key = KEYS[1]
local thread_key = KEYS[2]
local events_channel = KEYS[3]

local artefact_id = ARGV[1]
local logical_id = ARGV[2]
local version = tonumber(ARGV[3])
local structural_type = ARGV[4]
local artefact_type = ARGV[5]
local payload = ARGV[6]
local source_artefacts_json = ARGV[7]
local produced_by_role = ARGV[8]
local created_at_ms = ARGV[9]
local context_for_roles = ARGV[10]
local claim_id = ARGV[11]
local metadata = ARGV[12]
local artefact_json = ARGV[13]

redis.log(redis.LOG_NOTICE, "DEBUG_UNIQUE_ID_8888: Lua script running with metadata=" .. tostring(metadata))

-- Step 1: Create artefact hash
redis.call('HSET', artefact_key,
    'id', artefact_id,
    'logical_id', logical_id,
    'version', version,
    'structural_type', structural_type,
    'type', artefact_type,
    'payload', payload,
    'source_artefacts', source_artefacts_json,
    'produced_by_role', produced_by_role,
    'created_at_ms', created_at_ms,
    'context_for_roles', context_for_roles,
    'claim_id', claim_id,
    'metadata', metadata
)

-- Step 2: Update thread tracking (ZSET)
redis.call('ZADD', thread_key, version, artefact_id)

-- Step 3: Update reverse index for each parent artefact
-- Parse source_artefacts JSON array
local source_artefacts = cjson.decode(source_artefacts_json)

-- Only update index if there are parent artefacts
if source_artefacts and type(source_artefacts) == 'table' and next(source_artefacts) ~= nil then
    -- Extract instance name from artefact_key
    -- Pattern: holt:{instance}:artefact:{uuid}
    local instance_name = string.match(artefact_key, 'holt:([^:]+):')

    for _, parent_id in ipairs(source_artefacts) do
        -- Build reverse index key for this parent
        local children_key = 'holt:' .. instance_name .. ':index:children:' .. parent_id
        redis.call('SADD', children_key, artefact_id)
    end
end

-- Step 4: Publish artefact event
redis.call('PUBLISH', events_channel, artefact_json)

return artefact_id
`

// accumulatorAddAndCheckScript atomically adds a claim to the merge accumulator and checks completion.
// M5.1.1: Fan-In Accumulator pattern - Orchestrator-driven batch synchronization.
//
// REFACTORED: Supports BOTH COUNT mode (Producer-Declared) and TYPES mode (Named pattern).
//
// This script implements the core merge phase accumulation logic:
// 1. Create accumulator Hash if first claim (with deterministic Fan-In Claim ID)
// 2. Add claim ID to mode-specific data structure (COUNT: SET, TYPES: HASH)
// 3. Check if batch/set is complete
// 4. Return completion status (or error for duplicate types)
//
// KEYS:
//   [1] accumulator_key - holt:{inst}:claim-accumulator:{ancestor_id}:{role}
//
// ARGV:
//   [1] claim_id              - UUID of claim being accumulated
//   [2] mode                  - "count" or "types"
//   [3] expected_count        - Expected count (string, e.g., "15")
//   [4] merge_ancestor        - Ancestor artefact ID
//   [5] current_artefact_type - Type of the claim being accumulated (e.g., "TestResult")
//   [6] claimer               - Agent role name
//   [7] expected_types_json   - JSON array of expected types (only for TYPES mode, e.g., '["Test","Lint"]')
//
// Returns:
//    1 - COMPLETE (triggers grant)
//    0 - ACCUMULATING (still waiting)
//   -1 - ERROR (duplicate type in TYPES mode)
//
// CRITICAL: This script MUST be atomic to prevent duplicate Fan-In Claims.
// Multiple concurrent merge bids for same batch must execute serially.
const accumulatorAddAndCheckScript = `
-- Extract arguments
local key = KEYS[1]
local claim_id = ARGV[1]
local mode = ARGV[2]
local expected_count = tonumber(ARGV[3])
local merge_ancestor = ARGV[4]
local current_artefact_type = ARGV[5]
local claimer = ARGV[6]
local expected_types_json = ARGV[7]

-- Check if accumulator exists
local exists = redis.call('EXISTS', key)

if exists == 0 then
    -- First claim: Initialize accumulator Hash
    -- Generate deterministic Fan-In Claim ID: fanin:{ancestor}:{role}
    local fanin_claim_id = 'fanin:' .. merge_ancestor .. ':' .. claimer
    local now_ms = redis.call('TIME')[1] .. string.sub(redis.call('TIME')[2] .. '000', 1, 3)

    redis.call('HSET', key, 'id', fanin_claim_id)
    redis.call('HSET', key, 'status', 'accumulating')
    redis.call('HSET', key, 'mode', mode)
    redis.call('HSET', key, 'claimer', claimer)
    redis.call('HSET', key, 'merge_ancestor', merge_ancestor)
    redis.call('HSET', key, 'expected_count', expected_count)

    -- Mode-specific metadata
    if mode == 'count' then
        redis.call('HSET', key, 'target_type', current_artefact_type)
    elseif mode == 'types' then
        redis.call('HSET', key, 'expected_types', expected_types_json)
    end

    redis.call('HSET', key, 'created_at_ms', now_ms)
end

-- Mode-specific accumulation logic
if mode == 'count' then
    -- COUNT MODE: Track claims in SET
    local count_key = key .. ':count'
    redis.call('SADD', count_key, claim_id)
    local count = redis.call('SCARD', count_key)

    if count == expected_count then
        return 1  -- BATCH_COMPLETE
    else
        return 0  -- ACCUMULATING
    end

elseif mode == 'types' then
    -- TYPES MODE: Track unique types in HASH
    local types_key = key .. ':types'

    -- Check for duplicate type (STRICT VALIDATION)
    if redis.call('HEXISTS', types_key, current_artefact_type) == 1 then
        return -1  -- ERROR: Duplicate type detected
    end

    -- Add type -> claim_id mapping
    redis.call('HSET', types_key, current_artefact_type, claim_id)

    -- Check if all expected types present
    local type_count = redis.call('HLEN', types_key)

    if type_count == expected_count then
        return 1  -- SET_COMPLETE
    else
        return 0  -- ACCUMULATING
    end
end

-- Should never reach here (invalid mode)
return -1
`
