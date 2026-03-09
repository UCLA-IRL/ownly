import * as utils from '@/utils';
import * as Y from 'yjs';

import type { Router } from 'vue-router';
import type { WorkspaceAPI, MlsRefPub } from '@/services/ndn';
import type { SvsProvider } from '@/services/svs-provider';
import type { IProfile, IWkspStats } from '@/services/types';
import  { OpenMlsLiteClient, OpenMlsLiteGroup } from '@/services/openmls-lite';

const MLS_STORAGE_STATE_KEY = 'mls/storage/v1';
const MLS_GROUP_ID_STATE_KEY = 'mls/group-id/v1';

export class WorkspaceInviteManager {
  private readonly inviteeProfiles: Y.Map<IProfile>;
  private mlsClient: OpenMlsLiteClient | null = null;
  private mlsGroup: OpenMlsLiteGroup | null = null;
  private mlsInitPromise: Promise<void> | null = null;
  
  // For testing: temp owner-side kb table
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

  /**
   * Rotate the workspace shared secret after memebership changes
   */
  private async rotateWorkspaceMlsKey(): Promise<Uint8Array> {
    if (!this.mlsGroup) {
      throw new Error('MLS group is not initialized');
    }

    const key = this.mlsGroup.exportWorkspaceSecret();

    if (!(key instanceof Uint8Array) || key.length !== 32) {
      throw new Error(`Invalid MLS export key length: ${key?.length ?? 'unknown'}`);
    }
    await this.api.set_encrypt_key(key);

    const hex = utils.toHex(key);
    this.wsmeta.mlsKey = hex;
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

  private async restoreMlsStateIfAvailable(): Promise<boolean> {
    const [snapshot, groupId] = await Promise.all([
      this.provider.stateGet(MLS_STORAGE_STATE_KEY),
      this.provider.stateGet(MLS_GROUP_ID_STATE_KEY),
    ]);

    if (!snapshot && !groupId) return false;
    if (!snapshot || !groupId) {
      throw new Error('Incomplete MLS persisted state');
    }

    const client = await this.getMlsClient();
    client.importStorageSnapshot(snapshot);
    this.mlsGroup?.free();
    this.mlsGroup = client.loadGroup(groupId);
    await this.rotateWorkspaceMlsKey();

    if (this.wsmeta.owner && !this.wsmeta.mlsOwnerBootstrapped) {
      this.wsmeta.mlsOwnerBootstrapped = true;
      await _o.stats.put(this.wsmeta.name, this.wsmeta);
    }
    return true;
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
        throw new Error('Owner MLS group not initialized. Call bootstrapOwnerMlsIfNeeded() first.');
      }
      return this.mlsGroup;
  }

  /**
   * Join a group from a welcome message
   * 
   * @param welcome The welcome message to join from
   */
  public async joinMlsFromWelcome(welcome: Uint8Array) : Promise<void> {
    const client = await this.getMlsClient();
    this.mlsGroup?.free();
    this.mlsGroup = client.joinFromWelcome(welcome); // welcome-only first
    await this.rotateWorkspaceMlsKey();
  }

  /**
   * Apply a commit already merged by the owner
   * 
   * @param commit The commit to apply
   */
  public async applyMlsCommit(commit: Uint8Array) : Promise<void> {
    const group = this.checkMlsInitialized();
    group.applyCommit(commit);
    await this.rotateWorkspaceMlsKey();
  }

