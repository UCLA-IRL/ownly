import * as Y from 'yjs';
import * as awareProto from 'y-protocols/awareness.js';

import { GlobalBus } from '@/services/event-bus';
import { SvsProvider } from '@/services/svs-provider';
import * as opfs from '@/services/opfs';
import * as utils from '@/utils';
import { nanoid } from 'nanoid';

import type { WorkspaceAPI } from './ndn';
import type { IBlobVersion, IProject, IProjectFile } from './types';
import {
  excalidrawToFile,
  type ExcalidrawElementYMap,
  type ExcalidrawFilesYMap,
} from './excalidraw-types';
import type { ImportedDataState } from '@excalidraw/excalidraw/data/types';

/**
 * Project manager for the workspace.
 * Keeps track of the list of projects and their instances.
 * Each project has its own SVS sync group / provider.
 */
export class WorkspaceProjManager {
  private readonly list: Y.Map<IProject>;
  private readonly instances: Map<string, WorkspaceProj> = new Map();
  public active: WorkspaceProj | null = null;

  private constructor(
    private readonly wksp: WorkspaceAPI,
    private readonly root: Y.Doc,
  ) {
    this.list = this.root.getMap<IProject>('list');

    const listObserver = async () => GlobalBus.emit('project-list', this.getProjects());
    this.list.observe(listObserver);
    listObserver();
  }

  /** Name of the workspace (group) */
  public get group(): string {
    return this.wksp.group;
  }

  /** Create the project manager instance */
  public static async create(
    wksp: WorkspaceAPI,
    provider: SvsProvider,
  ): Promise<WorkspaceProjManager> {
    const doc = await provider.getDoc('proj');
    return new WorkspaceProjManager(wksp, doc);
  }

  /** Destroy the project manager instance */
  public async destroy() {
    await Promise.all(Array.from(this.instances.values()).map((proj) => proj.destroy()));
    this.instances.clear();
    this.root.destroy();
    this.active = null;
  }

  /** Get the list of projects */
  public getProjects(): IProject[] {
    return Array.from(this.list.values());
  }

  /** Create a new project */
  public async newProject(name: string) {
    if (!name) throw new Error('Project name is required');
    if (this.list.has(name)) throw new Error('Project already exists');
    const uuid = nanoid();
    this.list.set(uuid, { uuid, name });
  }

  /** Get a project instance */
  public async get(name: string): Promise<WorkspaceProj> {
    const pmeta = this.getProjects().find((p) => p.name === name);
    const puuid = pmeta?.uuid;
    if (!puuid) throw new Error('Project not found');

    let proj = this.instances.get(puuid);
    if (proj) return proj;

    // Create project instance
    proj = await WorkspaceProj.create(puuid, pmeta.name, this.wksp, this);
    this.instances.set(puuid, proj);
    return proj;
  }

  /**
   * Republish the encrypted Yjs state for every project provider.
   */
  public async republishEncryptedState(): Promise<void> {
    for (const meta of this.getProjects()) {
      const existing = this.instances.get(meta.uuid);
      if (existing) {
        await existing.republishEncryptedState();
        continue;
      }

      const temp = await WorkspaceProj.create(meta.uuid, meta.name, this.wksp, this);
      try {
        await temp.republishEncryptedState();
      } finally {
        await temp.destroy();
      }
    }
  }
}

/**
 * Project instance for the workspace.
 * Each project has its own SVS sync group / provider.
 */
export class WorkspaceProj {
  private readonly fileMap: Y.Map<IProjectFile>;

  private constructor(
    public readonly uuid: string,
    public readonly name: string,
    private readonly root: Y.Doc,
    private readonly provider: SvsProvider,
    private readonly manager: WorkspaceProjManager,
  ) {
    // Set up file list
    this.fileMap = root.getMap('file_list');
    this.fileMap.observe(() => this.onListChange());
  }

