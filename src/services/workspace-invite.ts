import * as utils from '@/utils';
import * as Y from 'yjs';

import type { Router } from 'vue-router';
import type { WorkspaceAPI, MlsRefPub } from '@/services/ndn';
import type { SvsProvider } from '@/services/svs-provider';
import type { IOwnerDeviceRecord, IProfile, IWkspStats } from '@/services/types';
import  { OpenMlsLiteClient, OpenMlsLiteGroup } from '@/services/openmls-lite';
import { accountIdentityPrefix, encodeMlsIdentity, parseMlsIdentity } from '@/services/mls-identity';
import { GlobalBus } from '@/services/event-bus';
import {
  serializeFastJoinBundle,
  type FastJoinInvitation,
} from '@/services/fast-join';

const MLS_STORAGE_STATE_KEY = 'mls/storage/v1';
const MLS_GROUP_ID_STATE_KEY = 'mls/group-id/v1';
const MLS_RESET_SENTINEL = '__mls_reset__';
const MLS_PREJOIN_SESSION_ID = 'prejoin';
const MLS_COMMIT_BROADCAST = '__mls_commit_broadcast__';
const MLS_OWNER_RECOVERY_SESSION_PREFIX = 'owner-recovery:';
const OWNER_CONTROL_MAP = 'owner-control';
const OWNER_MASTER_DEVICE_ID_KEY = 'masterDeviceId';
const OWNER_DEVICES_MAP = 'owner-devices';
type MlsSessionInfo = { groupIdHex: string; epoch: bigint };

export class WorkspaceInviteManager {
  private readonly inviteeProfiles: Y.Map<IProfile>;
  private readonly ownerControl: Y.Map<string>;
  private readonly ownerDevices: Y.Map<IOwnerDeviceRecord>;
  private mlsClient: OpenMlsLiteClient | null = null;
  private mlsGroup: OpenMlsLiteGroup | null = null;
  private mlsInitPromise: Promise<void> | null = null;
  private pendingCommitRefs: MlsRefPub[] = [];
  private pendingOwnerRecoveryKpRefs: MlsRefPub[] = [];
  private onOwnerSessionAdvanced: ((sessionId: string) => Promise<void>) | null = null;

  // Deduplicate MLS publications delivered through live and snapshot paths.
  private readonly seenMlsPub: Set<string> = new Set();
  private readonly ownerControlObserver = () => {
    void this.syncMasterDeviceFlag().catch((e) => {
      console.warn('Failed to sync master-device role from shared owner control', e);
    });
  };

  private constructor(
    private readonly api: WorkspaceAPI,
    private readonly wsmeta: IWkspStats,
    private readonly provider: SvsProvider,
    private readonly doc: Y.Doc,
  ) {
    this.inviteeProfiles = doc.getMap<IProfile>('invite-map');
    this.ownerControl = doc.getMap<string>(OWNER_CONTROL_MAP);
    this.ownerDevices = doc.getMap<IOwnerDeviceRecord>(OWNER_DEVICES_MAP);

    // Only seed the owner entry from a device that is already designated as
    // master. A fresh owner follower must first join MLS and sync the real
    // shared invite state.
    if (!this.inviteeProfiles.has(api.name) && this.wsmeta.owner && this.wsmeta.isMasterDevice) {
      this.inviteeProfiles.set(api.name, { name: api.name, owner: true });
    }
  }

  public static async create(
    api: WorkspaceAPI,
    wsmeta: IWkspStats,
    provider: SvsProvider,
  ): Promise<WorkspaceInviteManager> {
    const doc = await provider.getDoc('invite');
    const mgr = new WorkspaceInviteManager(api, wsmeta, provider, doc);

    provider.setMlsCallbacks({
      onMlsKpRef: async (pubs) => mgr.onMlsKpRefs(pubs),
      onMlsWelcomeRef: async (pubs) => mgr.onMlsWelcomeRefs(pubs),
      onMlsCommitRef: async (pubs) => mgr.onMlsCommitRefs(pubs),
    });

    await mgr.initializeOwnerDeviceRole();
    await mgr.restoreMlsStateOnStartup();

    return mgr;
  }

  /**
   * Destroy the chat module
   */
  public async destroy() {
    this.ownerControl.unobserve(this.ownerControlObserver);
    this.doc.destroy();
    this.mlsGroup?.free();
    this.mlsGroup = null;
    this.mlsClient?.free();
    this.mlsClient = null;
  }

  public setOnOwnerSessionAdvanced(cb: (sessionId: string) => Promise<void>) {
    this.onOwnerSessionAdvanced = cb;
  }

  private sharedMasterDeviceId(): string | undefined {
    const masterDeviceId = this.ownerControl.get(OWNER_MASTER_DEVICE_ID_KEY);
    const trimmed = masterDeviceId?.trim();
    return trimmed ? trimmed : undefined;
  }

  private workspaceOwnerAccountId(): string {
    const group = utils.normalizePath(this.api.group).replace(/\/+$/, '');
    const idx = group.lastIndexOf('/');
    if (idx <= 0) {
      throw new Error(`Cannot infer workspace owner from ${this.api.group}`);
    }
    return group.slice(0, idx);
  }

  private ownerRecoveryTarget(pub: MlsRefPub): string | null {
    if (!pub.session_id.startsWith(MLS_OWNER_RECOVERY_SESSION_PREFIX)) return null;
    const target = pub.session_id.slice(MLS_OWNER_RECOVERY_SESSION_PREFIX.length).trim();
    return target ? utils.normalizePath(target) : null;
  }

  private defaultOwnerDeviceLabel(deviceId: string): string {
    return `Device ${deviceId.slice(0, 8)}`;
  }

  private localOwnerDeviceRecord(now = Date.now()): IOwnerDeviceRecord {
    if (!this.wsmeta.owner || !this.wsmeta.deviceId) {
      throw new Error('Local device is not an owner device');
    }

    const existing = this.ownerDevices.get(this.wsmeta.deviceId);
    return {
      deviceId: this.wsmeta.deviceId,
      ownerId: this.api.name,
      label: existing?.label?.trim() || `Device ${this.wsmeta.deviceId.slice(0, 8)}`,
      registeredAt: existing?.registeredAt ?? now,
    };
  }

