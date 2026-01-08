/**
 * BootStateDb stores boot sync state per workspace group so it can be reused
 * when the workspace is reopened.
 */
export interface BootStateDb {
  /** Load persisted state for a boot sync group */
  get(group: string): Promise<Uint8Array | undefined>;

  /** Persist state for a boot sync group */
  put(group: string, state: Uint8Array): Promise<void>;

  /** Delete stored state for a boot sync group */
  del(group: string): Promise<void>;
}