  /**
   * Create a new project instance
   *
   * @param uuid Project uuid
   * @param name Project name (slug)
   * @param wksp Workspace API
   * @param manager Project manager instance
   */
  public static async create(
    uuid: string,
    name: string,
    wksp: WorkspaceAPI,
    manager: WorkspaceProjManager,
  ): Promise<WorkspaceProj> {
    // Start SVS for project
    const provider = await SvsProvider.create(wksp, uuid);

    // Create root document
    const root = await provider.getDoc('root');

    // Create project object
    return new WorkspaceProj(uuid, name, root, provider, manager);
  }

  /** Destroy the project instance */
  public async destroy() {
    this.root.destroy();
    await this.provider.destroy();
  }

  /**
   * Republish the encrypted Yjs state for this project provider.
   */
  public async republishEncryptedState(): Promise<void> {
    await this.provider.republishEncryptedState();
  }

  /** Make this the active project */
  public async activate(): Promise<void> {
    this.manager.active = this;
    this.onListChange();
  }

  /** Get the list of files */
  public getFileList(): IProjectFile[] {
    return Array.from(this.fileMap.values());
  }

  /** Callback when the list of files changes */
  private onListChange() {
    if (!this.fileMap || this.manager.active?.root.guid !== this.root.guid) return;
    GlobalBus.emit('project-files', this.uuid, this.getFileList());
  }

  /** Check if a file or folder exists */
  public getFileMeta(path: string): IProjectFile | undefined {
    path = utils.normalizePath(path);
    return this.fileMap.get(path);
  }

  /** Create a new file or folder in the project */
  public async newFile(path: string, is_blob?: boolean) {
    if (!path) throw new Error('File path is required');
    if (this.getFileMeta(path) || this.getFileMeta(path + '/'))
      throw new Error('File or folder already exists');

    // Check for invalid characters
    if (!utils.isPathValid(path)) {
      throw new Error(`Invalid characters in path: ${path}`);
    }

    // Create the file
    path = utils.normalizePath(path);
    const uuid = nanoid();
    const file: IProjectFile = { uuid, path, is_blob };
    this.fileMap.set(path, file);
    return file;
  }

  /** Delete a file or folder in the project */
  public async deleteFile(path: string) {
    path = utils.normalizePath(path);
    const isFolder = path.endsWith('/');

    let deletedCount = 0;
    this.root.transact(() => {
      this.fileMap.forEach((_, fpath) => {
        const matchFolder = isFolder && fpath.startsWith(path);
        const matchFile = fpath === path;
        if (matchFolder || matchFile) {
          this.fileMap.delete(fpath);
          deletedCount++;
        }
      });
    });

    if (!deletedCount) {
      throw new Error(`File or folder not found: ${path}`);
    }
  }

  /**
   * Get the content document for a file
   *
   * @param path File path.
   * @throws {Error} If file path is invalid.
   * @returns The Y.Doc instance for the file.
   */
  public async getFile(path: string): Promise<Y.Doc> {
    const meta = this.getFileMeta(path);
    if (!meta?.uuid) throw new Error(`File not found: ${path}`);
    return await this.provider.getDoc(meta.uuid);
  }

  /**
   * Move a file or folder to a new location.
   *
   * @param oldPath Old file path.
   * @param newPath New file path.
   */
  public async moveFile(oldPath: string, newPath: string) {
    oldPath = utils.normalizePath(oldPath);
    newPath = utils.normalizePath(newPath);

    // Check if moving file to folder and vice versa
    if (oldPath.endsWith('/') !== newPath.endsWith('/')) {
      throw new Error('Cannot move file to folder or vice versa');
    }

    // Check for invalid characters
    if (!utils.isPathValid(newPath)) {
      throw new Error(`Invalid characters in path: ${newPath}`);
    }

    // Check if the new path already exists
    if (this.fileMap.has(newPath)) {
      throw new Error(`File already exists: ${newPath}`);
    }

    // Get all matching files
    const oldIsFolder = oldPath.endsWith('/');
    const oldMetas: IProjectFile[] = this.getFileList().filter((f) => {
      if (f.path === oldPath) return true;
      if (oldIsFolder && f.path.startsWith(oldPath)) return true;
      return false;
    });
    if (!oldMetas.length) {
      throw new Error(`No matching files found: ${oldPath}`);
    }

    // Move all matching files in a single transaction
    this.root.transact(() => {
      for (const meta of oldMetas) {
        const fOldPath = meta.path;
        const fNewPath = fOldPath.replace(oldPath, newPath); // only first occurrence

        const newMeta = structuredClone(meta);
        newMeta.path = fNewPath;
        this.fileMap.delete(fOldPath);
        this.fileMap.set(fNewPath, newMeta);
      }
    });
  }