  private upsertLocalOwnerDeviceRecord(now = Date.now()): void {
    if (!this.wsmeta.owner || !this.wsmeta.deviceId) return;
    this.ownerDevices.set(this.wsmeta.deviceId, this.localOwnerDeviceRecord(now));
  }

  private canRegisterLocalOwnerDeviceRecord(): boolean {
    return !!this.wsmeta.owner &&
      !!this.wsmeta.deviceId &&
      (
        this.wsmeta.isMasterDevice ||
        this.wsmeta.mlsOwnerBootstrapped ||
        !!this.wsmeta.mlsKeys?.length ||
        !!this.mlsGroup
      );
  }

  private maybeRegisterLocalOwnerDeviceRecord(now = Date.now()): void {
    if (!this.canRegisterLocalOwnerDeviceRecord()) return;
    this.upsertLocalOwnerDeviceRecord(now);
  }

  private upsertOwnerDeviceFromMlsIdentity(identityValue: string, now = Date.now()): boolean {
    let identity: ReturnType<typeof parseMlsIdentity>;
    try {
      identity = parseMlsIdentity(identityValue);
    } catch {
      return false;
    }
    if (identity.accountId !== this.workspaceOwnerAccountId()) return false;

    const existing = this.ownerDevices.get(identity.deviceId);
    this.ownerDevices.set(identity.deviceId, {
      deviceId: identity.deviceId,
      ownerId: identity.accountId,
      label: existing?.label?.trim() || this.defaultOwnerDeviceLabel(identity.deviceId),
      registeredAt: existing?.registeredAt ?? now,
    });
    return true;
  }

  private reconcileOwnerDeviceRegistryFromMls(now = Date.now()): void {
    if (!this.wsmeta.owner || !this.mlsGroup) return;

    for (const identity of this.mlsGroup.memberIdentities()) {
      this.upsertOwnerDeviceFromMlsIdentity(identity, now);
    }
    this.maybeRegisterLocalOwnerDeviceRecord(now);
  }

  private removeOwnerDeviceRecordFromInvitee(invitee: string): void {
    let identity: ReturnType<typeof parseMlsIdentity>;
    try {
      identity = parseMlsIdentity(invitee);
    } catch {
      return;
    }
    if (identity.accountId !== this.workspaceOwnerAccountId()) return;
    this.ownerDevices.delete(identity.deviceId);
  }

  private isLocalRemovalTarget(invitee: string): boolean {
    if (invitee === this.api.name) return true;
    try {
      return invitee === this.currentMlsIdentity();
    } catch {
      return false;
    }
  }

  private async initializeOwnerDeviceRole(): Promise<void> {
    if (!this.wsmeta.owner || !this.wsmeta.deviceId) {
      await this.syncMasterDeviceFlag();
      return;
    }

    this.ownerControl.observe(this.ownerControlObserver);
    this.maybeRegisterLocalOwnerDeviceRecord();

    if (!this.sharedMasterDeviceId() && this.wsmeta.isMasterDevice) {
      this.ownerControl.set(OWNER_MASTER_DEVICE_ID_KEY, this.wsmeta.deviceId);
    }

    await this.syncMasterDeviceFlag();
  }

  private async syncMasterDeviceFlag(): Promise<void> {
    const nextIsMaster =
      !!this.wsmeta.owner &&
      !!this.wsmeta.deviceId &&
      this.sharedMasterDeviceId() === this.wsmeta.deviceId;

    if (this.wsmeta.isMasterDevice === nextIsMaster) return;

    this.wsmeta.isMasterDevice = nextIsMaster;
    await _o.stats.put(this.wsmeta.name, this.wsmeta);
  }

  public isMasterDevice(): boolean {
    return !!this.wsmeta.owner &&
      !!this.wsmeta.deviceId &&
      this.sharedMasterDeviceId() === this.wsmeta.deviceId;
  }

  private async deletePeerIdentityEntries(identity: string): Promise<void> {
    const target = identity.trim();
    if (!target) return;

    try {
      await this.api.forget_peer_identity(target);
    } catch (err) {
      console.warn(`Failed to clean peer identity entries for ${target}`, err);
    }
  }

  public hasMlsGroup(): boolean {
    return !!this.mlsGroup;
  }

  public shouldAutoRecoverOwnerMls(): boolean {
    return !!this.wsmeta.owner && !this.isMasterDevice() && !this.mlsGroup;
  }

  public getMasterOwnerDevice(): IOwnerDeviceRecord | undefined {
    const masterDeviceId = this.sharedMasterDeviceId();
    return masterDeviceId ? this.ownerDevices.get(masterDeviceId) : undefined;
  }

  public getOwnerDevices(): IOwnerDeviceRecord[] {
    return Array
      .from(this.ownerDevices.values())
      .sort((a, b) => a.registeredAt - b.registeredAt || a.deviceId.localeCompare(b.deviceId));
  }

  public async setOwnerDeviceLabel(deviceId: string, label: string): Promise<void> {
    if (!this.wsmeta.owner) {
      throw new Error('Only owner devices can rename registered owner devices');
    }

    const existing = this.ownerDevices.get(deviceId);
    if (!existing) {
      throw new Error(`Owner device ${deviceId} is not registered`);
    }

    const trimmedLabel = label.trim();
    this.ownerDevices.set(deviceId, {
      ...existing,
      label: trimmedLabel || `Device ${deviceId.slice(0, 8)}`,
    });
  }

  public async transferMasterRole(deviceId: string): Promise<void> {
    if (!this.wsmeta.owner || !this.wsmeta.deviceId) {
      throw new Error('Only owner devices can transfer control');
    }

    const targetDeviceId = deviceId.trim();
    if (!targetDeviceId) {
      throw new Error('Missing target owner device ID');
    }

    const claimingLocalDevice = targetDeviceId === this.wsmeta.deviceId;
    if (!this.isMasterDevice() && !claimingLocalDevice) {
      throw new Error('Only the current master owner device can transfer control to another device');
    }
    if (claimingLocalDevice && !this.mlsGroup) {
      throw new Error('This owner device must join MLS before it can become master');
    }

    if (claimingLocalDevice) {
      this.maybeRegisterLocalOwnerDeviceRecord();
    }

    const target = this.ownerDevices.get(targetDeviceId);
    if (!target) {
      throw new Error(`Owner device ${targetDeviceId} is not registered`);
    }
    if (this.sharedMasterDeviceId() === targetDeviceId) {
      return;
    }

    this.ownerControl.set(OWNER_MASTER_DEVICE_ID_KEY, targetDeviceId);
    await this.syncMasterDeviceFlag();
  }

