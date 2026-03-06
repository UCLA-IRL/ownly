/// <reference types="golang-wasm-exec" />

import * as Y from 'yjs';

import { StoreDexie, type StoreJS } from '@/services/database/store_js';
import { KeyChainDexie, type KeyChainJS } from '@/services/database/keychain_js';
import { GlobalBus } from '@/services/event-bus';

/* eslint-disable no-var */
declare global {
  var _ndnd_store_js: StoreJS;
  var _ndnd_keychain_js: KeyChainJS;
  var _yjs_merge_updates: (updates: Uint8Array[]) => Uint8Array;
  var _ndnd_conn_change_js: (connected: boolean, router: string) => void;
  var _ndnd_conn_state: { connected: boolean; router: string };

  // [0]: wksp name, [1]: requester name, [2]: should be suppressed (because the request has already been dealt with)
  var _access_requests: [string,string,boolean][];

  var set_ndn: undefined | ((ndn: NDNAPI) => void);
  var ndn_api: NDNAPI;
}
/* eslint-enable no-var */

interface NDNAPI {
  /** Check if there is a valid testbed key in the keychain */
  has_testbed_key(): Promise<boolean>;
  /** Check if the testbed certificate is expiring soon (within a week) */
  is_testbed_cert_expiring_soon(): Promise<boolean>;
  /** Get the full testbed key name */
  get_testbed_key(): Promise<string>;
  /** List locally managed identity keys and authenticated peer identities */
  list_identity_keys(): Promise<{
    identity: string;
    local: IdentityKeyInfo[];
    peers: IdentityKeyInfo[];
  }>;
  /** Generate a new managed identity key pair */
  generate_identity_key(): Promise<IdentityKeyInfo>;
  /** Import an existing identity key pair (MarshalSecret format) */
  import_identity_key(secret: Uint8Array): Promise<IdentityKeyInfo>;
  /** Import peer self-signed certificates */
  import_peer_certs(blobs: Uint8Array[]): Promise<IdentityKeyInfo[]>;
  /** Delete a managed identity or peer entry */
  delete_identity_entry(certName: string): Promise<void>;
  /** Export a managed identity key pair as MarshalSecret */
  export_identity_secret(keyName: string): Promise<Uint8Array>;
  /** Export authenticated peer certificates */
  export_peer_certs(names: string[]): Promise<Uint8Array[]>;
  /** Export your identity certificate (self-signed) */
  export_identity_cert(): Promise<Uint8Array>;
  /** Export a managed identity certificate by name */
  export_identity_cert_by_name(certName: string): Promise<Uint8Array>;

  /** Connect to the global NDN testbed */
  connect_testbed(): Promise<void>;

  /** NDNCERT email verfication challenge */
  ndncert_email(email: string, code: (status: string) => Promise<string>): Promise<void>;
  /** NDNCERT dns verification challenge */
  ndncert_dns(
    domain: string,
    confirm: (recordName: string, recordValue: string, status: string) => Promise<string>,
  ): Promise<void>;

  /** Join Workspace (generate keys etc.) */
  join_workspace(wksp: string, create: boolean, payload: Uint8Array | null): Promise<string>;
  /** Check if the user has owner permissions on the workspace */
  is_workspace_owner(wksp: string): Promise<boolean>;

  /** Wait until a user key is ready for the workspace */
  wait_user_key(wksp: string): Promise<void>;

  /** Set callbacks to load and persist boot sync state */
  load_boot_state(
    load: (group: string) => Promise<Uint8Array | undefined>,
    persist: (group: string, state: Uint8Array) => Promise<void>,
  ): Promise<void>;
  /** Callback for owner receiving participant boot-join app payload */
  on_boot_join_payload(
    cb: (
      workspace: string,
      preCertFullName: string,
      preCertKeyName: string,
      payload: Uint8Array,
    ) => Promise<void>,
  ): Promise<void>;

  /** Get a Workspace API */
  get_workspace(name: string, ignore: boolean): Promise<WorkspaceAPI>;
}

export type IdentityKeyInfo = {
  identity: string;
  keyName: string;
  certName: string;
  hasPrivate: boolean;
  source: 'local' | 'peer';
};

export interface WorkspaceAPI {
  /** Name of this user / node */
  name: string;
  /** Overall prefix of workspace */
  group: string;

  /** Set the encryption keys */
  set_encrypt_keys(psk: Uint8Array, dsk: Uint8Array): Promise<void>;

  /** Start the workspace */
  start(): Promise<void>;
  /** Stop the workspace */
  stop(): Promise<void>;

  /** Produce an NDN object under a given name */
  produce(name: string, data: Uint8Array): Promise<void>;
  /** Consume an NDN object with a name */
  consume(name: string): Promise<{ data: Uint8Array; name: string }>;

  /** SVS ALO instance */
  svs_alo(
    group: string,
    state: Uint8Array | undefined,
    persist_state: (state: Uint8Array) => Promise<void>,
  ): Promise<SvsAloApi>;

  /** Sign and publish an invitation for a given NDN name */
  sign_and_pub_invitation(invitee: string): Promise<Uint8Array>;

