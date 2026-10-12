import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  ReasonCode,
  clearRevocations,
  isRevoked,
  lookupRevocation,
  reasonLabel,
  recordRevocation,
  registerOnCertRevoked,
  type RevocationRecord,
} from './revocation';
import { GlobalBus } from './event-bus';

function rec(overrides: Partial<RevocationRecord> = {}): RevocationRecord {
  return {
    reason: ReasonCode.PrivilegeWithdrawn,
    invalidityTime: 0,
    certHash: 'AAAA',
    certName: '/alice/wksp/alice/KEY/k1/self/v=1',
    ...overrides,
  };
}

describe('revocation cache', () => {
  beforeEach(clearRevocations)
  afterEach(vi.restoreAllMocks)

  it('records, looks up, and reports revocation by certificate name', () => {
    recordRevocation(rec({ certName: '/workspace/alice/v=1' }))
    expect(isRevoked('/workspace/alice/v=1')).toBe(true)
    expect(lookupRevocation('/workspace/alice/v=1')?.reason).toBe(ReasonCode.PrivilegeWithdrawn)
    expect(isRevoked('UNKNOWN')).toBe(false)
  })

  it('keeps certificate versions distinct when they share a public key', () => {
    recordRevocation(rec({ certName: '/workspace/alice/v=1', certHash: 'Y' }))
    recordRevocation(rec({ certName: '/workspace/alice/v=2', certHash: 'Y' }))
    expect(isRevoked('/workspace/alice/v=1')).toBe(true)
    expect(isRevoked('/workspace/alice/v=2')).toBe(true)
  })

  it('does NOT emit cert-revoked when recording (recursion guard)', () => {
    const spy = vi.spyOn(GlobalBus, 'emit')
    recordRevocation(rec({ certName: '/workspace/alice/v=3' }))
    expect(spy.mock.calls.filter(([e]) => e === 'cert-revoked')).toHaveLength(0)
  })
})

describe('registerOnCertRevoked bridge', () => {
  beforeEach(clearRevocations)
  afterEach(vi.restoreAllMocks)

  it('converts snake_case payload to camelCase record and returns unregister', () => {
    const handler = vi.fn()
    const unregister = registerOnCertRevoked(handler)
    expect(typeof unregister).toBe('function')
    GlobalBus.emit('cert-revoked', {
      reason: ReasonCode.KeyCompromise,
      invalidity_time: 42,
      cert_hash: 'Q',
      cert_name: '/bob/wksp/bob/KEY/k/v=1',
    })
    expect(handler).toHaveBeenCalledOnce()
    const got = handler.mock.calls[0][0]
    expect(got.reason).toBe(ReasonCode.KeyCompromise)
    expect(got.invalidityTime).toBe(42)
    expect(got.certHash).toBe('Q')
    expect(got.certName).toBe('/bob/wksp/bob/KEY/k/v=1')
    unregister()
    GlobalBus.emit('cert-revoked', { reason: 0, invalidity_time: 0, cert_hash: 'X', cert_name: '' })
    expect(handler).toHaveBeenCalledOnce()
  })
})

describe('reasonLabel', () => {
  it('returns a human-readable string per known code and the fallback for unknown', () => {
    expect(reasonLabel(ReasonCode.KeyCompromise)).toBe('key compromise')
    expect(reasonLabel(ReasonCode.PrivilegeWithdrawn)).toBe('revoked by owner')
    expect(reasonLabel(99)).toBe('reason 99')
  })
})

// End-to-end bridge: simulate the Go-side on_cert_revoked callback
// (which fires the GlobalBus cert-revoked event with the snake_case
// payload), and verify the WorkspaceMembers-style handler receives
// the right camelCase record, and that the cache is updated so a
// subsequent listRevocations() shows the new entry.
describe('cert-revoked e2e (Go bridge → handler → cache)', () => {
  beforeEach(clearRevocations)
  afterEach(vi.restoreAllMocks)

  it('handler + recordRevocation + lookupRevocation reflect the new state', () => {
    const handler = vi.fn((r: RevocationRecord) => {
      // The WorkspaceMembers component rehydrates the cache from
      // the handler (re-emits recordRevocation, then re-reads from
      // list_revocations). The cache is the source of truth for
      // the [REVOKED] badge lookup.
      recordRevocation(r)
    })
    const unregister = registerOnCertRevoked(handler)

    // Simulate the Go-side bridge firing a cert-revoked event for a
    // wkspKey (matches the v2 publish path which validates
    // IsWkspKeyCertName before publishing).
    GlobalBus.emit('cert-revoked', {
      reason: ReasonCode.PrivilegeWithdrawn,
      invalidity_time: 0,
      cert_hash: 'abcdef0123456789',
      cert_name: '/alice@example.com/wksp/alice@example.com/KEY/k1/anchor/v=1',
    })

    expect(handler).toHaveBeenCalledOnce()
    const certName = '/alice@example.com/wksp/alice@example.com/KEY/k1/anchor/v=1'
    expect(isRevoked(certName)).toBe(true)
    const cached = lookupRevocation(certName)
    expect(cached?.reason).toBe(ReasonCode.PrivilegeWithdrawn)
    expect(cached?.certName).toBe(certName)
    expect(cached?.invalidityTime).toBe(0)

    // A second event for the same certificate refreshes the UI cache.
    GlobalBus.emit('cert-revoked', {
      reason: ReasonCode.KeyCompromise,
      invalidity_time: 1_700_000_000_000,
      cert_hash: 'abcdef0123456789',
      cert_name: '/alice@example.com/wksp/alice@example.com/KEY/k1/anchor/v=1',
    })
    expect(lookupRevocation(certName)?.reason).toBe(ReasonCode.KeyCompromise)
    expect(lookupRevocation(certName)?.invalidityTime).toBe(1_700_000_000_000)

    unregister()
  })

  it('multiple handlers all see the event (WorkspaceMembers + any other subscriber)', () => {
    const h1 = vi.fn()
    const h2 = vi.fn()
    const u1 = registerOnCertRevoked(h1)
    const u2 = registerOnCertRevoked(h2)

    GlobalBus.emit('cert-revoked', {
      reason: ReasonCode.CessationOfOperation,
      invalidity_time: 0,
      cert_hash: 'multi',
      cert_name: '/x/wksp/y/KEY/k/pre/v=1',
    })

    expect(h1).toHaveBeenCalledOnce()
    expect(h2).toHaveBeenCalledOnce()
    expect(h1.mock.calls[0][0].reason).toBe(ReasonCode.CessationOfOperation)
    expect(h2.mock.calls[0][0].certHash).toBe('multi')

    u1()
    u2()
  })
})
