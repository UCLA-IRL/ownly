import Dexie from 'dexie';

import type { IWkspStats } from '@/services/types';
import type { StatsDb } from './stats';

export class IDBStatsDb implements StatsDb {
  private db = new Dexie('ownly') as Dexie & {
    workspaces: Dexie.Table<IWkspStats, string>;
  };

  constructor() {
    this.db.version(1).stores({
      workspaces: 'name',
    });
  }

  public async list(): Promise<IWkspStats[]> {
    return this.db.workspaces.toArray();
  }

  public async get(name: string): Promise<IWkspStats | undefined> {
    return this.db.workspaces.get(name);
  }

  public async put(name: string, stats: IWkspStats): Promise<void> {
    // Vue props/state can be proxies, which IndexedDB cannot structured-clone.
    const plainStats = JSON.parse(JSON.stringify(stats)) as IWkspStats;
    await this.db.workspaces.put(plainStats, name);
  }

  public async del(name: string): Promise<void> {
    await this.db.workspaces.delete(name);
  }
}
