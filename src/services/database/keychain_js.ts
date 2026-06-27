/**
 * KeychainJS interface for storing keys and certificates.
 *
 * @license Apache-2.0
 */
export interface KeyChainJS {
  // Get all keys and certificates in the keychain
  list(): Promise<Uint8Array[]>;

  // Write a key or certificate to the keychain
  write(name: string, blob: Uint8Array): Promise<void>;

  // Delete keychain entries by their stored filename
  delete(name: string | string[]): Promise<void>;

  compactPollution(opts?: KeychainCompactOptions): Promise<KeychainCompactReport>;

  /**
   * purgeDuplicateKeys removes redundant `.key` rows that share the same
   * parsed keyName. Each call to the upstream ndnd KeyChainJS.InsertKey
   * invokes writeFile even when the in-memory state correctly dedupes,
   * and MarshalSecret produces non-deterministic ECDSA signatures, so the
   * SHA-256 filename dedup in `write()` cannot catch the redundant
   * stores. This walks the table, parses each `.key` blob to extract
   * its keyName, and deletes every duplicate after the first.
   *
   * Idempotent: the second call deletes 0 rows once the keychain has
   * already been deduped. Returns counts of scanned/kept/deleted rows.
   */
  purgeDuplicateKeys(): Promise<{
    scanned: number;
    deleted: number;
    remaining: number;
    ms: number;
  }>;
}

import Dexie from 'dexie';

const MAX_KEYCHAIN_ENTRY_BYTES = 1024 * 1024;
const MAX_KEYCHAIN_LIST_BYTES = 16 * 1024 * 1024;
const MAX_KEYCHAIN_LIST_ENTRIES = 2048;
const KEYCHAIN_COMPACT_BATCH_SIZE = 1000;

type KeychainRow = {
  name: string;
  blob: Uint8Array;
};

export type KeychainCompactOptions = {
  dryRun?: boolean;
  keepPerCertFamily?: number;
  deleteInvalidNonKeys?: boolean;
  deleteMismatchedNonKeys?: boolean;
  deleteOldCertVersions?: boolean;
};

export type KeychainCompactReport = {
  entries: number;
  totalBytes: number;
  ms: number;
  byExt: Array<{ key: string; count: number }>;
  byContentType: Array<{ key: string; count: number }>;
  byIssuer: Array<{ key: string; count: number }>;
  byCertFamily: Array<{ key: string; count: number }>;
  samples: EntrySample[];
  dryRun: boolean;
  keepPerCertFamily: number;
  deleted: number;
  deleteReasons: Array<{ key: string; count: number }>;
};

type ListStats = {
  entries: number;
  returned: number;
  skippedLarge: number;
  skippedOverEntryBudget: number;
  skippedOverBudget: number;
  totalBytes: number;
  returnedBytes: number;
  ms: number;
  largest: Array<{ name: string; bytes: number }>;
};

type TlvBlock = {
  type: number;
  valueStart: number;
  valueEnd: number;
  next: number;
};

type ParsedComponent = {
  type: number;
  label: string;
  text: string | null;
  number: number | null;
};

type EntryClass = {
  rowName: string;
  ext: string;
  bytes: number;
  contentType: string;
  dataName: string | null;
  issuer: string | null;
  certFamily: string | null;
  identityPrefix: string | null;
  randomKid: string | null;
  version: number | null;
  parseError: string | null;
};

type EntrySample = {
  rowName: string;
  ext: string;
  bytes: number;
  contentType: string;
  dataName: string | null;
  issuer: string | null;
  certFamily: string | null;
  identityPrefix: string | null;
  randomKid: string | null;
  version: number | null;
  parseError: string | null;
};

const decoder = new TextDecoder('utf-8', { fatal: false });

function extOf(name: string) {
  if (name.endsWith('.key')) return '.key';
  if (name.endsWith('.cert')) return '.cert';
  return '(other)';
}

function inc(map: Map<string, number>, key: string | null | undefined, by = 1) {
  map.set(key ?? '(none)', (map.get(key ?? '(none)') ?? 0) + by);
}

function topEntries(map: Map<string, number>, limit: number) {
  return Array.from(map.entries())
    .map(([key, count]) => ({ key, count }))
    .sort((a, b) => b.count - a.count || a.key.localeCompare(b.key))
    .slice(0, limit);
}

