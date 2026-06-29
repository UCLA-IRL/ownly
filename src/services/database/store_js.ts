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
}

import Dexie from 'dexie';

type PacketEntry = {
  name: Uint8Array;
  blob: Uint8Array;
};

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
  };

  private bulkTxns: Map<number, TxnEntry[]> = new Map();
  private txId = 0;

  constructor(name: string) {
    this.db = new Dexie(name) as any;
    this.db.version(1).stores({
      packets: 'name',
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
