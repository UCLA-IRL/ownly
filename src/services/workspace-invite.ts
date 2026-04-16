import * as utils from '@/utils';
import * as Y from 'yjs';

import type { Router } from 'vue-router';
import type { WorkspaceAPI, MlsRefPub } from '@/services/ndn';
import type { SvsProvider } from '@/services/svs-provider';
import type { IMlsKey, IProfile, IWkspStats } from '@/services/types';
import  { OpenMlsLiteClient, OpenMlsLiteGroup } from '@/services/openmls-lite';
import { GlobalBus } from '@/services/event-bus';

const MLS_STORAGE_STATE_KEY = 'mls/storage/v1';
const MLS_GROUP_ID_STATE_KEY = 'mls/group-id/v1';
const MLS_RESET_SENTINEL = '__mls_reset__';
const MLS_PREJOIN_SESSION_ID = 'prejoin';
const MLS_COMMIT_BROADCAST = '__mls_commit_broadcast__';
type LegacyMlsFields = { mlsKey?: string; mlsSessionId?: string };
type MlsSessionInfo = { groupIdHex: string; epoch: bigint };
type MlsReplicaBundle = {
  version: 1;
  workspace: string;
  identity: string;
  sourceDeviceId: string;
  sourceIsMasterDevice: boolean;
  exportedAt: number;
  storageSnapshotHex: string;
  groupIdHex: string;
  mlsKeys: IMlsKey[];
  ownerBootstrapped: boolean;
};

export class WorkspaceInviteManager {
  private readonly inviteeProfiles: Y.Map<IProfile>;
  private mlsClient: OpenMlsLiteClient | null = null;
  private mlsGroup: OpenMlsLiteGroup | null = null;
  private mlsInitPromise: Promise<void> | null = null;
  private pendingCommitRefs: MlsRefPub[] = [];
  private onOwnerSessionAdvanced: ((sessionId: string) => Promise<void>) | null = null;

  // Deduplicate MLS publications delivered through live and snapshot paths.
  private readonly seenMlsPub: Set<string> = new Set();

