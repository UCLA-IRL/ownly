import { DatabaseSync } from 'node:sqlite';

import type { BootStateDb } from './boot_db';

/**
 * Node BootStateDb backed by SQLite.
 */
export class NodeBootStateDb implements BootStateDb {
  private db = new DatabaseSync('ownly-boot.db');

  constructor() {
    this.db.exec(`
      CREATE TABLE IF NOT EXISTS boot_state (
        group_name TEXT PRIMARY KEY,
        state BLOB
      ) STRICT;
    `);
  }

  public async get(group: string): Promise<Uint8Array | undefined> {
    const sql = this.db.prepare(`SELECT state FROM boot_state WHERE group_name = ?`);
    const res: any = sql.get(group);
    if (!res?.state) return undefined;
    const state = new Uint8Array(res.state);
    return state.length ? state : undefined;
  }

  public async put(group: string, state: Uint8Array): Promise<void> {
    const sql = this.db.prepare(`
      INSERT INTO boot_state (group_name, state) VALUES (?, ?)
      ON CONFLICT(group_name) DO UPDATE SET state = excluded.state
    `);
    sql.run(group, Buffer.from(state));
  }

  public async del(group: string): Promise<void> {
    const sql = this.db.prepare(`DELETE FROM boot_state WHERE group_name = ?`);
    sql.run(group);
  }
}