  /**
   * Export the contents of a file directly.
   *
   * @throws {Error} If file path is invalid.
   * @throws {Error} If path is a folder.
   * @throws {Error} If file is too large.
   *
   * @returns The file content as Uint8Array.
   */
  public async exportFile(path: string): Promise<Uint8Array | null> {
    console.debug('Exporting file:', path);

    // Validate the path
    if (path.endsWith('/')) {
      throw new Error('Cannot export folder as file');
    }

    // Get the file metadata
    const meta = this.fileMap.get(path);
    if (!meta?.uuid) throw new Error(`File not found: ${path}`);

    // Always return a Uint8Array
    const toUtf8 = (str: string) => new TextEncoder().encode(str);

    // Get the file content
    const doc = new Y.Doc();
    try {
      await this.provider.readInto(doc, meta.uuid);

      if (meta.is_blob) {
        // Get the latest blob version
        const history = doc.getArray<IBlobVersion>('blobs');
        const latest = history.get(0);
        if (!latest) return null;

        // Get the blob content from local or network
        // TODO: this makes it very important to show progress
        const res = await this.provider.consumeBlob(latest.name);
        return res.data;
      } else if (utils.isExtensionType(path, 'code')) {
        // Get the text content as UTF-8
        return toUtf8(doc.getText('text').toString());
      } else if (utils.isExtensionType(path, 'milkdown')) {
        // https://github.com/pulsejet/ownly/issues/28
        return Y.encodeStateAsUpdateV2(doc);
      } else if (utils.isExtensionType(path, 'excalidraw')) {
        const elements: ExcalidrawElementYMap = doc.getMap('elements');
        const files: ExcalidrawFilesYMap = doc.getMap('files');
        return toUtf8(
          JSON.stringify(
            excalidrawToFile(Array.from(elements.values()), undefined, files.toJSON()),
          ),
        );
      }
    } finally {
      doc.destroy();
    }

    return null;
  }

