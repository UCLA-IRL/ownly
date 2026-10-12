# Revocation records

**Status:** ndnd migration in progress on PR #124
**Scope:** workspace-key certificates only
**Transport:** the existing workspace and boot SVS-ALO channels

Removing a member from MLS rotates the group key, but it does not invalidate the member's NDN certificate. Without a revocation record, a removed member can continue signing Sync publications with that certificate. Ownly closes that gap by publishing an owner-signed revocation record and installing it in ndnd's keychain-backed trust configuration.

This document describes the current implementation. The earlier Ownly-specific codec and in-memory negative-certificate design are obsolete.

## Goals and non-goals

The revocation path must:

- revoke every known workspace-key certificate for a removed member;
- accept records only from the workspace owner;
- reject malformed, cross-workspace, and non-workspace-key targets;
- retain the revoked certificate for audit and UI display;
- persist accepted records in the ndnd keychain store; and
- reject later validation chains that use a revoked certificate.

This change does not revoke personal identity certificates or fast-join ephemeral certificates. It also does not add network retrieval of missing revocation records or embed revocations in `SecurityConfig`. Those remain separate follow-up work.

## Ownership boundary

ndnd owns the generic certificate-revocation mechanism:

- record construction and parsing;
- record and certificate name conversion;
- durable record storage through `TrustConfig`;
- conflict detection for records that use the same name; and
- rejection of revoked certificates during trust validation.

Ownly owns application policy:

- the definition of a valid `wkspKey` certificate;
- the active workspace boundary;
- the requirement that the SVS publisher is `32=owner`;
- validation that the embedded record is signed by the workspace owner; and
- member removal, UI events, and publication reporting.

`TrustConfig.InsertRevoke` is a storage operation. A caller must authenticate and authorize the record before calling it.

## Wire format

`security.RevokeCert` creates a signed NDN Data packet. Its content is an ordered `RevocationRecord`:

| TLV type | Field | Encoding |
|---:|---|---|
| `0xC9` | `Timestamp` | natural number, Unix milliseconds |
| `0xCB` | `Reason` | natural number |
| `0xCA` | `PublicKeyHash` | SHA-256 of the certificate's public-key content |
| `0xCD` | `NotBefore` | optional natural number, Unix milliseconds |

The hash covers `cert.Content()`, not the complete certificate packet. This makes the record about the key carried by the named certificate while the record name identifies the exact certificate version.

Ownly uses RFC 5280 reason codes. Member removal uses `9` (`privilegeWithdrawn`). Reason `1` (`keyCompromise`) defaults `NotBefore` to the certificate's validity start when the caller does not supply a time. Ownly does not currently publish reason `5` records for fast-join ephemeral certificates.

## Record naming

For a certificate named:

```text
/<identity>/KEY/<key-id>/<issuer>/v=<version>
```

ndnd creates:

```text
/<identity>/REVOKE/<key-id>/<issuer>/v=<version>/<issuer>
```

`CertNameFromRevocationRecordName` reverses this mapping. The receive path uses the reconstructed certificate name before it looks in the local keychain, so a record can be authorized even when its target certificate has not arrived yet.

An Ownly revocation target must have this shape:

```text
/<workspace-owner>/wksp/<member-identity>/KEY/<key-id>/anchor/v=<version>
```

The target must also be under the active workspace name. Personal `/identity/` certificates, `/ephemeral/` certificates, other issuers, malformed versions, and certificates from another workspace are rejected.

## Publication

Member removal proceeds as follows:

1. The owner removes the member's MLS leaves, when present, and rotates the workspace encryption key.
2. Ownly asks the Go API for the member's workspace-key certificates in the active workspace.
3. For each certificate, the owner creates an ndnd record with reason `9` and publishes it through SVS-ALO.
4. Ownly persists the returned SVS state.
5. The UI reports the number of eligible certificates, successful publications, publication failures, or an enumeration failure.

Certificate revocation is attempted even when the member has no current MLS leaf. A missing MLS leaf does not restore certificate authority.

Publication fails closed when the owner workspace signer is unavailable. The signer identity must be exactly `/<workspace>/32=owner`; a regular member signer returned by a broad trust-schema suggestion is not accepted.