  private assertOwnerCanMergeMls(action: string): void {
    if (!this.wsmeta.owner) return;
    if (this.isMasterDevice()) return;
    throw new Error(`Only the master owner device can ${action}`);
  }

  private async notifyOwnerSessionAdvanced(sessionId: string, allowRecoveryHelper = false): Promise<void> {
    if (!this.onOwnerSessionAdvanced) return;
    if (!allowRecoveryHelper && (!this.wsmeta.owner || !this.isMasterDevice())) return;
    await this.onOwnerSessionAdvanced(sessionId);
  }

  private pubKey(pub: MlsRefPub): string {
    return `${pub.publisher}|${pub.boot_time}|${pub.seq_num}`;
  }

  private uniqueOrdered(pubs: MlsRefPub[]): MlsRefPub[] {
    const out: MlsRefPub[] = [];
    for (const p of pubs) {
      const key = this.pubKey(p);
      if (this.seenMlsPub.has(key)) continue;
      this.seenMlsPub.add(key);
      out.push(p);
    }
    out.sort((a, b) =>
      a.boot_time === b.boot_time ? a.seq_num - b.seq_num : a.boot_time - b.boot_time,
    );
    return out;
  }

  private orderedPendingCommits(): MlsRefPub[] {
    return [...this.pendingCommitRefs].sort((a, b) =>
      a.boot_time === b.boot_time ? a.seq_num - b.seq_num : a.boot_time - b.boot_time,
    );
  }

  private orderedPendingOwnerRecoveryKeyPackages(): MlsRefPub[] {
    return [...this.pendingOwnerRecoveryKpRefs].sort((a, b) =>
      a.boot_time === b.boot_time ? a.seq_num - b.seq_num : a.boot_time - b.boot_time,
    );
  }

  private enqueuePendingOwnerRecoveryKeyPackage(pub: MlsRefPub): void {
    const key = this.pubKey(pub);
    if (this.pendingOwnerRecoveryKpRefs.some((p) => this.pubKey(p) === key)) return;
    this.pendingOwnerRecoveryKpRefs.push(pub);
  }

  /**
   * get MLS group, throw error if not initialized
   */
  private checkMlsInitialized(): OpenMlsLiteGroup {
    if (!this.mlsGroup) {
      throw new Error("MLS group is not initialized");
    }
    return this.mlsGroup;
  }

  private currentMlsIdentity(): string {
    if (!this.wsmeta.deviceId) {
      throw new Error('Missing local MLS device ID');
    }

    return encodeMlsIdentity(this.api.name, this.wsmeta.deviceId);
  }

  private async keyPackageIdentity(kp: Uint8Array): Promise<string> {
    const client = await this.getMlsClient();
    return new TextDecoder().decode(client.keyPackageIdentity(kp));
  }

  /**
   * Get MLS client instance
   */
  private async getMlsClient(): Promise<OpenMlsLiteClient> {
    if (!this.mlsClient) {
      this.mlsClient = await OpenMlsLiteClient.create(
        await this.api.export_workspace_cert(),
        this.currentMlsIdentity(),
      );
    }
    return this.mlsClient;
  }

  private currentMlsSessionId(): string {
    const group = this.checkMlsInitialized();
    const groupIdHex = utils.toHex(group.groupIdBytes());
    const epoch = group.epoch();
    if (epoch < 0n) {
      throw new Error(`Invalid MLS epoch for session ID: ${epoch}`);
    }
    return `${groupIdHex}:${epoch.toString()}`;
  }

  private currentMlsSessionInfo(): MlsSessionInfo {
    const group = this.checkMlsInitialized();
    const groupIdHex = utils.toHex(group.groupIdBytes());
    const epoch = group.epoch();
    if (epoch < 0n) {
      throw new Error(`Invalid MLS epoch for session ID: ${epoch}`);
    }
    return { groupIdHex, epoch };
  }

  private parseMlsSessionId(sessionId: string): MlsSessionInfo | null {
    if (!sessionId || sessionId === MLS_PREJOIN_SESSION_ID) return null;
    const idx = sessionId.lastIndexOf(':');
    if (idx <= 0 || idx === sessionId.length - 1) {
      throw new Error(`Invalid MLS session ID: ${sessionId}`);
    }
    const groupIdHex = sessionId.slice(0, idx);
    const epochStr = sessionId.slice(idx + 1);
    const epoch = BigInt(epochStr);
    if (epoch < 0n) {
      throw new Error(`Invalid MLS epoch in session ID: ${sessionId}`);
    }
    return { groupIdHex, epoch };
  }

  /**
   * Install the workspace shared secret for the current MLS group state.
   */
  private async rotateWorkspaceMlsKey(expectedSessionId?: string): Promise<Uint8Array> {
    const group = this.checkMlsInitialized();
    const sessionId = this.currentMlsSessionId();
    if (expectedSessionId != null && expectedSessionId !== sessionId) {
      throw new Error(`MLS session mismatch: expected ${expectedSessionId}, got ${sessionId}`);
    }

    const key = group.exportWorkspaceSecret();

    if (!(key instanceof Uint8Array) || key.length !== 32) {
      throw new Error(`Invalid MLS export key length: ${key?.length ?? 'unknown'}`);
    }

    await this.api.set_encrypt_key(sessionId, key);

    const hex = utils.toHex(key);

    const recent = [
      { sessionId, mlsKey: hex },
      ...(this.wsmeta.mlsKeys ?? []).filter((x) => x.sessionId !== sessionId),
    ].slice(0, 5);

    this.wsmeta.mlsKeys = recent;
    this.wsmeta.mlsJoinRequested = false;
    await _o.stats.put(this.wsmeta.name, this.wsmeta);
    await this.persistMlsState();
    return key;
  }

