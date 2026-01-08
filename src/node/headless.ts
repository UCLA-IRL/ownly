/**
 * Ownly Headless Utility.
 *
 * This synchronizes an Ownly project to the filesystem.
 * Requires Node.js 23 or later.
 */

/// <reference types="node" />
/// <reference types="../services.d.ts" />

import fs from 'fs';
import util from 'util';
import crypto from 'crypto';

import { NodeStatsDb } from '../services/database/stats_node';
import { NodeProjDb } from '../services/database/proj_db_node';
import { NodeBootStateDb } from '../services/database/boot_db_node';
import { getOriginPrivateDirectory } from 'file-system-access';
import nodeAdapter from 'file-system-access/lib/adapters/node.js';

import ndn from '../services/ndn';
import { Workspace } from '../services/workspace';
import * as utils from '../utils';

async function loadServices() {
  globalThis._o = {
    stats: new NodeStatsDb(),
    ProjDb: NodeProjDb,
    bootState: new NodeBootStateDb(),

    getStorageRoot: () => getOriginPrivateDirectory(nodeAdapter, './'),
    streamSaver: null as any, // no node
  };
}

async function loadGoEnvironment() {
  // Prep environment for WebAssembly
  (globalThis as any).fs = fs;
  globalThis.TextEncoder = util.TextEncoder;
  /// @ts-expect-error - TextDecoder is not defined in Node
  globalThis.TextDecoder = util.TextDecoder;
  globalThis.performance ??= performance;
  globalThis.crypto ??= crypto as any;

  const wasm_exec = './wasm_exec.js';
  await import(wasm_exec);
  console.log('Go environment loaded');
}

async function setupWorkspace(wkspName: string): Promise<Workspace> {
  // Join the workspace if not already joined
  const wkspMeta = await globalThis._o.stats.get(wkspName);
  if (!wkspMeta) await Workspace.join(wkspName, wkspName, false);

  // Setup the workspace
  return await Workspace.setup(utils.escapeUrlName(wkspName));
}

async function main() {
  if (process.argv.length < 3) {
    console.error('Usage: node dist/headless.js </workspace/name> <project-name>');
    process.exit(1);
  }

  const wkspName = process.argv[2];
  const projName = process.argv[3];

  try {
    await loadServices();
    await loadGoEnvironment();
    await ndn.setup();

    // Setup the workspace
    const wksp = await setupWorkspace(wkspName);

    // TODO: hook to wait for sync
    await new Promise((resolve) => setTimeout(resolve, 2000));
    const proj = await wksp.proj.get(projName);

    // TODO: hook to wait for sync
    await new Promise((resolve) => setTimeout(resolve, 5000));

    // Sychronize the filesystem
    await proj.syncFs({
      useProjectName: true,
    });
    process.exit(0);
  } catch (e) {
    console.error('FATAL:', e);
    process.exit(1);
  }
}

main();
