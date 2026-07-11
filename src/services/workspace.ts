import { WorkspaceChat } from './workspace-chat';
import { WorkspaceProj, WorkspaceProjManager } from './workspace-proj';
import { WorkspaceInviteManager } from './workspace-invite';

import ndn from '@/services/ndn';
import { SvsProvider } from '@/services/svs-provider';
import { encodeMlsIdentity } from '@/services/mls-identity';


import { GlobalBus } from '@/services/event-bus';
import * as utils from '@/utils/index';
import { Toast } from '@/utils/toast';

import type { SvsAloApi, WorkspaceAPI, RefreshPongPub, RefreshPingPub } from '@/services/ndn';
import type { Router } from 'vue-router';
import type { IWkspStats } from '@/services/types';

/**
 * We keep an active instance of the open workspace.
 * This always runs in the background collecting data.
 */
declare global {
  // eslint-disable-next-line no-var
  var ActiveWorkspace: Workspace | null;
  // eslint-disable-next-line no-var
  var ActiveWorkspaceSetup: { name: string; promise: Promise<Workspace> } | null;
}

/**
 * Workspace service
 */
export class Workspace {
  private static readonly REFRESH_MAX_AGE_MS = 30_000;
  private static readonly REFRESH_STATE_TTL_MS = 60_000;

  private readonly refreshUnsubs = Array<() => void>();
  private readonly pendingRefreshPongs = new Map<string, {
    createdAt: number;
    responders: Map<string, RefreshPongPub>;
  }>();
  private readonly seenRefreshPings = new Map<string, number>();



  private constructor(
    public readonly metadata: IWkspStats,
    private readonly api: WorkspaceAPI,
    private readonly provider: SvsProvider,
    public readonly chat: WorkspaceChat,
    public readonly proj: WorkspaceProjManager,
    public readonly invite: WorkspaceInviteManager,
  ) {}

  private currentDeviceIdentity(): string {
    if (!this.metadata.deviceId) {
      throw new Error('Missing local device ID');
    }
    return encodeMlsIdentity(this.api.name, this.metadata.deviceId);
  }


  /**
   * Start the workspace.
   * This will connect to the testbed and start the SVS instance.
   */
  private static async create(metadata: IWkspStats): Promise<Workspace> {
    // Start connection to testbed
    await ndn.api.connect_testbed();

    await Workspace.ensureDeviceMetadata(metadata);



    // Set up workspace API and client
    let api: WorkspaceAPI | null = null;
    try {

      api = await ndn.api.get_workspace(
        metadata.name,
        metadata.ignore,
      );
      await api.start();

      const owner = await ndn.api.is_workspace_owner(metadata.name);
      if (metadata.owner !== owner) {
        metadata.owner = owner;
        await _o.stats.put(metadata.name, metadata);
      }

      // Wait until user key is ready before proceeding; toast follows route changes.
      const certToast = Toast.loading('Waiting for certificate issuance. You may quit while waiting for workspace creators to respond...');
      try {
        await ndn.api.wait_user_key(metadata.name);
        await certToast.success('Certificate Ready');
      } catch (err) {
        await certToast.error(`Certificate wait failed: ${err}`);
        throw err;
      }

      // Check if we have the encryption keys
      if (!metadata.psk) {
        throw new Error('Missing PSK, cannot start workspace');
      }
      if (!metadata.dsk) {
        await Workspace.findDskRoutine(metadata, api);
      }
      await api.set_encrypt_keys(utils.fromHex(metadata.psk), utils.fromHex(metadata.dsk!));

      if (metadata.mlsKeys?.length) {
        for (const entry of [...metadata.mlsKeys].reverse()) {
          await api.set_encrypt_key(entry.sessionId, utils.fromHex(entry.mlsKey));
        }
      }

      // Create general SVS group
      const provider = await SvsProvider.create(api, 'root');

      // Create general modules
      const chat = await WorkspaceChat.create(api, provider);
      const proj = await WorkspaceProjManager.create(api, provider);
      const invite = await WorkspaceInviteManager.create(api, metadata, provider);

      const shouldRequestMls =
        !(metadata.owner && metadata.isMasterDevice) &&
        !(metadata.mlsKeys?.length) &&
        (
          !metadata.mlsJoinRequested ||
          !metadata.mlsJoinRequestedAt ||
          Date.now() - metadata.mlsJoinRequestedAt > 5 * 60 * 1000 // retry after 5 min
        );

      if (shouldRequestMls) {
        try {
          await invite.requestMlsJoin();
        } catch (e) {
          // keep workspace usable; retry next startup
          console.warn('Failed to publish MLS key package ref', e);
          metadata.mlsJoinRequested = false;
          await _o.stats.put(metadata.name, metadata);
        }
      }


      const workspace = new Workspace(metadata, api, provider, chat, proj, invite);
      invite.setOnOwnerSessionAdvanced(async () => {
        await workspace.republishEncryptedState();
      });
      workspace.registerRefreshHandlers();
      await api.set_on_refresh_req(workspace.currentDeviceIdentity(), async () => {
        await workspace.republishEncryptedState();
      });
      if (metadata.owner) {
        await api.set_on_mls_rst_req(workspace.currentDeviceIdentity(), async () => {
          if (!invite.isMasterDevice()) {
            throw new Error('Only the master owner device can reset MLS state');
          }
          await invite.resetGroupMlsState();
        });
      }

      return workspace;
    } catch (e) {
      // Clean up if we failed to start
      api?.stop();
      throw e;
    }
  }

