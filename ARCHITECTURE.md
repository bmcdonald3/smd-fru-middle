# FRU to SMD Middleware Architecture (V1)

## 1. Purpose
Build a middleware service that ingests hardware discovery state from FRU-tracker, enriches it with Redfish data, and populates SMD RedfishEndpoints and ComponentEndpoints so downstream services can boot and manage nodes.

## 2. Scope
In scope:
- Populate SMD RedfishEndpoints
- Populate SMD ComponentEndpoints (via Redfish endpoint parsing path)
- Support initial full backfill and incremental updates

Out of scope:
- Full SMD inventory parity beyond endpoint resources
- FRU-tracker internal model redesign
- Non-HPE-specific advanced derivations in V1

## 3. Key Constraint Discovered
FRU-tracker does not currently expose an external subscribe-able event stream for another process.
- V1 ingestion should use polling with a watermark.
- Future phase can add an external bus or webhook bridge.

## 4. High-Level Architecture
Components:
1. FRU Poller
- Polls FRU-tracker resources on interval
- Tracks updated records using UpdatedAt watermark plus tie-breaker

2. Candidate Extractor
- Filters records that could represent manageable endpoints
- Extracts endpoint identity hints and metadata

3. Credential Resolver
- Looks up Redfish credentials from a secret-store mapping
- Returns username/password for each endpoint candidate

4. Redfish Enricher
- Calls Redfish service and required subordinate resources
- Builds normalized systems/managers representation

5. SMD Writer
- Upserts RedfishEndpoints
- Sends V2-style payload content that drives ComponentEndpoint creation

6. Checkpoint and Idempotency Store
- Persists poll watermark
- Stores processing outcomes to avoid duplicate effects on replay

7. Observability Layer
- Metrics, structured logs, and error counters

## 5. Data Flow
1. Poll FRU-tracker for changed records since last watermark.
2. Normalize and deduplicate candidates per cycle.
3. Validate candidate has resolvable xname strategy for V1.
4. Resolve Redfish credentials from secret store.
5. Query Redfish and assemble required endpoint payload.
6. Upsert into SMD RedfishEndpoints using V2-compatible request shape.
7. Allow SMD parser path to populate ComponentEndpoints.
8. Record success or failure and advance checkpoint.

## 6. Xname Strategy (V1)
Primary:
- Consume xname from FRU-provided properties when present.

Fallback:
- If xname is missing, skip record in V1 and emit explicit telemetry for operator action.

Future:
- Add deterministic HPE-specific derivation only after rule-set validation.

## 7. SMD Population Strategy
Required intent:
- Ensure RedfishEndpoints have minimum mandatory identity and auth fields.
- Include systems/managers sections so ComponentEndpoints auto-populate.

Write behavior:
- Upsert by endpoint identity.
- Replays should converge without duplicate effective components.
- Deletions and tombstones can be phase-gated after create/update path is stable.

## 8. Reliability and Failure Handling
1. Retry policy
- Exponential backoff with jitter for FRU, Redfish, and SMD calls
- Per-endpoint retry budget per cycle

2. Partial failure handling
- Process endpoints independently
- Dead-letter style error reporting for persistent failures

3. Resume behavior
- On restart, continue from persisted checkpoint
- Reprocess overlap window for safety if needed

## 9. Security Model
1. Credentials
- Source of truth is secret store mapping
- No credential values in logs
- Rotation-safe lookup on each processing cycle or with short cache TTL

2. Transport
- Prefer TLS for FRU, Redfish, and SMD connections
- Validate certs based on environment policy

## 10. Observability
Minimum metrics:
1. Records polled
2. Candidates accepted
3. Candidates skipped due to missing xname
4. Redfish enrichment success/failure
5. SMD write success/failure
6. End-to-end latency per endpoint
7. Checkpoint lag

Minimum logs:
- Correlation id per polling cycle
- Endpoint identity, phase, and outcome
- Structured error payloads with retry status

## 11. Deployment Model
Recommended V1:
- Single stateless service replica with persistent checkpoint storage
- Configurable poll interval
- Horizontal scaling later with partitioning or leader election

## 12. Rollout Plan
1. Dry-run mode
- Run poll and enrichment without SMD writes
- Validate candidate counts and redfish reachability

2. Controlled write mode
- Enable writes for a bounded subset
- Compare SMD endpoint counts and key fields

3. Full enablement
- Full backfill then continuous incremental polling
- Track failure rates and lag thresholds

## 13. Acceptance Criteria
1. After backfill, target endpoint set appears in SMD RedfishEndpoints.
2. Corresponding ComponentEndpoints are present and linked as expected.
3. Replay of unchanged data does not create unstable growth.
4. Restart resumes from checkpoint with no data loss.
5. Missing-xname and credential failures are visible and actionable.

## 14. Open Items
1. Exact FRU property key contract for xname in V1.
2. Secret-store keying convention for endpoint credential lookup.
3. Poll interval and maximum acceptable propagation delay.
4. Delete semantics for endpoints removed from FRU source.
