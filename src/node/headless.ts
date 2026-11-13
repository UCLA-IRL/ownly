/** Generic Ownly project API and one-shot export CLI for Node 23+. */
/// <reference types="node" />
/// <reference types="../services.d.ts" />
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import type { Doc } from 'yjs';
import { NodeStatsDb } from '../services/database/stats_node';
import { NodeProjDb } from '../services/database/proj_db_node';
import { NodeBootStateDb } from '../services/database/boot_db_node';
import { getOriginPrivateDirectory } from 'file-system-access';
import nodeAdapter from 'file-system-access/lib/adapters/node.js';
import ndn from '../services/ndn';
import { Workspace } from '../services/workspace';
import type { WorkspaceProj } from '../services/workspace-proj';
import { parseFastJoinBundle } from '../services/fast-join';
import * as utils from '../utils';
import { ensureTestbedCertificate } from './ndncert-cloudflare';

export type RefreshStatus = 'acknowledged' | 'unacknowledged' | 'not-requested';

export interface HeadlessOptions {
  workspace?: string;
  project: string;
  stateDir: string;
  timeoutMs?: number;
  refresh?: boolean;
  seedKeychain?: string;
  pskHex?: string;
  fastJoinBundle?: string;
  env?: NodeJS.ProcessEnv;
}

export interface HeadlessProject {
  project: WorkspaceProj;
  filesRoot: string;
  refresh: RefreshStatus;
  readFile(path: string, timeoutMs?: number): Promise<Uint8Array>;
  close(): Promise<void>;
}

let sessionActive = false;

function validateTimeout(ms: number) {
  if (!Number.isSafeInteger(ms) || ms < 1 || ms > 2_147_483_647) throw new Error('Invalid timeout');
}

function withTimeout<T>(promise: Promise<T>, ms: number, what: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout>;
  return Promise.race([
    promise,
    new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error(`${what} timed out`)), ms);
    }),
  ]).finally(() => clearTimeout(timer));
}

/** Initializes local databases and Go, but does not join a workspace or connect. */
async function bootHeadless(stateDir: string, seedKeychain?: string) {
  const files = path.join(stateDir, 'files');
  const keychain = path.join(stateDir, 'keychain');
  for (const dir of [stateDir, files, keychain, path.join(stateDir, 'projects')])
    fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  if (seedKeychain) {
    for (const entry of fs.readdirSync(seedKeychain, { withFileTypes: true })) {
      if (!entry.isFile()) continue;
      const destination = path.join(keychain, entry.name);
      fs.copyFileSync(path.join(seedKeychain, entry.name), destination);
      fs.chmodSync(destination, 0o600);
    }
  }
  process.chdir(stateDir);
  globalThis._o = {
    stats: new NodeStatsDb(),
    ProjDb: NodeProjDb,
    bootState: new NodeBootStateDb(),
    getStorageRoot: () => getOriginPrivateDirectory(nodeAdapter, files),
    streamSaver: null as never,
  };
  // Keep window absent: NewNodeApp supplies its native keychain and packet store.
  globalThis.origin = 'ownly-headless://local';
  Object.assign(globalThis, { fs });
  const builtAsset = new URL('./wasm_exec.js', import.meta.url);
  const wasmExec = fs.existsSync(builtAsset)
    ? builtAsset.href
    : new URL('../../public/wasm_exec.js', import.meta.url).href;
  await import(/* @vite-ignore */ wasmExec);
  await ndn.setup();
  return files;
}

async function waitForProject(wksp: Workspace, projName: string, timeout: number) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (wksp.proj.getProjects().some((proj) => proj.name === projName)) {
      const opening = wksp.proj.get(projName);
      try {
        return await withTimeout(opening, Math.max(1, deadline - Date.now()), 'project opening');
      } catch (error) {
        // A deadline cannot cancel an in-flight provider creation.
        void opening.then((proj) => proj.destroy()).catch(() => {});
        throw error;
      }
    }
    await new Promise((resolve) => setTimeout(resolve, Math.min(100, deadline - Date.now())));
  }
  throw new Error(`Project ${projName} did not appear within ${timeout} ms`);
}

