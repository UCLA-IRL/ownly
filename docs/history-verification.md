# History Verification — Design Specification

**Log prefix:** `/ndn/edu/ucla/ndnd/merkle`
**Outbox policy ID:** `ucla-ndnd-merkle/site-issued-v1`

The key words MUST, MUST NOT, and SHOULD describe requirements of this design.

## 1. Purpose and scope

Ownly retains workspace Data longer than its signing certificates remain valid.
A history log establishes that an exact signed packet existed during the
certificate's validity period, allowing a later reader to authenticate it.
Historical evidence MUST NOT grant current authority or replace signature,
trust-schema, or trust-anchor validation.

The log stores hashes, not application payloads. Packet storage and repository
replication remain responsible for content availability. Ownly uses NDNd's
Merkle client, wire format, inclusion verification, and certificate-expiry policy;
it does not implement a separate proof protocol.

## 2. Packet and log contract

For each signed NDN Data packet selected for capture in §4.1, the producer MUST submit:

```text
hash = SHA-256(complete signed Data wire)
```

Payload bytes, signature-covered bytes, and re-encoded packets MUST NOT be used
as substitutes. Each segment of an object requires its own hash.

The SDK's `Append` operation submits hashes; `Check` retrieves authenticated
inclusion evidence and the log's ingestion time. A duplicate append preserves
the first ingestion record. The timestamp is the log's statement of when it
accepted the hash, not the producer's claimed creation time.

## 3. Trust model

Log requests and responses MUST use the common LVS compiled from
[`schema.trust`](../ndn/app/schema.trust) into `schema.tlv`. Application code MUST
NOT wrap the schema to authorize a log prefix or operator.

The deployed response chain is:

```text
/ndn root → /ndn/edu/ucla site CA → /ndn/edu/ucla/ndnd/merkle service
```

The root is already pinned in Ownly. The relevant LVS rules are:

```text
#merkle_cert: "KEY"/_/"NA"/_
#merkle_cert: "KEY"/_/"NA"
#merkle_log_site_cert: "ndn"/"edu"/"ucla"/#merkle_cert <= #testbed_root_cert
#testbed_service_cert: "ndn"/"edu"/"ucla"/"ndnd"/service/#merkle_cert
    & { service: "merkle" | "repo" } <= #merkle_log_site_cert
#testbed_service_response: "ndn"/"edu"/"ucla"/"ndnd"/service/operation/_
    & { service: "merkle", operation: "32=check" | "32=append" }
    | { service: "repo", operation: "32=cmd" }
    <= #testbed_service_cert
#merkle_append_request: "ndn"/"edu"/"ucla"/"ndnd"/"merkle"/"32=append"/#user/_ <= #testbed_user_cert
```

The Merkle log and repository share the infrastructure certificate and
response rules. The `service` component MUST bind the reply to the same
service's certificate; the rule permits only Merkle `check`/`append` responses
and repository `cmd` responses. Infrastructure certificates use `NA`, separating
them from generic `NDNCERT` user rules. Only the fixed UCLA CA may authorize
these services. Append requests use a currently valid testbed user credential,
independently of the workspace certificate signing the retained content.

Log responses MUST pass ordinary current-validity checks throughout their
certificate chain. The log client MUST NOT use historical-expiry fallback to
validate its own responses.

Previously established workspace and identity trust anchors MUST remain
available after expiry. An arbitrary cached certificate MUST NOT become an
anchor merely because it is expired or included in the log.

## 4. Producer behavior

### 4.1 Capture

A live document update follows this path:

```text
Yjs updateV2 → frontend Bundler → pub_yjs_delta → encryptPub → SvsALO.Publish
→ wrapped Produce → signed Data → durable packet/outbox commit
→ commit callback → historySender → batcher → SDK Append
```

The frontend bundles Yjs updates before publication. The history batcher groups
hashes of the resulting signed packets; a raw keystroke does not directly reset
its timer. `SvsProvider.getDoc` creates one frontend `Bundler` per document and
feeds local `updateV2` events into it. The frontend defaults are a 400-ms idle
delay, a 1600-ms maximum delay, and a 3000-byte size threshold. These parameters
control Yjs merging; §4.3 controls submission of signed packet hashes.

`GetWorkspace` binds capture to the selected submitting identity. The resulting
`Capture` holds an immutable scope, so changing the active workspace or identity
does not retarget an operation already using that capture. The submitting
identity and the workspace signer are separate: the former authorizes the append
request, while the latter signs the retained content.

