/* tslint:disable */
/* eslint-disable */
export const memory: WebAssembly.Memory;
export const __wbg_client_free: (a: number, b: number) => void;
export const __wbg_group_free: (a: number, b: number) => void;
export const client_create_group: (a: number) => [number, number, number];
export const client_export_storage_snapshot: (a: number) => [number, number, number, number];
export const client_import_storage_snapshot: (a: number, b: number, c: number) => [number, number];
export const client_join_from_welcome: (a: number, b: number, c: number, d: number, e: number) => [number, number, number];
export const client_key_package: (a: number) => [number, number, number, number];
export const client_load_group: (a: number, b: number, c: number) => [number, number, number];
export const client_new: (a: number, b: number) => [number, number, number];
export const group_add_members: (a: number, b: number, c: number) => [number, number, number];
export const group_apply_commit: (a: number, b: number, c: number) => [number, number];
export const group_export_secret: (a: number, b: number, c: number, d: number, e: number, f: number) => [number, number, number, number];
export const group_group_id_bytes: (a: number) => [number, number];
export const group_member_index_by_identity: (a: number, b: number, c: number) => number;
export const group_merge_pending_commit: (a: number) => [number, number];
export const group_my_index: (a: number) => number;
export const group_remove_member: (a: number, b: number, c: number) => [number, number, number];
export const __wbindgen_exn_store: (a: number) => void;
export const __externref_table_alloc: () => number;
export const __wbindgen_externrefs: WebAssembly.Table;
export const __externref_table_dealloc: (a: number) => void;
export const __wbindgen_free: (a: number, b: number, c: number) => void;
export const __wbindgen_malloc: (a: number, b: number) => number;
export const __wbindgen_realloc: (a: number, b: number, c: number, d: number) => number;
export const __wbindgen_start: () => void;