  /**
   * Destroy the workspace.
   * This will stop the SVS instance and disconnect from the testbed.
   */
  public async destroy() {
    await this.proj.destroy();
    await this.chat.destroy();
    for (const off of this.refreshUnsubs) {
      off();
    }
    this.refreshUnsubs.length = 0;
    this.pendingRefreshPongs.clear();
    this.seenRefreshPings.clear();

    await this.provider?.destroy();
    await this.api?.stop();
    await this.invite.destroy();

    if (globalThis.ActiveWorkspace === this) {
      globalThis.ActiveWorkspace = null;
    }
  }

  private registerRefreshHandlers(): void {
    this.refreshUnsubs.push(
      this.provider.onRefreshPing((pubs) => this.handleRefreshPing(pubs)),
    );
    this.refreshUnsubs.push(
      this.provider.onRefreshPong((pubs) => this.handleRefreshPong(pubs)),
    );
  }

  private async handleRefreshPing(pubs: RefreshPingPub[]): Promise<void> {
    const now = Date.now();
    this.pruneRefreshState(now);
    const currentIdentity = this.currentDeviceIdentity();

    for (const pub of pubs) {
      if (this.isRefreshExpired(pub.sent_at, Workspace.REFRESH_MAX_AGE_MS)) continue;
      if (pub.requester === currentIdentity) continue;
      if (this.seenRefreshPings.has(pub.request_id)) continue;

      this.seenRefreshPings.set(pub.request_id, now);
      await this.provider.svs.pub_refresh_pong(
        pub.request_id,
        pub.requester,
        currentIdentity,
        Math.floor(Date.now() / 1000),
        new Date().toISOString(),
      );
    }
  }

  private async handleRefreshPong(pubs: RefreshPongPub[]): Promise<void> {
    this.pruneRefreshState();
    const currentIdentity = this.currentDeviceIdentity();

    for (const pub of pubs) {
      if (this.isRefreshExpired(pub.sent_at, Workspace.REFRESH_MAX_AGE_MS)) continue;
      if (pub.requester !== currentIdentity) continue;

      const entry = this.pendingRefreshPongs.get(pub.request_id);
      if (!entry) continue;

      entry.responders.set(pub.responder, pub);
    }
  }

  public async sendRefreshPing(): Promise<string> {
    const now = Date.now();
    this.pruneRefreshState(now);
    const currentIdentity = this.currentDeviceIdentity();

    const requestId = crypto.randomUUID();
    this.pendingRefreshPongs.set(requestId, {
      createdAt: now,
      responders: new Map(),
    });

    await this.provider.svs.pub_refresh_ping(
      requestId,
      currentIdentity,
      new Date().toISOString(),
    );

    return requestId;
  }