  private async persistMlsState(): Promise<void> {
    if (!this.mlsClient || !this.mlsGroup) return;
    const snapshot = this.mlsClient.exportStorageSnapshot();
    const groupId = this.mlsGroup.groupIdBytes();
    await this.provider.statePut(MLS_STORAGE_STATE_KEY, snapshot);
    await this.provider.statePut(MLS_GROUP_ID_STATE_KEY, groupId);
  }

  private async clearPersistedMlsState(): Promise<void> {
    await this.provider.statePut(MLS_STORAGE_STATE_KEY, new Uint8Array());
    await this.provider.statePut(MLS_GROUP_ID_STATE_KEY, new Uint8Array());
  }

  private isPersistedStatePresent(state: Uint8Array | undefined): boolean {
    return !!state && state.byteLength > 0;
  }

  private async restoreLegacyWorkspaceKey(): Promise<void> {
    if (!this.wsmeta.psk || !this.wsmeta.dsk) {
      throw new Error('Cannot reset MLS state without legacy PSK+DSK fallback');
    }
    await this.api.set_encrypt_keys(
      utils.fromHex(this.wsmeta.psk),
      utils.fromHex(this.wsmeta.dsk),
    );
  }

  private async resetLocalMlsState(source: string): Promise<void> {
    console.warn(`Resetting MLS state: ${source}`);

    this.mlsGroup?.free();
    this.mlsGroup = null;
    this.mlsClient?.free();
    this.mlsClient = null;
    this.pendingCommitRefs = [];
    this.pendingOwnerRecoveryKpRefs = [];

    this.wsmeta.mlsJoinRequested = false;
    this.wsmeta.mlsJoinRequestedAt = undefined;
    this.wsmeta.mlsJoinAttempts = undefined;
    this.wsmeta.mlsOwnerBootstrapped = false;
    this.wsmeta.mlsKeys = undefined;
    this.wsmeta.ownerRecoveryHelper = undefined;
    this.wsmeta.ownerRecoveryRequestedAt = undefined;

    await this.clearPersistedMlsState();
    await this.restoreLegacyWorkspaceKey();
    await _o.stats.put(this.wsmeta.name, this.wsmeta);

    if (!this.wsmeta.owner || !this.isMasterDevice()) {
      try {
        await this.requestMlsJoin();
      } catch (e) {
        console.warn('Failed to republish MLS key package after reset', e);
      }
    }
  }

  private async revokeLocalWorkspaceAccess(source: string): Promise<void> {
    console.warn(`Revoking workspace access: ${source}`);

    this.mlsGroup?.free();
    this.mlsGroup = null;
    this.mlsClient?.free();
    this.mlsClient = null;
    this.pendingCommitRefs = [];
    this.pendingOwnerRecoveryKpRefs = [];

    this.wsmeta.mlsJoinRequested = false;
    this.wsmeta.mlsJoinRequestedAt = undefined;
    this.wsmeta.mlsJoinAttempts = undefined;
    this.wsmeta.mlsOwnerBootstrapped = false;
    this.wsmeta.mlsKeys = undefined;
    this.wsmeta.ownerRecoveryHelper = undefined;
    this.wsmeta.ownerRecoveryRequestedAt = undefined;
    this.wsmeta.dsk = null;
    this.wsmeta.dskExch = undefined;
    this.wsmeta.revoked = true;

    await this.clearPersistedMlsState();
    await _o.stats.put(this.wsmeta.name, this.wsmeta);
    GlobalBus.emit(
      'workspace-revoked',
      this.wsmeta.name,
      new Error('You were removed from this workspace. Rejoin with a fresh invitation to regain access.'),
    );
  }

  private async restoreMlsStateIfAvailable(): Promise<boolean> {
    const [snapshot, groupId] = await Promise.all([
      this.provider.stateGet(MLS_STORAGE_STATE_KEY),
      this.provider.stateGet(MLS_GROUP_ID_STATE_KEY),
    ]);

    const hasSnapshot = this.isPersistedStatePresent(snapshot);
    const hasGroupId = this.isPersistedStatePresent(groupId);

    if (!hasSnapshot && !hasGroupId) return false;
    if (!hasSnapshot || !hasGroupId) {
      throw new Error('Incomplete MLS persisted state');
    }

    const client = await this.getMlsClient();
    client.importStorageSnapshot(snapshot!);
    this.mlsGroup?.free();
    this.mlsGroup = client.loadGroup(groupId!);

    await this.rotateWorkspaceMlsKey();
    await this.drainPendingCommitRefs();
    await this.drainPendingOwnerRecoveryKeyPackages();
    this.reconcileOwnerDeviceRegistryFromMls();

    if (this.wsmeta.owner && this.isMasterDevice() && !this.wsmeta.mlsOwnerBootstrapped) {
      this.wsmeta.mlsOwnerBootstrapped = true;
      await _o.stats.put(this.wsmeta.name, this.wsmeta);
    }
    this.maybeRegisterLocalOwnerDeviceRecord();
    return true;
  }

  private async drainPendingCommitRefs(): Promise<void> {
    if (!this.mlsGroup || this.pendingCommitRefs.length === 0) return;

    const pending = this.orderedPendingCommits();
    const stillPending: MlsRefPub[] = [];

    for (const pub of pending) {
      if (this.isLocalRemovalTarget(pub.invitee)) {
        await this.revokeLocalWorkspaceAccess(`removed from MLS group by ${pub.publisher}`);
        continue;
      }
      try {
        const current = this.currentMlsSessionInfo();
        const target = this.parseMlsSessionId(pub.session_id);
        if (!target) {
          continue;
        }
        if (target.groupIdHex !== current.groupIdHex) {
          console.warn('Dropping queued MLS commit for different group instance', pub);
          continue;
        }
        if (target.epoch <= current.epoch) {
          continue; // stale commit from before we joined/applied newer epochs
        }
        if (target.epoch > current.epoch + 1n) {
          stillPending.push(pub);
          continue;
        }
        const commit = (await this.provider.consumeBlob(pub.blob_name)).data;
        await this.applyMlsCommit(commit, pub.session_id);
        this.removeOwnerDeviceRecordFromInvitee(pub.invitee);
      } catch (e) {
        console.warn('Failed to apply queued MLS commit ref', pub, e);
      }
    }

    this.pendingCommitRefs = stillPending;
  }