  /**
   * Import a text or binary file into the project.
   *
   * If the file does not exist, it will be created.
   * If the file already exists, it will be overwritten.
   * Text or blob will be decided based on the file extension.
   *
   * @throws {Error} If file path is invalid.
   * @throws {Error} If attempting to replace a text with blob or vice versa.
   * @throws {Error} If text file cannot be decoded.
   * @throws {Error} If blob or text is too large.
   */
  public async importFile(path: string, content: ReadableStream<Uint8Array>) {
    path = utils.normalizePath(path);
    if (path.endsWith('/')) {
      throw new Error('Cannot import file as folder');
    }

    // Check if this is a blob or text file
    const isText = utils.isExtensionType(path, 'code');
    const isMilkdown = utils.isExtensionType(path, 'milkdown');
    const isExcalidraw = utils.isExtensionType(path, 'excalidraw');
    const isBlob = !isText && !isMilkdown && !isExcalidraw;

    // Get the existing file if present
    let meta = this.fileMap.get(path);
    if (meta && meta.is_blob !== isBlob) {
      throw new Error('Cannot replace text with blob or vice versa');
    }

    // Create a new file if it does not exist
    if (!meta) {
      meta = await this.newFile(path, isBlob);
    }

    // Import binary content
    if (isBlob) {
      // Read the full buffer for now. We can use a stream directly in the
      // future if we want to support big files for some reason.
      const buffer = await new Response(content).arrayBuffer();
      const name = await this.provider.publishBlob(meta.uuid, new Uint8Array(buffer));

      // Update the file version history
      const doc = await this.getFile(path);
      try {
        const version: IBlobVersion = {
          name: name,
          time: Date.now(),
          size: buffer.byteLength,
        };
        const history = doc.getArray<IBlobVersion>('blobs');
        history.unshift([version]);
      } finally {
        // TODO: see comment on isText block below
        doc.destroy();
      }

      return;
    }

    // Import text content
    if (isText || isMilkdown) {
      const buffer = await new Response(content).arrayBuffer();
      const doc = await this.getFile(path);
      try {
        if (isText) {
          const text = doc.getText('text');
          const strContent = new TextDecoder().decode(buffer);
          doc.transact(() => {
            text.delete(0, text.length);
            text.insert(0, strContent);
          });
        } else if (isMilkdown) {
          // https://github.com/pulsejet/ownly/issues/28
          Y.applyUpdateV2(doc, new Uint8Array(buffer));
        }
      } finally {
        // TODO: if the doc is already open, we should not destroy it
        // Or use some better technique like reference counting
        doc.destroy();
      }
      return;
    }

    // Import excalidraw figure JSON
    if (isExcalidraw) {
      const buffer = await new Response(content).arrayBuffer();
      const doc = await this.getFile(path);
      try {
        const jsonContent = JSON.parse(new TextDecoder().decode(buffer)) as ImportedDataState;
        const elements: ExcalidrawElementYMap = doc.getMap('elements');
        const files: ExcalidrawFilesYMap = doc.getMap('files');
        doc.transact(() => {
          elements.clear();
          for (const ele of jsonContent.elements ?? []) {
            elements.set(ele.id, JSON.parse(JSON.stringify(ele)));
          }
          files.clear();
          for (const [fid, file] of Object.entries(jsonContent.files ?? {})) {
            files.set(fid, JSON.parse(JSON.stringify(file)));
          }
        });
      } finally {
        // TODO: if the doc is already open, we should not destroy it
        // Or use some better technique like reference counting
        doc.destroy();
      }
      return;
    }
  }

  /**
   *  Download a file or folder from the project.
   *
   * @param path Path in the project.
   */
  public async download(path: string) {
    const fsPath = await this.syncFs({ path });
    if (fsPath.endsWith('/')) {
      const handle = await opfs.getDirectoryHandle(fsPath);
      await opfs.download(handle, {
        // Use the project name as the folder name if root
        name: path === '/' || path === '' ? `${this.name}.zip` : undefined,
      });
    } else {
      const handle = await opfs.getFileHandle(fsPath);
      await opfs.download(handle);
    }
  }

  /**
   * Get an awareness instance for a file.
   * Awareness is used by Yjs to track user cursors and selections.
   *
   * @param path File path.
   */
  public async getAwareness(path: string): Promise<awareProto.Awareness> {
    const uuid = this.fileMap.get(path)?.uuid;
    if (!uuid) throw new Error(`File not found: ${path}`);

    return await this.provider.getAwareness(uuid);
  }

