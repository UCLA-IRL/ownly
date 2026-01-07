import { WorkspaceChat } from './workspace-chat';
import { WorkspaceProj, WorkspaceProjManager } from './workspace-proj';
import { WorkspaceInviteManager } from './workspace-invite';
import {WorkspaceAgentManager} from './workspace-agent'

import ndn from '@/services/ndn';
import { SvsProvider } from '@/services/svs-provider';

import { GlobalBus } from '@/services/event-bus';
import * as utils from '@/utils/index';

import type { SvsAloApi, WorkspaceAPI } from '@/services/ndn';
import type { Router } from 'vue-router';
import type { IWkspStats } from '@/services/types';

/**
 * We keep an active instance of the open workspace.
 * This always runs in the background collecting data.
 */
declare global {
  // eslint-disable-next-line no-var
  var ActiveWorkspace: Workspace | null;
}

/**
 * Workspace service
 */
export class Workspace {
  private constructor(
    public readonly metadata: IWkspStats,
    private readonly api: WorkspaceAPI,
    private readonly provider: SvsProvider,
    public readonly chat: WorkspaceChat,
    public readonly proj: WorkspaceProjManager,
    public readonly invite: WorkspaceInviteManager,
    public readonly agent: WorkspaceAgentManager | null
  ) { }

  /**
   * Start the workspace.
   * This will connect to the testbed and start the SVS instance.
   */
  private static async create(metadata: IWkspStats): Promise<Workspace> {
    // Start connection to testbed
    await ndn.api.connect_testbed();

    // Set up workspace API and client
    let api: WorkspaceAPI | null = null;
    try {

      api = await ndn.api.get_workspace(metadata.name, metadata.ignore);
      await api.start();

      // Wait until user key is ready before proceeding.
      await ndn.api.wait_user_key(metadata.name);

      // Check if we have the encryption keys
      if (!metadata.psk) throw new Error('Missing PSK');
      if (!metadata.dsk) await Workspace.findDskRoutine(metadata, api);

      // Set encryption keys
      await api.set_encrypt_keys(utils.fromHex(metadata.psk), utils.fromHex(metadata.dsk!));

      // Create general SVS group
      const provider = await SvsProvider.create(api, 'root');

      // Create general modules
      const chat = await WorkspaceChat.create(api, provider);
      const proj = await WorkspaceProjManager.create(api, provider);
      const invite = await WorkspaceInviteManager.create(api, metadata, provider);

      // Create workspace object first (without agent)
      const workspace = new Workspace(metadata, api, provider, chat, proj, invite, null);

      // Then create agent with workspace reference
      const agent = await WorkspaceAgentManager.create(api, provider, workspace);

      // Update workspace with agent
      (workspace as any).agent = agent;

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
    await this.provider?.destroy();
    if (this.agent) {
      await this.agent.destroy();
    }
    await this.api?.stop();
    await this.invite.destroy();
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

    // Store last access time
    metadata.lastAccess = Date.now();
    _o.stats.put(space, metadata); // background

    // Start workspace if not already active
    if (globalThis.ActiveWorkspace?.metadata.name !== metadata.name) {
      try {
        await globalThis.ActiveWorkspace?.destroy();
      } catch (e) {
        console.error(e);
        GlobalBus.emit('wksp-error', new Error(`Failed to stop workspace: ${e}`));
      }
      globalThis.ActiveWorkspace = await Workspace.create(metadata);
    }

    return globalThis.ActiveWorkspace;
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
      GlobalBus.emit('wksp-error', new Error(`Failed to start workspace: ${e}`));
      router.push('/');
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
   * @param psk Pre-shared key for encryption
   */
  public static async join(
    label: string,
    wksp: string,
    create: boolean, ignore: boolean,
    psk: Uint8Array | null,
  ): Promise<string> {
    const metadata = await _o.stats.get(wksp);
    if (metadata) throw new Error('You have already joined this workspace');

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
    const finalName = await ndn.api.join_workspace(wksp, create);

    // Check if we have the owner permissions
    const isOwner = await ndn.api.is_workspace_owner(finalName);

    // Insert workspace metadata to database
    await _o.stats.put(finalName, {
      label: label,
      name: finalName,
      owner: isOwner,
      ignore: ignore,
      pendingSetup: create ? true : undefined,
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
}