  private async drainPendingOwnerRecoveryKeyPackages(): Promise<void> {
    if (!this.mlsGroup || this.pendingOwnerRecoveryKpRefs.length === 0) return;

    const pending = this.orderedPendingOwnerRecoveryKeyPackages();
    this.pendingOwnerRecoveryKpRefs = [];
    for (const pub of pending) {
      await this.tryProcessOwnerRecoveryKeyPackage(pub);
    }
  }

  private async restoreMlsStateOnStartup(): Promise<void> {
    try {
      const restored = await this.restoreMlsStateIfAvailable();
      if (!restored && this.wsmeta.owner && this.isMasterDevice() && this.wsmeta.mlsOwnerBootstrapped) {
        throw new Error('Owner MLS restore failed: missing persisted state');
      }
    } catch (e) {
      if (this.wsmeta.owner && this.isMasterDevice() && this.wsmeta.mlsOwnerBootstrapped) {
        throw e;
      }
      console.warn('MLS restore failed; resetting join request flags', e);
      this.wsmeta.mlsJoinRequested = false;
      this.wsmeta.mlsJoinRequestedAt = undefined;
      await _o.stats.put(this.wsmeta.name, this.wsmeta);
    }
  }

  /**
   * Get the merge-capable owner MLS group instance.
   */
  private async getMlsGroup(): Promise<OpenMlsLiteGroup> {
    if (!this.wsmeta.owner || !this.isMasterDevice()) {
      throw new Error('Only the master owner device can use the owner MLS group');
    }
    if (!this.mlsGroup) {
      throw new Error('Owner MLS group not initialized. Call bootstrapOwnerMls() first.');
    }
    return this.mlsGroup;
  }

  /**
   * Join a group from a welcome message
   *
   * @param welcome The welcome message to join from
   */
  public async joinMlsFromWelcome(welcome: Uint8Array, sessionId: string) : Promise<void> {
    const client = await this.getMlsClient();
    this.mlsGroup?.free();
    this.mlsGroup = client.joinFromWelcome(welcome);
    await this.rotateWorkspaceMlsKey(sessionId);
    await this.drainPendingCommitRefs();
    await this.drainPendingOwnerRecoveryKeyPackages();
    this.reconcileOwnerDeviceRegistryFromMls();
    if (this.wsmeta.owner && this.wsmeta.ownerRecoveryRequestedAt) {
      this.wsmeta.ownerRecoveryRequestedAt = undefined;
      this.wsmeta.ownerRecoveryHelper = undefined;
      await this.syncMasterDeviceFlag();
      await _o.stats.put(this.wsmeta.name, this.wsmeta);
    }
  }

  /**
   * Apply a commit already merged by the owner
   *
   * @param commit The commit to apply
   */
  public async applyMlsCommit(commit: Uint8Array, sessionId: string) : Promise<void> {
    const group = this.checkMlsInitialized();
    group.applyCommit(commit);
    await this.rotateWorkspaceMlsKey(sessionId);
    this.reconcileOwnerDeviceRegistryFromMls();
  }

  private async onMlsKpRefs(pubs: MlsRefPub[]): Promise<void> {
    if (this.wsmeta.owner && this.isMasterDevice() && !this.mlsGroup && !this.wsmeta.mlsOwnerBootstrapped) {
      await this.bootstrapOwnerMls();
    }

    for (const pub of this.uniqueOrdered(pubs)) {
      if (this.ownerRecoveryTarget(pub)) {
        await this.tryProcessOwnerRecoveryKeyPackage(pub);
        continue;
      }
      if (!this.wsmeta.owner || !this.isMasterDevice()) continue;
      const kp = (await this.provider.consumeBlob(pub.blob_name)).data;
      let inviteeIdentity: string;
      let commit: Uint8Array;
      let welcome: Uint8Array;
      let sessionId: string;
      try {
        ({ inviteeIdentity, commit, welcome, sessionId } = await this.addMemberFromKeyPackage(kp, pub.invitee));
      } catch (e) {
        console.warn(`Ignoring invalid MLS KP ref from ${pub.invitee}`, e);
        continue;
      }

      const inviteeKey = utils.escapeUrlName(inviteeIdentity);
      const commitBlob = await this.provider.publishBlob(`mls-commit-${inviteeKey}`, commit);
      const welcomeBlob = await this.provider.publishBlob(`mls-welcome-${inviteeKey}`, welcome);

      await this.provider.svs.pub_mls_commit_ref(MLS_COMMIT_BROADCAST, commitBlob, sessionId);
      await this.provider.svs.pub_mls_welcome_ref(inviteeIdentity, welcomeBlob, sessionId);
      await this.notifyOwnerSessionAdvanced(sessionId);
    }
  }

  private async tryProcessOwnerRecoveryKeyPackage(pub: MlsRefPub): Promise<void> {
    const targetHelper = this.ownerRecoveryTarget(pub);
    const localAccount = utils.normalizePath(this.api.name);
    const localIdentity = this.currentMlsIdentity();
    if (!targetHelper || (targetHelper !== localAccount && targetHelper !== localIdentity)) return;
    if (!this.mlsGroup) {
      console.warn('Ignoring owner recovery KP ref; local device is not in MLS', pub);
      this.enqueuePendingOwnerRecoveryKeyPackage(pub);
      return;
    }

    console.log(`Processing owner recovery MLS KP ref from ${pub.invitee}`);
    const kp = (await this.provider.consumeBlob(pub.blob_name)).data;
    let inviteeIdentity: string;
    let commit: Uint8Array;
    let welcome: Uint8Array;
    let sessionId: string;
    try {
      ({ inviteeIdentity, commit, welcome, sessionId } = await this.addOwnerRecoveryFromKeyPackage(kp, pub.invitee));
    } catch (e) {
      console.warn(`Ignoring invalid owner recovery MLS KP ref from ${pub.invitee}`, e);
      return;
    }

    const inviteeKey = utils.escapeUrlName(inviteeIdentity);
    const commitBlob = await this.provider.publishBlob(`mls-commit-owner-recovery-${inviteeKey}`, commit);
    const welcomeBlob = await this.provider.publishBlob(`mls-welcome-owner-recovery-${inviteeKey}`, welcome);

    await this.provider.svs.pub_mls_commit_ref(MLS_COMMIT_BROADCAST, commitBlob, sessionId);
    await this.provider.svs.pub_mls_welcome_ref(inviteeIdentity, welcomeBlob, sessionId);
    await this.notifyOwnerSessionAdvanced(sessionId, true);
  }

