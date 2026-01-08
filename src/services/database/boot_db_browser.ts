import Dexie from 'dexie';

import type { BootStateDb } from './boot_db';

type BootStateEntry = {
  group: string;
  state: Uint8Array;
};

/**
 * Browser BootStateDb using IndexedDB via Dexie.
 */
export class IDBBootStateDb implements BootStateDb {
  private db: Dexie & {
    state: Dexie.Table<BootStateEntry, string>;
  };

  constructor() {
    this.db = new Dexie('ownly-boot') as any;
    this.db.version(1).stores({
      state: 'group',
    });
  }

  public async get(group: string): Promise<Uint8Array | undefined> {
    const entry = await this.db.state.get(group);
    if (!entry?.state?.length) return undefined;
    return entry.state;
  }

  public async put(group: string, state: Uint8Array): Promise<void> {
    await this.db.state.put({ group, state });
  }

  public async del(group: string): Promise<void> {
    await this.db.state.delete(group);
  }
}