  /**
   * Synchronize the project to the OPFS.
   *
   * @returns The path to the project in the OPFS + prefix.
   */
  public async syncFs(opts?: SyncFsOpts): Promise<string> {
    let prefix = opts?.path ?? '/';

    // Check if this is a single file to sync
    if (prefix[prefix.length - 1] !== '/') {
      return this.syncFsFile(opts);
    }

    // Get the folder in the FS
    prefix = utils.normalizePath(prefix);
    const basepdir = opts?.useProjectName ? this.name : this.uuid;
    const basedir = `${this.manager.group}/${basepdir}`;
    const folder = await opfs.getDirectoryHandle(basedir + prefix, { create: true });

    // TODO: show progress
    // TODO: catch errors but continue

    // Map path => [uuid, utime]
    const updateList = new Map<string, [string, number]>();
    for (const entry of this.fileMap.values()) {
      if (!entry.path.startsWith(prefix)) continue;
      updateList.set(entry.path, [entry.uuid, -2]);
    }

    // Iterate over each file in the project already in the FS.
    for await (const [path, entry, parent] of opfs.walk(folder, prefix)) {
      const uuid = updateList.get(path)?.[0];
      if (!uuid) {
        // Delete the file from the OPFS filesystem.
        // We may need to preserve some, e.g. generated files
        await parent.removeEntry(entry.name);
        continue;
      }

      // Check if the file is up to date
      const file = await entry.getFile();
      const utime = await this.provider.needsSync(uuid, file.lastModified);
      if (utime === null) {
        updateList.delete(path);
        continue;
      }

      // Update the file in the OPFS filesystem.
      updateList.set(path, [uuid, utime]);
    }

    for (const [path, meta] of updateList.entries()) {
      const uuid = meta[0];

      // Folders are automatically created, don't bother writing them
      if (path.endsWith('/')) continue;

      try {
        // Get the update time for the file
        let utime = meta[1];
        if (utime < 0) utime = (await this.provider.needsSync(uuid, -1)) ?? -1;

        // Write the file to the OPFS filesystem.
        //
        // TODO: this will fetch files from the network for blobs.
        // It might make sense to do this in parallel.
        //
        const content = await this.exportFile(path);
        if (content !== null) {
          const relpath = path.substring(prefix.length);
          const fileHandle = await opfs.getFileHandle(relpath, { create: true, root: folder });
          if (fileHandle) {
            await opfs.writeContents(fileHandle, basedir + path, content);

            const mtime = (await fileHandle.getFile()).lastModified;
            await this.provider.markSynced(uuid, utime, mtime);
          }
        }
      } catch (err) {
        GlobalBus.emit('wksp-error', new Error(`Sync failed for ${path}: ${err}`));
        continue;
      }
    }

    return basedir + prefix;
  }

  /**
   * Sync a single file in the OPFS.
   */
  private async syncFsFile(opts?: SyncFsOpts): Promise<string> {
    let path = opts?.path ?? '/';

    // Check if this is a folder
    if (path.endsWith('/')) throw new Error('Cannot sync folder as file');

    // Get metadata for the file
    path = utils.normalizePath(path);
    const meta = this.fileMap.get(path);
    if (!meta) throw new Error(`File not found: ${path}`);

    // Get the file in the FS
    const basepdir = opts?.useProjectName ? this.name : this.uuid;
    const basedir = `${this.manager.group}/${basepdir}`;
    const folder = await opfs.getDirectoryHandle(basedir, { create: true });
    const fileHandle = await opfs.getFileHandle(path, { create: true, root: folder });
    if (!fileHandle) throw new Error(`File could not be created: ${path}`);

    // Check if the file is up to date
    let mtime = (await fileHandle.getFile()).lastModified;
    const utime = await this.provider.needsSync(meta.uuid, mtime);
    if (utime === null) return basedir + path;

    // Write the file to the filesystem.
    const content = await this.exportFile(path);
    if (content !== null) {
      await opfs.writeContents(fileHandle, basedir + path, content);

      mtime = (await fileHandle.getFile()).lastModified;
      await this.provider.markSynced(meta.uuid, utime, mtime);
    }

    return basedir + path;
  }

  /**
   * Repair any issues in the project.
   * This should hopefully never be needed.
   */
  private repair() {
    this.root.transact(() => {
      this.fileMap.forEach((meta, path) => {
        if (!utils.isPathValid(path)) {
          this.fileMap.delete(path);
          console.warn(`Invalid path removed: ${path}`);
        }

        if (meta.path !== path) {
          this.fileMap.delete(path);
          this.fileMap.set(meta.path, meta);
          console.warn(`Path repaired: ${path} => ${meta.path}`);
        }
      });
    });
  }
}

/** Options for the FileSystem Sync module */
type SyncFsOpts = {
  /** The path to synchronize, relative in the project.  */
  path?: string;
  /** Use the project name as the folder name in the OPFS, instead of UUID.  */
  useProjectName?: boolean;
};