## Receive and authorization flow

An SVS publication is treated as a revocation candidate when its embedded Data name contains the `REVOKE` marker. The candidate then passes these checks in order:

1. Parse the signed Data packet and reconstruct the target certificate name.
2. Confirm that the target is a structurally valid `wkspKey` certificate under the active workspace.
3. Confirm that the SVS publisher is `32=owner`.
4. Confirm that the Data key locator belongs to the workspace owner identity.
5. Validate the embedded Data signature and certificate chain through the workspace trust configuration.
6. Parse the record body and require a 32-byte public-key hash.
7. If the target certificate is local, hash its public-key content and require an exact match.
8. Store the record with `TrustConfig.InsertRevoke`.
9. Emit the `cert-revoked` UI event.

A failure at any step prevents insertion. The outer SVS packet's validity is not used as proof that the embedded record is authorized.

## Storage and enforcement

ndnd stores the complete signed record under its deterministic record name in the keychain's public store. Recreating `TrustConfig` with the same keychain retains the record.

Ownly does not delete the target certificate. Keeping it preserves the exact name, issuer, public key, and member row needed for audit and UI display. During later validation, ndnd derives the corresponding revocation-record name from the certificate and queries the store. Validation fails when a record is present, except when the record's optional `NotBefore` explicitly permits an older certificate signature time.

`list_revocations` rebuilds the visible list by walking known workspace certificates and querying `TrustConfig.CheckRevoke`. The list is therefore a view over durable keychain state, not a second source of truth.

If a valid record arrives before its certificate, the record is still stored under the exact derived name. When the certificate later arrives, ordinary trust validation finds the existing sibling record and rejects the certificate. No hash-only alias or pending in-memory map is required.

## Replay and conflict handling

Receiving the same signed record more than once is idempotent. If a different wire packet tries to reuse an existing revocation-record name, ndnd rejects it as a conflict instead of overwriting the first stored record.

Revocation is final for a certificate version. Re-inviting a member creates a new workspace key and certificate version; it does not remove the earlier record.

## Failure behavior

- A malformed record is logged and ignored.
- A non-owner publisher or signer is logged and ignored.
- A record for a personal, ephemeral, cross-workspace, or malformed certificate is logged and ignored.
- A known certificate whose public key does not match the record hash is logged and ignored.
- A storage error prevents the UI event and leaves trust state unchanged.
- Failure to enumerate a removed member's certificates is reported separately from finding zero eligible certificates.
- Partial publication failure does not roll back an MLS removal that has already been merged.

## UI integration

Go emits one `on_cert_revoked` callback after durable insertion. The TypeScript bridge translates it into the global `cert-revoked` event. The previous unused SVS `on_revocation` callback has been removed, leaving one receive path.

The TypeScript cache supports badges and current-session event subscribers. It is not consulted for trust decisions. Reloaded UI state comes from `list_revocations`, which queries ndnd's durable records.

## Test ownership

ndnd tests the generic contract:

- record codec and optional `NotBefore`;
- certificate-to-record and record-to-certificate name mapping;
- malformed names and TLV fields;
- public-key hashing;
- idempotent insertion and conflicting-record rejection;
- persistence across `TrustConfig` reconstruction; and
- revoked-certificate enforcement, including cached certificates.

Ownly tests application policy:

- strict `wkspKey` name classification;
- owner-only publication and receipt;
- workspace-bound target checks;
- known-certificate hash matching;
- member removal with and without MLS leaves;
- certificate-enumeration and publication reporting;
- durable list reconstruction; and
- invitation ordering by `boot_time`, publisher, and per-publisher `seq_num`.

The release gate is the repository's full build, lint, Go/WASM, Go test, Go vet, Vue type-check, and Vitest matrix, plus focused ndnd tests and race tests.

## Deferred work

Two propagation features remain outside this migration:

- fetching a missing revocation record from the network during certificate validation; and
- adding revocation records to the repository `SecurityConfig` re-shoot.

Fast-join ephemeral certificate retirement is also deferred. It needs a separate lifecycle policy because ephemeral certificates are not `wkspKey` targets.