  private async onMlsKpRefs(pubs: MlsRefPub[]): Promise<void> {
    if (!this.wsmeta.owner) return;
    console.log(`Received ${pubs.length} MLS KP refs`);

    for (const pub of this.uniqueOrdered(pubs)) {
      console.log(`Processing MLS KP ref from ${pub.invitee}`);
      const invited = this.inviteeProfiles.has(pub.invitee);
      if (!invited) {
        console.warn(`Ignoring unauthorized MLS KP ref from ${pub.invitee}`);
        continue;
      }
      const kp = (await this.provider.consumeBlob(pub.blob_name)).data;
      const { commit, welcome } = await this.addMemberFromKeyPackage(pub.invitee, kp);

      const inviteeKey = utils.escapeUrlName(pub.invitee);
      const commitBlob = await this.provider.publishBlob(`mls-commit-${inviteeKey}`, commit);
      const welcomeBlob = await this.provider.publishBlob(`mls-welcome-${inviteeKey}`, welcome);

      await this.provider.svs.pub_mls_commit_ref(pub.invitee, commitBlob);
      await this.provider.svs.pub_mls_welcome_ref(pub.invitee, welcomeBlob);
    }
  }

  private async onMlsWelcomeRefs(pubs: MlsRefPub[]): Promise<void> {
    for (const pub of this.uniqueOrdered(pubs)) {
      if (pub.invitee !== this.api.name) continue; // only target invitee handles welcome
      const welcome = (await this.provider.consumeBlob(pub.blob_name)).data;
      await this.joinMlsFromWelcome(welcome);
    }
  }

  private async onMlsCommitRefs(pubs: MlsRefPub[]): Promise<void> {
    for (const pub of this.uniqueOrdered(pubs)) {
      if (this.wsmeta.owner) continue;            // owner already merged pending
      if (pub.invitee === this.api.name) continue; // invitee uses welcome path
      const commit = (await this.provider.consumeBlob(pub.blob_name)).data;
      await this.applyMlsCommit(commit);
    }
  }

  public async publishKeyPackageRef(): Promise<void> {
    const client = await this.getMlsClient();
    const kp = client.keyPackage();
    const inviteeKey = utils.escapeUrlName(this.api.name);
    console.log('Publishing MLS key package ref', { invitee: `mls-kp-${inviteeKey}`, blobSize: kp.byteLength });
    const blob = await this.provider.publishBlob(`mls-kp-${inviteeKey}`, kp);
    await this.provider.svs.pub_mls_kp_ref(this.api.name, blob);
  }

  public async addMemberFromKeyPackage( invitee: string, kp: Uint8Array ): Promise<{ commit: Uint8Array; welcome: Uint8Array }> {
    if (!this.wsmeta.owner) {
      throw new Error('Only workspace owner can add members');
    }
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

    await this.rotateWorkspaceMlsKey();

    return { commit, welcome };
  }

  public async removeMember(name: string): Promise<void> {
    if (!this.wsmeta.owner) throw new Error('Only workspace owner can remove members');
    if (!name) throw new Error('Missing member name');

    const group = await this.getMlsGroup();

    const idx = group.memberIndexByIdentity(new TextEncoder().encode(name));
    if (idx == null) throw new Error(`Member ${name} not found in MLS group`);
    if (idx === group.myIndex()) throw new Error('Refusing to remove self in this flow');

    const { commit } = group.removeMembers([idx]);
    group.mergePendingCommit();
    await this.rotateWorkspaceMlsKey(); // includes persist

    // publish commit ref so other members apply
    const blob = await this.provider.publishBlob(
      `mls-commit-rm-${utils.escapeUrlName(name)}`,
      commit,
    );
    await this.provider.svs.pub_mls_commit_ref(name, blob);

    // remove from authorization map
    this.inviteeProfiles.delete(name);
  }


  public async bootstrapOwnerMls(): Promise<void> {
    if (!this.wsmeta.owner) return;
    if (this.mlsGroup) return;

    // If already bootstrapped but no in-memory group, restore is required.
    if (this.wsmeta.mlsOwnerBootstrapped) {
      throw new Error('Owner MLS state not restored; cannot re-bootstrap implicitly');
    }

    if (!this.mlsInitPromise) {
      this.mlsInitPromise = (async () => {
        const client = await this.getMlsClient();
        this.mlsGroup = client.createGroup();
        await this.rotateWorkspaceMlsKey();

        this.wsmeta.mlsOwnerBootstrapped = true;
        await _o.stats.put(this.wsmeta.name, this.wsmeta);
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