async function refreshWorkspace(wksp: Workspace, timeout: number): Promise<RefreshStatus> {
  const budget = Math.min(timeout, 5_000);
  try {
    await withTimeout(wksp.sosRequest(budget), budget, 'peer refresh');
    return 'acknowledged';
  } catch {
    // An unavailable peer does not invalidate locally available data.
    return 'unacknowledged';
  }
}

function ready(doc: Doc) {
  return doc.store.clients.size > 0 && !doc.store.pendingStructs && !doc.store.pendingDs;
}

/** One active session per process; closing does not unload the global Go runtime. */
export async function openHeadlessProject(options: HeadlessOptions): Promise<HeadlessProject> {
  const env = options.env ?? process.env;
  const timeout = options.timeoutMs ?? 120_000;
  validateTimeout(timeout);
  const bundleText = options.fastJoinBundle ?? env.OWNLY_FAST_JOIN_BUNDLE;
  const bundle = bundleText ? parseFastJoinBundle(bundleText) : null;
  const wkspName = options.workspace ?? bundle?.wksp;
  if (!wkspName || !options.project) throw new Error('Workspace and project are required');
  if (!options.stateDir) throw new Error('State directory is required');
  const stateDir = path.resolve(options.stateDir);
  const seed = options.seedKeychain && path.resolve(options.seedKeychain);
  if (seed && !fs.statSync(seed).isDirectory()) throw new Error('Invalid seed keychain directory');
  const pskText = options.pskHex ?? env.OWNLY_PSK_HEX ?? bundle?.psk;
  if (pskText !== undefined && !/^[0-9a-fA-F]{64}$/.test(pskText))
    throw new Error('Joining requires a 32-byte PSK (64 hex characters)');
  if (sessionActive) throw new Error('Only one active headless session per process is supported');
  sessionActive = true;

  let wksp: Workspace | undefined;
  let setup: Promise<Workspace> | undefined;
  let closing: Promise<void> | undefined;
  const close = (): Promise<void> => {
    closing ??= (async () => {
      const opened = wksp ?? (await setup);
      await opened?.destroy();
      // A failed teardown can leave process-global resources active.
      sessionActive = false;
    })();
    return closing;
  };

  try {
    const filesRoot = await bootHeadless(stateDir, seed);
    await ensureTestbedCertificate(ndn.api, env);
    // Upstream ownership checks require a regular identity even with fast join.
    const identities = await ndn.api.list_identity_keys();
    if (!identities.local.length) await ndn.api.generate_identity_key();
    const existing = await _o.stats.get(wkspName);
    if (bundle && (!existing || existing.revoked || !existing.dsk))
      await ndn.api.import_fast_join_identity(
        bundle.ephemeralSecret,
        bundle.ephemeralCert,
        bundle.ownerCert,
      );
    let finalName = wkspName;
    if (!existing || existing.revoked) {
      if (!pskText) throw new Error('Joining requires a 32-byte PSK (64 hex characters)');
      finalName = await withTimeout(
        Workspace.join(bundle?.label ?? wkspName, wkspName, false, utils.fromHex(pskText), null),
        timeout,
        'workspace join',
      );
    }
    setup = Workspace.setup(utils.escapeUrlName(finalName));
    wksp = await withTimeout(setup, timeout, 'workspace setup');
    if (options.refresh && !wksp.proj.getProjects().some((proj) => proj.name === options.project))
      await refreshWorkspace(wksp, timeout);
    const project = await waitForProject(wksp, options.project, timeout);
    // Subscribe to the project before asking peers to republish its documents.
    const refresh = options.refresh ? await refreshWorkspace(wksp, timeout) : 'not-requested';

    return {
      project,
      filesRoot,
      refresh,
      async readFile(entryPath, timeoutMs = timeout) {
        validateTimeout(timeoutMs);
        const deadline = Date.now() + timeoutMs;
        const remaining = () => {
          if (closing) throw new Error('Headless session is closed');
          const ms = deadline - Date.now();
          if (ms <= 0) throw new Error('File read timed out');
          return ms;
        };
        while (true) {
          remaining();
          // The existing root document owns the integrated project file list.
          if (ready(project['root'])) {
            const meta = project.getFileMeta(entryPath);
            if (meta) {
              const doc = await withTimeout(project.getFile(meta.path), remaining(), 'file read');
              if (ready(project['root']) && ready(doc) && project.getFileMeta(entryPath) === meta) {
                const content = await withTimeout(
                  project.exportFile(meta.path),
                  remaining(),
                  'file read',
                );
                remaining();
                if (content !== null) return content;
              }
            }
          }
          // No outer race: after any timed-out await this loop cannot resume polling.
          await new Promise((resolve) => setTimeout(resolve, Math.min(100, remaining())));
        }
      },
      close,
    };
  } catch (error) {
    if (setup && !wksp) {
      // Keep ownership until late setup settles, then destroy any acquired workspace.
      void close().catch(() => {});
    } else {
      try {
        await close();
      } catch (cleanupError) {
        throw new AggregateError(
          [error, cleanupError],
          'Headless initialization and cleanup failed',
        );
      }
    }
    throw error;
  }
}

