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
}

import Dexie from 'dexie';

/**
 * KeychainJS implementation using Dexie.
 *
 * @license Apache-2.0
 */
export class KeyChainDexie implements KeyChainJS {
  private db = new Dexie('keychain') as Dexie & {
    keys: Dexie.Table<{ name: string; blob: Uint8Array }, string>;
  };

  constructor() {
    this.db.version(1).stores({
      keys: 'name',
    });
  }

  private async ensureOpen(): Promise<boolean> {
    try {
      if (!this.db.isOpen()) {
        await this.db.open();
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
      const list = await this.db.keys.toArray();
      return list.map((k) => k.blob);
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
      await this.db.keys.put({ name, blob });
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
}