  /** Wait for DSK to appear for the given key */
  wait_for_dsk(key: Uint8Array): Promise<Uint8Array>;
}

/** API of the SVS ALO instance */
export interface SvsAloApi {
  /** Sync prefix of the instance */
  sync_prefix: string;
  /** Data prefix of the instance */
  data_prefix: string;

  /** Start the SVS instance */
  start(): Promise<void>;
  /** Stop the SVS instance */
  stop(): Promise<void>;
  /** Set the error callback */
  set_on_error(): void;
  /** Get list of names in the group */
  names(): Promise<string[]>;

  /** Publish chat message to SVS ALO */
  pub_yjs_delta(uuid: string, binary: Uint8Array): Promise<void>;
  /** Publish blob fetch command */
  pub_blob_fetch(name: string, encapsulate: Uint8Array | undefined): Promise<string>;
  /** Publish request for the DSK */
  pub_dsk_request(): Promise<Uint8Array>;
  /** Publish ack for the DSK response */
  pub_dsk_ack(key: Uint8Array): Promise<void>;

  /** Set SVS ALO subscription callbacks */
  subscribe(params: {
    on_yjs_delta: SvsAloSub<{ uuid: string; binary: Uint8Array }>;
  }): Promise<void>;

  /** Awareness instance piggybacking on this SVS instance */
  awareness(uuid: string): Promise<AwarenessApi>;
}

/** Subscription to SVS ALO */
export type SvsAloSub<T> = (pub: T[]) => Promise<void>;

/** Metadata of received publication */
export type SvsAloPubInfo = {
  publisher: string;
  boot_time: number;
  seq_num: number;
};

/** API for Awareness */
export interface AwarenessApi {
  /** Start the awareness */
  start(): Promise<void>;
  /** Stop the awareness */
  stop(): Promise<void>;
  /** Publish new data */
  publish(data: Uint8Array): Promise<void>;
  /** Subscribe to data */
  subscribe(cb: (pub: Uint8Array) => void): Promise<void>;
}

/**
 * Named Data Networking Service
 */
class NDNService {
  public api!: NDNAPI;

  constructor() {}

  async setup() {
    if (this.api) return;

    // Provide JS APIs
    globalThis._ndnd_store_js = new StoreDexie('store');
    globalThis._ndnd_keychain_js = new KeyChainDexie();
    globalThis._yjs_merge_updates = Y.mergeUpdatesV2;
    globalThis._ndnd_conn_change_js = _ndnd_conn_change_js;
    globalThis._ndnd_conn_state = { connected: false, router: String() };
    globalThis._access_requests = new Array<[string,string,boolean]>();

    // Load the Go WASM module
    const go = new Go();
    let result: WebAssembly.WebAssemblyInstantiatedSource;

    if (typeof window !== 'undefined') {
      result = await WebAssembly.instantiateStreaming(fetch('/main.wasm'), go.importObject);
    } else {
      const fsImport = 'fs/promises';
      const fs = await import(/* @vite-ignore */ fsImport);
      const buffer = await fs.readFile(import.meta.dirname + '/../../main.wasm');
      result = await WebAssembly.instantiate(buffer, go.importObject);
    }

    // Callback given by WebAssembly to set the NDN API
    const ndnPromise = new Promise<NDNAPI>((resolve, reject) => {
      const cancel = setTimeout(() => reject(new Error('NDN API not set')), 5000);
      globalThis.set_ndn = (ndn: NDNAPI) => {
        globalThis.set_ndn = undefined;
        globalThis.ndn_api = ndn;
        resolve(ndn);
        clearTimeout(cancel);
      };
    });

    go.run(result.instance).then(() => {
      GlobalBus.emit(
        'wksp-error',
        new Error('WASM backend crashed. Check JS console for logs and refresh the page.'),
      );
    });
    this.api = await ndnPromise;

    const bootState = globalThis._o?.bootState;
    if (bootState && typeof this.api.load_boot_state === 'function') {
      try {
        await this.api.load_boot_state(
          async (group: string) => {
            try {
              const state = await bootState.get(group);
              if (!state || state.length === 0) return undefined;
              return state;
            } catch (err) {
              console.error('Failed to load boot state', err);
              return undefined;
            }
          },
          async (group: string, state: Uint8Array) => {
            try {
              await bootState.put(group, state);
            } catch (err) {
              console.error('Failed to persist boot state', err);
            }
          },
        );
      } catch (err) {
        console.error('Failed to register boot state persistence', err);
      }
    }

    if (typeof this.api.on_boot_join_payload === 'function') {
      try {
        await this.api.on_boot_join_payload(
          async (
            workspace: string,
            preCertFullName: string,
            preCertKeyName: string,
            payload: Uint8Array,
          ) => {
            GlobalBus.emit('boot-join-payload', workspace, preCertFullName, preCertKeyName, payload);
          },
        );
      } catch (err) {
        console.error('Failed to register boot join payload callback', err);
      }
    }
  }
}

function _ndnd_conn_change_js(connected: boolean, router: string) {
  try {
    router = new URL(router).host;
  } catch {}
  try {
    globalThis._ndnd_conn_state = { connected, router };
    GlobalBus.emit('conn-change');
  } catch {}
}

export default new NDNService();