| Capture | Packet classes |
| --- | --- |
| Included | Workspace publications, Yjs updates, blobs, snapshots, HIDX indexes, generated object metadata, and every signed segment. |
| Included | Retained boot envelopes and public authorization packets required by historical replay: workspace certificates, precertificates, fast-join certificates, pre-anchors, anchors, and CertLists. |
| Excluded | Awareness, directed live refresh/reset replies, repository security configuration, NDNCERT enrollment credentials, and automatic capture of provisioned identity anchors. |
| Excluded | Log append/check traffic and private-key or secret packets. |

Required inner authorization packets MUST be captured independently of their
outer envelope. Historical validation of a retained boot envelope MUST NOT
relax current-authority checks for a new enrollment or live exchange.

### 4.2 Durable outbox

Production MUST atomically commit the exact packet wires and pending hashes in
one IndexedDB transaction before reporting success or exposing the packets.
Upstream object production signs and segments into private staging; direct
signing paths use the same awaited persistence contract.

The outbox uses a monotonically assigned row ID and a unique `(scope, hash)`
index. A scope binds the producing identity to the log endpoint and trust policy:

```text
scope = JSON([hex(identity.BytesInner()), outbox policy ID])
```

An endpoint or authorization-policy change MUST use a new policy ID. Existing
rows MUST NOT be silently retargeted, relabeled, or dequeued. Database upgrades
and network cache writes do not backfill old packets into the outbox.

Dispatch MUST leave pending rows intact. After a validated `OK` or `Duplicate`
result, the sender MUST await durable dequeue before completing that part of the
batch. Dequeue MUST match scope, row ID, and hash, and MUST retain packet storage.
An error MUST leave the affected rows pending.

`StoreDexie` database version 2 stores:

| Table | Record | Indexes |
| --- | --- | --- |
| `packets` | `name: Uint8Array`, `blob: Uint8Array` | Primary key `name`. |
| `history_outbox` | `id: number`, `scope: string`, `name: Uint8Array`, `hash: string` | Auto-increment `id`, unique `[scope+hash]`, ordered `[scope+id]`. |

`name` contains NDN name value bytes (`BytesInner`), `blob` contains the complete
signed Data wire, and `hash` is lowercase hexadecimal SHA-256. A pending row
contains no second copy of the packet wire. Row IDs identify an enqueue
generation, not a packet or a log leaf.

The Go/JavaScript boundary has three awaited operations:

| Operation | Contract |
| --- | --- |
| `history_commit(scope, packets)` | Write packets and insert hashes absent from that scope's pending rows in one transaction. Resolve only after commit. |
| `history_pending(scope, after, limit)` | Return rows with `id > after` in enqueue order, capped at 64; do not claim or remove them. |
| `history_ack(scope, rows)` | Dequeue only matching `(scope, id, hash)` records in one transaction; leave the packet table intact. |

`JSOutbox` copies Go buffers into JavaScript and awaits each Promise. It MUST
run in a goroutine or asynchronous JS bridge, allowing the browser event loop to
resolve database operations. After a successful nonempty commit, `NewApp` clears
the NDN store cache and wakes the scope's sender. Ordinary cache `put` operations
MUST NOT invoke this capture path.

Deduplication lasts while a hash is pending. Producing the same wire after dequeue
may create a new row; the log's duplicate semantics preserve its first ingestion
record. An acknowledgement for an earlier row MUST NOT remove this later row.

### 4.3 Scheduling and recovery

| Parameter | Value |
| --- | --- |
| Idle delay | 1 second |
| Maximum batch age | 5 seconds |
| Hashes per append / durable read page | 64 |
| Concurrent appends per sender scope | 1 |
| Failure retry | 1 second initially; doubles to a 1-minute cap |

For unique hashes entering the pending scheduling window:

```text
due = min(last unique arrival + 1 second, oldest pending arrival + 5 seconds)
```

Each new unique hash moves the idle deadline; a duplicate MUST NOT move it.
A full batch is ready immediately. No upload is scheduled for an empty batch.
A ready batch waits for any in-flight append to finish.

Failure retry is separate from edit-driven delay. Failed or partially rejected
batches retain their unresolved hashes; retry resumes submission. A fully
acknowledged batch resets the retry delay.

After startup or reconnect, the application MUST discover pending rows for
locally owned identities without requiring another edit. Timer state is not
persistent; committed outbox rows are authoritative. A crash after remote
acceptance but before local dequeue is recovered by duplicate submission.

