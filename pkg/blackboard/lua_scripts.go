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