  private async onMlsWelcomeRefs(pubs: MlsRefPub[]): Promise<void> {
    const currentIdentity = this.currentMlsIdentity();
    for (const pub of this.uniqueOrdered(pubs)) {
      if (pub.invitee !== currentIdentity) continue;
      const welcome = (await this.provider.consumeBlob(pub.blob_name)).data;
      await this.joinMlsFromWelcome(welcome, pub.session_id);
    }
  }

  private async onMlsCommitRefs(pubs: MlsRefPub[]): Promise<void> {
    for (const pub of this.uniqueOrdered(pubs)) {
      if (pub.invitee === MLS_RESET_SENTINEL) {
        if (this.wsmeta.owner && this.isMasterDevice()) continue;
        await this.resetLocalMlsState(`remote group reset from ${pub.publisher}`);
        continue;
      }
      if (this.isLocalRemovalTarget(pub.invitee)) {
        if (this.mlsGroup || this.wsmeta.mlsKeys?.length) {
          await this.revokeLocalWorkspaceAccess(`removed from MLS group by ${pub.publisher}`);
        }
        continue; // self-targeted commit refs are member removals
      }
      if (!this.mlsGroup) {
        this.pendingCommitRefs.push(pub);
        continue;
      }
      const current = this.currentMlsSessionInfo();
      const target = this.parseMlsSessionId(pub.session_id);
      if (!target) {
        continue;
      }
      if (target.groupIdHex !== current.groupIdHex) {
        console.warn('Ignoring MLS commit for different group instance', pub);
        continue;
      }
      if (target.epoch <= current.epoch) {
        continue;
      }
      if (target.epoch > current.epoch + 1n) {
        this.pendingCommitRefs.push(pub);
        continue;
      }
      const commit = (await this.provider.consumeBlob(pub.blob_name)).data;
      await this.applyMlsCommit(commit, pub.session_id);
      this.removeOwnerDeviceRecordFromInvitee(pub.invitee);
      await this.drainPendingCommitRefs();
    }
  }

  public async publishKeyPackageRef(sessionId = MLS_PREJOIN_SESSION_ID): Promise<void> {
    const client = await this.getMlsClient();
    const kp = client.keyPackage();
    const identity = this.currentMlsIdentity();
    const inviteeKey = utils.escapeUrlName(identity);
    const blob = await this.provider.publishBlob(`mls-kp-${inviteeKey}`, kp);
    await this.provider.svs.pub_mls_kp_ref(identity, blob, sessionId);
  }

  public async requestMlsJoin(): Promise<void> {
    if (this.wsmeta.owner && this.isMasterDevice()) {
      throw new Error('Master owner device does not request MLS join via key package');
    }

    try {
      await this.publishKeyPackageRef();
      this.wsmeta.mlsJoinRequested = true;
      this.wsmeta.mlsJoinRequestedAt = Date.now();
      this.wsmeta.mlsJoinAttempts = (this.wsmeta.mlsJoinAttempts ?? 0) + 1;
      this.wsmeta.ownerRecoveryHelper = undefined;
      this.wsmeta.ownerRecoveryRequestedAt = undefined;
    } catch (e) {
      this.wsmeta.mlsJoinRequested = false;
      this.wsmeta.mlsJoinRequestedAt = undefined;
      await _o.stats.put(this.wsmeta.name, this.wsmeta);
      throw e;
    }

    await _o.stats.put(this.wsmeta.name, this.wsmeta);
  }

  public async requestOwnerRecoveryMlsJoin(helperAccountId: string): Promise<void> {
    if (!this.wsmeta.owner) {
      throw new Error('Only owner devices can request owner recovery');
    }
    if (this.isMasterDevice()) {
      throw new Error('Master owner device does not need owner recovery');
    }
    if (this.mlsGroup) {
      throw new Error('This owner device is already in MLS; make it master instead');
    }

    const helper = utils.normalizePath(helperAccountId.trim());
    if (
      !helper ||
      helper === '/' ||
      helper === this.currentMlsIdentity() ||
      helper === utils.normalizePath(this.api.name) ||
      helper === this.workspaceOwnerAccountId()
    ) {
      throw new Error('Choose another online workspace device as recovery helper');
    }

    try {
      await this.publishKeyPackageRef(`${MLS_OWNER_RECOVERY_SESSION_PREFIX}${helper}`);
      this.wsmeta.mlsJoinRequested = true;
      this.wsmeta.mlsJoinRequestedAt = Date.now();
      this.wsmeta.mlsJoinAttempts = (this.wsmeta.mlsJoinAttempts ?? 0) + 1;
      this.wsmeta.ownerRecoveryHelper = helper;
      this.wsmeta.ownerRecoveryRequestedAt = Date.now();
    } catch (e) {
      this.wsmeta.mlsJoinRequested = false;
      this.wsmeta.mlsJoinRequestedAt = undefined;
      this.wsmeta.ownerRecoveryHelper = undefined;
      this.wsmeta.ownerRecoveryRequestedAt = undefined;
      await _o.stats.put(this.wsmeta.name, this.wsmeta);
      throw e;
    }

    await _o.stats.put(this.wsmeta.name, this.wsmeta);
  }