The five-second bound applies to scheduling while running, not guaranteed log
acceptance during suspension, disconnection, retries, or an in-flight request.
A late upload cannot establish ingestion before certificate expiry.

### 4.4 Sender lifecycle and publication boundaries

`App.historySenders` owns one sender per scope within an application instance.
`historyCapture` attempts sender creation but still permits durable capture when
the engine is not ready. `ConnectTestbed` calls `resumeHistorySenders` on face-up;
it derives current-policy scopes from keychain identities with locally owned
private keys. An identity MUST remain available to resume its pending rows.

`Wake` coalesces concurrent requests to read the oldest outbox page. It always
reads from row zero; there is no durable dispatch cursor. Acknowledgement and
subsequent commits wake further reads. The sender's hash-to-row map and the
batcher's queue are transient scheduling state, not additional durable queues.
Multiple tabs may submit the same rows; correctness depends on duplicate append
semantics and generation-specific dequeue, not a cross-tab leader or lease.

The batcher does not retry automatically. A failed completion restores unresolved
hashes and pauses dispatch; the sender's retry timer calls `Flush` and `Wake`.
The completion bitmap follows request order, and successful entries MUST be
durably dequeued before completion reaches the batcher. `Close` stops scheduling
and retry timers without clearing the outbox or waiting for remote acceptance.

Packet/outbox persistence, SVS state persistence, and project Yjs persistence are
separate operations. The history durability guarantee starts at a successful
packet/outbox commit; it does not cover edits still held by the frontend bundler.
`pub_yjs_delta` publishes before persisting SVS state, and `SvsProvider` persists
the Yjs update after that call returns. Future changes MUST NOT describe these
separate writes as a single transaction.

Successful captured production confirms local persistence, not remote log
acceptance. Network and append errors are handled by the sender without rolling
back the publication. If first ingestion occurs after certificate expiry, later
historical consumption still rejects that packet.

Browser `NewApp` enables the IndexedDB outbox. `NewNodeApp` currently has a memory
packet store and disables producer capture. A future headless producer MUST
supply a durable `Outbox` and startup recovery before enabling capture.

## 5. Consumer behavior

The historical reader applies NDNd's Merkle expiry callback only when consuming
retained workspace objects, including publications, snapshots, HIDX metadata,
and boot envelopes. Direct validation, live SVS, commands, and enrollment remain
strict.

When an authorizing certificate is expired, the consumer MUST:

1. Preserve ordinary signature, schema, and trust-chain validation.
2. Hash the exact received Data wire and obtain evidence through the strict log client.
3. Verify the response and inclusion proof through the SDK.
4. Require `certificate.NotBefore ≤ ingestion time ≤ certificate.NotAfter`.

Every consumed segment MUST validate. Required certificate dependencies MUST
also satisfy their own validation relations; logging only the application packet
is insufficient when an intermediate authorization packet needs evidence.
Missing evidence MUST reject the affected packet unless the log authenticates
its absence and the explicit fallback in §5.2 is enabled. Malformed, untrusted,
or late evidence MUST reject the packet in either mode.
Rejected content MUST NOT reach Yjs or application state; failure uses the normal
consumption error path.

Snapshot certificate expiry uses the same policy as publication expiry.
`FreshnessPeriod` governs cache freshness and does not establish signer validity.
For example, a packet logged at 11:59:59 under a certificate expiring at 12:00
can be read at 12:01; first ingestion at 12:00:01 cannot justify that expiry.

### 5.1 Client composition

`GetWorkspace` constructs clients in this order:

```text
object client using app trust
→ historyReader (Consume / ConsumeExt expiry policy)
→ historylog.WrapClient (Produce capture)
→ workspace publications, snapshots, blobs, and boot sync
```

`historyExpiryClient` overrides only `Consume` and `ConsumeExt`; it supplies
`OnCertExpired` and delegates the remaining client methods. Direct `Validate`,
live SVS validation, and command signing therefore keep ordinary validity rules.
A caller needing strict consumption MUST use a strict client; passing an empty
expiry callback through this wrapper does not disable its policy.

`historyLogClient` creates a separate object client with `getTrustConfig`, the
embedded common schema, and the pinned testbed root. It MUST remain outside both
wrappers to keep log traffic outside application capture and expiry fallback.
Applying historical expiry to log responses would make proof authentication
recursively depend on another proof.

