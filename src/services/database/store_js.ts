/**
 * StoreJS interface for storing NDN data.
 *
 * @license Apache-2.0
 */
export interface StoreJS {
  /** Get gets a page of packets from the storage */
  get(
    name: Uint8Array,
    prefix: boolean,
    last: Uint8Array | undefined,
  ): Promise<[Uint8Array, Uint8Array][]>;

  /** Put stores a packet in the storage */
  put(name: Uint8Array, blob: Uint8Array, tx: number): Promise<void>;

  /** Remove removes packets from the storage */
  remove(name: Uint8Array, prefix: boolean): Promise<void>;
  /** RemoveFlatRange removes a flat range from the storage */
  remove_flat_range(first: Uint8Array, last: Uint8Array): Promise<void>;

  /** Starts a bulk transaction */
  begin(): number;

  /** Commit a bulk transaction */
  commit(tx: number): Promise<void>;

  /** Rollback a bulk transaction */
  rollback(tx: number): Promise<void>;

  /** Atomically persist produced packets and pending history hashes. */
  history_commit(scope: string, packets: HistoryPacket[]): Promise<void>;
  /** Read a bounded page, in original enqueue order. */
  history_pending(scope: string, after: number, limit: number): Promise<HistoryPending[]>;
  /** Durably dequeue acknowledged entries; retain their signed packets. */
  history_ack(scope: string, rows: HistoryAck[]): Promise<void>;
}

import Dexie from 'dexie';

type PacketEntry = {
  name: Uint8Array;
  blob: Uint8Array;
};

export type HistoryPacket = PacketEntry & { hash: string };
export type HistoryAck = { id: number; hash: string };
export type HistoryPending = HistoryAck & { name: Uint8Array };
type HistoryEntry = HistoryPending & { scope: string };

type TxnEntry = { op: 'put'; entry: PacketEntry };

const STORE_PAGE_LIMIT = 64;

/**
 * StoreJS implementation using Dexie.
 *
 * @license Apache-2.0
 */
export class StoreDexie implements StoreJS {
  private db: Dexie & {
    packets: Dexie.Table<PacketEntry, Uint8Array>;
    history_outbox: Dexie.Table<HistoryEntry, number, Omit<HistoryEntry, 'id'>>;
  };

  private bulkTxns: Map<number, TxnEntry[]> = new Map();
  private txId = 0;

  constructor(name: string) {
    this.db = new Dexie(name) as any;
    this.db.version(1).stores({
      packets: 'name',
    });
    this.db.version(2).stores({
      packets: 'name',
      history_outbox: '++id,&[scope+hash],[scope+id]',
    });
  }

  /** Closing a connection never discards durable pending work. */
  public close() {
    this.db.close();
  }

  public async history_commit(scope: string, packets: HistoryPacket[]) {
    // The Go bridge supplies fresh JS buffers and hashes the original signed wire.
    await this.db.transaction('rw', this.db.packets, this.db.history_outbox, async () => {
      await this.db.packets.bulkPut(packets.map(({ name, blob }) => ({ name, blob })));
      const unique = [...new Map(packets.map((entry) => [entry.hash, entry])).values()];
      const rows = await this.db.history_outbox
        .where('[scope+hash]')
        .anyOf(unique.map(({ hash }) => [scope, hash]))
        .toArray();
      const pending = new Set(rows.map((row) => row.hash));
      const added = unique
        .filter(({ hash }) => !pending.has(hash))
        .map(({ name, hash }) => ({ scope, name, hash }));
      await this.db.history_outbox.bulkAdd(added);
    });
  }

  public async history_pending(
    scope: string,
    after: number,
    limit: number,
  ): Promise<HistoryPending[]> {
    const rows = await this.db.history_outbox
      .where('[scope+id]')
      .between([scope, after], [scope, Dexie.maxKey], false, true)
      .limit(Math.min(limit, STORE_PAGE_LIMIT))
      .toArray();
    return rows.map(({ id, name, hash }) => ({ id, name, hash }));
  }

  public async history_ack(scope: string, acknowledged: HistoryAck[]) {
    await this.db.transaction('rw', this.db.history_outbox, async () => {
      const rows = await this.db.history_outbox.bulkGet(acknowledged.map(({ id }) => id));
      const ids = rows.flatMap((row, i) =>
        row?.scope === scope && row.hash === acknowledged[i].hash ? [row.id] : [],
      );
      // This is a dequeue, not packet deletion. Row IDs prevent stale ACKs from
      // removing a later enqueue of the same digest (including from another tab).
      await this.db.history_outbox.bulkDelete(ids);
    });
  }

  public async get(
    name: Uint8Array,
    prefix: boolean,
    last: Uint8Array | undefined,
  ): Promise<[Uint8Array, Uint8Array][]> {
    // Get the range of packets
    const range = this.range(name, prefix, last);

    // ndnd hints at most one segment page. Keep this bounded so a bad range
    // cannot materialize a large IndexedDB slice into the JS heap.
    if (last && !prefix) {
      const entries = await range.limit(STORE_PAGE_LIMIT).toArray();
      return entries.map((pkt) => [pkt.name, pkt.blob]);
    }

    const pkt = await range.last();
    if (!pkt?.blob) return [];
    return [[pkt.name, pkt.blob]];
  }

  public async put(name: Uint8Array, blob: Uint8Array, bulkId: number) {
    if (bulkId) {
      this.txPush(bulkId, { op: 'put', entry: { name, blob } });
      return;
    }

    await this.db.packets.put({ name, blob });
  }

  public async remove(name: Uint8Array, prefix: boolean) {
    await this.range(name, prefix, undefined).delete();
  }

  public async remove_flat_range(first: Uint8Array, last: Uint8Array) {
    await this.range(first, false, last).delete();
  }

  public begin() {
    this.txId++;
    return this.txId;
  }

  public async commit(tx: number) {
    const ops = this.bulkTxns.get(tx);
    if (!ops) return;
    this.bulkTxns.delete(tx);

    const entries = ops.filter((op) => op.op === 'put').map((op) => op.entry);
    await this.db.packets.bulkPut(entries);
  }

  public async rollback(tx: number) {
    this.bulkTxns.delete(tx);
  }

  private txPush(tx: number, val: TxnEntry) {
    const ops = this.bulkTxns.get(tx) ?? [];
    ops.push(val);
    this.bulkTxns.set(tx, ops);
  }

  private range(name: Uint8Array, prefix: boolean, last: Uint8Array | undefined) {
    last = last ?? name;

    // Prefix always overwrites last
    if (prefix) {
      // T=255 for the next byte (see TLV encoding)
      last = new Uint8Array(name.length + 1);
      last.set(name, 0);
      last[name.length] = 255;
    }

    return this.db.packets.where('name').between(name, last, true, true);
  }
}