  public async addMemberFromKeyPackage(
    kp: Uint8Array,
    expectedIdentity?: string,
  ): Promise<{ inviteeIdentity: string; commit: Uint8Array; welcome: Uint8Array; sessionId: string }> {
    if (!this.wsmeta.owner) {
      throw new Error('Only workspace owner can add members');
    }
    this.assertOwnerCanMergeMls('add members');
    if (!(kp instanceof Uint8Array) || kp.length === 0) {
      throw new Error('Invalid key package');
    }

    const inviteeIdentity = await this.keyPackageIdentity(kp);
    if (expectedIdentity && inviteeIdentity !== expectedIdentity) {
      throw new Error(`MLS key package identity mismatch: expected ${expectedIdentity}, got ${inviteeIdentity}`);
    }
    const identity = parseMlsIdentity(inviteeIdentity);
    if (!this.inviteeProfiles.has(identity.accountId)) {
      throw new Error(`Invitee ${identity.accountId} is not authorized`);
    }

    const group = await this.getMlsGroup();
    const { commit, welcome } = group.addMembers([kp]);

    // Owner finalizes own pending commit.
    group.mergePendingCommit();

    const sessionId = this.currentMlsSessionId();
    await this.rotateWorkspaceMlsKey(sessionId);
    this.reconcileOwnerDeviceRegistryFromMls();

    return {
      inviteeIdentity,
      commit,
      welcome,
      sessionId,
    };
  }

  private async addOwnerRecoveryFromKeyPackage(
    kp: Uint8Array,
    expectedIdentity?: string,
  ): Promise<{ inviteeIdentity: string; commit: Uint8Array; welcome: Uint8Array; sessionId: string }> {
    if (!(kp instanceof Uint8Array) || kp.length === 0) {
      throw new Error('Invalid key package');
    }

    const inviteeIdentity = await this.keyPackageIdentity(kp);
    if (expectedIdentity && inviteeIdentity !== expectedIdentity) {
      throw new Error(`MLS key package identity mismatch: expected ${expectedIdentity}, got ${inviteeIdentity}`);
    }

    const identity = parseMlsIdentity(inviteeIdentity);
    if (identity.accountId !== this.workspaceOwnerAccountId()) {
      throw new Error(`Owner recovery KP is for ${identity.accountId}, not workspace owner ${this.workspaceOwnerAccountId()}`);
    }

    const group = this.checkMlsInitialized();
    const { commit, welcome } = group.addMembers([kp]);
    group.mergePendingCommit();

    const sessionId = this.currentMlsSessionId();
    await this.rotateWorkspaceMlsKey(sessionId);

    this.upsertOwnerDeviceFromMlsIdentity(inviteeIdentity);
    this.reconcileOwnerDeviceRegistryFromMls();

    return {
      inviteeIdentity,
      commit,
      welcome,
      sessionId,
    };
  }

  public async removeMember(name: string): Promise<void> {
    if (!this.wsmeta.owner) throw new Error('Only workspace owner can remove members');
    this.assertOwnerCanMergeMls('remove members');
    if (!name) throw new Error('Missing member name');

    const wasAuthorized = this.inviteeProfiles.has(name);
    const group = this.mlsGroup;
    if (!group) {
      if (!wasAuthorized) throw new Error(`Member ${name} not found`);
      this.inviteeProfiles.delete(name);
      await this.deletePeerIdentityEntries(name);
      return;
    }

    const encoder = new TextEncoder();
    const indexes = group.memberIndexesByIdentityPrefix(encoder.encode(accountIdentityPrefix(name)));
    if (!indexes.length) {
      if (!wasAuthorized) throw new Error(`Member ${name} not found`);
      this.inviteeProfiles.delete(name);
      await this.deletePeerIdentityEntries(name);
      return;
    }
    if (indexes.includes(group.myIndex())) throw new Error('Refusing to remove self in this flow');

    const { commit } = group.removeMembers(indexes);
    group.mergePendingCommit();

    const sessionId = this.currentMlsSessionId();
    await this.rotateWorkspaceMlsKey(sessionId);

    // publish commit ref so other members apply
    const blob = await this.provider.publishBlob(
      `mls-commit-rm-${utils.escapeUrlName(name)}`,
      commit,
    );
    await this.provider.svs.pub_mls_commit_ref(name, blob, sessionId);

    // remove from authorization map
    this.inviteeProfiles.delete(name);
    await this.deletePeerIdentityEntries(name);
    await this.notifyOwnerSessionAdvanced(sessionId);
  }

  public async removeOwnerDevice(deviceId: string): Promise<void> {
    if (!this.wsmeta.owner) {
      throw new Error('Only workspace owner devices can remove owner devices');
    }
    if (!this.isMasterDevice()) {
      throw new Error('Only the master owner device can remove owner devices');
    }

    const targetDeviceId = deviceId.trim();
    if (!targetDeviceId) {
      throw new Error('Missing target owner device ID');
    }
    if (targetDeviceId === this.wsmeta.deviceId) {
      throw new Error('Refusing to remove the current owner device');
    }
    if (this.sharedMasterDeviceId() === targetDeviceId) {
      throw new Error('Transfer master control before removing this owner device');
    }

    const target = this.ownerDevices.get(targetDeviceId);
    if (!target) {
      throw new Error(`Owner device ${targetDeviceId} is not registered`);
    }

    const group = this.checkMlsInitialized();
    const identity = encodeMlsIdentity(target.ownerId || this.workspaceOwnerAccountId(), targetDeviceId);
    const indexes = group.memberIndexesByIdentity(new TextEncoder().encode(identity));

    if (!indexes.length) {
      this.ownerDevices.delete(targetDeviceId);
      return;
    }
    if (indexes.includes(group.myIndex())) {
      throw new Error('Refusing to remove self in this flow');
    }

    const { commit } = group.removeMembers(indexes);
    group.mergePendingCommit();

    const sessionId = this.currentMlsSessionId();
    await this.rotateWorkspaceMlsKey(sessionId);
    this.ownerDevices.delete(targetDeviceId);

    const blob = await this.provider.publishBlob(
      `mls-commit-rm-owner-device-${utils.escapeUrlName(identity)}`,
      commit,
    );
    await this.provider.svs.pub_mls_commit_ref(identity, blob, sessionId);
    await this.notifyOwnerSessionAdvanced(sessionId);
  }