  private constructor(
    private readonly api: WorkspaceAPI,
    private readonly wsmeta: IWkspStats,
    private readonly provider: SvsProvider,
    private readonly doc: Y.Doc,
  ) {
    this.inviteeProfiles = doc.getMap<IProfile>('invite-map');

    // Add owner to the profiles
    if (!this.inviteeProfiles.has(api.name) && this.wsmeta.owner) {
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

    await mgr.restoreMlsStateOnStartup();

    return mgr;
  }

  /**
   * Destroy the chat module
   */
  public async destroy() {
    this.doc.destroy();
    this.mlsGroup?.free();
    this.mlsGroup = null;
    this.mlsClient?.free();
    this.mlsClient = null;
  }

  public setOnOwnerSessionAdvanced(cb: (sessionId: string) => Promise<void>) {
    this.onOwnerSessionAdvanced = cb;
  }

  public isMasterDevice(): boolean {
    return this.wsmeta.isMasterDevice ?? true;
  }

  public async setIsMasterDevice(isMasterDevice: boolean): Promise<void> {
    if (this.wsmeta.isMasterDevice === isMasterDevice) return;
    this.wsmeta.isMasterDevice = isMasterDevice;
    await _o.stats.put(this.wsmeta.name, this.wsmeta);
  }

  private assertOwnerCanMergeMls(action: string): void {
    if (!this.wsmeta.owner) return;
    if (this.isMasterDevice()) return;
    throw new Error(`Only a merge-enabled owner device can ${action}`);
  }

  private async notifyOwnerSessionAdvanced(sessionId: string): Promise<void> {
    if (!this.wsmeta.owner || !this.onOwnerSessionAdvanced) return;
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

  /** 
   * get MLS group, throw error if not initialized
   */
  private checkMlsInitialized(): OpenMlsLiteGroup {
    if (!this.mlsGroup) {
      throw new Error("MLS group is not initialized");
    }
    return this.mlsGroup;
  }

  /**
   * Get MLS client instance
   */
  private async getMlsClient(): Promise<OpenMlsLiteClient> {
    if (!this.mlsClient) {
      this.mlsClient = await OpenMlsLiteClient.create(this.api.name);
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
    delete (this.wsmeta as IWkspStats & LegacyMlsFields).mlsKey;
    delete (this.wsmeta as IWkspStats & LegacyMlsFields).mlsSessionId;
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

  public async exportReplicatedMlsStateBundle(): Promise<string> {
    if (!this.mlsClient || !this.mlsGroup) {
      throw new Error('MLS state is not initialized on this device');
    }

    await this.persistMlsState();

    const [snapshot, groupId] = await Promise.all([
      this.provider.stateGet(MLS_STORAGE_STATE_KEY),
      this.provider.stateGet(MLS_GROUP_ID_STATE_KEY),
    ]);
    if (!this.isPersistedStatePresent(snapshot) || !this.isPersistedStatePresent(groupId)) {
      throw new Error('MLS state bundle is incomplete');
    }

    const bundle: MlsReplicaBundle = {
      version: 1,
      workspace: this.wsmeta.name,
      identity: this.api.name,
      sourceDeviceId: this.wsmeta.deviceId ?? 'unknown-device',
      sourceIsMasterDevice: this.isMasterDevice(),
      exportedAt: Date.now(),
      storageSnapshotHex: utils.toHex(snapshot!),
      groupIdHex: utils.toHex(groupId!),
      mlsKeys: [...(this.wsmeta.mlsKeys ?? [])],
      ownerBootstrapped: !!this.wsmeta.mlsOwnerBootstrapped,
    };
    return JSON.stringify(bundle);
  }

  public async importReplicatedMlsStateBundle(
    bundleText: string,
    isMasterDevice = false,
  ): Promise<void> {
    const bundle = JSON.parse(bundleText) as Partial<MlsReplicaBundle>;
    if (bundle.version !== 1) {
      throw new Error('Unsupported MLS replica bundle version');
    }
    if (bundle.workspace !== this.wsmeta.name) {
      throw new Error(`MLS replica bundle targets ${bundle.workspace}, expected ${this.wsmeta.name}`);
    }
    if (bundle.identity !== this.api.name) {
      throw new Error(`MLS replica bundle belongs to ${bundle.identity}, expected ${this.api.name}`);
    }
    if (!bundle.storageSnapshotHex || !bundle.groupIdHex) {
      throw new Error('MLS replica bundle is missing required state');
    }

    this.mlsGroup?.free();
    this.mlsGroup = null;
    this.pendingCommitRefs = [];

    await this.provider.statePut(MLS_STORAGE_STATE_KEY, utils.fromHex(bundle.storageSnapshotHex));
    await this.provider.statePut(MLS_GROUP_ID_STATE_KEY, utils.fromHex(bundle.groupIdHex));

    this.wsmeta.isMasterDevice = isMasterDevice;
    this.wsmeta.revoked = undefined;
    this.wsmeta.mlsJoinRequested = false;
    this.wsmeta.mlsJoinRequestedAt = undefined;
    this.wsmeta.mlsJoinAttempts = undefined;
    this.wsmeta.mlsKeys = [...(bundle.mlsKeys ?? [])];
    if (this.wsmeta.owner) {
      this.wsmeta.mlsOwnerBootstrapped = !!bundle.ownerBootstrapped;
    }

    await _o.stats.put(this.wsmeta.name, this.wsmeta);

    const restored = await this.restoreMlsStateIfAvailable();
    if (!restored) {
      throw new Error('Failed to restore imported MLS replica state');
    }
  }

  private async clearPersistedMlsState(): Promise<void> {
    await this.provider.statePut(MLS_STORAGE_STATE_KEY, new Uint8Array());
    await this.provider.statePut(MLS_GROUP_ID_STATE_KEY, new Uint8Array());
  }

  private isPersistedStatePresent(state: Uint8Array | undefined): boolean {
    return !!state && state.byteLength > 0;
  }

  private isResetPub(pub: MlsRefPub): boolean {
    return pub.invitee === MLS_RESET_SENTINEL;
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

    delete (this.wsmeta as IWkspStats & LegacyMlsFields).mlsKey;
    delete (this.wsmeta as IWkspStats & LegacyMlsFields).mlsSessionId;
    this.wsmeta.mlsJoinRequested = false;
    this.wsmeta.mlsJoinRequestedAt = undefined;
    this.wsmeta.mlsJoinAttempts = undefined;
    this.wsmeta.mlsOwnerBootstrapped = false;
    this.wsmeta.mlsKeys = undefined;

    await this.clearPersistedMlsState();
    await this.restoreLegacyWorkspaceKey();
    await _o.stats.put(this.wsmeta.name, this.wsmeta);

    if (!this.wsmeta.owner) {
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

    delete (this.wsmeta as IWkspStats & LegacyMlsFields).mlsKey;
    delete (this.wsmeta as IWkspStats & LegacyMlsFields).mlsSessionId;
    this.wsmeta.mlsJoinRequested = false;
    this.wsmeta.mlsJoinRequestedAt = undefined;
    this.wsmeta.mlsJoinAttempts = undefined;
    this.wsmeta.mlsOwnerBootstrapped = false;
    this.wsmeta.mlsKeys = undefined;
    this.wsmeta.dsk = null;
    this.wsmeta.dskExch = undefined;
    this.wsmeta.revoked = true;

    await this.clearPersistedMlsState();
    await _o.stats.put(this.wsmeta.name, this.wsmeta);
    GlobalBus.emit('wksp-error', new Error('You were removed from this workspace. Rejoin with a fresh invitation to regain access.'));
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

    if (this.wsmeta.owner && !this.wsmeta.mlsOwnerBootstrapped) {
      this.wsmeta.mlsOwnerBootstrapped = true;
      await _o.stats.put(this.wsmeta.name, this.wsmeta);
    }
    return true;
  }

  private async drainPendingCommitRefs(): Promise<void> {
    if (!this.mlsGroup || this.pendingCommitRefs.length === 0) return;

    const pending = this.orderedPendingCommits();
    const stillPending: MlsRefPub[] = [];

    for (const pub of pending) {
      if (pub.invitee === this.api.name) continue;
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
      } catch (e) {
        console.warn('Failed to apply queued MLS commit ref', pub, e);
      }
    }

    this.pendingCommitRefs = stillPending;
  }

  private async restoreMlsStateOnStartup(): Promise<void> {
    try {
      const restored = await this.restoreMlsStateIfAvailable();
      if (!restored && this.wsmeta.owner && this.wsmeta.mlsOwnerBootstrapped) {
        throw new Error('Owner MLS restore failed: missing persisted state');
      }
    } catch (e) {
      if (this.wsmeta.owner && this.wsmeta.mlsOwnerBootstrapped) {
        throw e;
      }
      console.warn('MLS restore failed; resetting join request flags', e);
      this.wsmeta.mlsJoinRequested = false;
      this.wsmeta.mlsJoinRequestedAt = undefined;
      await _o.stats.put(this.wsmeta.name, this.wsmeta);
    }
  }

  /**
   * Get MLS group instance, only owner can create it
   */
  private async getMlsGroup(): Promise<OpenMlsLiteGroup> {
      if (!this.wsmeta.owner) {
        throw new Error('Only workspace owner can use owner MLS group');
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
    this.mlsGroup = client.joinFromWelcome(welcome); // welcome-only first
    await this.rotateWorkspaceMlsKey(sessionId);
    await this.drainPendingCommitRefs();
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
  }

  private async onMlsKpRefs(pubs: MlsRefPub[]): Promise<void> {
    if (!this.wsmeta.owner) return;
    if (!this.isMasterDevice()) return;
    if (!this.mlsGroup && !this.wsmeta.mlsOwnerBootstrapped) {
      await this.bootstrapOwnerMls();
    }
    console.log(`Received ${pubs.length} MLS KP refs`);

    for (const pub of this.uniqueOrdered(pubs)) {
      console.log(`Processing MLS KP ref from ${pub.invitee}`);
      const invited = this.inviteeProfiles.has(pub.invitee);
      if (!invited) {
        console.warn(`Ignoring unauthorized MLS KP ref from ${pub.invitee}`);
        continue;
      }
      const kp = (await this.provider.consumeBlob(pub.blob_name)).data;
      const { commit, welcome, sessionId } = await this.addMemberFromKeyPackage(pub.invitee, kp);

      const inviteeKey = utils.escapeUrlName(pub.invitee);
      const commitBlob = await this.provider.publishBlob(`mls-commit-${inviteeKey}`, commit);
      const welcomeBlob = await this.provider.publishBlob(`mls-welcome-${inviteeKey}`, welcome);

      await this.provider.svs.pub_mls_commit_ref(MLS_COMMIT_BROADCAST, commitBlob, sessionId);
      await this.provider.svs.pub_mls_welcome_ref(pub.invitee, welcomeBlob, sessionId);
      await this.notifyOwnerSessionAdvanced(sessionId);
    }
  }

  private async onMlsWelcomeRefs(pubs: MlsRefPub[]): Promise<void> {
    for (const pub of this.uniqueOrdered(pubs)) {
      if (pub.invitee !== this.api.name) continue; // only target invitee handles welcome
      console.log('Processing MLS welcome ref', pub);
      const welcome = (await this.provider.consumeBlob(pub.blob_name)).data;
      await this.joinMlsFromWelcome(welcome, pub.session_id);
    }
  }

  private async onMlsCommitRefs(pubs: MlsRefPub[]): Promise<void> {
    for (const pub of this.uniqueOrdered(pubs)) {
      if (this.isResetPub(pub)) {
        if (this.wsmeta.owner && pub.publisher === this.api.name) continue;
        await this.resetLocalMlsState(`remote group reset from ${pub.publisher}`);
        continue;
      }
      if (this.wsmeta.owner) continue;            // owner already merged pending
      if (pub.invitee === this.api.name) {
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
      await this.drainPendingCommitRefs();
    }
  }

  public async publishKeyPackageRef(): Promise<void> {
    const client = await this.getMlsClient();
    const kp = client.keyPackage();
    const inviteeKey = utils.escapeUrlName(this.api.name);
    console.log('Publishing MLS key package ref', { invitee: `mls-kp-${inviteeKey}`, blobSize: kp.byteLength });
    const blob = await this.provider.publishBlob(`mls-kp-${inviteeKey}`, kp);
    await this.provider.svs.pub_mls_kp_ref(this.api.name, blob, MLS_PREJOIN_SESSION_ID);
  }

  public async requestMlsJoin(): Promise<void> {
    if (this.wsmeta.owner) {
      throw new Error('Owner does not request MLS join via key package');
    }

    try {
      await this.publishKeyPackageRef();
      this.wsmeta.mlsJoinRequested = true;
      this.wsmeta.mlsJoinRequestedAt = Date.now();
      this.wsmeta.mlsJoinAttempts = (this.wsmeta.mlsJoinAttempts ?? 0) + 1;
    } catch (e) {
      this.wsmeta.mlsJoinRequested = false;
      await _o.stats.put(this.wsmeta.name, this.wsmeta);
      throw e;
    }

    await _o.stats.put(this.wsmeta.name, this.wsmeta);
  }

  public async addMemberFromKeyPackage(
    invitee: string,
    kp: Uint8Array,
  ): Promise<{ commit: Uint8Array; welcome: Uint8Array; sessionId: string }> {
    if (!this.wsmeta.owner) {
      throw new Error('Only workspace owner can add members');
    }
    this.assertOwnerCanMergeMls('add members');
    if (!this.inviteeProfiles.has(invitee)) {
      throw new Error(`Invitee ${invitee} is not authorized`);
    }
    if (!invitee) {
      throw new Error('Missing invitee');
    }
    if (!(kp instanceof Uint8Array) || kp.length === 0) {
      throw new Error('Invalid key package');
    }

    const group = await this.getMlsGroup();
    const { commit, welcome } = group.addMembers([kp]);

    // Owner finalizes own pending commit.
    group.mergePendingCommit();

    const sessionId = this.currentMlsSessionId();
    await this.rotateWorkspaceMlsKey(sessionId);

    return { commit, welcome, sessionId };
  }

  public async removeMember(name: string): Promise<void> {
    if (!this.wsmeta.owner) throw new Error('Only workspace owner can remove members');
    this.assertOwnerCanMergeMls('remove members');
    if (!name) throw new Error('Missing member name');

    const group = await this.getMlsGroup();

    const idx = group.memberIndexByIdentity(new TextEncoder().encode(name));
    if (idx == null) throw new Error(`Member ${name} not found in MLS group`);
    if (idx === group.myIndex()) throw new Error('Refusing to remove self in this flow');

    const { commit } = group.removeMembers([idx]);
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
        await this.notifyOwnerSessionAdvanced(sessionId);
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

  /**
   * Try to invite an agent to the workspace
   *
   * @param invitee Profile of the invitee
   * @param inviteChannel The channel to assign
   * @param inviteUrl The external server URL for the agent
   */
  public async invokeAgent(inviteChannel: string, inviteUrl: string): Promise<void> {
    if (!inviteUrl) {
      console.warn("No inviteUrl provided for agent invite — skipping external request.");
      return;
    }

    try {
      const body = {
        wkspName: this.wsmeta.name,
        psk: this.wsmeta.psk,
        channel: inviteChannel,
      };

      const response = await fetch(inviteUrl, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify(body),
      });

      if (!response.ok) {
        throw new Error(`Server responded with ${response.status} ${response.statusText}`);
      }

      console.log(`Agent invite sent successfully to ${inviteUrl}`);
    } catch (err) {
      console.error(`Failed to send agent invite to ${inviteUrl}:`, err);
      throw err; // rethrow so UI can display Toast error
    }
  }

  /**
   * Generate and publish an invitation for a name
   *
   * @param name NDN name to invite
   */
  public async invite(name: string): Promise<void> {
    // Sign the invitation
    const invite = await this.api.sign_invitation(name);

    // Alert repo to fetch the invitation
    // name is unused when encapsulated
    await this.provider.svs.pub_blob_fetch(String(), invite);
  }

  /**
   * Get the join link for the workspace
   * @param router Vue router instance
   */
  public async getJoinLink(router: Router) {
    const space = utils.escapeUrlName(this.wsmeta.name);
    const inviteHref = router.resolve({
      name: 'join',
      params: { space },
      query: {
        label: this.wsmeta.label,
        psk: this.wsmeta.psk,
      },
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
