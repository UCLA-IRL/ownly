/* tslint:disable */
/* eslint-disable */

export class Client {
    free(): void;
    [Symbol.dispose](): void;
    create_group(): Group;
    export_storage_snapshot(): Uint8Array;
    import_storage_snapshot(snapshot: Uint8Array): void;
    join_from_welcome(welcome_bytes: Uint8Array, ratchet_tree_bytes?: Uint8Array | null): Group;
    key_package(): Uint8Array;
    key_package_identity(kp_bytes: Uint8Array): Uint8Array;
    load_group(group_id_bytes: Uint8Array): Group;
    constructor(workspace_cert: Uint8Array, identity: string);
}

export class Group {
    private constructor();
    free(): void;
    [Symbol.dispose](): void;
    add_members(key_packages: any[]): any;
    apply_commit(commit_bytes: Uint8Array): void;
    epoch(): bigint;
    export_secret(label: string, context: Uint8Array, len: number): Uint8Array;
    group_id_bytes(): Uint8Array;
    member_identities(): Array<any>;
    member_indexes_by_identity(identity_value: Uint8Array): Uint32Array;
    member_indexes_by_identity_prefix(identity_prefix: Uint8Array): Uint32Array;
    merge_pending_commit(): void;
    my_index(): number;
    remove_member(leaves: Uint32Array): any;
}

export type InitInput = RequestInfo | URL | Response | BufferSource | WebAssembly.Module;

export interface InitOutput {
    readonly memory: WebAssembly.Memory;
    readonly __wbg_client_free: (a: number, b: number) => void;
    readonly __wbg_group_free: (a: number, b: number) => void;
    readonly client_create_group: (a: number) => [number, number, number];
    readonly client_export_storage_snapshot: (a: number) => [number, number, number, number];
    readonly client_import_storage_snapshot: (a: number, b: number, c: number) => [number, number];
    readonly client_join_from_welcome: (a: number, b: number, c: number, d: number, e: number) => [number, number, number];
    readonly client_key_package: (a: number) => [number, number, number, number];
    readonly client_key_package_identity: (a: number, b: number, c: number) => [number, number, number, number];
    readonly client_load_group: (a: number, b: number, c: number) => [number, number, number];
    readonly client_new: (a: number, b: number, c: number, d: number) => [number, number, number];
    readonly group_add_members: (a: number, b: number, c: number) => [number, number, number];
    readonly group_apply_commit: (a: number, b: number, c: number) => [number, number];
    readonly group_epoch: (a: number) => bigint;
    readonly group_export_secret: (a: number, b: number, c: number, d: number, e: number, f: number) => [number, number, number, number];
    readonly group_group_id_bytes: (a: number) => [number, number];
    readonly group_member_identities: (a: number) => [number, number, number];
    readonly group_member_indexes_by_identity: (a: number, b: number, c: number) => [number, number];
    readonly group_member_indexes_by_identity_prefix: (a: number, b: number, c: number) => [number, number];
    readonly group_merge_pending_commit: (a: number) => [number, number];
    readonly group_my_index: (a: number) => number;
    readonly group_remove_member: (a: number, b: number, c: number) => [number, number, number];
    readonly __wbindgen_exn_store: (a: number) => void;
    readonly __externref_table_alloc: () => number;
    readonly __wbindgen_externrefs: WebAssembly.Table;
    readonly __externref_table_dealloc: (a: number) => void;
    readonly __wbindgen_free: (a: number, b: number, c: number) => void;
    readonly __wbindgen_malloc: (a: number, b: number) => number;
    readonly __wbindgen_realloc: (a: number, b: number, c: number, d: number) => number;
    readonly __wbindgen_start: () => void;
}

export type SyncInitInput = BufferSource | WebAssembly.Module;

/**
 * Instantiates the given `module`, which can either be bytes or
 * a precompiled `WebAssembly.Module`.
 *
 * @param {{ module: SyncInitInput }} module - Passing `SyncInitInput` directly is deprecated.
 *
 * @returns {InitOutput}
 */
export function initSync(module: { module: SyncInitInput } | SyncInitInput): InitOutput;

/**
 * If `module_or_path` is {RequestInfo} or {URL}, makes a request and
 * for everything else, calls `WebAssembly.instantiate` directly.
 *
 * @param {{ module_or_path: InitInput | Promise<InitInput> }} module_or_path - Passing `InitInput` directly is deprecated.
 *
 * @returns {Promise<InitOutput>}
 */
export default function __wbg_init (module_or_path?: { module_or_path: InitInput | Promise<InitInput> } | InitInput | Promise<InitInput>): Promise<InitOutput>;
