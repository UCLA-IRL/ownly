type WasmModule = {
    default: () => Promise<unknown>;
    Client: new (name: string) => WasmClient;
};

type WasmClient = {
    create_group(): WasmGroup;
    join_from_welcome(welcomeBytes: Uint8Array, ratchetTreeBytes?: Uint8Array | null): WasmGroup;
    key_package(): Uint8Array;
    load_group(groupIdBytes: Uint8Array): WasmGroup;
    export_storage_snapshot(): Uint8Array;
    import_storage_snapshot(snapshot: Uint8Array): void;
    free?: () => void;
};

type WasmGroup = {
    add_members(keyPackageBytes: Uint8Array[]): unknown;
    remove_member(leaves: Uint32Array): unknown;
    merge_pending_commit(): void;
    apply_commit(commitBytes: Uint8Array): void;
    my_index(): number;
    member_index_by_identity(identity: Uint8Array): number | undefined;
    group_id_bytes(): Uint8Array;
    export_secret(label: string, context: Uint8Array, len: number): Uint8Array;
    free?: () => void;
};

export type AddMembersResult = {
    commit: Uint8Array;
    welcome: Uint8Array;
};

export type RemoveMembersResult = {
    commit: Uint8Array;
};

let wasmModulePromise: Promise<WasmModule> | null = null;

async function loadWasmModule(): Promise<WasmModule> {
    if (!wasmModulePromise) {
        wasmModulePromise = ( async () => {
            const modulePath = '/openmls_lite.js';
            const module = (await import('@/wasm/openmls_lite.js')) as unknown as WasmModule;
            await module.default();
            return module;
        })();
    }
    return wasmModulePromise;
};

function getU8Field(obj: unknown, field: string): Uint8Array {
  const value = (obj as Record<string, unknown>)[field];
  if (!(value instanceof Uint8Array)) {
    throw new Error(`Invalid OpenMLS output: '${field}' is not Uint8Array`);
  }
  return value;
}

export class OpenMlsLiteClient {
    private constructor (private readonly inner: WasmClient) {}

    static async create(name: string): Promise<OpenMlsLiteClient> {
        const module = await loadWasmModule();
        return new OpenMlsLiteClient(new module.Client(name));
    }

    keyPackage(): Uint8Array {
        return this.inner.key_package();
    }

    createGroup(): OpenMlsLiteGroup {
        return new OpenMlsLiteGroup(this.inner.create_group());
    }

    joinFromWelcome(welcome: Uint8Array, ratchetTree?: Uint8Array | null): OpenMlsLiteGroup {
        return new OpenMlsLiteGroup(this.inner.join_from_welcome(welcome, ratchetTree ?? null));
    }

    exportStorageSnapshot(): Uint8Array {
        return this.inner.export_storage_snapshot();
    }

    importStorageSnapshot(snapshot: Uint8Array): void {
        this.inner.import_storage_snapshot(snapshot);
    }

    loadGroup(groupIdBytes: Uint8Array): OpenMlsLiteGroup {
        return new OpenMlsLiteGroup(this.inner.load_group(groupIdBytes));
    }

    free(): void {
        this.inner.free?.();
    }
}

export class OpenMlsLiteGroup {
    constructor (private readonly inner: WasmGroup) {}

    myIndex(): number {
        return this.inner.my_index();
    }

    groupIdBytes(): Uint8Array {
        return this.inner.group_id_bytes();
    }

    mergePendingCommit(): void {
        this.inner.merge_pending_commit();
    }

    applyCommit(commit: Uint8Array): void {
        this.inner.apply_commit(commit);
    }

    addMembers(keyPackages: Uint8Array[]): AddMembersResult {
        const out = this.inner.add_members(keyPackages);
        return {
            commit: getU8Field(out, "commit"),
            welcome: getU8Field(out, "welcome"),
        };
    }

    removeMembers(leaves: number[] | Uint32Array): RemoveMembersResult {
        const idx = leaves instanceof Uint32Array ? leaves : new Uint32Array(leaves);
        const out = this.inner.remove_member(idx);
        return {
            commit: getU8Field(out, "commit"),
        };
    }

    memberIndexByIdentity(identity: Uint8Array): number | undefined {
        return this.inner.member_index_by_identity(identity);
    }

    exportSecret(label: string, context = new Uint8Array(), len = 32): Uint8Array {
        return this.inner.export_secret(label, context, len);
    }

    exportWorkspaceSecret(context = new Uint8Array()): Uint8Array {
        return this.exportSecret('ownly/svs/aead', context, 32);
    }

    free(): void {
        this.inner.free?.();
    }
}