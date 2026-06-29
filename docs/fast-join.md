# Fast-Join — Design Specification

**Status:** implemented on branch `dev` (uncommitted)
**Bundle format version:** `v: 4`
**Issuer-id for ephemeral certs:** `ephemeral`

A single URL is sufficient to invite a brand-new device to a workspace, with the trust chain ending at the owner's identity cert (which the invitee promotes as a local trust anchor on first use, adding it to a multi-rooted trust set). The legacy PSK is carried in-band for backward compatibility with workspace content encrypted before the invitee joined. **Fast-join does NOT carry a cross-schema invitation wire** — the ephemeral cert chains to a trust root via the regular LVS schema, making the cross-schema redundant. The legacy invite flow (`sign_and_pub_invitation`) still publishes cross-schema invitations under `/<wksp>/32=boot/32=INVITE/<user>/v=<ts>` for backward compat with bundles issued before v: 4.

---

## 1. Motivation

The previous invitation flow required the invitee to receive an out-of-band PSK and to trust the workspace anchor transitively through a precert signed by the owner. This had two problems:

1. **PSK must be communicated out-of-band.** The PSK is the only thing that lets a new device decrypt existing workspace SVS state.
2. **Trust is implicit.** The invitee trusts the workspace because the owner signed the user's precert. There is no in-band proof that the owner is who they claim to be.

Fast-join fixes both: a single URL carries the owner's identity cert (in-band provenance), an ephemeral identity signed by the owner (in-band trust binding), and the legacy PSK (in-band decryption key). The cross-schema invitation wire that was originally part of the bundle is no longer needed — the ephemeral cert chains to a trust root via the regular LVS schema rule, making a fallback cross-schema redundant. Legacy invites (pre-v:4 bundles) still carry and use the cross-schema.

**Non-goals:** fast-join does not change the MLS state machine — only the trust bootstrap. It is owner-only; members cannot invite. The PSK stays in-band; rotation is a separate problem.

---

## 2. The bundle

### 2.1 Bundle wire shape

