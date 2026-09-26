/**
 * Ownly revocation service: in-memory cache of revoked wkspKey certs
 * plus the snake-case -> camelCase bridge from the Go-side
 * cert-revoked event.
 *
 * The cache is keyed by exact certificate name so certificate versions that
 * reuse a public key remain distinct. ndnd's keychain store is authoritative.
 */

import { GlobalBus } from '@/services/event-bus';

/** Ownly reason codes (RFC 5280 §5.3.1). */
export const ReasonCode = {
  Unspecified: 0,
  KeyCompromise: 1,
  CessationOfOperation: 5,
  PrivilegeWithdrawn: 9,
} as const;

export type ReasonCodeValue = (typeof ReasonCode)[keyof typeof ReasonCode];

export interface RevocationRecord {
  reason: ReasonCodeValue;
  /** RFC 5280 §5.3.2; 0 = "now", or Unix-millisecond timestamp. */
  invalidityTime: number;
  /** SHA-256 of the certificate's public-key content, hex-encoded. */
  certHash: string;
  /** Full certificate NDN name. */
  certName: string;
}

const revokedByName = new Map<string, RevocationRecord>();

/**
 * Cache a revocation. State-only: does NOT emit cert-revoked. The
 * Go bridge is the single source of the event, so emitting here
 * would recurse forever on any handler that calls back into the
 * cache.
 */
export function recordRevocation(rec: RevocationRecord): void {
  if (!rec.certName) return;
  revokedByName.set(rec.certName, rec);
}

export function lookupRevocation(certName: string): RevocationRecord | undefined {
  return revokedByName.get(certName);
}

export function isRevoked(certName: string): boolean {
  return revokedByName.has(certName);
}

export function listRevocations(): RevocationRecord[] {
  return Array.from(revokedByName.values());
}

/** Clear the in-memory cache. Test-only. */
export function clearRevocations(): void {
  revokedByName.clear();
}

/**
 * Subscribe to Go-side cert-revoked events. Handler receives a
 * camelCase record. Returns an unregister function.
 */
export function registerOnCertRevoked(
  handler: (rec: RevocationRecord) => void,
): () => void {
  const listener = (payload: any) => {
    handler({
      reason: payload.reason as ReasonCodeValue,
      invalidityTime: payload.invalidity_time,
      certHash: payload.cert_hash,
      certName: payload.cert_name,
    });
  };
  GlobalBus.on('cert-revoked', listener);
  return () => GlobalBus.off('cert-revoked', listener);
}

export function reasonLabel(reason: number): string {
  switch (reason) {
    case ReasonCode.Unspecified: return 'unspecified';
    case ReasonCode.KeyCompromise: return 'key compromise';
    case ReasonCode.CessationOfOperation: return 'cessation of operation';
    case ReasonCode.PrivilegeWithdrawn: return 'revoked by owner';
    default: return `reason ${reason}`;
  }
}