  public async resetGroupMlsState(): Promise<void> {
    if (!this.wsmeta.owner) {
      throw new Error('Only workspace owner can reset MLS state for the group');
    }
    this.assertOwnerCanMergeMls('reset MLS state for the group');

    await this.resetLocalMlsState('owner-triggered group reset');

    const resetBlob = await this.provider.publishBlob(
      `mls-reset-${Date.now()}`,
      new TextEncoder().encode('reset'),
    );
    await this.provider.svs.pub_mls_commit_ref(MLS_RESET_SENTINEL, resetBlob, MLS_PREJOIN_SESSION_ID);
    await this.bootstrapOwnerMls();
    await this.notifyOwnerSessionAdvanced(this.currentMlsSessionId());
  }


  public async bootstrapOwnerMls(): Promise<void> {
    if (!this.wsmeta.owner) return;
    if (this.mlsGroup) return;
    this.assertOwnerCanMergeMls('bootstrap MLS state');

    // If already bootstrapped but no in-memory group, restore is required.
    if (this.wsmeta.mlsOwnerBootstrapped) {
      throw new Error('Owner MLS state not restored; cannot re-bootstrap implicitly');
    }

    if (!this.mlsInitPromise) {
      this.mlsInitPromise = (async () => {
        const client = await this.getMlsClient();
        this.mlsGroup = client.createGroup();
        const sessionId = this.currentMlsSessionId();
        await this.rotateWorkspaceMlsKey(sessionId);

        this.wsmeta.mlsOwnerBootstrapped = true;
        await _o.stats.put(this.wsmeta.name, this.wsmeta);
        this.reconcileOwnerDeviceRegistryFromMls();
      })();
    }

    try {
      await this.mlsInitPromise;
    } finally {
      this.mlsInitPromise = null;
    }
  }

  /**
   * Try to invite a profile to the workspace
   *
   * @param invitee Profile of the invitee
   */
  public async tryInvite(invitee: IProfile): Promise<void> {
    if (!this.wsmeta.owner) throw new Error('Only owner can invite');

    // Explicit MLS bootstrap at first intentional owner action.
    await this.bootstrapOwnerMls();

    if (this.inviteeProfiles.has(invitee.name)) {
      throw new Error(`Invitation for ${invitee.name} already exists`);
    }
    this.inviteeProfiles.set(invitee.name, invitee);
    await this.invite(invitee.name);
  }

  public async tryFastInvite(invitee: IProfile, router: Router): Promise<string> {
    if (!this.wsmeta.owner) throw new Error('Only owner can invite');

    await this.bootstrapOwnerMls();

    if (this.inviteeProfiles.has(invitee.name)) {
      throw new Error(`Invitation for ${invitee.name} already exists`);
    }

    this.inviteeProfiles.set(invitee.name, invitee);
    try {
      const invitation = await this.api.make_fast_join_invitation(invitee.name);
      return this.getFastJoinLink(router, invitation);
    } catch (err) {
      this.inviteeProfiles.delete(invitee.name);
      throw err;
    }
  }

  public async tryInviteWithFastJoin(invitee: IProfile, router: Router): Promise<string> {
    if (!this.wsmeta.owner) throw new Error('Only owner can invite');

    await this.bootstrapOwnerMls();

    if (this.inviteeProfiles.has(invitee.name)) {
      throw new Error(`Invitation for ${invitee.name} already exists`);
    }

    this.inviteeProfiles.set(invitee.name, invitee);
    try {
      await this.invite(invitee.name);
      const invitation = await this.api.make_fast_join_invitation(invitee.name);
      return this.getFastJoinLink(router, invitation);
    } catch (err) {
      this.inviteeProfiles.delete(invitee.name);
      throw err;
    }
  }

  /**
   * Regenerate the fast-join link for an existing invitee (pending or
   * already-joined member). The owner can use this to resend a lost link
   * or rotate a member's ephemeral cert. The old cert is cleaned up by
   * the Go side via forgetFastJoinCertForGroup.
   */
  public async resendFastInvite(inviteeName: string, router: Router): Promise<string> {
    if (!this.wsmeta.owner) throw new Error('Only owner can resend');

    await this.bootstrapOwnerMls();

    const invitation = await this.api.make_fast_join_invitation(inviteeName);
    return this.getFastJoinLink(router, invitation);
  }

  /**
   * Generate and publish an invitation for a name
   *
   * @param name NDN name to invite
   */
  public async invite(name: string): Promise<void> {
    // Sign and publish the invitation, surfacing backend errors to the UI
    try {
      await this.api.sign_and_pub_invitation(name);
    } catch (err) {
      const reason = err instanceof Error ? err.message : String(err);
      throw new Error(`Failed to publish invitation: ${reason}`);
    }
  }

  /**
   * Get the join link for the workspace
   * @param router Vue router instance
   */
  public async getJoinLink(router: Router) {
    return this.buildJoinHref(router, {
      query: {
        label: this.wsmeta.label,
        psk: this.wsmeta.psk,
      },
    });
  }

  public getFastJoinLink(router: Router, invitation: FastJoinInvitation) {
    const fastJoin = serializeFastJoinBundle({
      v: 5,
      label: this.wsmeta.label,
      wksp: this.wsmeta.name,
      psk: this.wsmeta.psk,
      inviteeIdentity: invitation.invitee_identity,
      ownerCert: invitation.owner_cert,
      ephemeralSecret: invitation.ephemeral_secret,
      ephemeralCert: invitation.ephemeral_cert,
    });
    return this.buildJoinHref(router, {
      hash: `#fj=${fastJoin}`,
    });
  }

  private buildJoinHref(
    router: Router,
    opts: { query?: Record<string, string>; hash?: string },
  ): string {
    const space = utils.escapeUrlName(this.wsmeta.name);
    const inviteHref = router.resolve({
      name: 'join',
      params: { space },
      ...(opts.query ? { query: opts.query } : {}),
      ...(opts.hash ? { hash: opts.hash } : {}),
    }).href;
    return `${window.location.origin}${inviteHref}`;
  }

  /**
   * Get the invitation list
   *
   * @returns Array of invitations
   */
  public getInviteArray(): IProfile[] {
    return [...this.inviteeProfiles.values()];
  }
}