  private sleep(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms));
  }

  public async sosRequest(timeoutMs = 3_000, pollMs = 200): Promise<{ requestId: string; responder: string }> {
    const requestId = await this.sendRefreshPing();
    const deadline = Date.now() + timeoutMs;
    const attemptedResponders = new Set<string>();
    let sawResponder = false;

    try {
      while (Date.now() < deadline) {
        const responders = this.getRefreshResponders(requestId)
          .filter((pub) => !attemptedResponders.has(pub.responder));

        if (responders.length === 0) {
          await this.sleep(pollMs);
          continue;
        }

        sawResponder = true;
        for (const responder of responders) {
          attemptedResponders.add(responder.responder);

          try {
            const status = await this.requestRefresh(requestId, responder.responder);
            if (status === 'ok') {
              return {
                requestId,
                responder: responder.responder,
              };
            }

            console.warn('SOS responder reported refresh failure', {
              request_id: requestId,
              requester: this.currentDeviceIdentity(),
              responder: responder.responder,
            });
          } catch (e) {
            console.warn('SOS responder request failed', {
              request_id: requestId,
              requester: this.currentDeviceIdentity(),
              responder: responder.responder,
              err: e,
            });
          }
        }

        await this.sleep(pollMs);
      }
    } finally {
      this.pendingRefreshPongs.delete(requestId);
    }

    if (!sawResponder) {
      throw new Error('No online responder acknowledged the SOS request');
    }

    throw new Error('All online responders failed to republish the SOS request');
  }

  public getRefreshResponders(requestId: string): RefreshPongPub[] {
    this.pruneRefreshState();

    const entry = this.pendingRefreshPongs.get(requestId);
    return entry ? Array.from(entry.responders.values()) : [];
  }

  private refreshReqName(responder: string, requestId: string): string {
    return `${utils.normalizePath(this.api.group)}/root/32=REFRESH_REQ${utils.normalizePath(responder)}/${requestId}${utils.normalizePath(this.currentDeviceIdentity())}`;
  }

  public async requestRefresh(requestId: string, responder: string): Promise<'ok' | 'fail'> {
    return await this.api.send_refresh_req(this.refreshReqName(responder, requestId));
  }

  private mlsResetReqName(responder: string, requestId: string): string {
    return `${utils.normalizePath(this.api.group)}/root/32=MLS_RST_REQ${utils.normalizePath(responder)}/${requestId}${utils.normalizePath(this.currentDeviceIdentity())}`;
  }

  public async requestMlsReset(): Promise<void> {
    if (this.metadata.owner && this.invite.isMasterDevice()) {
      await this.invite.resetGroupMlsState();
      return;
    }

    const masterOwnerDevice = this.invite.getMasterOwnerDevice();
    if (!masterOwnerDevice) {
      throw new Error('No master owner device is registered');
    }
    if (!masterOwnerDevice.ownerId) {
      throw new Error('Master owner device is missing owner identity metadata');
    }

    const responder = encodeMlsIdentity(masterOwnerDevice.ownerId, masterOwnerDevice.deviceId);
    const status = await this.api.send_mls_rst_req(
      this.mlsResetReqName(responder, crypto.randomUUID()),
    );
    if (status !== 'ok') {
      throw new Error('Master owner device rejected the MLS reset request');
    }
  }

  private pruneRefreshState(now = Date.now()): void {
    for (const [requestId, entry] of this.pendingRefreshPongs) {
      if (now - entry.createdAt > Workspace.REFRESH_STATE_TTL_MS) {
        this.pendingRefreshPongs.delete(requestId);
      }
    }

    for (const [requestId, seenAt] of this.seenRefreshPings) {
      if (now - seenAt > Workspace.REFRESH_STATE_TTL_MS) {
        this.seenRefreshPings.delete(requestId);
      }
    }

  }

  private isRefreshExpired(sentAt: string, maxAgeMs = 30_000): boolean {
    const sentTime = new Date(sentAt).getTime();
    if (isNaN(sentTime)) {
      console.warn(`Invalid sentAt timestamp: ${sentAt}`);
      return true;
    }
    const currentTime = Date.now();
    return currentTime - sentTime > maxAgeMs;
  }

  /**
   * Username is the NDN name of the user.
   * This is not necessarily the display name.
   */
  get username(): string {
    return this.api.name;
  }

  /**
   * Get the members of the workspace.
   * This currently returns the names in the root svs group;
   * this may not include everyone, e.g. if they never published.
   */
  public async getMembers(): Promise<string[]> {
    return await this.provider.svs.names();
  }

  /**
   * Republish the currently encrypted Yjs state under the active workspace key.
   * This is used after owner-driven MLS session changes so late or idle
   * members can recover the current encrypted state under the new session.
   */
  public async republishEncryptedState(): Promise<void> {
    await this.provider.republishEncryptedState();
    await this.proj.republishEncryptedState();
  }

  /**
   * Setup workspace from URL parameter.
   * @param space Workspace name from URL
   * @returns Workspace object or null if not found
   */
  public static async setup(space: string): Promise<Workspace> {
    if (!space) {
      throw new Error('No workspace name provided');
    }

    // Unescape URL name
    space = utils.unescapeUrlName(space);

    // Get workspace configuration from storage
    const metadata = await _o.stats.get(space);
    if (!metadata) {
      throw new Error(`Workspace not found, have you joined it? <br/> [${space}]`);
    }
    if (metadata.revoked) {
      throw new Error('Workspace access was revoked. Rejoin with a fresh invitation.');
    }

    // Store last access time
    metadata.lastAccess = Date.now();
    _o.stats.put(space, metadata); // background

    // Start workspace if not already active
    if (globalThis.ActiveWorkspace?.metadata.name === metadata.name) {
      return globalThis.ActiveWorkspace;
    }

    const pendingSetup = globalThis.ActiveWorkspaceSetup;
    if (pendingSetup) {
      const pendingWorkspace = await pendingSetup.promise.catch(() => null);
      if (pendingWorkspace?.metadata.name === metadata.name) {
        return pendingWorkspace;
      }
    }

    try {
      await globalThis.ActiveWorkspace?.destroy();
    } catch (e) {
      console.error(e);
      GlobalBus.emit('wksp-error', new Error(`Failed to stop workspace: ${e}`));
    }

    const setup = {
      name: metadata.name,
      promise: Workspace.create(metadata).then((workspace) => {
        globalThis.ActiveWorkspace = workspace;
        return workspace;
      }),
    };
    globalThis.ActiveWorkspaceSetup = setup;
    try {
      return await setup.promise;
    } finally {
      if (globalThis.ActiveWorkspaceSetup === setup) {
        globalThis.ActiveWorkspaceSetup = null;
      }
    }
  }

  /**
   * Setup workspace from URL parameter or redirect to home.
   *
   * @param router Vue router
   */
  public static async setupOrRedir(router: Router): Promise<Workspace | null> {
    try {
      return await Workspace.setup(router.currentRoute.value.params.space as string);
    } catch (e) {
      console.error(e);
      if (router.currentRoute.value.path !== '/') {
        await router.push('/');
      }
      GlobalBus.emit('wksp-error', new Error(`Failed to start workspace: ${e}`));
      return null;
    }
  }

  /**
   * Utility to setupOrRedir and get the active project.
   *
   * @param router Vue router
   */
  public static async setupAndGetActiveProj(router: Router): Promise<WorkspaceProj> {
    const wksp = await Workspace.setupOrRedir(router);
    if (!wksp) throw new Error('Workspace not found');

    if (wksp.proj.active) return wksp.proj.active;

    // No active project, try to get it from the URL
    const projName = router.currentRoute.value.params.project as string;
    if (!projName) throw new Error('No project name provided');

    const proj = await wksp.proj.get(projName);
    await proj.activate();
    return proj;
  }

  /**
   * Join a workspace by name and the default identity.
   *
   * @param label Display name
   * @param wksp Workspace name
   * @param create Create the workspace if it does not exist
   * @param ignore Ignore validity checks while consuming workspace data
   * @param psk Pre-shared key for encryption
   * @param payload App-defined payload passed to boot sync; pass null when unused
   */
  public static async join(
    label: string,
    wksp: string,
    create: boolean,
    ignore: boolean,
    psk: Uint8Array | null,
    payload: Uint8Array | null,
  ): Promise<string> {
    const metadata = await _o.stats.get(wksp);
    if (metadata && !metadata.revoked) throw new Error('You have already joined this workspace');

    // Generate or validate PSK
    if (create) {
      psk = new Uint8Array(32);
      globalThis.crypto.getRandomValues(psk);
    } else if (psk?.length !== 32) {
      throw new Error('Invalid PSK length != 32');
    }

    // Generate DSK if creating a new workspace
    const dsk = create ? new Uint8Array(32) : null;
    if (create && dsk) globalThis.crypto.getRandomValues(dsk);

    // Join workspace - this will check invitation etc.
    const finalName = await ndn.api.join_workspace(wksp, create, payload);

    // A fresh join should not reuse stale local sync/MLS/boot state from a
    // previous membership instance of the same workspace.
    const slug = utils.escapeUrlName(finalName);
    await _o.ProjDb.deleteWksp(slug);
    await _o.bootState?.del(`${finalName}/32=boot`);

    // Check if we have the owner permissions
    const isOwner = await ndn.api.is_workspace_owner(finalName);

    // Insert workspace metadata to database
    await _o.stats.put(finalName, {
      label: label,
      name: finalName,
      owner: isOwner,
      ignore: ignore,
      pendingSetup: create ? true : undefined,
      revoked: undefined,
      deviceId: globalThis.crypto.randomUUID(),
      isMasterDevice: create && isOwner,
      psk: utils.toHex(psk),
      dsk: dsk ? utils.toHex(dsk) : null,
    });

    return finalName;
  }

  /**
   * Routine to get the DSK key if it is not already present.
   *
   * @param metadata Metadata of the workspace
   * @param api Workspace API
   *
   * @throws Error if DSK key cannot be obtained
   */
  private static async findDskRoutine(metadata: IWkspStats, api: WorkspaceAPI) {
    // Start the root SVS group without subscribing
    // This will allow us to publish the DSK key request
    let rootSvs: SvsAloApi | null = null;

    try {
      const { svs } = await SvsProvider.createComponents(api, 'root');
      rootSvs = svs;
      await rootSvs.start();

      if (!metadata.dskExch) {
        const dskExch = await rootSvs.pub_dsk_request();
        metadata.dskExch = utils.toHex(dskExch);

        // Persist the key exchange key so that this process can be asynchronous
        await globalThis._o.stats.put(metadata.name, metadata);
      }

      // Wait for DSK key or throw error
      const dskExch = utils.fromHex(metadata.dskExch);
      const dsk = await api.wait_for_dsk(dskExch);

      // Persist the DSK key
      metadata.dsk = utils.toHex(dsk);
      await globalThis._o.stats.put(metadata.name, metadata);

      // Acknowledge the DSK key
      await rootSvs.pub_dsk_ack(dskExch);
    } catch (e) {
      throw new Error(`No DSK, try again later when others are online: ${e}`);
    } finally {
      rootSvs?.stop();
    }
  }

  private static async ensureDeviceMetadata(metadata: IWkspStats): Promise<void> {
    let changed = false;

    if (!metadata.deviceId) {
      metadata.deviceId = globalThis.crypto.randomUUID();
      changed = true;
    }

    if (metadata.isMasterDevice === undefined) {
      metadata.isMasterDevice = false;
      changed = true;
    }

    if (changed) {
      await _o.stats.put(metadata.name, metadata);
    }
  }
}