The bundle is a `fastJoinIdentity` struct in Go ([workspace.go:1069-1083](ndn/app/workspace.go#L1069)) with four binary fields, exposed to JS via `toJs()`:

| Go field | JS field | Bytes (approx) | What it is |
|---|---|---|---|
| `OwnerCert` | `owner_cert` | ~420 | Signed NDN Data packet. Owner's self-signed identity cert (`#IDCERT`). |
| `EphemeralSecret` | `ephemeral_secret` | ~150 | Signed NDN Data packet carrying the private key bytes (see §2.1.1). |
| `EphemeralCert` | `ephemeral_cert` | ~440 | Signed NDN Data packet. Ephemeral identity cert (issuer-id `ephemeral`, signed by `OwnerCert`). |

#### 2.1.1 Why the secret key is wrapped in an NDN Data packet

`EphemeralSecret` is **not** just the raw PKIX `ECPrivateKey` DER — it's a full signed NDN Data packet produced by `sig.MarshalSecret` ([`std/security/signer/marshal.go:29-54`](.../marshal.go#L29)):

```go
data, _ := spec.Spec{}.MakeData(name, &ndn.DataConfig{
    ContentType: optional.Some(ndn.ContentTypeSigningKey),
}, enc.Wire{sk}, key)
return data.Wire, nil
```

The Data packet's `Name` is `<invitee>/KEY/<ek-rand>` (the ephemeral key name), `ContentType = 0x04 = ContentTypeSigningKey`, `Content` is the raw PKIX `ECPrivateKey` DER, and it's **self-signed** by the ephemeral key itself.

Three reasons the wire must be a Data packet:

1. **Self-describing name.** Without the Data wrapper the receiver wouldn't know which key the bytes belong to — the cert (`EphemeralCert`) and the secret (`EphemeralSecret`) must agree on `<invitee>/KEY/<ek-rand>`, and that name travels in the Data's `Name` field.

2. **Well-formedness proof.** The self-signature lets `UnmarshalSecret` verify the bytes parse as a valid key and the cert actually signs the same key — without it, an attacker could substitute a junk key with the matching name.

3. **Pipeline uniformity.** The keychain stores private keys in exactly this wire format (`keychain_dir.go:239` writes via `sec.PemEncode(MarshalSecret(...))`), the WASM JS shim's `KeyChainJS.InsertKey` calls the same `MarshalSecret`, and `importFastJoinIdentity` decodes with `security.DecodeFile(secret)` → `UnmarshalSecret`. Using a different wire format for fast-join would require a parallel code path.

`UnmarshalSecret` ([marshal.go:67-101](.../marshal.go#L67)) explicitly enforces the shape: it checks `ContentType == ContentTypeSigningKey`, verifies a signature is present, and only then calls the algorithm-specific decoder (`ParseEcc` for P-256). So the wire MUST be a Data packet — raw DER bytes would fail this check.

### 2.2 URL shape

The bundle is wrapped in a JSON envelope with all binary fields base64url-encoded, then base64url-encoded again, then placed in the URL fragment `#fj=...`:

```
https://ownly.work/join/<escaped-wksp>/#fj=<base64url(json-envelope)>
```

```ts
// src/services/fast-join.ts
type FastJoinBundle = {
  v: 4;
  label: string;
  wksp: string;
  psk: string;
  ownerCert: Uint8Array;
  ephemeralSecret: Uint8Array;
  ephemeralCert: Uint8Array;
};
```

`v: 4` is the bundle format version; `parseFastJoinBundle` rejects other versions. The fragment (not query string) keeps the bundle out of HTTP server logs. `v: 3` bundles (which carried an `invitation` field) are rejected by the version check.

### 2.3 The `ephemeral` issuer-id (vs `identity`)

The ephemeral cert uses a dedicated issuer-id `ephemeral`, not `identity`. This is structurally distinct from any self-signed identity cert the user may have. The LVS schema defines a dedicated macro and rule:

```
#EPHEMERAL: "KEY"/_/"ephemeral"/_
#ephemeral_cert: #user/#EPHEMERAL <= #ownerid_cert
#user_precert:  #owner/wksp/#user/#PREKEY <= #ephemeral_cert
```

The rename means:

- `localIdentityEntries` (which filters on `At(-2) == identityIssuer`) automatically excludes ephemeral certs. The keychain UI shows only the user's real identities, never starter keys.
- Peer-index cleanup can audit "all ephemeral certs under `/<invitee>/KEY/_/ephemeral/_`" without false positives against self-signed identity certs. This eliminates the stale-cert ambiguity the previous design had.
- The two LVS rules `#ownerid_cert` and `#ephemeral_cert` are disjoint by namespace.

The Go constant is `ephemeralIssuer` in [identity_keys.go:22](ndn/app/identity_keys.go#L22). `makeOwnerSignedIdentityCert` uses it ([identity_keys.go:270](ndn/app/identity_keys.go#L270)). `importFastJoinIdentity` validates against it ([identity_keys.go:424](ndn/app/identity_keys.go#L424)). The piggybacked cert in `BootJoin.InviteeIdCert` is also validated against it ([boot_sync.go:59](ndn/app/boot_sync.go#L59)).

---

## 3. The trust model

### 3.1 LVS rules used by fast-join

From [ndn/app/schema.trust](ndn/app/schema.trust) (compiled to [ndn/app/schema.tlv](ndn/app/schema.tlv) via python-ndn, see [ownly-trust-schema-build](../.claude/projects/-Users-tianyuan-Documents-Work-ownly/memory/ownly-trust-schema-build.md)):

```
#KEY:       "KEY"/_/_/_
#TC:        "KEY"/_/"NDNCERT"/_
#ANCHOR:    "KEY"/_/"self"/_
#PREKEY:    "KEY"/_/"pre"/_
#IDCERT:    "KEY"/_/"identity"/_
#EPHEMERAL: "KEY"/_/"ephemeral"/_       # NEW
#ANSI:      "KEY"/_/"anchor"/_
#owner:     "ndn"/owner10 | ... 6-deep
#user:      "ndn"/user10 | ... 6-deep

# Trust anchors
#testbed_root_cert:  /"ndn"/#KEY
#testbed_owner_cert: #owner/#TC <= #testbed_site_cert | #testbed_root_cert
#testbed_user_cert:  #user/#TC  <= #testbed_site_cert | #testbed_root_cert

# Owner identity, user identity, and fast-join ephemeral identity
#ownerid_cert:    #owner/#IDCERT
#userid_cert:     #user/#IDCERT                       # NEW: legacy self-signed user identity
#ephemeral_cert:  #user/#EPHEMERAL <= #ownerid_cert   # NEW: fast-join starter

# Workspace anchor (created by owner at workspace setup)
#wksp_anchor:    #owner/wksp/#ANCHOR

# Mutual authentication
#wksp_cl:        #owner/wksp/#CL          <= #wksp_anchor
#wksp_precert:   #owner/wksp/#PREKEY      <= #ownerid_cert
# Precert may be signed by EITHER the user's own self-signed identity cert
# (legacy path: user already has a long-term #IDCERT) OR an ephemeral cert
# (fast-join path: user has a starter #EPHEMERAL signed by owner's ownerid_cert).
# LVS pattern variables (#user) are shared between data and cert names during
# validation, so the signer must be for the SAME <user> as the precert itself.
#user_precert:   #owner/wksp/#user/#PREKEY <= #userid_cert | #ephemeral_cert

# Final certificates
#owner_cert:     #owner/wksp/"32=owner"/#ANSI <= #wksp_anchor
#user_cert:      #owner/wksp/#user/#ANSI      <= #wksp_anchor

# Boot group
#boot_data:      #owner/wksp/"32=boot"/"32=owner"/_/_ <= #owner_cert
#boot_data:      #owner/wksp/"32=boot"/#user/_/_      <= #user_precert | #user_cert
```

### 3.2 Cert name shapes (with concrete examples)

All examples below use Alice (owner at `/ndn/site/alice`) and Bob (invitee at `/ndn/site/bob`), workspace `/ndn/site/alice/wksp/MarketingTeam`.

| Cert | Name | Issuer-id | Signer | Rule satisfied |
|---|---|---|---|---|
| Alice's identity cert | `/ndn/site/alice/KEY/<alice-kid>/identity/<v>` | `identity` | Alice (self) | `#ownerid_cert` |
| Bob's identity cert (legacy) | `/ndn/site/bob/KEY/<bob-kid>/identity/<v>` | `identity` | Bob (self) | `#userid_cert` |
| Alice's pre-anchor | `/<wksp>/KEY/<root-kid>/pre/<v>` | `pre` | Alice's identity | `#wksp_precert` |
| Workspace anchor | `/<wksp>/KEY/<root-kid>/self/<v>` | `self` | Anchor key (self) | `#wksp_anchor` |
| Alice's owner cert | `/<wksp>/"32=owner"/KEY/<owner-kid>/anchor/<v>` | `anchor` | Workspace anchor | `#owner_cert` |
| Bob's ephemeral cert (fast-join only) | `/<user>/KEY/<ek-rand>/ephemeral/<v>` | `ephemeral` | Alice's identity | `#ephemeral_cert` |
| Bob's precert | `/<wksp>/<user>/KEY/<user-kid>/pre/<v>` | `pre` | Bob's `#IDCERT` (legacy) OR Bob's ephemeral key (fast-join) | `#user_precert` |
| Bob's final cert | `/<wksp>/<user>/KEY/<user-kid>/anchor/<v>` | `anchor` | Workspace anchor | `#user_cert` |

### 3.3 Trust chain after a fast-join (Bob's keychain view)

`TrustConfig` is **multi-rooted** — every self-signed cert Bob knows about becomes a trust anchor via `PromoteAnchor`. After a fast-join, Bob's keychain has all of the following as **independent trust roots** (any one of them can start a chain):

```
Bob's trust roots (set by trust_config.go + promoted by PromoteAnchor at every GetWorkspace):
├── testbed_root_cert (built-in, always present from app.go:124)
├── Bob's own #IDCERT (self-signed, promoted via promoteIdentityAnchors)
├── Alice's ownerid_cert (self-signed, PROMOTED via importFastJoinIdentity line 401)
├── (any other self-signed certs Bob happens to have)
```

Alice's identity cert being promoted as a trust root is what makes fast-join work without requiring Alice to be testbed-chained to Bob — **but the testbed chain remains valid as a parallel path**. Validation succeeds if **any** root leads to a valid signed chain.

The full chain of certs in Bob's keychain after fast-join, drawn as signing relations:

```
                                        ┌──────────────────────────────────────┐
                                        │ Bob's trust roots (independent)      │
                                        │ • testbed_root_cert                  │
                                        │ • Bob's #IDCERT (always promoted)    │
                                        │ • Alice's #IDCERT (PROMOTED in step │
                                        │   7.7.2 — fast-join adds this)       │
                                        └──────────────────────────────────────┘
                                                          │
                ┌─────────────────────────────────────────┼─────────────────────────────────────┐
                │                                         │                                     │
                ▼                                         ▼                                     ▼
  testbed chain                       Bob's own-id chain (legacy)         Alice's ownerid chain (fast-join)
       │                                       │                                     │
       ▼                                       │                                     │
  Alice's testbed_owner_cert                  │                                     │
  /ndn/site/alice/KEY/<alice-tb-kid>          │                                     │
  /NDNCERT/<alice-tb-v>                       │                                     │
  (NDNCERT-issued)                            │                                     │
       │ (signed by testbed root)             │                                     │
       ▼                                       │                                     │
  Alice's ownerid_cert                        │                                     │
  /ndn/site/alice/KEY/<alice-kid>             │                                     │
  /identity/<alice-v>                          │                                     │
       │ (self-signed, but matches              │                                     │
       │  #ownerid_cert rule — also a root)      │                                     │
       │                                       │                                     │
       ├─────── (fast-join adds) ──────────────┼─────────────────┐                   │
       │                                       │                 │                   │
       ├──► ephemeral_cert ────────────────────┼──► user_precert │ ──► user_cert     │
       │    (Bob's starter, owner-signed,        │  (signed by     │  (anchor-signed,  │
       │     only present after fast-join)       │   ephemeral key,│   180d)           │
       │                                         │   no cross-schema)               │
       │                                         │                 │                   │
       │                                       Bob's #IDCERT ─────┘                   │
       │                                       (legacy precert                     │
       │                                        signer, no                           │
       │                                        ephemeral needed)                    │
       │                                                                     │
       └──► pre-anchor ──► anchor ──┬──► owner_cert (Alice's, anchor-signed)         │
            (identity-signed)        │                                              │
                (root signer)        │                                              │
                                     └──► user_cert (Bob's, anchor-signed, 180d)    │
                                          (KeyLocator = anchor cert)                │
                                                                                    │
                                     cert-list at /<root-key>/32=auth/<v>           │
                                     (CL rule, anchor-signed)                       │
```

The `#user_precert` rule accepts EITHER signer:

```
#user_precert: #owner/wksp/#user/#PREKEY <= #userid_cert | #ephemeral_cert
```

The `<user>` LVS variable is shared between the data name and cert name during validation (see `lvs.go:215` — `s.Match(key, pktCtx)` reuses the data's context). So the signer cert must be for the same `<user>` as the precert itself.

Four important observations:

1. **The same Alice's `ownerid_cert` appears as both a child of `testbed_owner_cert` AND as an independent trust root.** The schema `#testbed_owner_cert: #owner/#TC <= #testbed_site_cert | #testbed_root_cert` describes the testbed chain; `#ownerid_cert: #owner/#IDCERT` is a separate rule with no signer constraint (the cert is self-signed and is a root). Promotion via `PromoteAnchor` makes the second path available without the first.

2. **`signPreCert` is the SAME function for both paths** ([workspace.go:1017-1067](ndn/app/workspace.go#L1017)). It accepts whatever signer is passed in and signs the precert with it. The only difference is which signer is selected:
   - **Legacy path**: the normal local identity signer is used.
   - **Fast-join path**: `import_fast_join_identity` imports the owner-signed EK cert, then Go asks the trust schema for a signer under `/<wksp>/32=KD/32=fast/<user>`, which resolves to the ephemeral signer.
   The schema rule `#user_precert <= #userid_cert | #ephemeral_cert` validates either signer against the same precert name pattern.

3. **`signPreCert` optionally attaches a `CrossSchema` to the precert** ([workspace.go:1053](ndn/app/workspace.go#L1053)). In the **legacy** path, a cross-schema invitation wire (published by `sign_and_pub_invitation`) is attached — this was historically what made the precert validate against cross-namespace signatures. In the **fast-join** path, `invitation` is nil and the precert has no `CrossSchema` field; the schema rule `#user_precert <= #userid_cert | #ephemeral_cert` validates via the regular trust path using the LVS context binding between the data name and cert name (see `lvs.go:215` — `s.Match(key, pktCtx)` reuses the data's context variables).

4. **The `pre-anchor` is signed by the owner's identity cert** (not the anchor). The anchor is then **self-signed**. This means the chain from the workspace anchor back to a root goes anchor → pre-anchor → ownerid_cert (a root), NOT anchor → ownerid_cert directly. The `#wksp_precert: #owner/wksp/#PREKEY <= #ownerid_cert` rule exists to validate this.

### 3.4 Cert name vs signer — three roles to keep straight

Three distinct things can sign a cert/data packet, and the validator distinguishes them by `At(-2)` of the **KeyLocator** in the signature info:

- **Self-signed**: KeyLocator points at the cert itself (or a no-version sibling). Used for `pre-anchor` (KeyLocator = `ownerid_cert`), `anchor` (KeyLocator = anchor-self), `identity` certs (KeyLocator = identity-self).
- **Owner-signed**: KeyLocator points at an `ownerid_cert`. Used for `ephemeral_cert` (Bob's starter) and `pre-anchor` (Alice's anchor key).
- **Anchor-signed**: KeyLocator points at a `wksp_anchor` cert. Used for `owner_cert` and `user_cert` (final 180-day certs).

### 3.5 How `TrustConfig.Validate` uses the multi-rooted set

The validator ([`std/security/trust_config.go:146-392`](.../trust_config.go#L146)) walks up the signing chain from the leaf cert toward any of the registered roots. The schema check (`schema.Check`) decides whether a given (packet, cert) pair is allowed at each step. The validator succeeds if **any** root is reachable — it does not require a single canonical chain.

This is why fast-join works without Alice having a testbed chain to Bob: even if Bob has never seen Alice's testbed cert, the local promotion of her `ownerid_cert` as a root gives the validator an alternative path. The two paths are not in conflict; the schema rules are designed so that either chain satisfies the same downstream rules.

---

## 4. Owner-side issuance

### 4.1 Trigger

`WorkspaceInviteManager.tryFastInvite(invitee, router)` at [workspace-invite.ts:839-857](src/services/workspace-invite.ts#L839), called from `InvitePeopleModal.vue` after the owner adds an invitee and clicks "Generate Invites".

### 4.2 The two-phase issuance

**Phase A — JS-side cleanup:**
1. Assert caller is workspace owner (`wsmeta.owner`).
2. `bootstrapOwnerMls()` — no-op if MLS group exists; creates one if needed so the master device has a real epoch to invite against.
3. Reject duplicates: throw if `inviteeProfiles` already contains the invitee name.

**Note:** no explicit TS-side peer-cert cleanup is needed before calling `make_fast_join_invitation`. `makeFastJoinIdentity` ([workspace.go:1106](ndn/app/workspace.go#L1106)) itself calls `forgetPeerIdentityForGroup(invitee, bootGroup, certData.Name())` with the freshly-signed cert as the `keepCert` argument, which removes all other peer certs under the invitee's identity in the boot group while preserving the new one. (A prior TS-side `deletePeerIdentityEntries(invitee.name)` call was redundant and has been removed.)

**Phase B — Go-side bundle construction** ([workspace.go:937-962](ndn/app/workspace.go#L937)):

```go
fast, err := a.makeFastJoinIdentity(wkspName, invitee, identitySigner) // (1)
a.publishPendingBootPeers()                                          // (2)
a.ExecWithConnectivity(func() {
    a.NotifyRepoJoin(client, bootGroup, dataPrefix, false)           // (3)
})
// No cross-schema invitation. The ephemeral cert chains to the owner's
// #ownerid_cert (a trust anchor on the invitee side), so the regular
// schema rule #user_precert <= #userid_cert | #ephemeral_cert validates
// the precert without needing a fallback cross-schema. Legacy invites
// still go through sign_and_pub_invitation + signPreCert with the
// cross-schema attached, for backward compatibility.
return fast.toJs(), nil                                              // (4)
```

Step (1) `makeFastJoinIdentity` ([workspace.go:1103-1151](ndn/app/workspace.go#L1103)) generates the P-256 ephemeral key, signs its identity cert with the owner's identity cert using the `ephemeral` issuer-id, drops stale peer certs, and registers the new cert in the peer index. Returns `fastJoinIdentity{OwnerCert, EphemeralSecret, EphemeralCert}`.

Step (2) `publishPendingBootPeers` republishes the peer index entries to SVS so the new ephemeral cert is visible to the rest of the workspace.

Step (3) `NotifyRepoJoin` publishes a fresh `SecurityConfigObject` (LVS schema + anchors) and tells the repo to `SyncJoin` the boot group. The repo joins in passive mode and starts mirroring.

Step (4) Returns the three-field bundle to JS (no invitation).

### 4.3 URL construction (TypeScript)

[workspace-invite.ts:930-948](src/services/workspace-invite.ts#L930):

```ts
const fastJoin = serializeFastJoinBundle({
    v: 4, label, wksp, psk,
    ownerCert, ephemeralSecret, ephemeralCert,
});
const inviteHref = router.resolve({
    name: 'join', params: { space },
    hash: `#fj=${fastJoin}`,
}).href;
return `${window.location.origin}${inviteHref}`;
```

`InvitePeopleModal.vue:486-489` copies all generated links to the clipboard as `email: url` lines.

---

## 5. Invitee-side import

### 5.1 Modal detection and form

`JoinWorkspaceModal.vue` opens on `/join/:space/`. The watcher at [JoinWorkspaceModal.vue:96-110](src/components/home/JoinWorkspaceModal.vue#L96) detects a fast-join link:

```ts
const fast = route.query.fj ?? new URLSearchParams(route.hash.slice(1)).get('fj');
if (typeof fast === 'string' && fast) {
    const bundle = parseFastJoinBundle(fast);
    fastJoin.value = bundle;
    opts.value.name = bundle.wksp;
    opts.value.label = bundle.label;
    opts.value.psk = bundle.psk;
}
```

When `fastJoin` is set, the modal hides the NDN Name and PSK inputs and shows only the editable Label field. Submit triggers `import_fast_join_identity` then `Workspace.join`.

### 5.2 `import_fast_join_identity` ([identity_keys.go:378-503](ndn/app/identity_keys.go#L378))

The six-step validation flow:

1. **Parse and validate the owner cert.** Read as NDN Data, check `ContentType == ContentTypeKey`, check not expired, check **self-signed** (`sig.ValidateData(data, sigCov, data)`).

2. **Insert owner cert into keychain** if not already present, then **`PromoteAnchor`** — this is the load-bearing step. `a.trust.PromoteAnchor(ownerCertData, enc.Wire{ownerCertWire})` adds Alice's identity cert to the multi-rooted trust set (see §3.3).

3. **Decode the ephemeral private key** via `security.DecodeFile(secret)`. The wire is a self-signed NDN Data packet (see §2.1.1) with `ContentType = ContentTypeSigningKey`; `DecodeFile` calls `UnmarshalSecret` internally and returns `[]ndn.Signer`. Find the one whose `KeyName()` matches the ephemeral cert's key name.

4. **Validate the ephemeral cert:**
   - `ContentType == ContentTypeKey` ✓
   - Not expired ✓
   - `At(-2) == ephemeralIssuer` ✓ (the dedicated issuer-id, see §2.3)
   - `Signature().KeyName() == ownerCertData.Name()` — KeyLocator points at owner cert
   - Cryptographically verify: `sig.ValidateData(certData, sigCov, ownerCertData)` ✓
   - **Cert identity** is the invitee's NDN name

5. **Insert key + cert** into keychain (skip key insert if identity+key already exists).

6. **Store the invitation wire (optional)** — if a non-empty `invitationWire` was passed (legacy flow only; fast-join passes empty bytes), parse as NDN Data, check name is under `/<wksp>/32=boot/32=INVITE/<invitee>...`, store at exact name so `wait_user_key` can fetch the latest version. The fast-join path skips this step entirely.

Return an `identityEntry{Identity, KeyName, CertName, HasPrivate: true, Source: "local"}`. The TS caller does not pass the cert name back as a hint; signer selection is schema-assisted on the Go side.

### 5.3 `Workspace.join` and the rest of the bootstrap

`Workspace.join(label, name, create=false, ignore=false, pskBuf, null)` ([workspace.ts:531-585](src/services/workspace.ts#L531)) calls `ndn.api.get_workspace(name, ignore)`.

`GetWorkspace` ([workspace.go:233-389](ndn/app/workspace.go#L233)):

1. `getIdentitySignerForWorkspace(wkspName)` first tries `trust.Suggest(/<wksp>/32=KD/32=fast/<candidate-user>)`; for a fast-join invitee this resolves Bob's ephemeral signer from the imported owner-signed EK cert. If no fast-join signer exists, it falls back to the normal local identity signer.
2. `trust.Suggest` fetches `/<wksp>/32=KD`, `/<wksp>/32=RD`, `/<wksp>/32=PD` via the object client (network fetch; the repo serves from its mirror).
3. **Participant branch** (Bob is not owner):
   - Look up local invitation under `/<wksp>/32=boot/32=INVITE/<idName>` (may be nil for fast-join participants; legacy participants have one published via `sign_and_pub_invitation`).
   - `signPreCert(wkspName, identitySigner=bobEphemeralSigner, invitation)` builds Bob's workspace key, signs a precert under `/<wksp>/<user>/KEY/<kid>/pre/<v>` with `IssuerId: "pre"`, NotAfter=14d. The `invitation` wire is attached as the cert's `CrossSchema` field if present (legacy only); fast-join passes nil and the precert has no `CrossSchema` field.
4. `StartBootSyncParticipant` publishes the precert to the boot-sync SVS group.

### 5.4 Boot-sync final cert issuance

The owner's `ownerSub` ([boot_sync.go:324-487](ndn/app/boot_sync.go#L324)) processes the precert:

- Schema-validates the precert via `client.ValidateExt`. The `#user_precert <= #userid_cert | #ephemeral_cert` rule succeeds because:
  - In the **fast-join** case, the precert is signed by Bob's ephemeral key (`#ephemeral_cert` rule), and the ephemeral cert is in the owner's local keychain (received via boot-sync peer-index sync).
  - In the **legacy** case, the precert is signed by Bob's own self-signed `#IDCERT` (`#userid_cert` rule), which is a self-signed root in Alice's keychain.
  - Both cases satisfy the alternation. The `<user>` LVS context variable ensures the signer cert is for the same `<user>` as the precert.
- Calls `SignFinalCert(preWire, rootSigner)` ([boot_sync.go:563-578](ndn/app/boot_sync.go#L563)):

  ```go
  return security.SignCert(security.SignCertArgs{
      Data:      certData,              // = Bob's user-key Data packet (the precert content)
      Signer:    rootCtxSigner,         // = workspace anchor with KeyLocator = anchor cert
      IssuerId:  enc.NewGenericComponent("anchor"),
      NotBefore: time.Now().Add(-time.Hour),
      NotAfter:  time.Now().AddDate(0, 0, 180),
  })
  ```

  Result: Bob's user-key Data packet re-signed with the workspace anchor, issuer-id `anchor`, 180-day validity. The resulting cert name is `/<wksp>/<user>/KEY/<kid>/anchor/<v>`, satisfying `#user_cert <= #wksp_anchor`.

- The owner inserts the final cert into their local keychain, stores it in the local packet store, and publishes it back to the boot-sync SVS group.
- Bob's `participantSub` receives the published final cert, validates it, and inserts it into Bob's keychain.

### 5.5 DSK round-trip and MLS join

Once Bob has the final cert:

- DSK exchange ([workspace.ts:595-628](src/services/workspace.ts#L595)): Bob posts a `DSKRequest` (X25519 ephemeral pubkey) on `/<wksp>/root/32=DSK/...`. Alice's owner-subscriber encrypts the DSK with X25519+HKDF+AES-GCM and publishes the response. Bob installs the AEAD cipher via `set_encrypt_keys(psk, dsk)`.
- MLS key package ref published to SVS. Alice's master device calls `OpenMlsLiteGroup.addMembers([bobKp])`. Bob receives the Welcome, joins the MLS group. Epoch advances.

Bob's workspace is now active.

---

## 6. URL, modal, and frontend flow

- **Trigger:** [JoinWorkspaceModal.vue](src/components/home/JoinWorkspaceModal.vue) opens from `/join/:space/`.
- **Detection:** parses `#fj=...` or `?fj=...` (the former is the canonical form; the latter is a fallback for chat clients that strip fragments).
- **Form:** when `fastJoin` is set, only the Label input is visible. Submit calls `import_fast_join_identity` then `Workspace.join(label, name, false, false, pskBuf, null)`.
- **Result:** navigates to `/<wksp>/` and shows the workspace dashboard.

The bundle URL is single-use in spirit (each issuance creates a fresh ephemeral cert) but is not replay-protected — see §8.

---

## 7. Worked example

### 7.1 Cast and starting state

- **Alice** owns `/ndn/site/alice`. Her self-signed identity cert is `/ndn/site/alice/KEY/<alice-kid>/identity/<alice-v>`.
- **Bob** is at `/ndn/site/bob`. He's never been in this workspace.
- **Workspace:** `/ndn/site/alice/wksp/MarketingTeam`.
- Workspace has PSK `<psk>`, DSK `<dsk>`, MLS group epoch 1 with Alice as sole member.
- Boot-sync SVS `/<wksp>/32=boot/32=svs/32=v=3` running, owner publisher `32=owner`. Repo at `/ndnd/ucla/repo3` is mirroring.

Bob's keychain (T=0): testbed root cert, Bob's NDNCERT testbed cert, Bob's self-signed identity cert. No workspace keys.

### 7.2 T = 0: Alice invites Bob

Alice types `/ndn/site/bob` in `InvitePeopleModal` and clicks Generate. `tryFastInvite` runs `bootstrapOwnerMls` (no-op) and `deletePeerIdentityEntries(bob)` (no-op for first-time invitee).

### 7.3 T ≈ 5 ms: Go-side bundle generation

`make_fast_join_invitation(wkspName, invitee=/ndn/site/bob)` runs:

```go
// makeFastJoinIdentity:
keyName      := security.MakeKeyName(/ndn/site/bob)
              = /ndn/site/bob/KEY/8b3f9c2e1a0d4756  (8 random bytes)
ephemeralSigner := sig.KeygenEcc(keyName, P-256)
ownerCertName := /ndn/site/alice/KEY/<alice-kid>/identity/<alice-v>
ownerCertWire := (read from local store, ~420 bytes)
ephemeralCert := makeOwnerSignedIdentityCert(ephemeralSigner, aliceSigner, ownerCertName)
                = signed Data packet, IssuerId="ephemeral", NotAfter=+14d, signed by Alice
                → /ndn/site/bob/KEY/8b3f9c2e1a0d4756/ephemeral/<bob-ek-v>
secretWire    := sig.MarshalSecret(ephemeralSigner)
                = self-signed Data packet, Name=/ndn/site/bob/KEY/8b3f9c2e1a0d4756,
                  ContentType=ContentTypeSigningKey, content=PKIX ECPrivateKey DER
                // Self-signature proves the wire is well-formed and binds the
                // name to the key bytes. See §2.1.1 for why the wire must be
                // a Data packet rather than raw DER.
bootGroup     := /<wksp>/32=boot
// forgetPeerIdentityForGroup: no-op
// rememberPeerIdentityCert: peer index gets ephemeral cert under bootGroup

publishPendingBootPeers:
  → SVS publication: ephemeral cert as Data packet under
    /<wksp>/32=boot/<alice>/<boot-ts>/<seq>/32=meta

NotifyRepoJoin:
  → publish SecurityConfigObject (LVS schema + Alice's owner cert + workspace anchor)
  → express RepoCmd{SyncJoin: ...} to /<wksp>/32=repo-cmd/32=syncjoin
  → repo joins boot group in passive mode, mirrors state

// No cross-schema invitation wire. The ephemeral cert chains to Alice's
// ownerid_cert (a trust anchor on Bob's side after import_fast_join_identity
// promotes it). The schema rule #user_precert <= #userid_cert | #ephemeral_cert
// validates the precert via the regular trust path — no cross-schema fallback
// needed. Legacy invites still use sign_and_pub_invitation + signPreCert with
// a cross-schema attached; that's the backward-compat path.
      Signer: signer,
      Content: CrossSchemaContent{
          SimpleSchemaRules: [{ NamePrefix: preCertNameRule,
                                KeyLocator: /ndn/site/bob/KEY }],
      },
      NotAfter: now + 50y,
  }
  → publish via bootSyncSession.alo.Publish(RepoCmd.BlobFetch{Data: [wire]}.Encode())

Bundle returned to JS (3 fields, no invitation):
  { owner_cert: 420B, ephemeral_secret: 150B, ephemeral_cert: 440B }
```

### 7.4 T ≈ 20 ms: URL construction

```ts
serializeFastJoinBundle({v:4, label, wksp, psk, ownerCert, ephemeralSecret, ephemeralCert})
// JSON-encode all fields (binaries as base64url), then base64url the whole JSON
// Result: ~1700 bytes of base64url text (smaller than v: 3 — no invitation field)
const url = `${origin}/join/MarketingTeam/#fj=${bundle}`
```

Alice pastes the URL into a chat to Bob.

### 7.5 T = days later: Bob clicks the URL

`JoinWorkspaceModal.vue` opens. The watcher detects `#fj=`, calls `parseFastJoinBundle`, sets `fastJoin.value = bundle`, prefills label/wksp/psk, hides NDN name and PSK inputs.

### 7.6 T ≈ +50 ms: Bob clicks Join

```ts
await ndn.api.import_fast_join_identity(
    '/ndn/site/alice/wksp/MarketingTeam',
    bundle.ephemeralSecret, bundle.ephemeralCert, bundle.ownerCert, new Uint8Array(),
);

Workspace.join(label, name, false, false, pskBuf, null);
// → ndn.api.get_workspace(name, false)
```

### 7.7 T ≈ +80 ms: Go-side import

`importFastJoinIdentity` runs the 6-step validation:

1. Parse owner cert Data packet. `ContentType == ContentTypeKey` ✓. `!CertIsExpired` ✓. `sig.ValidateData(data, sigCov, data)` (self-signed) ✓.
2. `keychain.InsertCert(ownerCertWire)`. **`trust.PromoteAnchor(ownerCertData, enc.Wire{ownerCertWire})`** — Alice's identity cert is added to Bob's multi-rooted trust set (alongside the testbed root and Bob's own identity cert).
3. `security.DecodeFile(secret)` returns `[bobEphemeralSigner]`. Match KeyName against ephemeral cert's key name.
4. Parse ephemeral cert Data packet. `ContentType` ✓. Not expired ✓. `At(-2) == ephemeralIssuer` ✓. `Signature().KeyName() == ownerCertData.Name()` ✓. `sig.ValidateData(certData, sigCov, ownerCertData)` ✓.
5. `keychain.InsertKey(bobEphemeralSigner)`. `keychain.InsertCert(certWire)`.
6. Invitation wire storage is skipped (empty bytes passed).

Returns `entry`.

Bob's keychain now has: testbed root, Bob's NDNCERT cert, Bob's identity cert, **Alice's identity cert** (promoted), and **Bob's ephemeral key + cert**. No invitation wire is stored (fast-join doesn't carry one).

### 7.8 T ≈ +200 ms: Bob publishes precert

`GetWorkspace(name, ignore)`:

1. `getIdentitySignerForWorkspace(/<wksp>)` probes `/<wksp>/32=KD/32=fast/<candidate-user>` through LVS and returns `bobEphemeralSigner`.
2. `trust.Suggest(/<wksp>/32=KD)`, `/<wksp>/32=RD`, `/<wksp>/32=PD)` — network fetches (repo serves from mirror).
3. Participant branch. `client.Store().Get(/<wksp>/32=boot/32=INVITE/ndn/site/bob, true)` returns nil (fast-join path doesn't store an invitation wire). `signPreCert` is called with `invitation=nil`, producing a precert with no `CrossSchema` field. The precert validates against the regular schema rule.
4. `signPreCert(wkspName, bobEphemeralSigner, invitation)`:
   - `userName := /<wksp>/ndn/site/bob`
   - `userKeyName := /<wksp>/ndn/site/bob/KEY/<user-kid>`
   - `userSigner := KeygenEcc(userKeyName, P-256)`
   - `identityCertName := /.../8b3f9c2e1a0d4756/ephemeral/<v>` (the ephemeral cert)
   - `identityCtxSigner := WithKeyLocator(bobEphemeralSigner, identityCertName)`
   - `preCertWire := SignCert` with Signer=identityCtxSigner, IssuerId="pre", NotAfter=+14d, CrossSchema=nil (no cross-schema in fast-join path)
   - Result: `/<wksp>/ndn/site/bob/KEY/<user-kid>/pre/<pre-v>`
   - `keychain.InsertKey(userSigner); keychain.InsertCert(preCertWire.Join())`
5. `StartBootSyncParticipant` publishes the precert to boot-sync SVS as Bob's publication.

### 7.9 T ≈ +500 ms: Alice signs the final cert

Alice's `ownerSub` receives Bob's precert publication:

- Schema-validates the precert via `client.ValidateExt`. The chain `#user_precert <= #ephemeral_cert <= #ownerid_cert` is valid because Bob's ephemeral cert (received in step 7.7.5 and stored during `importBootJoinIdentityCert`) chains back to Alice's `ownerid_cert` (promoted as a trust root in Bob's keychain in step 7.7.2). Note: Alice's own keychain has her own `ownerid_cert` natively, so her validation doesn't need the promotion step.
- `SignFinalCert(preWire, rootSigner)`:
  - `Data = certData` (Bob's user-key Data packet)
  - `Signer = rootCtxSigner` (workspace anchor with KeyLocator = anchor cert)
  - `IssuerId = "anchor"`
  - `NotAfter = now + 180d`
  - Result: `/<wksp>/ndn/site/bob/KEY/<user-kid>/anchor/<final-v>` — satisfies `#user_cert <= #wksp_anchor`.
- Alice inserts the final cert locally, stores it, publishes it back to boot-sync SVS.

### 7.10 T ≈ +550 ms: Bob receives final cert

Bob's `participantSub` receives the published final cert and `keychain.InsertCert(finalCertWire)`.

### 7.11 T ≈ +800 ms: DSK round-trip

Bob's `findDskRoutine`:

- Generates X25519 ephemeral keypair.
- Publishes `DSKRequest{ephemeralPub}` to `/<wksp>/root/32=DSK/...`.
- Alice's owner-subscriber responds with the DSK encrypted under X25519+HKDF+AES-GCM.
- Bob decrypts and calls `api.set_encrypt_keys(pskBuf, dsk)`.

### 7.12 T ≈ +1.5 s: MLS join

- `requestMlsJoin`: publishes Bob's MLS key package ref to SVS.
- Alice's master device calls `OpenMlsLiteGroup.addMembers([bobKp])`. Bob receives the Welcome, joins the MLS group. Epoch advances 1 → 2.

### 7.13 T ≈ +5 s: Bob sends "Hi Alice!"

`WorkspaceChat.sendMessage` adds the message to a Y.Array. The Yjs binary update is bundled, encrypted with the epoch-2 MLS exporter secret (`ownly/svs/aead`, 32 bytes), wrapped in `AeadBlock{TLV 0xC6}`, published to SVS. Alice's SVS subscriber decrypts and applies the Yjs update. Alice sees "Hi Alice!".

### 7.14 Final keychain contents

**Bob:**
```
identity/   /ndn/site/bob/KEY/<bob-kid>/identity/<bob-id-v>             (existing, self-signed)
identity/   /ndn/site/alice/KEY/<alice-kid>/identity/<alice-v>           (PROMOTED anchor)
ephemeral/  /ndn/site/bob/KEY/8b3f9c2e1a0d4756/ephemeral/<bob-ek-v>      (new, owner-signed)
pre/        /<wksp>/ndn/site/bob/KEY/<user-kid>/pre/<pre-v>             (new, ephemeral-signed)
anchor/     /<wksp>/ndn/site/bob/KEY/<user-kid>/anchor/<final-v>         (new, anchor-signed, 180d)
key/        /<wksp>/ndn/site/bob/KEY/<user-kid>                          (new, workspace user key)

```

**Alice (post-exchange):**
```
identity/   /ndn/site/alice/KEY/<alice-kid>/identity/<alice-v>           (unchanged)
identity/   /ndn/site/bob/KEY/8b3f9c2e1a0d4756/ephemeral/<bob-ek-v>      (peer, from boot-sync)
pre/        /<wksp>/ndn/site/bob/KEY/<user-kid>/pre/<pre-v>             (peer precert)
anchor/     /<wksp>/ndn/site/bob/KEY/<user-kid>/anchor/<final-v>         (peer final cert, signed by Alice)
```

Bob appears in Alice's dashboard. Alice can remove him via the standard member-removal flow, which calls `deletePeerIdentityEntries(bob)` to clean up his peer index entry.

### 7.15 Re-visit on a fresh device (T = a week later)

Bob on a new browser: bundle is re-parsed, `importFastJoinIdentity` re-runs. The cert key name `8b3f9c2e1a0d4756` is not in the new device's keychain, so the key is inserted. The new device gets a separate ephemeral identity, which becomes a separate MLS leaf for the same NDN account — **multi-device support at work**.

---

## 8. Security properties

### 8.1 Trust chain

`TrustConfig` is multi-rooted (see §3.3). After fast-join, Bob's trust roots include:

- The built-in testbed root.
- Bob's own self-signed identity cert.
- **Alice's self-signed identity cert** (promoted via `PromoteAnchor` in `importFastJoinIdentity`, see §5.2 step 2).
- Any other self-signed certs in Bob's keychain.

The promotion of Alice's `ownerid_cert` is the load-bearing piece of fast-join: it provides an alternative trust path that doesn't require Alice to be testbed-chained to Bob. **The testbed chain (testbed_root → testbed_owner_cert → ownerid_cert) remains valid as a parallel anchor** — the schema and validator don't care which root leads to a valid chain.

### 8.2 Threat model

| Threat | Mitigation |
|---|---|
| Forged bundle (attacker creates a fake bundle pointing at a workspace they don't own) | The owner cert in the bundle is self-signed; the invitee promotes it as a trust anchor. **Gap:** no check against previously-known owner cert for the same key name. See §9.1. |
| MITM during URL sharing | TLS provides transport security. The bundle itself is not encrypted (intentional, the PSK must be readable). |
| Replayed bundle (same bundle used twice) | The ephemeral cert is unique per issuance. Each accept creates a separate ephemeral identity for the same NDN name. Multi-device support handles this gracefully. |
| Stale bundle (> 14 days old) | `import_fast_join_identity` checks `CertIsExpired` and refuses. |
| Bundle modified in transit (attacker swaps the owner cert) | The ephemeral cert's signature is verified against the owner cert on the invitee side. If the owner cert is swapped, the signature check fails. |
| Owner loses control of their identity cert (key compromise) | Out of scope — same as for any PKI. The owner can re-issue trust anchors via the standard cert rotation flow. |

### 8.3 What fast-join does NOT protect

- **Confidentiality of workspace content before the invitee joined.** The PSK is in the bundle. If the owner wants to revoke a previously-shared PSK, they need to rotate the workspace's AEAD key.
- **The invitee's identity namespace.** A compromised invitee device sees the workspace PSK and could impersonate other invitees using the same bundle.
- **Repudiation.** Once the invitee joins, they have the same access as any other member. No time-limited access.

---

## 9. Open issues

### 9.1 Trust-on-first-use for the owner cert

`import_fast_joinIdentity` accepts any valid self-signed owner cert and promotes it as a trust anchor (adding it to the multi-rooted set described in §3.3). There's no check against a previously-known owner cert for the same key name (e.g. one the invitee has already imported from a prior workspace the same owner runs). A malicious party could:

1. Send a bundle to invitee A claiming to be `alice`.
2. Send a different bundle to invitee B claiming to be `alice`, but with a different ephemeral key.
3. Both A and B now have different `alice` trust anchors in their respective trust sets.

Each invitee's `TrustConfig` will validate packets signed by either root, but a packet claiming to come from `alice` signed by the wrong root will fail validation against the schema's `#ownerid_cert` rule (which constrains on the full cert name). The vulnerability is at the cross-invitee boundary: A and B don't have a way to verify they're talking to the same `alice`.

Proper fix: compare the incoming owner cert against `peerIdentityEntries` (and refuse if there's a key-name collision with a different cert content).

### 9.2 Bundle replay

A bundle URL can be used by multiple invitees if shared. Each accept creates a new ephemeral identity for the same NDN name. This is intentional (multi-device support), but no detection of "this bundle was already used" exists. A malicious invitee could join many times with the same bundle and exhaust the owner's invitation budget (if any limit is added later).

### 9.3 PSK leakage

The PSK travels in the bundle in cleartext. Anyone who gets the bundle has the PSK. By design (the invitee needs it to decrypt old content), but the PSK should be rotated whenever a fast-join invitee is removed. A future improvement: omit the PSK and have the invitee receive it via the DSK round-trip after joining, requiring a workspace-wide re-encryption.

### 9.4 Cross-schema validity — RESOLVED in v: 4

Fast-join bundles no longer carry a cross-schema invitation wire. The ephemeral cert chains to a trust anchor (the owner's promoted `ownerid_cert`) via the regular schema rule `#user_precert <= #userid_cert | #ephemeral_cert`, eliminating the need for a fallback cross-schema.

Legacy bundles (v: 3 and earlier) DO carry a cross-schema and are still valid. The cross-schema was signed with 50-year validity; if an invitee had a v: 3 bundle, the cross-schema would still be valid long after the 14-day ephemeral cert expired. With v: 4 this issue doesn't arise — there's no cross-schema to expire.

The 14-day ephemeral cert validity itself remains a concern (a slow invitee would find their ephemeral cert expired on first use). The mitigation is the same as before: invitee should accept the bundle within 14 days. Owner can issue a fresh fast-join bundle if the original expires.

### 9.5 No bundle signature

The bundle itself is not signed. Tampering (e.g. swapping the workspace name) is detected only indirectly by `import_fast_join_identity`'s field-level validation. A simple fix: add a COSE Sign1 over the bundle using the owner's identity key. Currently not implemented because per-field signatures already provide most of the security.

### 9.6 No owner cert expiry vs latest version check

`import_fast_join_identity` checks `CertIsExpired` but doesn't check that the owner cert is the latest version. A revoked cert would still be accepted if not explicitly revoked in the keychain. Trust-schema doesn't have CRLs; the only mitigation is the in-band peer index.

### 9.7 The `ephemeral` issuer-id rename

**Resolved in this revision.** The ephemeral cert now uses issuer-id `ephemeral` rather than `identity`. See §2.3 for the rationale (peer-index cleanup, keychain UI clarity, disjoint LVS rules). Implementation:

- [ndn/app/identity_keys.go:22](ndn/app/identity_keys.go#L22) — `var ephemeralIssuer = enc.NewGenericComponent("ephemeral")`.
- [ndn/app/identity_keys.go:270](ndn/app/identity_keys.go#L270) — `makeOwnerSignedIdentityCert` uses `ephemeralIssuer`.
- [ndn/app/identity_keys.go:424](ndn/app/identity_keys.go#L424) — `importFastJoinIdentity` checks `At(-2) == ephemeralIssuer`.
- [ndn/app/boot_sync.go:59](ndn/app/boot_sync.go#L59) — `importBootJoinIdentityCert` checks `At(-2) == ephemeralIssuer`.
- [ndn/app/schema.trust:18-19](ndn/app/schema.trust#L18) — `#EPHEMERAL` macro.
- [ndn/app/schema.trust:57,67](ndn/app/schema.trust#L57) — `#ephemeral_cert` and `#user_precert` rules.

**Build impact:** `schema.tlv` must be recompiled from `schema.trust` using the python-ndn `lvs` compiler. See [ownly-trust-schema-build](../.claude/projects/-Users-tianyuan-Documents-Work-ownly/memory/ownly-trust-schema-build.md) for the build procedure (Python venv at `/tmp/ownly-schema-build/venv`, then `rm -rf` to clean up).

**Bundle version:** current bundles are `v: 4`. Earlier `v: 3` draft bundles carried a cross-schema invitation field; v4 removes that field and relies on the owner-signed ephemeral cert plus LVS rules.

---

## 10. File map

**Go (`ndn/app/`):**
- [workspace.go:936-965](ndn/app/workspace.go#L936) — `make_fast_join_invitation` JS API.
- [workspace.go:1069-1083](ndn/app/workspace.go#L1069) — `fastJoinIdentity` struct + `toJs`.
- [workspace.go:1085-1101](ndn/app/workspace.go#L1085) — `certWireByName` helper.
- [workspace.go:1103-1151](ndn/app/workspace.go#L1103) — `makeFastJoinIdentity` core logic.
- [workspace.go:923-934](ndn/app/workspace.go#L923) — `forget_peer_identity` JS API.
- [workspace.go:381-435](ndn/app/workspace.go#L381) — `publishInvitation` (cross-schema, used by legacy `sign_and_pub_invitation` only).
- [workspace.go:1017-1067](ndn/app/workspace.go#L1017) — `signPreCert` (participant precert).
- [workspace.go:1153-1223](ndn/app/workspace.go#L1153) — `setupOwner` (workspace anchor + owner cert).
- [workspace.go:233-389](ndn/app/workspace.go#L233) — `GetWorkspace` (participant branch).
- [identity_keys.go:22](ndn/app/identity_keys.go#L22) — `ephemeralIssuer` constant.
- [identity_keys.go:253-280](ndn/app/identity_keys.go#L253) — `makeOwnerSignedIdentityCert`.
- [identity_keys.go:378-503](ndn/app/identity_keys.go#L378) — `importFastJoinIdentity`.
- [identity_keys.go:1261-1316](ndn/app/identity_keys.go#L1261) — `forgetPeerIdentityForGroup`.
- [boot_sync.go:47-98](ndn/app/boot_sync.go#L47) — `importBootJoinIdentityCert` (piggybacked cert).
- [boot_sync.go:563-578](ndn/app/boot_sync.go#L563) — `SignFinalCert` (180-day re-sign).
- [boot_sync.go:255-322](ndn/app/boot_sync.go#L255) — `StartBootSyncParticipant`.
- [boot_sync.go:521-555](ndn/app/boot_sync.go#L521) — `StartBootSyncOwner`.
- [schema.trust](ndn/app/schema.trust) — LVS source (the `ephemeral` rename is here).
- [schema.tlv](ndn/app/schema.tlv) — compiled binary, embedded via `//go:embed`.

**TypeScript (`src/`):**
- [services/fast-join.ts](src/services/fast-join.ts) — bundle serialization (`v: 4`).
- [services/workspace-invite.ts:839-857](src/services/workspace-invite.ts#L839) — `tryFastInvite`.
- [services/workspace-invite.ts:930-948](src/services/workspace-invite.ts#L930) — `getFastJoinLink`.
- [services/workspace-invite.ts:177-186](src/services/workspace-invite.ts#L177) — `deletePeerIdentityEntries`.
- [services/workspace.ts:531-585](src/services/workspace.ts#L531) — `Workspace.join`.
- [services/ndn.ts](src/services/ndn.ts) — `import_fast_join_identity`, `make_fast_join_invitation`, `forget_peer_identity` JS API typings.
- [components/home/JoinWorkspaceModal.vue](src/components/home/JoinWorkspaceModal.vue) — fast-join detection + import UI.
- [components/InvitePeopleModal.vue:461-493](src/components/InvitePeopleModal.vue#L461) — bulk fast-join link generation + clipboard copy.
- [router/index.ts](src/router/index.ts) — `/join/:space/` route handling.

---

## 11. Glossary

- **Ephemeral cert**: a per-invitation P-256 identity cert, signed by the owner's identity cert, with issuer-id `ephemeral`. Valid for 14 days; re-signed as `user_cert` (180 days) during boot sync.
- **Owner identity cert**: the owner's self-signed `#IDCERT` (`/<owner>/KEY/<kid>/identity/<v>`). Becomes a trust root on the invitee's side via `PromoteAnchor` (one of several roots in the multi-rooted trust set).
- **Workspace anchor**: the workspace's self-signed trust root (`/<owner>/wksp/KEY/<kid>/self/<v>`). Re-signs the final `user_cert` during boot sync.
- **Pre-anchor**: a transitional cert (`/<owner>/wksp/KEY/<kid>/pre/<v>`) signed by the owner's identity cert; bridges the identity cert chain to the workspace anchor.
- **Owner cert**: Alice's member cert (`/<owner>/wksp/32=owner/KEY/<kid>/anchor/<v>`), signed by the workspace anchor with issuer-id `anchor`.
- **User cert / Final cert**: a member cert for a non-owner user, signed by the workspace anchor with issuer-id `anchor`, 180-day validity. Replaces the precert after boot sync.
- **Precert**: a 14-day cert (`/<wksp>/<user>/KEY/<kid>/pre/<v>`) signed by the user's identity or ephemeral key. Legacy joins may attach a `CrossSchema` field; fast joins do not. Submitted to boot-sync for owner to re-sign as the final user cert.
- **Cross-schema**: a signed NDN Data packet carrying rules that authorise a key from outside the LVS trust schema to participate in the workspace. Attached to precerts to make them validatable.
- **Peer index**: a `cert → group → published` map, persisted via JS callbacks under `/local/peer-identities`. Tracks which peer certs have been published to which boot-sync groups.
- **Bundle**: the JSON-envelope + base64url-encoded set of three binary fields (owner cert, ephemeral secret, ephemeral cert) plus workspace metadata (label, wksp, psk), embedded in a URL fragment. Version `v: 4`.
- **PromoteAnchor**: the `TrustConfig.PromoteAnchor` operation that installs a self-signed cert as a local trust root. Adds to a multi-rooted set (any root can validate a chain). The key step that lets fast-join work without testbed chaining, but not the only source of trust — the testbed root is always present too.
- **Master / Follower device**: in multi-device mode, only one owner device (the master) can perform MLS-mutating actions; followers forward to the master via directed Interests.
- **Issuer-id**: the second-to-last component of an NDN cert name (e.g. `identity`, `ephemeral`, `pre`, `anchor`, `self`). The LVS schema uses this to classify certs.
- **KD fast selector**: the local signer probe `/<wksp>/32=KD/32=fast/<user>`, resolved through LVS. It selects an owner-signed ephemeral cert for fast join without passing a frontend identity hint.

---

*Document version: 3.0 (v: 4 bundles; cross-schema invitation removed from fast-join; legacy invites retain backward compat; both `#userid_cert` and `#ephemeral_cert` paths validate `#user_precert`).*
