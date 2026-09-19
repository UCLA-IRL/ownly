# Verifying history data

Status: Phases 1 and 2 implemented, tested, and reviewed on `merkle`, created from `dev`
at `df6d750`. The user-reviewed revisions are incorporated: maximum batch age is
10 seconds, and Phase 2 provides durable outbox persistence before producer logging
is enabled. Phase 2's subagent review found no unresolved correctness or minimality
issues. Phase 3 is accepted. Capture remains disabled until Phase 4 connects
trusted configuration to a working sender and recovery loop.
The NDNd dependency is unchanged.

**Scope reduction — 2026-09-17:** the user approved the consumer-driven cuts below.
Repository configuration, NDNCERT enrollment, and automatic self-signed identity
anchor capture have been removed, along with the unused explicit-signer helper.
Validation passed and the subagent found no correctness or minimality issues.
Bootstrap replay and metadata selection remain explicit Phase 3 scope decisions.

## Scope and references

The goal is to validate historical NDN Data after a certificate in its validation
chain expires, using evidence that the exact packet was logged during the
authorizing certificate's validity period.

- [Ownly trust-plane slides](https://docs.google.com/presentation/d/18oQr3g9sXwQc4cYFUxNIWiTFNflAPPTLUAU0rsFYUcw/edit): slide 34 describes timestamped hash logging and auditing; slide 37 describes delayed, per-user submission of aggregated hashes. Slides 33, 35, and 36 supply the surrounding model and initial interface proposal.
- [NDNd PR #219](https://github.com/named-data/ndnd/pull/219) merged the `merkle` branch into `main` at `609a6939147394ef927b7534f3c08e32920c3777`.
- [Merged protocol specification](https://github.com/named-data/ndnd/blob/609a6939147394ef927b7534f3c08e32920c3777/docs/merkle-log-design.md) describes the implemented interface and its limits. Use this revision for integration rather than the earlier slide-level wire proposal.

The current implementation trusts one log operator. Auditors, consistency proofs,
and gossip remain future upstream work. Ownly does not need a new Merkle tree or
a new snapshot-integrity protocol for this first integration.

## Reuse the upstream implementation

The merged `std/merklelog` package provides:

- `NewClient(objectClient, logPrefix, requester)` for authenticated append
  commands and public single-hash checks.
- `Client.Append` for batches of complete Data packet hashes. Results distinguish
  successful insertion, an existing hash, and failure. First ingestion wins.
- `Client.Check` for signature-validated responses and verified inclusion proofs.
- `NewCertExpiredPolicy` for `OnCertExpired`: hash `RawData`, check inclusion, and
  require `Cert.NotBefore <= IngestTime <= Cert.NotAfter`. Normal schema and
  signature checks still apply, including recursive certificate validation.

Use the supplied expiry policy rather than duplicating proof or time checking.
Its log client must validate log responses independently, without using the same
expiry policy to justify those responses.

## Consumer-driven scope review — 2026-09-17

Logging is useful only when a supported consumer may need to authenticate the
exact retained packet after an authorizing certificate expires. A packet used
only for an operation requiring current authority needs ordinary validation,
not a history-log submission. Log hashes, not application payloads.

Apply that rule to dependencies too: a reader reopening an old document may need
its signing certificate and the rest of its authorization chain. Retaining only
the document hash cannot establish every expired validation relation. Conversely,
being signed, persisted, or named a certificate does not by itself justify logging.

The classification must follow actual consumers. A short `FreshnessPeriod` does
not establish a hard consumption deadline: non-fresh Data is still valid Data
under the [NDN packet specification](https://docs.named-data.net/NDN-packet-spec/current/data.html#freshnessperiod).

### Confirmed unnecessary capture

| Removed hook | Consumer evidence | Implemented reduction |
| --- | --- | --- |
| `repo.go:publishSecurityConfig` and `historylog.ProduceWithSigner` | Each `NotifyRepoJoin` creates a new configuration version for that join. The repository consumes it to install schema/anchors; no history reader reconstructs workspace content from old configuration packets. | Restored ordinary `object.Produce` in `repo.go` and removed the otherwise-unused explicit-signer capture helper. A later join supplies current configuration. |
| Both `ndncert.go` enrollment hooks | `schema.trust` uses testbed certificates for management/repository operations. Workspace history uses workspace certificates and identity anchors. Deriving an identity name from the testbed key does not make that key part of the content's signature chain. | Removed the two `persistIdentityHistory` calls. Enrollment, ordinary certificate storage, and current-authority validation continue without history logging. |
| `identity_keys.go:makeIdentityCert` | Provisioned self-signed identity anchors terminate the content validation chain. Their trust is established separately; the historical evidence concerns their signed children. | Removed automatic capture of the anchor's own wire. Ordinary certificate storage and provisioning remain. Fetched workspace self-signed anchors and their CertList path still require capture. |

### Boundaries that need an explicit consumer contract

| Packet or mechanism | Finding and proposed scope |
| --- | --- |
| Workspace-wide `WrapClient` in `GetWorkspace` | It intercepts every `Produce`, including boot publications. Choose capture at the content-producing client/call sites instead of inheriting it for every workspace service. Keep the staging/outbox mechanism for selected durable content; do not add a generic packet classification framework. |
| Awareness and directed refresh/reset replies | Existing capture already excludes them. They are handled in a live exchange; keep ordinary validation and do not add history logging or history fallback. State changes published into document SVS remain separate durable content. |
| Boot SVS envelopes | These are stored, repository-replicated, resumed from saved state, and delivered in order, without snapshots. A missed expired envelope can block later delivery. They are not proven to be live-only. Do not silently remove capture and promise complete long-offline boot catch-up. Either explicitly leave that capability out of the first release or retain transport history verification. A replacement current-state bootstrap protocol is separate work. |
| Invitations, participant precertificates, fast-join credentials | New enrollment should require current authority; history evidence must not revive permission to join. Their current hooks are candidates for removal from a content-history release. However, precertificates can sign boot envelopes, and legacy precertificates embed invitations. Excluding their hashes requires excluding the corresponding historical boot validation too. The invitation's 50-year validity is longer than its workspace signer's 10-year validity, so a signer-validity consumption deadline is not enforced merely by its name. |
| Final workspace certificates, workspace pre-anchor, workspace anchor and CertList | Keep exact wires and the hashes actually needed by historical content validation. A new reader may need these after expiry to authenticate old updates, blobs, and snapshots. Do not remove them just because issuance happened during bootstrap. |
| Object metadata | Versioned blob, SVS publication, and `HIST` snapshot consumers use known versions and bypass RDR metadata. `HIDX` discovery uses an unversioned name and does need metadata. Logging every produced metadata packet adds avoidable hashes, but defer selective filtering if it would introduce more branching than this optimization warrants. Never disable metadata capture globally. |
| DSK response | Do not add it to this change. Its wire is repository-stored and its requester can resume later using a persisted exchange key, so its 60-second freshness is not a lifetime guarantee. The present inner consumer parses it without `Validate`; changing that security contract is separate work. Its outer ordered SVS publication remains covered by the content-stream decision. |

Retain document updates, blobs, snapshots and indexes used by late readers,
including any required discovery metadata and authorization dependencies. Even a
control payload inside an ordered content stream cannot simply be removed from
capture if readers must validate that sequence to advance.

The certificate dependency analysis was checked against the local upstream NDNd
checkout at `e87c160`: its expiry callback covers expired certificate Data,
self-signed certificates when explicitly validated, cross-schema packets, and
CertLists authenticated through expired listed certificates. Verify these paths
again against the exact dependency selected in Phase 3; do not assume the current
pinned validator and the future expiry-policy validator behave identically.

Ownly currently skips expired identity anchors when rebuilding its trusted-anchor
set (`identity_keys.go:localTrustAnchorCertNames` and `peerTrustAnchorCertNames`).
Historical validation needs an explicit way to retain previously accepted trust
decisions. Logging a self-signed anchor cannot establish or restore that trust.

### Approved minimal revision

1. Remove repository security-config capture and its explicit-signer adapter;
   remove NDNCERT enrollment and automatic identity-anchor capture. These cuts need
   no new files or abstractions.
2. Before broadening validation in Phase 3, agree on whether historical boot
   transport is supported. Keep new enrollment and live command validation strict
   in either case. Narrow the wrapper and related invitation/precertificate hooks
   together once that boundary is chosen.
3. Keep durable packet/outbox commit, recovery, acknowledged dequeue, bounded
   batching, and the **10-second maximum batch age** for retained historical data.
   App termination during backoff is a real requirement already requested by the
   user; narrowing capture does not remove the need for persistence.
4. Adjust the existing local-only tests: excluded setup traffic must not enqueue
   or fail because of outbox errors; document content and required certificate
   evidence must still enqueue atomically and survive restart. Use cold-reader
   expiry cases rather than adding tests for hypothetical input failures.
5. Have the subagent review the scope reduction and its checks before marking
   the revised Phase 2 complete. No new files, dependency changes, log service,
   or bootstrap redesign are part of the confirmed cuts.

**Reduction completed — 2026-09-17:** removed the three capture paths and unused
adapter above. `repo.go` and `ndncert.go` now match their pre-Merkle versions exactly.
Updated the existing local tests to verify identity-anchor creation bypasses the
outbox while workspace authorization capture still propagates storage failures.
Native `go test -race ./...`, both Go/WASM test packages, the full WASM build to
`/tmp/ownly-merkle-cut.wasm`, all eight local IndexedDB scenarios, and whitespace
checks passed. The subagent reported no correctness or minimality findings.
All four Merkle test files remain locally ignored; no new files were added.

## Packet capture and delayed submission

The table below describes the retained capture paths after the approved cuts.
Resolve the remaining consumer-contract decisions above before enabling capture.

Hash the exact complete encoded Data packet, including its signature:
`SHA256(DataWire)`. Content bytes, signature-covered bytes, and re-encoded parsed
packets are not substitutes. A segmented object requires a hash for each packet.

| Packet source                                        | Ownly integration point                                                                                                                                                                                                   |
| ---------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Publications, blobs, snapshots, and snapshot indexes | `ndn/app/workspace.go` uses object production and SVS. Capture the actual packets below these APIs, including internally generated snapshots, indexes, and object metadata.                                               |
| Certificates and CertLists                           | `ndn/app/identity_keys.go`, `workspace.go`, and `boot_sync.go` capture the retained authorization paths. Recursive expiry validation needs evidence for authorization packets as well as application Data; testbed credentials and provisioned identity anchors are excluded. |
| Other directly signed packets                        | `ndn/app/awareness.go`, `encrypt.go`, and direct `MakeData` calls in `workspace.go` bypass object production. Decide which ephemeral/control packets need historical use before including them.                           |

A producer-specific client wrapper will stage the packets generated by upstream
object production, then commit those exact wires with their outbox records. This
distinguishes local production from cached network responses and makes producer
success depend on the durable commit. Direct signing paths need explicit coverage.

The submission queue should deduplicate hashes per user and extend its timer as
new packets arrive, as described on slide 37. Bound delay to prevent continuous
editing from postponing submission indefinitely. Split batches to respect the
8,800-byte packet limit, including request/response names, signatures, and result
overhead; upstream does not preflight or split batches. Remove only acknowledged
hashes, retain failures for bounded retries, and exclude logging traffic to avoid
a submission loop. Durable recovery after abrupt disconnection is required in
Phase 2, extending the initial slide 37 scope at the user's request. Orderly
shutdown flushing is an additional optimization, not the persistence mechanism.

### Producer timer and upper bounds

Maintain one hash queue per submitting user/log pair, shared across that user's
documents. The timer runs after actual Data production, independently of the
existing per-document Yjs `Bundler` in `src/utils/bundler.ts`.

Use a **1-second idle delay** and the user-selected **10-second maximum batch
age**, both configurable. These are Ownly settings, not protocol requirements.
For the oldest pending hash enqueued at `firstQueuedAt`, each new unique hash
schedules:

```text
flushAt = min(now + idleDelay, firstQueuedAt + maxBatchAge)
```

Use a monotonic clock for elapsed time. New arrivals may move the idle deadline,
but must never reset `firstQueuedAt` or extend the maximum-age deadline. Duplicate
hashes do not postpone submission. For example, continuous production beginning
at time 0 still makes the batch due at time 10 seconds.

Flush earlier when a batch reaches the encoded packet-size budget, on an explicit
flush, or before a known authorizing certificate expires, allowing time for log
ingestion. Split a large pending queue into commands that fit both request and
response limits; a fixed hash count alone cannot guarantee that fit.

The age bound limits batching delay before an append attempt, not successful
ingestion: a suspended browser, unavailable log, or failed request can exceed it.
Failure retries need their own bounded exponential backoff with jitter. Keep failed
hashes pending without repeatedly firing an already-expired batching timer, and
allow newly produced hashes to queue while an append is in flight. Keep request
timeouts and queue resource limits explicit in the implementation.

Logging must happen while the authorizing certificate is valid. Submitting old
packets after expiry cannot recover their earlier signing time. A valid proof
establishes membership in the log; this revision still relies on the operator's
honest clock.

## Dependency and validator migration

Ownly currently pins NDNd at `362a01097784` in `ndn/go.mod`. The merged API removes
`UseSignatureTime` and `IgnoreValidity` in favor of `OnCertExpired`, so changing
the dependency alone will not compile.

| Current call site                                                       | Required migration                                                                                                                               |
| ----------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| `workspace.go`: object consume, `SvSyncOpts`, and `SnapshotNodeHistory` | Supply the configured history policy where historical validation is needed. NDNd propagates the SVS policy to publication and snapshot fetching. |
| `testbed.go`: `verifyTestbedCert`                                       | Supply original `certWire` as `RawData`; choose strict current validation or history validation for each use.                                    |
| `boot_sync.go`: SVS options and directed response validation            | Replace removed validity fields. Keep strict current validation where historical acceptance is unnecessary.                                      |
| Direct `ValidateExt` / `TrustConfig.Validate` calls                     | Forward exact `RawData` wherever history validation is enabled; missing wire is rejected.                                                        |
| `WorkspaceCard.vue` and workspace `ignoreValidity` argument             | Decide how the existing “Magic” expiry bypass is retired or isolated. It must not silently become successful history verification.               |

Configure a separate object client for the log, with rules for append signing
and authenticated responses. Keep its expiry validation strict to avoid recursive
dependence on its own history service. The deployed log prefix, response
signer/trust anchor, and accepted requester identity are deployment inputs; the
sample `/ndnd/merkle` prefix is not evidence of an available service.

## Live Yjs updates, snapshots, and existing history

### Live update publication and log submission

Live edits already follow this path:

```text
Yjs updateV2 -> frontend Bundler -> pub_yjs_delta -> encrypt -> SVS ALO Publish
            -> object Produce -> signed Data packets
```

`src/services/svs-provider.ts` publishes each bundled update, and
`ndn/app/workspace.go` passes the producer wrapper into SVS ALO. Once capture is
enabled, that wrapper commits each exact signed packet and its pending hash in
one transaction before production reports success. This includes individual
publications between snapshots; capture does not wait for a snapshot threshold.
These updates need history evidence because a late reader may fetch them after
the signing certificate expires, even though they were originally sent live.

The current Phase 2 commit supplies the capture hooks and durable outbox, but
`historyLogID` is unset: production capture and network appends are not enabled.
Phase 3 adds the reader and trust configuration. Phase 4 enables capture and
connects the outbox to authenticated `Append` calls, with the 1-second idle delay
and 10-second maximum batching age. Submit hashes of the signed Data wires to the
Merkle log; the encrypted content continues through existing SVS/repository
storage. Do not add a second raw-Yjs upload path.

Persistence here begins at signed packet production. Raw edits still waiting in
the existing frontend Yjs bundler are outside the Merkle outbox guarantee; this
work must not claim to make that earlier in-memory window durable.

### Snapshot certificate expiry

Ownly compresses and re-encrypts Yjs updates before signing snapshots. Log the
resulting snapshot packets as their own Data objects. Logging only original
updates will not provide expiry evidence for these packets. Snapshot compression
can remain enabled.

The same producer wrapper covers signed `HIST` snapshot segments, `HIDX` index
segments, and index discovery metadata. Retain the required certificate/CertList
wires and submit their hashes through the authorization capture paths. The
snapshot contains publication content rather than the original signed Data
wires; its signature attests to the compressed history, and original update
proofs cannot substitute for proof of the new snapshot bytes.

A late reader discovers the index and fetches the snapshot through the history
reader. Normal signature, schema, and chain validation still apply. If a signing
certificate has expired, require authenticated inclusion evidence for the exact
packet, with the log ingestion time inside the relevant certificate's validity
interval. Apply the upstream policy recursively to the required authorization
chain. Only deliver the snapshot and advance its sequence after validation
succeeds. The index and discovery metadata must pass the same checks before the
reader can reach the snapshot.

Certificate expiry does not require periodic snapshot re-signing or renewal.
Timely evidence remains usable after expiry, provided the signed packets,
authorization material, and log evidence remain available. If evidence is missing
or ingestion was too late, reject the object; logging it after expiry cannot fix
that. A newly signed snapshot would be a new attestation, not proof that the old
snapshot was timely. Cache freshness and packet pruning are separate availability
concerns; a Merkle hash does not retain or recover deleted content.

Phase 4 should flush before a known certificate deadline rather than waiting the
full 10 seconds. This only reduces avoidable delay: suspension or log outages can
still miss the ingestion window. Do not turn either failure into an expiry bypass.

Local Yjs rows contain application content and are compacted; they cannot
reconstruct original signed packet hashes. Existing expired packets without timely
evidence remain unverifiable under this policy. Packet and certificate retention
is still required because the log stores hashes rather than replayable Data.

## Workflow for every phase

Each phase follows the same sequence requested by the user:

1. **Plan:** update this document with the phase scope, proposed files/API changes,
   behavior, checks, and completion criteria. Prepare the plan before changing
   implementation code.
2. **User review:** present the concrete plan and ask the user to approve or revise
   it. This is the only step for questions to the user. Wait for an explicit
   response; silence is not approval. Resolve any required configuration or design
   choices here. Approval of one phase does not approve later phases.
3. **Implement:** follow the approved or revised plan and run its checks. If a
   finding requires a material scope change or a new user decision, return to the
   review step with an amended plan before making that change.
4. **Subagent review:** after implementation, have a subagent review the diff for
   correctness and minimal necessary changes. Give it the approved scope and test
   results. Address its findings within that scope and rerun affected checks.
5. **Record completion:** update this document with the implemented behavior,
   validation results, review disposition, and remaining limitations. Prepare the
   next phase's plan for its own user review.

Routine implementation choices within the approved scope do not require another
question. Planning and review do not authorize publishing or deployment.

## Roadmap

| Phase | Deliverable                                                    | Depends on                                 | Status   |
| ----- | -------------------------------------------------------------- | ------------------------------------------ | -------- |
| 1     | Tested producer batching core with an upper bound on delay     | User review revisions incorporated         | Complete |
| 2     | Exact signed-packet collection and durable outbox recovery     | Phase 1                                    | Complete |
| 3     | Merged NDNd API migration and independently trusted log client | Phase 2; configuration decisions at review | Awaiting review |
| 4     | Producer submission through the real log client                | Phases 1–3                                 | Planned  |
| 5     | Complete historical validation, replay, and failure handling   | Phases 3–4                                 | Planned  |
| 6     | End-to-end validation and rollout documentation                | Phases 1–5                                 | Planned  |

Later phases describe the intended direction. Before each starts, expand its
file-level plan and resolve its remaining choices at that phase's review step.

### Phase 1 — Producer batching core

**Goal:** implement the producer timer and queue independently of networking,
packet capture, and certificate validation. This establishes the behavior discussed
above without requiring a log deployment or changing the pinned NDNd version.

**Implemented code changes:**

| File                                   | Change                                                                                                                                                         |
| -------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ndn/historylog/batcher.go` (new)      | Small Go batching component using only the standard library, with digest deduplication, bounded storage, timers, and an injected asynchronous submit function. |
| `ndn/historylog/batcher_test.go` (new) | Deterministic tests of timer deadlines, queue bounds, submission completion, and shutdown.                                                                     |
| `docs/history-verification.md`         | Record approval/revisions, implementation results, and the subagent review.                                                                                    |

The package accepts fixed-size SHA-256 digests; collecting and hashing exact Data
wire belongs to Phase 2. It does not implement a Merkle tree, proof verification,
NDN encoding, or a second application-update bundler. One instance represents one
submitting user/log pair; the application integration will own that mapping.

**Implemented interface:** constructor with validated options, an injected submit
function, and an error callback for asynchronous failures; `Enqueue` for a digest;
`Flush` to request immediate submission; and
`Close` to stop scheduling and return unresolved digests. Submission completion
reports an acknowledgement for each submitted hash plus any request error. The
later NDNd adapter maps both `OK` and `DUPLICATE` to acknowledgement. This interface
keeps upstream wire/status types out of the batching core.

**Implemented defaults:**

| Option                    | Initial value | Meaning                                                                                                    |
| ------------------------- | ------------- | ---------------------------------------------------------------------------------------------------------- |
| Idle delay                | 1 second      | Wait after the last newly queued digest.                                                                   |
| Maximum batch age         | 10 seconds    | Fixed deadline from the oldest digest in the pending batch; updated by user review.                        |
| Maximum hashes per batch  | 64            | Bound one submission; reaching this count makes it ready immediately.                                      |
| Maximum unresolved hashes | 4,096         | Bound queued plus in-flight digests; reject a new unique digest with an explicit capacity error when full. |

These values are configurable implementation defaults, not protocol requirements.
The hash-count bound is not a wire-size guarantee; Phase 4 must enforce encoded
request and response limits as well. Invalid option combinations must be rejected.
Zero option fields select the defaults; negative values, a maximum age below the
idle delay, and pending capacity below batch capacity are rejected.

**Required behavior:**

1. Keep digests in arrival order and deduplicate across pending and in-flight
   batches. A duplicate does not extend the timer or consume another queue slot.
   Deduplication need only cover unresolved digests; the log handles later repeats.
2. Schedule `min(lastUniqueArrival + idleDelay, firstQueuedAt + maxBatchAge)` using
   monotonic elapsed time. Never move the oldest digest's deadline when another
   digest arrives. Cancel stale timers when a batch is drained or the queue closes.
3. Allow at most one logical submission in flight. Detach the submitted batch so
   new arrivals remain separately queued with their own first-arrival time. Invoke
   the injected submit function without holding the queue lock, and handle both
   synchronous and asynchronous completion safely.
4. Count in-flight digests against the capacity bound. Remove only acknowledged
   digests. A request error retains the whole submitted batch; a valid partial
   result retains only unacknowledged entries. Malformed completion results retain
   the batch and surface an error instead of silently losing evidence.
5. For Phase 1, a failed submission pauses automatic sending until explicit
   `Flush`; retained hashes must not cause a busy retry loop. New hashes can still
   enqueue within capacity. Automatic retry timing, jitter, and request deadlines
   are implemented with the transport in Phase 4.
6. `Flush` on an empty queue sends nothing. During a submission it records pending
   flush intent rather than starting an overlapping request. After `Close`, reject
   new entries, stop timers, ignore late/duplicate completion callbacks, and return
   unresolved hashes without claiming they were logged. Phase 2's durable outbox
   remains authoritative across shutdown; Phase 4 handles bounded draining.

The 10-second bound makes a batch **ready** on time. An existing in-flight request,
paused failure state, or suspended runtime can delay the actual attempt; readiness
must not be reported as successful logging. When the sender becomes available,
an already-due batch should proceed without another idle delay. Hashes left after
a size-triggered split retain their original age.

**Verification:** use deterministic time control, such as Go 1.25's
`testing/synctest`, and a controllable fake submitter rather than sleeps or a live
log. Cover idle expiry, repeated extension, continuous production reaching the
10-second cap, duplicate traffic, count-triggered flushing, capacity errors,
enqueues during submission, partial acknowledgement, full failure, explicit retry,
empty flush, shutdown, and late/duplicate callbacks. Include contention coverage
under the race detector. These tests exercise observable scheduling and retention
behavior rather than private implementation details.

Run from `ndn/` after implementation:

```sh
go test -race ./historylog
go test ./...
GOOS=js GOARCH=wasm go build ./historylog
GOOS=js GOARCH=wasm go build -o /tmp/ownly-merkle-phase1.wasm ./cmd
```

Run formatting and diff checks. The WASM build is required because native Go
builds exclude most of `ndn/app`. Frontend builds are unnecessary for this phase
unless the approved scope changes to include frontend files.

**Completion criteria:** the approved queue contract passes these checks; the
subagent review has no unresolved correctness or scope findings; and the document
records the result. Phase 1 delivers a tested component, not active hash logging
or historical verification in the application.

**Persistence integration contract:** the in-memory batcher is a bounded scheduling
window over Phase 2's durable outbox. Commit packet/outbox records before enqueueing
their digests; leave rows pending while requests are in flight. The transport
adapter must durably dequeue acknowledged entries before reporting completion to
the batcher. If dequeue fails, report a failure and retry safely. Restore pending
rows on restart and explicitly flush them without another 10-second debounce.
No monotonic timestamp needs to survive restart. When the scheduling window is
full, leave additional rows in the outbox and load them as capacity becomes free;
the in-memory limit is not permission to discard durable work.

**Completion record — 2026-09-17:** implemented `NewBatcher`, `Enqueue`, `Flush`,
and `Close` with the defaults above, one logical in-flight request, per-hash
acknowledgements, retained failures, explicit retry, and timer cancellation. Closing
returns unresolved hashes; an already-selected transport submission may still
finish, and its late callback is ignored. Persistence and automatic network retries
remain assigned to Phases 2 and 4 respectively.

Validation passed using `GOCACHE=/tmp/ownly-merkle-go-cache` for all Go commands:

- `go test -race ./historylog`
- `go test ./...`
- `GOOS=js GOARCH=wasm go build ./historylog`
- `GOOS=js GOARCH=wasm go build -o /tmp/ownly-merkle-phase1.wasm ./cmd`
- Go formatting, document formatting, and whitespace checks.

The subagent review identified one scheduling defect: a batch that became due
while a request was in flight could be postponed by another arrival. A regression
test reproduced it before the fix. The implementation now keeps that pending
window ready until drained or paused after failure. The checks above passed after
the fix, and the subagent's focused follow-up found no unresolved correctness or
minimality issues. No application logging, database changes, or dependency upgrade
were included in Phase 1.

### Phase 2 — Capture exact signed packets and persist the outbox

**Review status:** the user approved this plan on 2026-09-17 after clarifying
dequeue semantics: acknowledgement removes a pending outbox entry, retaining the
signed packet and the remote log evidence. Implementation, checks, and subagent
review are complete.

**Goal:** record hashes of exact produced packets durably, including segmented
objects and certificates, so killing the app during the timer delay or a request
does not forget pending log submissions.

**Implementation boundary:** deliver the durable store, exact-wire producer hooks,
and recovery/acknowledgement primitives. Exercise them with explicit test scopes
and a fake sender. Application capture stays disabled through Phase 3's reader
configuration; Phase 4 enables it with live submission and the recovery pump.
Keep the current NDNd dependency and Phase 1's 1-second idle
delay / **10-second maximum batch age**. This phase does not introduce a log service,
change certificate validation, or add a settings UI.

#### Why the existing producer store cannot guarantee persistence

The pinned NDNd implementation has two relevant behaviors:

- `object.Produce` defers `tx.Commit()` and ignores its error, including on an
  earlier production error.
- `storage.JsStore` populates its shared cache before durable commit. Its ordinary
  `Put` does not await JavaScript, and its transaction methods do not propagate
  rejected JavaScript promises.

Adding an outbox table to the current `put` path alone would therefore still allow
successful production and cache visibility without a successful durable write.
Wrapping every store write would also collect fetched remote packets. Address both
issues locally through a producer-only staging path, without forking upstream
segmentation, signing, or the generic read/fetch store.

#### File and API plan

| File                                                                                | Change                                                                                                                   |
| ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| `ndn/historylog/producer.go`                                                        | Scope, exact-wire packet preparation, outbox contract, and producer wrapper with private staging.                        |
| `ndn/historylog/outbox_js.go`                                                       | Awaited browser store bridge, isolated by the `js && wasm` build constraint.                                             |
| `ndn/app/app.go`                                                                    | Optional capture helpers, immutable scope binding, and browser outbox initialization.                                    |
| `src/services/database/store_js.ts`                                                 | Version-2 migration, atomic packet/outbox commit, bounded reads, and durable dequeue.                                    |
| `ndn/app/workspace.go`, `boot_sync.go`, `identity_keys.go` | Hooks for produced objects, snapshots, required public certificates, and invitations. Repository configuration and NDNCERT enrollment retain their original behavior. |
| `ndn/historylog/producer_test.go`                                                   | Portable producer, exact-wire, failure-ordering, and snapshot tests.                                                     |
| `ndn/app/history_log_test.go`                                                       | WASM application and Go/JS bridge tests, including the optional real-browser recovery check.                             |
| `src/services/database/__tests__/store_js.test.ts`                                  | All eight IndexedDB regression scenarios in one file, run directly by Node.                                              |

Only two runtime files are new for Phase 2. The three test files listed above and
Phase 1's `ndn/historylog/batcher_test.go` are kept locally, excluded through
`.git/info/exclude`, and used during development without being committed.
App helpers live in the existing `app.go`. The separate
outbox type file, shared test-case file, JS bridge test file, custom test tsconfig,
and permanent browser runner/page have been consolidated or removed.

Run the local IndexedDB tests with
`node --experimental-strip-types --test src/services/database/__tests__/store_js.test.ts`.
They use Node's built-in test runner and TypeScript stripping. The local
`fake-indexeddb` dependency is already installed; after reinstalling dependencies,
restore it with `npm install --no-save --package-lock=false fake-indexeddb@6.2.5`.
No test-only script or dependency is added to the package manifests.
Browser termination/restart was verified with a
one-off harness outside the repository: it builds both Go/WASM test packages,
serves temporary assets on localhost, and kills/relaunches an isolated Chromium
profile. This is recorded verification, not a repository browser-test command.
`src/services/ndn.ts` needed no change because its existing `StoreJS` typing already
exposes the extended shim.

The proposed durable interface has three operations, with corresponding JavaScript
methods named `history_commit`, `history_pending`, and `history_ack`:

```text
CommitProduced(scope, packets) -> error
Pending(scope, afterRowID, limit) -> pending rows, error
Ack(scope, acknowledged row IDs and hashes) -> error
```

`CommitProduced` accepts complete signed packets whose names and SHA-256 digests
were prepared in Go. Preparation may parse a name for indexing but never re-encodes
the bytes being hashed. Database methods perform no signing, hashing, or network
work inside an IndexedDB transaction. All persistence errors cross the JS/Go
boundary. Awaiting must run from a Go goroutine/async entry point so JavaScript can
complete the promise; the tests must exercise rejected promises as well as success.

#### Producer ordering and packet coverage

1. Select the signer using the existing client behavior. Capture an immutable
   submitting identity/log scope for this operation; do not look up a different
   current login later while committing or recovering it.
2. Call upstream `object.Produce` with a private staging store. Retain each complete
   signed segment and metadata packet. The upstream deferred commit only completes
   staging; it cannot expose packets or commit persistent state.
3. If signing or object production fails, discard the staged packets. Otherwise,
   await one `CommitProduced` transaction for all packets and pending hashes.
4. After commit succeeds, invalidate the shared packet cache as needed so reads
   see committed wires, and return producer success. A failed commit returns an
   error before the caller can advertise the new object or advance publication
   state. Verify this ordering with the real upstream SVS/snapshot call paths.
5. Direct signing paths use a `PersistProducedData` helper on the exact returned
   wire before keychain insertion, exports, or publication. For invitations,
   disable `SignCrossSchema` auto-storage and explicitly persist its returned wire.
   Propagate errors from the new atomic history writes before publication, keeping
   existing keychain bookkeeping behavior. Private-key storage remains separate;
   a later failure may leave an unused public packet pending, which is safe.

Staging is private to one production call and cannot mix concurrent producers.
Release it after commit/failure. The current browser transaction already buffers
the whole produced object; this design retains that model rather than adding a
streaming producer or a new object-size limit. Avoid unnecessary wire copies and
test a multi-segment object. Memory remains proportional to the object being
produced, separate from the batcher's bounded pending-hash window.

| Packet category                                                                                                                    | Phase 2 treatment                                                                                                                                                                                  |
| ---------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Workspace publications and blobs; boot SVS publications                                                                            | Capture every locally produced signed segment and object metadata packet through the wrapper.                                                                                                      |
| History snapshots and snapshot indexes                                                                                             | Pass the same wrapper into SVS's snapshot implementation so internally produced objects follow the same transaction.                                                                               |
| Workspace owner certificates, pre-certificates and anchors, final participant certificates, fast-join certificates, CertLists | Persist the exact public Data through the direct helper before sharing or installation. Capture an inner certificate independently of any outer SVS publication containing it. |
| NDNCERT credentials, provisioned self-signed identity anchors, repository security-configuration objects | Exclude from capture. Preserve ordinary production, certificate storage, and current-authority validation. |
| Cross-schema invitations | Capture the direct signed invitation wire; reconsider alongside the boot-history scope in Phase 3. |
| Generic fetched packets, peer imports, static root certificates                                                                    | Exclude from automatic capture. Reading/caching/importing a packet does not make it newly produced local history.                                                                                  |
| Direct awareness, DSK response, and SOS/MLS reset response packets                                                                 | Exclude these transient responses; their existing signed SVS publications still pass through normal producer capture.                                                                              |
| Private-key/secret packets, unsigned application updates, Merkle append/check traffic                                              | Exclude. Hash only the final signed public Data in the selected paths; never hash `MarshalSecret` output or attach capture to the future log client.                                               |

#### Persistent schema and isolation

Add `history_outbox` **in the same IndexedDB database as `packets`**, with these
fields and indexes:

- An auto-increment row ID for stable enqueue order and acknowledgement identity.
- A scope key encoding the submitting NDN identity and a configured log identifier.
  The log identifier must bind its prefix and trusted log identity, to be supplied
  by Phase 3. Encode fields unambiguously rather than concatenating name strings.
- The exact packet digest (32 bytes, with a stable hexadecimal index key) and
  packet name for diagnosis.
- A unique compound index on scope/digest and an ordered index on scope/row ID.

The atomic read/write transaction stores packet wires and inserts only missing
pending rows. Repeated capture of a pending digest preserves its original row ID
and order. The same digest under a different scope has a separate pending record.
No monotonic timestamp is persisted; recovered rows become immediately due.

Existing version-1 packets survive migration without automatic hash backfill.
Finding an old packet locally cannot establish that it was ingested before expiry.
Ordinary packet pruning must not delete pending outbox rows. Pending entries only
need the digest for submission; packet availability for later replay continues to
depend on existing packet/snapshot retention and replicas.

Read pending rows in pages of at most 64, using the stable row ID as the cursor.
Never materialize the entire backlog in Go. When Phase 4 fills the batcher's
4,096-entry scheduling window, excess rows stay on disk. Restarting resets the
cursor and loads the original scope only; changing users or logs cannot retarget
old rows. The log requester key may rotate within the same submitting identity,
subject to the configured log authorization in Phase 4.

`Ack` durably **dequeues** only entries whose scope, ID, and digest all match the
acknowledged request. At the database level, this deletes the matching pending row
from `history_outbox`; it does **not** delete the signed packet from `packets` or
remove evidence from the remote log. Packet retention remains a separate policy.
A stale callback must not dequeue a new row created for the same digest after an
earlier acknowledgement. A repeated acknowledgement of an already-dequeued row is
harmless. This storage primitive does not authenticate responses; Phase 4 may call
it only after authenticated `OK`/`DUPLICATE` results.

**Persistence contract:**

1. Key records by submitting identity, log identity, and exact packet digest, with
   a stable enqueue order and any context needed to resume. Keep each record until
   the configured log returns an authenticated `OK` or `DUPLICATE` acknowledgement.
2. Commit the produced packet and pending outbox record atomically before reporting
   successful production or exposing the packet to network publication. Where a
   direct signing path needs separate keychain bookkeeping, commit its packet and
   outbox entry first, then update the keychain. Do not leave a crash window between
   packet persistence and recording its need for logging.
3. Persist pending work through request dispatch, timeout, failure, app termination,
   and restart. On startup, load it in bounded pages and make recovered batches
   immediately ready once the correct identity/log client is available.
4. Dequeue only acknowledged entries in a committed database operation. A crash
   after remote acceptance but before durable dequeue causes a safe duplicate
   submission. A dequeue failure leaves the work pending; it must not be reported
   as drained. Dequeue never deletes the signed packet itself.
5. Do not clear pending records on ordinary workspace close, logout, or shutdown.
   Retain the original user/log association and never send another identity's work
   under the current login. Surface durable storage failures; do not silently
   evict pending evidence.

**Proposed lifecycle decisions for review:** ordinary logout, workspace close, and
identity-key removal retain pending rows. If the original identity's keys are
unavailable, its rows wait for those keys rather than using another identity. Add
no automatic expiry, quota eviction, or new purge UI in this phase; explicit browser
site-data deletion removes the database. An IndexedDB quota or transaction error
fails the affected production operation and rolls back packet/outbox writes.

Multiple tabs may commit to the same scope; IndexedDB transactions and the unique
index deduplicate pending inserts. Do not introduce a leader lease or persisted
in-flight state. Later senders may submit the same hash from multiple tabs, relying
on the protocol's duplicate handling and guarded durable dequeue. Phase 1's
single in-flight guarantee is per batcher instance, not across tabs. Test concurrent
database instances and stale acknowledgements explicitly.

The current headless Go app uses an in-memory packet store and documents consumer
mode. Keep producer logging disabled there until a durable backend exists; a
headless producer must satisfy the same contract before it is enabled. Persistence
protects committed outbox work from app termination, not storage deletion or a
missing ingestion window: restarting after certificate expiry cannot establish
that a packet was logged before expiry.

#### Validation and completion criteria

Use the following observable outcomes, rather than only checking internal helpers:

| Scenario                                                                                      | Required result                                                                                                                                        |
| --------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| A known signed wire; the same content with different signature bytes                          | Hashes match SHA-256 of the complete original wires and differ when the wire differs.                                                                  |
| Multi-segment object, metadata, snapshots/indexes, and direct certificates/invitations        | Every selected exact wire has a pending entry before the producer reports success or publishes it.                                                     |
| Signing failure partway through an object; rejected JS commit; quota/transaction abort        | No partial packet/outbox transaction is committed, no newly staged wire enters the shared cache, and the producer returns an error.                    |
| Kill/reopen during the 1-second idle delay or before the 10-second maximum age                | All successfully committed rows survive. A recovery test feeds them to the Phase 1 batcher with explicit `Flush`, without a new debounce period.       |
| Kill after fake dispatch or remote acceptance but before durable dequeue                      | Rows remain pending; resubmission and a fake `DUPLICATE` acknowledgement safely dequeue them. Signed packets remain stored. No live log is required.   |
| Partial acknowledgement, rejected dequeue, or late completion from a previous sender          | Only committed matching acknowledgements dequeue entries; failed/unacknowledged work and newer replacement rows survive. Signed packets remain stored. |
| More than 4,096 pending rows; switch user or log; two database instances writing concurrently | Bounded pages recover all work without cross-scope submission, eviction, or duplicate pending rows within one scope.                                   |
| Version-1 database upgrade, packet pruning, logout/close                                      | Existing packets and unresolved outbox work survive their respective operations; old packets are not automatically backfilled.                         |
| Fetched/peer packets, secret material, and an unwrapped client used for log traffic           | These paths create no automatic outbox entries.                                                                                                        |

Run the Go race tests and full Go suite, build the full application for JS/WASM,
and run frontend type checking, focused database tests, and the production build.
Use a real browser IndexedDB harness to verify transaction rollback and recovery
after closing/reopening the database and reloading the page, with no network log.
The fake IndexedDB unit suite is additional coverage, not evidence of browser
restart durability. Exercise the awaited Go/JS bridge with storage failures; a
WASM compile alone is insufficient to check this ordering.

After implementation, request the mandated subagent review of the Phase 2 diff
for minimal necessary changes, packet coverage, commit/error ordering, and crash
recovery. Resolve findings and rerun affected checks. Record exact results and any
remaining limits here before marking Phase 2 complete. Completion means durable
capture and recoverable pending work under an explicit scope; active log submission
and historical acceptance are still the later phases' deliverables.

**Initial completion record — 2026-09-17 (before scope reduction):** implemented the producer-only client wrapper,
private per-object staging, direct public-Data hooks, awaited Go/JS outbox bridge,
and version-2 packet database. Packet wires and pending digests commit atomically;
the shared Go cache is invalidated only after success. Captured objects include
upstream-generated publication segments, metadata, snapshots, and snapshot indexes.
Direct hooks cover the selected certificates, CertLists, invitations, and explicitly
signed repository configuration objects. Failed persistence is returned before
the selected producer paths can continue to publication.

Outbox reads are limited to 64 rows per page and retain the original identity/log
scope. Duplicate pending captures retain enqueue order. Acknowledgement checks the
scope, row ID, and digest, then durably dequeues only matching entries; the signed
packets remain stored. Packet pruning and ordinary lifecycle operations do not
clear the outbox. No persistent in-flight state, cross-tab lease, or backfill was
introduced. `src/services/ndn.ts` needed no change because its existing `StoreJS`
typing already exposes the extended shim.

Validation passed:

- `go test -race ./historylog` and `go test ./...`, using
  `GOCACHE=/tmp/ownly-merkle-go-cache`.
- The full application JS/WASM build to `/tmp/ownly-merkle-phase2.wasm`.
- Both `historylog` and `app` test packages executed as JS/WASM under Node and in
  real Chromium. Tests exercise rejected promises, certificate error ordering,
  exact-wire digests, and immutable scope binding. The native suite alone excludes
  the WASM application tests.
- The local Node test command above: all eight IndexedDB scenarios passed, including
  migration, atomic rollback, stale/partial acknowledgements, concurrent database
  connections, and a 4,107-entry backlog.
- One-off browser verification: those eight scenarios passed with real IndexedDB.
  The runner killed its isolated Chrome process after committing pending entries,
  relaunched the same temporary profile, reloaded the page, and recovered all three
  unresolved rows. Dequeue retained the packets. The real Go/JS bridge also drained
  recovered work through the Phase 1 batcher with an explicit immediate flush.
- The upstream SVS integration test confirmed failed production does not advance
  publication sequence state, and captures internally generated history snapshots
  and indexes through the same wrapper.
- Frontend type checking and production build, test-source type checking, targeted
  ESLint, formatting, and whitespace checks. The production build reported
  third-party `use client` directives and outdated Browserslist data, without errors.

The subagent reviewed the final implementation for correctness and minimal changes
and reported **no unresolved findings**. Application capture is deliberately dormant
through Phase 3's reader configuration; the real sender, automatic recovery
pump, and network retries activate capture in Phase 4. The browser tests use a fake acknowledged
log result and do not claim live log ingestion. The **10-second maximum batch age**
and 1-second idle delay are unchanged.

**Minimality revision — 2026-09-17:** at the user's request, removed unrelated
error-handling additions around existing certificate/keychain operations, duplicate
Go/JS input checks, redundant parsing of packets just signed by upstream, and a
second copy of buffers already owned by the Go bridge. Removed the corresponding
artificial-input test. Atomic commit errors still propagate, recovery remains
bounded, and dequeue still checks scope/row ID/digest and retains signed packets.
The reduced implementation passed Go race tests, the application WASM build,
frontend type checking, targeted lint, all eight store scenarios, and real-browser
termination/restart recovery with both WASM test packages. The subagent's follow-up
found no correctness regressions or remaining unrelated additions.

**File consolidation — 2026-09-17:** reduced Phase 2 from 12 new files to five,
retaining two runtime files and three focused test files. Moved application helpers
into `app.go`, combined portable producer/outbox code, grouped WASM bridge tests
with application tests, and combined IndexedDB cases with their Node test entry.
Removed the custom test tsconfig and repository browser runner/page; the browser
check remains documented one-off verification. Native race tests, both Go/WASM
test packages, the WASM application build, all eight IndexedDB cases, TypeScript
checks, lint, and isolated browser restart verification passed after consolidation.
The subagent found no correctness or minimality issues.

### Phase 3 — NDNd migration and log trust

**Implementation status:** accepted. The Ownly migration, consumer boundary,
expired-anchor preservation, and Magic removal are implemented. Log response
trust is wholly in the compiled LVS, so ordinary `SchemaBytes` trust
configuration is the only trust-schema source. NDNd `e3c4e9c2` supplies the
required fetcher fix and Ownly pins its Go pseudo-version.
Phase 1 is committed as `e061bc6` (`merkle: basic batcher`) and Phase 2 as
`a224a39` (`merkle: initial history capture`). This document remains uncommitted;
all Merkle tests remain local and excluded by `.git/info/exclude`.

**Goal:** configure an independently trusted history-log reader and migrate
historical consumption to the upstream expiry policy. Ordinary current-validity
operations must work without a configured log. This phase does not start a
producer sender or activate the durable capture hooks before Phase 4 can drain
and acknowledge them.

#### Verified dependency and API changes

Pin NDNd to `e3c4e9c2f1e025b8899e53df81d461d6338f0515`, the follow-up to merged
Merkle commit `609a6939`. Its canonical Go pseudo-version is
`v1.5.4-0.20260919160701-e3c4e9c2f1e0`. The inspected `merklelog` client/policy,
trust validator, object validator, and SVS files match the reviewed upstream
implementation. Go remains 1.25.0. Change only dependency entries needed by this
revision; do not introduce a local `replace` or upgrade unrelated modules.

The removed `UseSignatureTime` and `IgnoreValidity` fields are replaced by
`OnCertExpired`. The policy requires exact `RawData`. Use upstream
`merklelog.NewCertExpiredPolicy`; do not implement another proof verifier,
certificate-time validator, or success fallback.

#### Upstream multi-segment validation fix

The Phase 3 integration fixture exposed a defect in the merged NDNd revision:
when `OnCertExpired` completes asynchronously, the object segment fetcher
validates segment zero but does not resume scheduling later segments. This stalls
large blobs and multi-segment snapshots after their first packet. It reproduces
with real history proofs; it is not a speculative failure path.

The minimal upstream correction is one `s.check()` call at the end of the
segment-validation callback in `std/object/client_consume_seg.go`, after either
successful or failed validation. The existing scheduler call remains in place so
other streams can progress while validation is pending. It landed as NDNd
`e3c4e9c2` on 2026-09-19. The pinned dependency passes the expired
multi-segment-object and compressed-snapshot-plus-following-publication fixtures;
Ownly carries no scheduler workaround or local module replacement.

#### Proposed behavior and review decisions

1. **Remove the Magic bypass.** Remove the switch, its preference writes, the
   `ignore` property from the TypeScript contract, and the argument through
   `Workspace.join`, `get_workspace`, and Go `GetWorkspace`. Update all browser
   and headless callers together. Existing stored `ignore` fields are inert;
   do not migrate or rewrite the workspace database just to delete them.
   Historical acceptance becomes proof-based whenever a log is configured.
2. **Keep historical boot transport.** Existing boot SVS is ordered, persistent
   history. Apply the history policy to its stored outer publications so a
   late reader can advance through old entries when evidence exists. Keep
   enclosed precertificate/enrollment checks, expired-certificate rejection,
   and new-join authorization strict. Preserve the retained invitation and
   precertificate capture dependencies for that transport.
3. **Keep live operations strict.** Awareness, live sync messages, directed
   refresh/reset replies, NDNCERT/testbed validation, repository commands, and
   log responses receive no history fallback. The capture cuts from Phase 2
   remain in effect.
4. **Keep metadata handling unchanged.** Snapshot-index discovery needs its
   metadata. Defer selective omission of unused metadata; add no name classifier
   or new producer mode for that optimization.
5. **Separate read configuration from producer activation.** Configuring a
   history reader must not populate the existing capture-enabling `historyLogID`.
   Keep a separate read configuration/policy. Phase 4 explicitly enables capture
   only when the sender and recovery loop are ready. Preserve the 1-second idle
   delay and 10-second maximum batch age.

Removing Magic means existing expired packets without timely evidence will fail
validation. Phase 3 cannot retroactively establish their signing time. Existing
current-validity content continues to work; test fixtures supply timely history
evidence without requiring a deployed log or enabling production submissions.

#### History policy boundary

Upstream `SvSyncOpts.OnCertExpired` is shared by live sync validation and ALO
publication fetching, and it overwrites the snapshot strategy's expiry option.
Setting that option globally would broaden the consumer scope.

Instead, add a small history-reader adapter that embeds `ndn.Client` and overrides
only `ConsumeExt` to provide the configured expiry policy. Use it for the
workspace's retained object consumers, boot ALO, and snapshot client. These paths
all call the supplied client's `ConsumeExt`; upstream then propagates the policy
and exact wire through object segments and metadata. Leave the SVS-wide option
nil. Direct `Validate`/`ValidateExt`, live sync validation, and command traffic
continue through the underlying client without a history callback.

This adapter selects the consumer boundary, not packet names. It does not inspect
or filter every produced packet. The existing producer capture wrapper remains
separate and dormant. The log object client must never receive either wrapper.

Add `RawData` from the existing wire at direct validation sites where available:
`certWire` in `verifyTestbedCert`, and `args.RawData` in the direct response and
precertificate validations. Keep their policy nil. Do not re-encode parsed Data
or treat a missing wire as permission to accept expiry.

#### Log trust configuration (revised 2026-09-19)

The user approved the history scope, preserving previously trusted expired
anchors, and removing Magic. The log reader follows repository trust exactly:
construct its trust configuration from `SchemaBytes` through `getTrustConfig`.
`schema.trust` defines the `/ndnd/merkle/32=check/<hash>` response name and the
allowed testbed site-operator certificate class. There is no Go trust-schema
wrapper and no Go-side operator identity. Updating either relationship requires
editing schema text and rebuilding `schema.tlv`.

Use the ordinary object client for strict log-response validation, and put the
history-reader adapter only on stored-history consumption. No recursive history
validation of log responses is allowed. The service endpoint remains separate
protocol configuration, like the repository endpoint; a mismatch with the LVS
rule fails validation.

The service prefix is deployment configuration, like the repository prefix. No
response-certificate import API, log-specific trust database, configuration UI,
or automatic discovery is needed. Keep producer capture disabled until Phase 4.
A local fixture provides the log and a chain under a test root for verification;
do not infer that the upstream sample service is deployed.

#### Preserve previously established anchor trust

Stop filtering expired anchors only when rebuilding the already-established local
and peer trust sets in `localTrustAnchorCertNames` and `peerTrustAnchorCertNames`.
Reuse the existing owned-key inventory and explicit peer index; keep exact stored
certificate wires and self-signature checks. Generic packet-cache contents do not
become trusted anchors. Explicit key/peer removal still removes that trust.

Keep fresh import/enrollment checks, active identity selection, and signer
selection unchanged. A previously accepted expired anchor may justify historical
Data only through the per-call expiry policy; it must not authorize new live
operations without current validity. Cover restart and exact certificate-version
lookup in the tests so warm caches do not hide missing evidence. Broad certificate
retention/compaction redesign and automatic renewal are outside this phase.

#### Planned file changes

| File | Change |
| --- | --- |
| `ndn/go.mod`, `ndn/go.sum` | Pin the merged NDNd revision and required checksums. |
| `ndn/app/schema.trust`, `ndn/app/schema.tlv` | Define the Merkle check response prefix and permitted operator certificate class in the common LVS, then compile it. |
| `ndn/app/history_reader.go` (one new runtime file) | Keep the endpoint, strict proof client, and small `ConsumeExt` history-reader adapter together; reuse the ordinary `SchemaBytes` client for log trust. |
| `ndn/app/app.go` | Remove the obsolete workspace boolean argument and leave producer activation for Phase 4. Keep producer activation off. |
| `ndn/app/workspace.go`, `boot_sync.go`, `testbed.go` | Remove obsolete options, bind the history consumer client to stored object/replay paths, preserve strict live/enrollment validation, and forward exact wire at direct calls. |
| `ndn/app/identity_keys.go` | Retain previously provisioned historical anchors during trust reconstruction, without relaxing import or signing checks. |
| `src/services/ndn.ts`, `workspace.ts`, `types.ts` | Remove bypass parameters and preference usage. |
| `src/components/home/WorkspaceCard.vue` | Remove Magic switch, handlers, preference writes, and its unused styles. |
| `src/components/home/CreateWorkspaceModal.vue`, `JoinWorkspaceModal.vue`, `src/node/headless.ts` | Update `Workspace.join` calls after removing the bypass argument. |
| Existing local Merkle test files | Extend focused boundary/trust/expiry cases; keep them ignored and outside commits. |
| `docs/history-verification.md` | Record approval, implementation, verification, and subagent review; keep uncommitted. |

One new runtime file is sufficient. No additional database, schema-generator
pipeline, configuration UI, retry manager, bootstrap protocol, or tracked test
infrastructure is required. Build the matching WASM/frontend pair for validation;
never test the new one-argument JS API against the old prebuilt WASM artifact.

#### Checks and exit criteria

- Current-validity object consumption works with no configured history reader;
  expired data fails. A legacy stored `ignore: true` cannot bypass validation.
- A fixture using the configured site-operator signer accepts a valid inclusion
  proof and timely ingestion. A current, correctly chained testbed-user signer,
  an expired site-operator signer, missing evidence, changed packet bytes, and
  ingestion outside certificate validity fail.
- The real ALO and snapshot paths receive the history policy through `ConsumeExt`,
  including HIDX metadata. Direct validation and live sync messages stay strict,
  including a chain with a current leaf and expired intermediate certificate.
- With fixture evidence recorded during certificate validity, a late reader can
  consume a compressed snapshot after expiry, then the subsequent individual
  publications. Missing or late evidence for either path prevents delivery and
  sequence advancement. Original update proofs alone cannot validate the snapshot.
- Old boot envelopes with evidence can reach delivery, while an expired enclosed
  enrollment credential is still rejected by the application checks.
- Restart with a previously accepted expired anchor preserves the original trust
  decision and verifies child evidence; a cached but never trusted certificate
  cannot become an anchor. Expired credentials remain unavailable for signing.
- Log-response validation cannot call its own history policy. Configuring the
  reader produces no pending outbox rows and starts no append sender.
- Run native race tests, Go/WASM tests, the matching WASM application build,
  frontend type checking/build, and affected lint checks. Exercise both browser
  and headless API signatures. Do not write tests that merely mirror removals.
- Have the subagent review correctness and minimality after implementation; record
  its findings and resolve them before presenting the phase as complete.

**Implemented, reviewed, and accepted 2026-09-19:** the user approved historical read scope,
preservation of expired trust anchors, and removal of Magic. The subsequent
trust-schema revision puts both the check prefix and operator class in
`schema.trust`, recompiles `schema.tlv`, and removes the Go response-schema
adapter. Native race tests, WASM history/snapshot replay, LVS compilation and
positive/negative checks, TypeScript checking, production build, and served-WASM
embedding checks passed. The final subagent review found no correctness or
minimality issues; it specifically confirmed that a current user certificate
issued by the valid site operator is rejected as a log responder. The served WASM
SHA-256 is `200c0164274b82a1eb2a18ea5d1997c69c6517780a5d6982dd5a34bef8ace69f`.
`npx eslint . --no-fix` reports one pre-existing unrelated `prefer-const` error
in `src/components/landing/HelpComponent.vue`; this phase leaves it untouched.
Keep this document uncommitted and Merkle tests locally ignored.

### Phase 4 — Connect producer submission and recovery

**Goal:** connect the durable outbox, collector, and queue to real authenticated
log appends. Phase 2 persistence is a prerequisite for enabling this producer path.

**Planned changes:** implement an adapter around NDNd `Client.Append`; map validated
results to queue acknowledgements; enforce encoded request and worst-case response
size bounds. Define the response-signing overhead bound from the accepted log
configuration. Split batches without losing order/age, add request deadlines and
capped retry backoff with jitter, and guard against late callbacks after timeout.
Own queue creation and cleanup at the user/log lifecycle, with explicit handling
of identity/workspace changes and reconnects. Dispatch from committed outbox rows
and commit durable dequeue before completing the in-memory batch.
Enable the existing capture hooks for live Yjs publications as well as internally
produced snapshots, indexes, metadata, blobs, and retained authorization packets.
No separate frontend Yjs logging hook is needed. Flush early for known certificate
deadlines without treating the 10-second batch-age limit as an ingestion guarantee.

**Checks and exit criteria:** a local log receives hashes under burst and continuous
production; requests fit the wire limit; mixed success/duplicate/failure results
retain exactly the unresolved hashes; delayed or lost responses can be retried
safely. Test outage capacity handling, sustained traffic during failures, and
orderly shutdown with a bounded drain. Kill and restart the producer during the
10-second batching window and after log acceptance but before durable dequeue;
confirm automatic recovery and safe duplicate handling against a real local log.
Verify that a live Yjs edit produces logged publication hashes before any snapshot
is created, and that snapshot creation submits its own distinct signed packet
hashes together with the index/discovery packets. A late reader must recover the
snapshot plus the subsequent updates after certificate expiry using those proofs.
Exclude logging packets from collection. Document that durable recovery and queue
readiness do not guarantee ingestion before certificate expiry. Complete Go/WASM
and subagent checks.

### Phase 5 — Historical replay and failure handling

**Goal:** finish validation coverage across application history and authorization
chains, and make failures recoverable and understandable.

**Planned changes:** exercise and close gaps in snapshot/index/metadata consumption,
blob replay, certificate and CertList validation, and key renewal. Review
`workspace.go`, `testbed.go`, `boot_sync.go`, and `src/services/svs-provider.ts` for
propagation of validation or persistence failure. Adjust application progress and
error reporting where needed so rejected history is not silently skipped or shown
as verified. Add targeted tests rather than another proof implementation.

**Checks and exit criteria:** expired Data with timely authenticated evidence
succeeds through the real validation path; absent evidence, altered wire, invalid
proofs, wrong log signers, invalid Data signatures, and out-of-period ingestion
fail. Test recursive expired chains, compressed snapshot packets, missing-key
recovery, and retry after application storage failure. Define legacy-data behavior
explicitly: late logging does not establish earlier existence. Complete the
subagent review and affected Go/WASM/frontend checks.

### Phase 6 — End-to-end validation and rollout documentation

**Goal:** demonstrate the complete producer-to-log-to-consumer flow and document
the supported deployment and limitations.

**Planned changes:** add focused integration fixtures and a local log setup guide;
update this document with configuration, trust provisioning, startup/shutdown,
legacy history behavior, and diagnostic guidance. Add CI coverage appropriate to
the new packages and WASM integration in `.github/workflows/ci.yml` if reproducible
without private credentials or deployed infrastructure.

**Checks and exit criteria:** cover live edits, a large segmented blob, snapshot
catch-up, forced termination during batching and append, producer/consumer restart,
certificate expiry/renewal, and log outages. Verify browser producer persistence
and the supported headless consumer path. Record the trusted-operator assumption,
durable outbox recovery, and deferred auditor work. Run the final applicable test
suite once, resolve subagent findings, and report the evidence and remaining
limitations. Deployment is a separate action and requires explicit authorization.