NDNd owns response authentication, append result count/order/hash matching,
inclusion proof validation, and ingestion-time policy. Ownly's sender relies on
these SDK guarantees when mapping results to rows. The reader returns
failures through ordinary object-consumption errors; log failure MUST NOT
automatically enable the compatibility mode below.

### 5.2 Temporary missing-record fallback

The workspace card exposes a manual Magic toggle for compatibility with retained
data absent from the history log. Its saved preference is the optional
workspace metadata field `ignore`; absence means false. Existing saved choices
remain applicable. The card MUST merge the selected value into the current saved
metadata before launching the workspace. Changes take effect on its next launch;
an active or pending workspace client MUST be recreated if its effective
preference differs from the saved value.

Stopping a workspace MUST clear its matching boot-sync session and owner or
participant restart guard. Relaunch MUST construct boot sync with the new
workspace client so boot objects follow the selected history policy. A stale
workspace handle MUST NOT stop another workspace's boot-sync session.

`get_workspace(name, allowMissingHistory?)` carries this preference into workspace
client construction. Certificate validity remains enforced unless an expired
authorization relation triggers a history check. The strict log client MUST
check the exact received Data wire hash before the fallback can apply:

| Authenticated log result | Magic OFF | Magic ON |
| --- | --- | --- |
| Included | Require normal proof and ingestion-time validation | Same |
| NotFound | Reject | Waive the expired-certificate check |
| Request, authentication, or protocol error | Reject | Reject |

On NotFound with Magic enabled, the reader uses NDNd's `IgnoreExpiredCert`
callback for that relation, then ordinary validation continues. The choice
applies to retained publications, snapshots, metadata, and boot objects, and
to each required certificate dependency independently. It MUST NOT waive late
ingestion or accept an unverified absence response. Included packets delegate
to NDNd's normal history policy, which performs a second check; this keeps proof
and ingestion-time handling in the SDK without matching error strings.

The mode MUST preserve signature verification, trust-schema authorization, and
established trust-anchor requirements. It MUST NOT alter direct validation,
live SVS, log request/response validity, command signing, or enrollment. Producer
capture and history uploads remain enabled. A log error alone MUST NOT switch
the mode on.

The fallback is a client consumption policy, not a schema exception or global
trust setting. Its eventual removal consists of deleting the UI preference/API
parameter and the reader's compatibility branch; durable packet and outbox
formats do not depend on this preference.

## 6. Deployment and limits

The UCLA testbed runs the log on Suns as the `ndnd-merkle` Compose service.
It connects to the existing NFD socket, keeps its keychain and Badger stores in
separate persistent mounts, and uses normal testbed certificate maintenance.
Ownly addresses the log by its NDN prefix through the connected router.
`wss://suns.cs.ucla.edu/ws/` is the default WebSocket router endpoint;
router discovery may select another face. The server accepts at most 100 hashes
per append; Ownly's 64-hash batches fit that count limit.

Repository replication targets `/ndn/edu/ucla/ndnd/repo`. Migration from
`/ndnd/ucla/repo3` changes repository management and routing as specified below.

Consumers trust the log operator's clock and statements about ingestion time.
Inclusion proofs do not independently establish wall-clock time or prevent
operator equivocation. This implementation adds no gossip, consistency audit,
proof cache, or automatic migration of old outbox scopes. Retention and timely
submission remain necessary: an unlogged packet cannot gain historical evidence
retroactively after its authorizing certificate expires.

### 6.1 Repository protocol migration

The new service uses separate routing and management namespaces:

| Purpose | Name |
| --- | --- |
| Repository base and forwarding hint | `/ndn/edu/ucla/ndnd/repo` |
| Management Interest destination | `<repository base>/32=cmd` |
| Embedded command Data | `<testbed identity>/32=repo/v=…` |
| Security configuration object prefix | `<testbed identity>/32=repo-security/v=…` |
| Management response Data | `<repository base>/32=cmd/<parameters-digest>` |

Produced configuration Data appends a terminal segment component to its object
prefix; the embedded command is an unsegmented Data packet.

The caller identity MUST come from the selected, currently valid NDNCERT
signer's key name. Command and configuration names MUST use that identity;
workspace identities and workspace owner labels do not grant testbed management
authority. The existing key announcement path MUST also announce
`<testbed identity>/32=repo` and `<testbed identity>/32=repo-security`, since
the repository uses command/config names as forwarding hints for certificate
retrieval and the key prefix covers neither name. Only management Interests
append `32=cmd` to the repository
base; invitation forwarding hints use the base itself.