const HELP = `Ownly headless project consumer (Node 23+)
Usage: node dist/headless/headless.js --wksp <name> --proj <name> [options]
Also accepts the original positional <workspace> <project> arguments.
--state-dir <dir>       Persistent local state (default: ./ownly-state)
--seed-keychain <dir>   Copy existing certificate/key inputs before boot
--psk <hex>            Join with 32-byte PSK (or OWNLY_PSK_HEX)
--bundle <base64url>   Import existing fast-join bundle (or OWNLY_FAST_JOIN_BUNDLE)
--timeout <ms>         Setup/drain deadline per phase (default: 120000)
--refresh              Best-effort outgoing SOS before drain (at most 5000 ms)
--help                 Show help
OWNLY_NDNCERT_DOMAIN enables Cloudflare DNS enrollment if no valid testbed key exists.
Set CLOUDFLARE_API_TOKEN and CLOUDFLARE_ZONE_ID; valid seeded keys skip enrollment.
Refresh reports acknowledged, unacknowledged or not-requested; no ack is required.
Complete available data is required, not proof of globally latest peer state.
No creation, publishing or invitation generation is performed.`;

async function main(args: string[]) {
  const { values, positionals } = parseArgs({
    args,
    allowPositionals: true,
    options: {
      wksp: { type: 'string' },
      proj: { type: 'string' },
      'state-dir': { type: 'string' },
      'seed-keychain': { type: 'string' },
      psk: { type: 'string' },
      bundle: { type: 'string' },
      timeout: { type: 'string', default: '120000' },
      refresh: { type: 'boolean', default: false },
      help: { type: 'boolean' },
    },
  });
  if (values.help) {
    console.log(HELP);
    return;
  }
  if (positionals.length > 2) throw new Error('Expected at most workspace and project positionals');
  const timeout = Number(values.timeout);
  const session = await openHeadlessProject({
    workspace: values.wksp ?? positionals[0],
    project: values.proj ?? positionals[1] ?? '',
    stateDir: values['state-dir'] ?? './ownly-state',
    seedKeychain: values['seed-keychain'],
    pskHex: values.psk,
    fastJoinBundle: values.bundle,
    timeoutMs: timeout,
    refresh: values.refresh,
  });
  try {
    const deadline = Date.now() + timeout;
    const remaining = () => {
      const ms = deadline - Date.now();
      if (ms <= 0) throw new Error('Project export timed out');
      return ms;
    };
    const files = await withTimeout(
      session.project.waitForAvailableFiles(remaining()),
      remaining(),
      'project export',
    );
    // A partial local list must never delete exports it has not learned about.
    for (const file of files) {
      await withTimeout(
        session.project.syncFs({ path: file.path, useProjectName: true }),
        remaining(),
        'project export',
      );
    }
    const result = {
      outputDir: path.resolve(
        session.filesRoot,
        session.project.getFsBasePath({ useProjectName: true }).replace(/^\/+/, ''),
      ),
      refresh: session.refresh,
    };
    console.log('HEADLESS RESULT: ' + JSON.stringify(result));
  } finally {
    await session.close();
  }
}

if (
  process.argv[1] &&
  fs.existsSync(process.argv[1]) &&
  fs.realpathSync(path.resolve(process.argv[1])) === fileURLToPath(import.meta.url)
) {
  main(process.argv.slice(2))
    .then(() => process.exit(0))
    .catch((error) => {
      console.error('FATAL:', error.message);
      process.exit(1);
    });
}
