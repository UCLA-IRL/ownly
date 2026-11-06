import * as zip from '@zip.js/zip.js';
export { getFileHandle, getDirectoryHandle } from './opfs-common.ts';

/**
 * Write to a file and close it
 * @param handle File handle
 * @param content Content to write
 */
export async function writeContents(
  handle: FileSystemFileHandle,
  content: Uint8Array,
): Promise<number> {
  const writable = await handle.createWritable();
  await writable.truncate(0);
  await writable.write(content);
  await writable.close();
  return content.length;
}

/**
 * Walk through the OPFS and call a callback for each file
 * @param folder Root directory handle
 * @param path Path to the directory
 */
export async function* walk(
  folder: FileSystemDirectoryHandle,
  path: string,
): AsyncGenerator<[string, FileSystemFileHandle, FileSystemDirectoryHandle]> {
  for await (const [name, entry] of folder.entries()) {
    if (entry.kind === 'file') {
      yield [`${path}${name}`, entry as FileSystemFileHandle, folder];
    } else if (entry.kind === 'directory') {
      yield* walk(entry as FileSystemDirectoryHandle, `${path}${name}/`);
    }
  }
}

/**
 * Download a file or folder from OPFS
 *
 * @param handle File or folder handle
 */
export async function download(
  handle: FileSystemFileHandle | FileSystemDirectoryHandle,
  opts?: {
    name?: string;
  },
): Promise<void> {
  if (handle.kind === 'file') {
    // Stream file directly to the user
    const fileStream = _o.streamSaver.createWriteStream(opts?.name ?? handle.name);
    const readable = await handle.getFile();
    await readable.stream().pipeTo(fileStream);
  } else if (handle.kind === 'directory') {
    // Stream folder as a ZIP file
    const fileStream = _o.streamSaver.createWriteStream(opts?.name ?? handle.name + '.zip');
    const writer = new zip.ZipWriter(fileStream);
    for await (const [path, entry] of walk(handle, String())) {
      const readable = await entry.getFile();
      await writer.add(path, readable.stream());
    }
    await writer.close();
  } else {
    throw new Error('Invalid handle type');
  }
}