The common textual LVS schema MUST replace the obsolete repo3 request rules
with caller-bound `32=repo` and `32=repo-security` rules. A fixed UCLA repository
response rule MUST authorize the service's site-issued `NA` certificate through
the UCLA site certificate and pinned testbed root. The shared service rules in
§3 MUST preserve the signer binding and permitted operations for each service.
Request rules MUST bind each supported caller identity length separately;
combining variable-length identity aliases can lose the caller binding across
alternatives. `#user1` through `#user6` define each supported user identity
path once; `#user` aggregates those rules for existing workspace policies.
The numbered testbed-user certificate aliases and repo requests MUST reuse the
same numbered user rule, preserving its component bindings. New identity
lengths MUST extend the aggregate, certificate aliases, and request rules
together. These schema names do not change NDN identity or certificate names.
The compiled `schema.tlv` and browser WASM MUST be rebuilt together.

Management commands and responses MUST retain ordinary signature, schema,
trust-chain, and current certificate validity checks. The authenticated response
body MUST parse as `RepoCmdRes` with status `200` before reporting a successful
join. This status acknowledges subscription setup, not archive completion.
If mandatory security-config publication fails, Ownly MUST abort that join
attempt rather than request a group with default trust. Existing `SyncJoin`
fields, anchor collection, snapshot threshold, and asynchronous launch behavior
remain unchanged. Normal workspace stop MUST NOT unsubscribe the repository;
the unused legacy leave helper is outside this migration.

### 6.2 Repository boundaries

Repository management authenticates testbed users; the current server does not
enforce workspace-owner-only administration. The migration does not introduce
new workspace privileges. Saved group ALO state survives a repo restart, but
active subscriptions require another `SyncJoin`; autonomous group restoration
is outside this change. Cold SVS discovery after all original SyncData signers
expire remains distinct from consuming retained objects by their known names.

The deployed configuration names the new service and its management schema.
Before switching clients, the deployed binary, public service certificate chain,
and compiled management schema MUST agree with this protocol. Deployment
configuration alone does not establish which certificate signs live responses.

## 7. Implementation map

| File | Responsibility |
| --- | --- |
| [`producer.go`](../ndn/historylog/producer.go) | Exact-wire capture, private staging, and scope definition. |
| [`outbox_js.go`](../ndn/historylog/outbox_js.go), [`store_js.ts`](../src/services/database/store_js.ts) | Awaited Go/JS bridge and atomic IndexedDB commit/dequeue. |
| [`batcher.go`](../ndn/historylog/batcher.go) | Deduplication, timing, and batch bounds. |
| [`history_sender.go`](../ndn/app/history_sender.go) | Durable row discovery, append, acknowledgement, and retry. |
| [`history_reader.go`](../ndn/app/history_reader.go), [`schema.trust`](../ndn/app/schema.trust) | Historical consumption policy and common log trust. |
| [`app.go`](../ndn/app/app.go), [`testbed.go`](../ndn/app/testbed.go) | Outbox setup, cache invalidation, sender ownership, and reconnect recovery. |
| [`workspace.go`](../ndn/app/workspace.go) | Workspace client composition, Yjs publication, SVS/snapshot configuration, and direct authorization capture. |
| [`boot_sync.go`](../ndn/app/boot_sync.go), [`identity_keys.go`](../ndn/app/identity_keys.go) | Boot authorization capture and persistence of established identity trust. |
| [`svs-provider.ts`](../src/services/svs-provider.ts), [`bundler.ts`](../src/utils/bundler.ts) | Local Yjs update events, frontend merging, publication calls, and project persistence. |
| [`yjs_update.go`](../ndn/app/yjs_update.go) | Per-document Yjs merging and encryption within snapshots. |

The repo migration edits [`repo.go`](../ndn/app/repo.go) for management
names and reply status handling, [`workspace.go`](../ndn/app/workspace.go) for
the repository base and security-config route, and
[`schema.trust`](../ndn/app/schema.trust) for common request/response trust.
[`schema.tlv`](../ndn/app/schema.tlv) and [`main.wasm`](../public/main.wasm) are
generated outputs. No NDNd change, new runtime file, or persistent application
format is required for the client protocol migration. Manual store/keychain
transfer is separate operational work; any temporary
migration tooling remains outside the committed application runtime.

## 8. Maintenance and evolution

### 8.1 Capture and SDK integration

