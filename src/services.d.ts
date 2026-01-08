import type { StatsDb } from '@/services/database/stats';
import type { ProjDbConstructor } from '@/services/database/proj_db';
import type { BootStateDb } from '@/services/database/boot_db';

declare global {
  // compile-time constants
  // eslint-disable-next-line no-var
  var __BUILD_VERSION__: string;

  // Global services
  interface OwnlyServices {
    stats: StatsDb;
    ProjDb: ProjDbConstructor;
    bootState: BootStateDb;

    getStorageRoot(): Promise<FileSystemDirectoryHandle>;
    streamSaver: typeof import('streamsaver'); // browser only
  }

  // eslint-disable-next-line no-var
  var _o: OwnlyServices;
}