function readVarNum(buf: Uint8Array, pos: number, end = buf.byteLength): [number, number] {
  if (pos >= end) throw new Error('unexpected EOF reading TL number');
  const first = buf[pos++];
  if (first < 253) return [first, pos];
  const bytes = first === 253 ? 2 : first === 254 ? 4 : 8;
  if (pos + bytes > end) throw new Error('unexpected EOF reading extended TL number');
  let value = 0n;
  for (let i = 0; i < bytes; i++) value = (value << 8n) | BigInt(buf[pos++]);
  if (value > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error('TL number exceeds JS safe integer');
  return [Number(value), pos];
}

function readTlv(buf: Uint8Array, pos: number, end = buf.byteLength): TlvBlock {
  const [type, afterType] = readVarNum(buf, pos, end);
  const [length, valueStart] = readVarNum(buf, afterType, end);
  const valueEnd = valueStart + length;
  if (valueEnd > end) throw new Error('TLV length exceeds container');
  return { type, valueStart, valueEnd, next: valueEnd };
}

function readNat(buf: Uint8Array, start: number, end: number) {
  let value = 0n;
  for (let i = start; i < end; i++) value = (value << 8n) | BigInt(buf[i]);
  if (value > BigInt(Number.MAX_SAFE_INTEGER)) return null;
  return Number(value);
}

function bytesText(buf: Uint8Array, start: number, end: number) {
  return decoder.decode(buf.subarray(start, end));
}

function percentEncode(buf: Uint8Array, start: number, end: number) {
  let out = '';
  for (let i = start; i < end; i++) {
    const b = buf[i];
    const isUnreserved =
      (b >= 0x30 && b <= 0x39) ||
      (b >= 0x41 && b <= 0x5a) ||
      (b >= 0x61 && b <= 0x7a) ||
      b === 0x2d ||
      b === 0x2e ||
      b === 0x5f ||
      b === 0x7e;
    out += isUnreserved ? String.fromCharCode(b) : `%${b.toString(16).padStart(2, '0').toUpperCase()}`;
  }
  return out;
}

function componentLabel(buf: Uint8Array, tlv: TlvBlock): ParsedComponent {
  const text = bytesText(buf, tlv.valueStart, tlv.valueEnd);
  const escaped = percentEncode(buf, tlv.valueStart, tlv.valueEnd);
  const number = readNat(buf, tlv.valueStart, tlv.valueEnd);
  switch (tlv.type) {
    case 0x08:
      return { type: tlv.type, label: escaped, text, number: null };
    case 0x20:
      return { type: tlv.type, label: `32=${escaped}`, text, number: null };
    case 0x32:
      return { type: tlv.type, label: number == null ? `seg=${escaped}` : `seg=${number}`, text: null, number };
    case 0x36:
      return { type: tlv.type, label: number == null ? `v=${escaped}` : `v=${number}`, text: null, number };
    default:
      return { type: tlv.type, label: `${tlv.type}=${escaped}`, text, number };
  }
}

function parseName(buf: Uint8Array, start: number, end: number) {
  const comps: ParsedComponent[] = [];
  let pos = start;
  while (pos < end) {
    const comp = readTlv(buf, pos, end);
    comps.push(componentLabel(buf, comp));
    pos = comp.next;
  }
  return comps;
}

function nameString(comps: ParsedComponent[]) {
  return `/${comps.map((comp) => comp.label).join('/')}`;
}

function isGeneric(comp: ParsedComponent | undefined, text: string) {
  return comp?.type === 0x08 && comp.text === text;
}

function classifyContentType(value: number | null) {
  switch (value) {
    case 2:
      return 'cert';
    case 9:
      return 'key';
    case null:
      return '(missing)';
    default:
      return `other:${value}`;
  }
}

function classifyEntry(entry: KeychainRow): EntryClass {
  const base = {
    rowName: entry.name,
    ext: extOf(entry.name),
    bytes: entry.blob.byteLength,
    contentType: '(parse-error)',
    dataName: null,
    issuer: null,
    certFamily: null,
    identityPrefix: null,
    randomKid: null,
    version: null,
  };

  try {
    const dataTlv = readTlv(entry.blob, 0);
    if (dataTlv.type !== 0x06) throw new Error(`not Data TLV: ${dataTlv.type}`);

    let pos = dataTlv.valueStart;
    let comps: ParsedComponent[] | null = null;
    let contentType: number | null = null;
    while (pos < dataTlv.valueEnd) {
      const child = readTlv(entry.blob, pos, dataTlv.valueEnd);
      if (child.type === 0x07) {
        comps = parseName(entry.blob, child.valueStart, child.valueEnd);
      } else if (child.type === 0x14) {
        let metaPos = child.valueStart;
        while (metaPos < child.valueEnd) {
          const meta = readTlv(entry.blob, metaPos, child.valueEnd);
          if (meta.type === 0x18) contentType = readNat(entry.blob, meta.valueStart, meta.valueEnd);
          metaPos = meta.next;
        }
      }
      pos = child.next;
    }

    const dataName = comps ? nameString(comps) : null;
    const label = classifyContentType(contentType);
    let issuer: string | null = null;
    let certFamily: string | null = null;
    let version: number | null = null;
    let identityPrefix: string | null = null;
    let randomKid: string | null = null;
    if (comps) {
      // Both keys (<identity>/KEY/<kid>) and certs
      // (<identity>/KEY/<kid>/<issuer>/<ver>) carry KEY as the second-to-last
      // component. Find it from the end so we tolerate trailing comps.
      const keyIdx = comps.findIndex((c) => isGeneric(c, 'KEY'));
      if (keyIdx >= 0 && keyIdx + 1 < comps.length) {
        identityPrefix = nameString(comps.slice(0, keyIdx));
        randomKid = comps[keyIdx + 1]?.text ?? null;
      }
    }
    if (label === 'cert' && comps && comps.length >= 4 && isGeneric(comps[comps.length - 4], 'KEY')) {
      issuer = comps[comps.length - 2]?.label ?? null;
      version = comps[comps.length - 1]?.number ?? null;
      certFamily = `${nameString(comps.slice(0, -2))}/${issuer ?? '(issuer)'}`;
    }

    return {
      ...base,
      contentType: label,
      dataName,
      issuer,
      certFamily,
      identityPrefix,
      randomKid,
      version,
      parseError: null,
    };
  } catch (err) {
    return {
      ...base,
      parseError: err instanceof Error ? err.message : String(err),
    };
  }
}

function sampleOf(info: EntryClass): EntrySample {
  return {
    rowName: info.rowName,
    ext: info.ext,
    bytes: info.bytes,
    contentType: info.contentType,
    dataName: info.dataName,
    issuer: info.issuer,
    certFamily: info.certFamily,
    identityPrefix: info.identityPrefix,
    randomKid: info.randomKid,
    version: info.version,
    parseError: info.parseError,
  };
}

/**
 * KeychainJS implementation using Dexie.
 *
 * @license Apache-2.0
 */
type KeychainKeyRow = {
  name: string;
  /** Parsed keyName for keys (set when content type is SigningKey). Empty for certs. */
  keyName: string;
  blob: Uint8Array;
};

export class KeyChainDexie implements KeyChainJS {
  private db = new Dexie('keychain') as Dexie & {
    keys: Dexie.Table<KeychainKeyRow, string>;
  };
  private lastListStats: ListStats | null = null;

  constructor() {
    // v1: only the SHA-256 filename index (legacy).
    this.db.version(1).stores({
      keys: 'name',
    });
    // v2: add a secondary index on the parsed keyName for keys. Upstream
    // ndnd's KeyChainJS.InsertKey calls writeFile unconditionally even when
    // the in-memory state correctly dedupes; because MarshalSecret uses
    // non-deterministic ECDSA signatures, every call produces a fresh SHA-256
    // filename, defeating the v1 dedup. The keyName index lets us detect
    // re-stores of the same logical key (across WASM reloads or repeated
    // InsertKey calls for the same signer) and skip the write.
    this.db.version(2).stores({
      keys: 'name, keyName',
    });
  }

  private purgeDone = false;
  private compactDone = false;

  /**
   * Auto-purge runs the first time `ensureOpen` succeeds for this
   * `KeyChainDexie` instance. The `purgeDone`/`compactDone` flags are
   * instance fields, so they reset on every page reload (a fresh JS
   * realm gets a fresh `KeyChainDexie`), but the underlying purge is
   * idempotent: a re-run on an already-clean keychain does nothing
   * because there are no duplicate `.key` rows left to find.
   *
   * Stage 1 (key dedup) walks the table once, parses each `.key` blob's
   * keyName, and deletes every row whose keyName was seen earlier. This
   * eliminates the pollution from the upstream `KeyChainJS.InsertKey`
   * bug that writes a fresh blob every call despite state-level dedup.
   *
   * Stage 2 (cert compact) runs only when the table still exceeds
   * `COMPACT_THRESHOLD` rows after stage 1. It drops old cert versions
   * (keeps the most recent `keepPerCertFamily` versions per cert
   * family) plus any rows whose parsed keyName no longer matches the
   * row's stored filename. All private-key rows are preserved by
   * default. The compact pass only runs when cert pollution is
   * meaningfully large to avoid overhead on clean keychains.
   *
   * Both stages are gated by size thresholds so a clean keychain does
   * not pay the full-walk cost on every app load. Profiles affected
   * by the upstream bug currently carry ~500k redundant rows; healthy
   * profiles carry tens.
   */
  private static readonly PURGE_THRESHOLD = 5000;
  private static readonly COMPACT_THRESHOLD = 50000;
  private static readonly AUTO_KEEP_PER_CERT_FAMILY = 4;

  /**
   * preWasmInit runs the auto-purge eagerly and synchronously awaits it
   * before any other shim method is callable. Callers should invoke this
   * before passing the shim to upstream `keychain.NewKeyChainJS`, because
   * that constructor immediately calls `list()` and re-inserts every
   * persisted file — which on a polluted profile (hundreds of thousands
   * of duplicate `.key` rows from the upstream `KeyChainJS.InsertKey`
   * write-on-dedup-hit bug) makes WASM init take so long that the JS
   * `set_ndn` promise times out with "NDN API not set".
   *
   * The pre-WASM purge dedups by keyName and (if still large) compacts
   * cert families, exactly like the lazy auto-purge. It also sets
   * `purgeDone`/`compactDone` so the lazy path is a no-op.
   */
  public async preWasmInit(): Promise<void> {
    if (this.purgeDone) return;
    await this.maybeAutoPurge();
  }

  private async maybeAutoPurge(): Promise<void> {
    if (this.purgeDone) return;
    this.purgeDone = true;
    try {
      const total = await this.db.keys.count();
      if (total > KeyChainDexie.PURGE_THRESHOLD) {
        const result = await this.purgeDuplicateKeys();
        if (result.deleted > 0) {
          console.info(
            'Keychain Dexie: auto-purge finished',
            {
              scanned: result.scanned,
              deleted: result.deleted,
              remaining: result.remaining,
              ms: result.ms,
            },
          );
        }
      }
      if (this.compactDone) return;
      this.compactDone = true;
      // Re-check row count after the key dedup, then run compact if needed.
      const afterKeyPurge = await this.db.keys.count();
      if (afterKeyPurge <= KeyChainDexie.COMPACT_THRESHOLD) return;
      const compactResult = await this.compactPollution({
        dryRun: false,
        keepPerCertFamily: KeyChainDexie.AUTO_KEEP_PER_CERT_FAMILY,
        deleteInvalidNonKeys: true,
        deleteMismatchedNonKeys: true,
        deleteOldCertVersions: true,
      });
      if (compactResult.deleted > 0) {
        console.info(
          'Keychain Dexie: auto-compact finished',
          {
            scanned: compactResult.entries,
            deleted: compactResult.deleted,
            remaining: afterKeyPurge - compactResult.deleted,
            ms: compactResult.ms,
          },
        );
      }
    } catch (err) {
      console.error('Keychain Dexie: auto-purge failed', err);
    }
  }

  private async ensureOpen(): Promise<boolean> {
    try {
      const wasOpen = this.db.isOpen();
      if (!wasOpen) {
        await this.db.open();
      }
      // Run the auto-purge on every successful open. The purge itself is
      // idempotent and gated by a row-count threshold, so a clean
      // keychain pays only a single count() call per app load.
      if (!this.purgeDone) {
        await this.maybeAutoPurge();
      }
      return true;
    } catch (err) {
      console.warn('Keychain Dexie: failed to open IndexedDB, using empty in-memory view', err);
      return false;
    }
  }

  public async list() {
    if (!(await this.ensureOpen())) {
      return [];
    }
    try {
      const startedAt = performance.now();
      const blobs: Uint8Array[] = [];
      const largest: Array<{ name: string; bytes: number }> = [];
      let entries = 0;
      let totalBytes = 0;
      let returnedBytes = 0;
      let skippedLarge = 0;
      let skippedOverEntryBudget = 0;
      let skippedOverBudget = 0;

      await this.db.keys.each((entry) => {
        entries++;
        const bytes = entry.blob.byteLength;
        totalBytes += bytes;
        largest.push({ name: entry.name, bytes });
        largest.sort((a, b) => b.bytes - a.bytes);
        largest.length = Math.min(largest.length, 5);

        if (bytes > MAX_KEYCHAIN_ENTRY_BYTES) {
          skippedLarge++;
          return;
        }
        if (blobs.length >= MAX_KEYCHAIN_LIST_ENTRIES) {
          skippedOverEntryBudget++;
          return;
        }
        if (returnedBytes + bytes > MAX_KEYCHAIN_LIST_BYTES) {
          skippedOverBudget++;
          return;
        }

        blobs.push(entry.blob);
        returnedBytes += bytes;
      });

      this.lastListStats = {
        entries,
        returned: blobs.length,
        skippedLarge,
        skippedOverEntryBudget,
        skippedOverBudget,
        totalBytes,
        returnedBytes,
        ms: performance.now() - startedAt,
        largest,
      };
      if (skippedLarge || skippedOverEntryBudget || skippedOverBudget) {
        console.warn('Keychain Dexie: skipped oversized persisted entries', this.lastListStats);
      }

      return blobs;
    } catch (err) {
      console.warn('Keychain Dexie: failed to read entries, returning empty list', err);
      return [];
    }
  }

  public async write(name: string, blob: Uint8Array) {
    if (!(await this.ensureOpen())) {
      return;
    }
    try {
      // Parse the keyName out of signing-key blobs so the keyName index
      // can dedup re-stores of the same logical key (e.g. when
      // KeyChainJS.InsertKey writes a fresh blob per call despite in-memory
      // dedup). Certs don't carry a keyName we can trust for dedup, so
      // they fall back to the filename-only check.
      let keyName = '';
      if (name.endsWith('.key')) {
        try {
          const info = classifyEntry({ name, blob });
          keyName = info.dataName ?? '';
        } catch {
          // Best-effort parse failure: fall back to filename dedup.
          keyName = '';
        }
      }

      if (keyName) {
        const existingByKeyName = await this.db.keys.where('keyName').equals(keyName).count();
        if (existingByKeyName > 0) {
          return;
        }
      } else {
        const exists = await this.db.keys.where('name').equals(name).count();
        if (exists > 0) {
          return;
        }
      }

      await this.db.keys.put({ name, blob, keyName });
    } catch (err) {
      console.error('Keychain Dexie: failed to write entry', err);
    }
  }

  public async delete(name: string | string[]) {
    if (!(await this.ensureOpen())) {
      return;
    }
    try {
      const names = Array.isArray(name) ? name : [name];
      await this.db.keys.bulkDelete(names);
    } catch (err) {
      console.error('Keychain Dexie: failed to delete entries', err);
    }
  }

  public async purgeDuplicateKeys(): Promise<{
    scanned: number;
    deleted: number;
    remaining: number;
    ms: number;
  }> {
    const startedAt = performance.now();
    if (!(await this.ensureOpen())) {
      return { scanned: 0, deleted: 0, remaining: 0, ms: 0 };
    }
    try {
      // Walk once: parse each .key blob's keyName, delete every subsequent
      // row that shares a keyName with an earlier one. Keys (ext === '.key')
      // are deduplicated; non-key rows are left alone.
      const seen = new Set<string>();
      const toDelete: string[] = [];
      const toUpdate: KeychainKeyRow[] = [];
      let scanned = 0;
      await this.db.keys.each((entry) => {
        scanned++;
        if (!entry.name.endsWith('.key')) return;
        let keyName = '';
        try {
          const info = classifyEntry(entry);
          keyName = info.dataName ?? '';
        } catch {
          return;
        }
        if (!keyName) return;
        if (seen.has(keyName)) {
          toDelete.push(entry.name);
          return;
        }
        seen.add(keyName);
        if (entry.keyName !== keyName) {
          toUpdate.push({ ...entry, keyName });
        }
      });

      // Dexie bulkDelete in batches of 1000 to avoid oversized transactions.
      const BATCH = 1000;
      for (let i = 0; i < toDelete.length; i += BATCH) {
        await this.db.keys.bulkDelete(toDelete.slice(i, i + BATCH));
      }
      for (let i = 0; i < toUpdate.length; i += BATCH) {
        await this.db.keys.bulkPut(toUpdate.slice(i, i + BATCH));
      }

      const remaining = await this.db.keys.count();
      const ms = performance.now() - startedAt;
      if (toDelete.length > 0) {
        console.info(
          'Keychain Dexie: purged duplicate .key rows',
          { scanned, deleted: toDelete.length, remaining, ms },
        );
      }
      return { scanned, deleted: toDelete.length, remaining, ms };
    } catch (err) {
      console.error('Keychain Dexie: failed to purge duplicate keys', err);
      return { scanned: 0, deleted: 0, remaining: 0, ms: performance.now() - startedAt };
    }
  }

  public async compactPollution(opts: KeychainCompactOptions = {}): Promise<KeychainCompactReport> {
    const dryRun = opts.dryRun ?? true;
    const keepPerCertFamily = opts.keepPerCertFamily ?? 8;
    const deleteInvalidNonKeys = opts.deleteInvalidNonKeys ?? true;
    const deleteMismatchedNonKeys = opts.deleteMismatchedNonKeys ?? true;
    const deleteOldCertVersions = opts.deleteOldCertVersions ?? true;
    const startedAt = performance.now();
    const byExt = new Map<string, number>();
    const byContentType = new Map<string, number>();
    const byIssuer = new Map<string, number>();
    const byCertFamily = new Map<string, number>();
    const familyCounts = new Map<string, number>();
    const familyKeep = new Map<string, Array<{ rowName: string; version: number }>>();
    const deleteReasons = new Map<string, number>();
    const samples: EntrySample[] = [];
    let entries = 0;
    let totalBytes = 0;
    let deleted = 0;

    if (!(await this.ensureOpen())) {
      return {
        entries,
        totalBytes,
        ms: performance.now() - startedAt,
        byExt: [],
        byContentType: [],
        byIssuer: [],
        byCertFamily: [],
        samples,
        dryRun,
        keepPerCertFamily,
        deleted,
        deleteReasons: [],
      };
    }

    await this.db.keys.each((entry) => {
      const info = classifyEntry(entry);
      entries++;
      totalBytes += info.bytes;
      inc(byExt, info.ext);
      inc(byContentType, info.contentType);
      inc(byIssuer, info.issuer ?? (info.contentType === 'cert' ? '(unknown-cert-issuer)' : info.contentType));
      if (info.certFamily) {
        inc(byCertFamily, info.certFamily);
        inc(familyCounts, info.certFamily);
        const keep = familyKeep.get(info.certFamily) ?? [];
        keep.push({ rowName: info.rowName, version: info.version ?? -1 });
        keep.sort((a, b) => b.version - a.version || a.rowName.localeCompare(b.rowName));
        keep.length = Math.min(keep.length, keepPerCertFamily);
        familyKeep.set(info.certFamily, keep);
      }
      if (samples.length < 20 && (info.parseError || info.contentType !== 'key')) {
        samples.push(sampleOf(info));
      }
    });

    const keepRows = new Set<string>();
    for (const keep of familyKeep.values()) {
      for (const entry of keep) keepRows.add(entry.rowName);
    }

    const reasonFor = (info: EntryClass) => {
      if (info.contentType === 'key') return null;
      if (info.parseError && info.ext !== '.key' && deleteInvalidNonKeys) {
        return 'invalid-non-key';
      }
      if (deleteMismatchedNonKeys && info.ext === '.cert' && info.contentType !== 'cert') {
        return 'mismatched-cert-row';
      }
      if (deleteMismatchedNonKeys && info.ext === '(other)' && info.contentType !== 'key') {
        return 'unknown-extension';
      }
      if (
        deleteOldCertVersions &&
        info.contentType === 'cert' &&
        info.certFamily &&
        (familyCounts.get(info.certFamily) ?? 0) > keepPerCertFamily &&
        !keepRows.has(info.rowName)
      ) {
        return `old-cert:${info.issuer ?? '(issuer)'}`;
      }
      return null;
    };

    const namesToDelete: string[] = [];

    await this.db.keys.each((entry) => {
      const info = classifyEntry(entry);
      const reason = reasonFor(info);
      if (!reason) return;
      inc(deleteReasons, reason);
      deleted++;
      if (!dryRun) namesToDelete.push(entry.name);
    });

    if (!dryRun) {
      for (let i = 0; i < namesToDelete.length; i += KEYCHAIN_COMPACT_BATCH_SIZE) {
        await this.db.keys.bulkDelete(namesToDelete.slice(i, i + KEYCHAIN_COMPACT_BATCH_SIZE));
      }
    }

    return {
      entries,
      totalBytes,
      ms: performance.now() - startedAt,
      byExt: topEntries(byExt, 20),
      byContentType: topEntries(byContentType, 20),
      byIssuer: topEntries(byIssuer, 20),
      byCertFamily: topEntries(byCertFamily, 20),
      samples,
      dryRun,
      keepPerCertFamily,
      deleted,
      deleteReasons: topEntries(deleteReasons, 20),
    };
  }

}