A new retained object type SHOULD use the workspace's wrapped client's `Produce`.
A path that directly signs public Data MUST call `PersistProducedData` or
`persistIdentityHistory` before keychain installation, export, or publication.
Logging an outer object does not implicitly log embedded authorization packets.
Private signing-key wires MUST NOT be passed to these helpers.

`historylog.PersistProducedData(client, wire)` currently detects the outer
`producerClient` by type assertion. Adding another outer wrapper requires
preserving that dispatch; otherwise direct signing falls back to ordinary packet
storage and skips the outbox. Client composition is therefore part of the capture
contract, not just a convenience.

The NDNd dependency is pinned in [`go.mod`](../ndn/go.mod). Capture uses upstream
object production for signing, segmentation, and metadata, but redirects its
writes into a private `stagingStore`. The pinned producer does not propagate its
deferred store-commit error; staging lets Ownly await the real packet/outbox
transaction before returning success. An SDK upgrade MUST preserve capture of
all generated packets and error propagation before exposing produced objects.
The staging adapter currently supports only upstream production's store writes
and transaction methods.

Changing snapshot compression changes the resulting signed packet hashes. The
new snapshot, metadata, and HIDX packets MUST flow through the wrapped client;
old log records remain associated with the old exact wires. Snapshot pruning and
repository retention govern content availability independently of outbox dequeue.

### 8.2 Storage and scheduling changes

A database migration MUST preserve pending scopes, digests, enqueue IDs, and
their ordering, as well as the atomic packet/outbox transaction. The Go and
TypeScript bridge signatures and encodings MUST change together. Row IDs cross
the bridge as JavaScript numbers; changing their representation must preserve
exact comparisons used by dequeue.

`Outbox` is the portability boundary for another durable store. Its implementation
must retain atomic commit, bounded ordered reads, and awaited dequeue. The NDN
packet store's ordinary buffered `begin`/`commit` methods do not replace this
contract, because they do not insert history rows.

Scheduling defaults belong to `historylog.Options`; retry delays belong to
`history_sender.go`; Yjs merging delays belong to the frontend `Bundler`.
Changing one does not change the others. Batch-count changes MUST account for
both `PendingPageSize` and the TypeScript page cap, the server's append limit,
and encoded request size. The generic batcher's 4096-hash capacity bounds memory;
it is not an outbox limit or permission to discard durable rows.

### 8.3 Endpoint and trust changes

`historyLogName` is routing configuration. `schema.trust` is authorization policy.
`historyLogScopeID` identifies their combined policy for durable work; it is
neither an NDN name component nor the server's internal log identifier.

An endpoint or signer-policy change for the history log MUST update its endpoint,
LVS rules, and policy ID consistently. A new policy ID isolates old rows; the
current startup path resumes only the configured policy's scopes. It does not discover or migrate
previous scopes. Old work must retain its original endpoint and authorization
binding; changing an ID MUST NOT silently send it to the new service.

Renewing an operator certificate under the same authorized identity and issuer
rules does not itself change the policy ID. Renewal MUST keep currently valid
certificates available for the log response chain. Established workspace and
identity anchors remain retained for historical reading; that retention MUST
NOT relax expiry checks when importing new authority.

`schema.tlv` is generated from `schema.trust` with the python-ndn LVS compiler and
embedded as `SchemaBytes` in `workspace.go`. Schema changes require recompiling
that binary and rebuilding [`public/main.wasm`](../public/main.wasm) with
`npm run go:wasm` from the repository root. Editing the schema text alone does
not change the browser's embedded policy. Go backend changes also require a WASM
rebuild; frontend-only scheduling changes do not change the embedded schema.

### 8.4 Failure ownership

| Failure | Responsible path and durable effect |
| --- | --- |
| Signing, packet preparation, or packet/outbox commit | Producer returns an error. Staged objects are not exposed; the IndexedDB transaction does not partially commit. |
| Pending-row read, append transport, or SDK response validation | Sender schedules retry and retains pending work. |
| Individual append result is `Failed` | Sender dequeues only `OK`/`Duplicate` entries; the batcher retains failed hashes for retry. |
| Local dequeue after remote acceptance | Sender retries retained work; the log may answer `Duplicate` on resubmission. |
| Historical evidence or trust validation | Consumer rejects the packet through its consumption error path, before application delivery. |

Packet retention, outbox retention, and log retention are separate policies.
Deleting a pending row abandons submission; deleting its packet wire removes a
local content source; removing log evidence removes historical authentication.
Future cleanup behavior MUST make these effects explicit rather than treating
dequeue as packet deletion or log insertion as content replication.
