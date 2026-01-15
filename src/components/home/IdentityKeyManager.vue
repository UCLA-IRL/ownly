<template>
  <ModalComponent :show="show" @close="emit('close')">
    <div class="title is-5 mb-1">Identity keys</div>
    <p class="mb-4">
      Your identity key is your global ID in Ownly. It authenticates you across workspaces; it does not secure your data.
    </p>

    <div v-if="loading" class="has-text-centered my-4">
      <LoadingSpinner text="Loading identity keys ..." />
    </div>

    <div v-else>
      <article class="card-block">
        <header class="card-head">
          <div>
            <p class="is-size-6 has-text-weight-semibold">My identity keys</p>
            <p class="is-size-7">Identity name: <code>{{ identity || '--' }}</code></p>
          </div>
          <div class="actions">
            <label class="button is-small is-primary" :class="{ 'is-loading': busy && action === 'import-id' }">
              <input
                class="is-hidden"
                type="file"
                accept=".ndnkey"
                @change="onIdentityImport"
              />
              Import identity secret
            </label>
            <div class="stack-actions">
              <button
                class="button is-small"
                :class="{ 'is-loading': busy && action === 'generate-id' }"
                :disabled="busy"
                @click="generateIdentityKey"
              >
                Generate new key
              </button>
              <button class="button is-small" :disabled="busy" @click="openScanner('secret')">
                Scan encrypted identity QR
              </button>
            </div>
          </div>
        </header>

        <table class="table is-fullwidth is-hoverable wide-table">
          <thead>
            <tr>
              <th>Key name</th>
              <th>Certificate</th>
              <th class="has-text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="key in localKeys" :key="key.certName">
              <td><code>{{ key.keyName }}</code></td>
              <td><code>{{ key.certName }}</code></td>
              <td class="has-text-right">
                <button
                  class="button is-small mr-1"
                  :class="{ 'is-loading': busy && action === 'export-id' }"
                  :disabled="busy"
                  @click="exportIdentityKey(key)"
                >
                  Export secret
                </button>
                <button class="button is-small mr-1" :disabled="busy" @click="exportIdentityCert(key)">
                  Download cert
                </button>
                <button class="button is-small is-text has-text-danger" :disabled="busy" @click="deleteEntry(key)">
                  Delete
                </button>
              </td>
            </tr>
            <tr v-if="!localKeys.length">
              <td colspan="3" class="has-text-grey">No identity keys yet.</td>
            </tr>
          </tbody>
        </table>
        <p v-if="identityError" class="help is-danger mt-2">{{ identityError }}</p>
      </article>

      <article class="card-block">
        <header class="card-head">
          <div>
            <p class="is-size-6 has-text-weight-semibold">Authenticated peers</p>
            <p class="is-size-7">Import self-signed peer certificates to trust them.</p>
          </div>
          <div class="actions">
            <label class="button is-small is-primary" :class="{ 'is-loading': busy && action === 'import-peer' }">
              <input
                class="is-hidden"
                type="file"
                multiple
                accept=".cert,.ndn,.pem,application/octet-stream"
                @change="onPeerImport"
              />
              Import peer certs
            </label>
            <div class="stack-actions">
              <button
                class="button is-small"
                :disabled="!selectedPeers.size || busy"
                :class="{ 'is-loading': busy && action === 'export-peer' }"
                @click="exportSelectedPeers"
              >
                Export selected
              </button>
              <button
                class="button is-small"
                :disabled="busy"
                @click="openScanner('peer')"
              >
                Scan peer cert QR
              </button>
            </div>
            <button
              class="button is-small is-light is-danger"
              :disabled="!selectedPeers.size || busy"
              :class="{ 'is-loading': busy && action === 'delete-peer' }"
              @click="deleteSelectedPeers"
            >
              Delete selected
            </button>
          </div>
        </header>

        <table class="table is-fullwidth is-hoverable wide-table">
          <thead>
            <tr>
              <th style="width: 40px">
                <input type="checkbox" :checked="allPeersSelected" :disabled="!peerKeys.length" @change="toggleAllPeers" />
              </th>
              <th>Key name</th>
              <th>Certificate</th>
              <th class="has-text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="peer in peerKeys" :key="peer.certName">
              <td>
                <input type="checkbox" :checked="selectedPeers.has(peer.certName)" @change="togglePeer(peer.certName)" />
              </td>
              <td><code>{{ peer.keyName }}</code></td>
              <td><code>{{ peer.certName }}</code></td>
              <td class="has-text-right">
                <button class="button is-small mr-1" :disabled="busy" @click="exportPeer(peer.certName)">
                  Export
                </button>
                <button class="button is-small is-text has-text-danger" :disabled="busy" @click="deleteEntry(peer)">
                  Delete
                </button>
              </td>
            </tr>
            <tr v-if="!peerKeys.length">
              <td colspan="4" class="has-text-grey">No peer identities added yet.</td>
            </tr>
          </tbody>
        </table>
        <p v-if="peerError" class="help is-danger mt-2">{{ peerError }}</p>
      </article>
    </div>

    <QrModal
      :show="showScanner"
      mode="scan"
      :title="scannerTitle"
      :message="scannerMessage"
      @decoded="onScan"
      @close="showScanner = false"
    />

    <ModalComponent :show="showSecretPasswordModal" @close="closeSecretPasswordModal()">
      <div class="title is-5 mb-3">Decrypt identity key</div>
      <p class="mb-3">Enter the password used when generating the encrypted identity QR.</p>
      <div class="field">
        <label class="label is-small">Password</label>
        <div class="control">
          <input
            class="input"
            type="password"
            autocomplete="current-password"
            v-model="secretPassword"
            :disabled="busy"
            autofocus
            @keyup.enter="confirmSecretImport"
          />
        </div>
      </div>
      <div class="field has-text-right">
        <button class="button mr-2" type="button" :disabled="busy" @click="closeSecretPasswordModal()">
          Cancel
        </button>
        <button
          class="button is-primary"
          type="button"
          :disabled="!secretPassword || busy"
          @click="confirmSecretImport"
        >
          {{ busy && action === 'import-secret' ? 'Importing...' : 'Import key' }}
        </button>
      </div>
      <p class="help is-danger mt-2" v-if="secretPasswordError">{{ secretPasswordError }}</p>
    </ModalComponent>
  </ModalComponent>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';

import LoadingSpinner from '@/components/LoadingSpinner.vue';
import ModalComponent from '@/components/ModalComponent.vue';
import ndn, { type IdentityKeyInfo } from '@/services/ndn';
import { Toast } from '@/utils/toast';
import { formatIdentityFilename } from '@/utils/identity';
import { describeIdentityKeyImportError, describePeerCertImportError } from '@/utils/identity-errors';
import { decodeQrDataPayload, decryptSecretPayload } from '@/utils/qr-crypto';
import QrModal from '@/components/QrModal.vue';

const props = defineProps({
  show: {
    type: Boolean,
    required: true,
  },
});

const emit = defineEmits(['close']);

const loading = ref(true);
const identity = ref(String());
const localKeys = ref<IdentityKeyInfo[]>([]);
const peerKeys = ref<IdentityKeyInfo[]>([]);
const selectedPeers = ref<Set<string>>(new Set());
const busy = ref(false);
const action = ref<string | null>(null);
const identityError = ref(String());
const peerError = ref(String());
const showScanner = ref(false);
const scanMode = ref<'peer' | 'secret' | null>(null);
const showSecretPasswordModal = ref(false);
const secretPassword = ref(String());
const scannedSecretPayload = ref<string | null>(null);
const secretPasswordError = ref(String());

const scannerTitle = computed(() =>
  scanMode.value === 'peer' ? 'Scan peer certificate QR' : 'Scan encrypted identity QR',
);
const scannerMessage = computed(() =>
  scanMode.value === 'peer'
    ? 'Point the camera at the peer certificate QR code.'
    : 'Point the camera at the encrypted identity QR code.',
);

const allPeersSelected = computed(
  () => peerKeys.value.length > 0 && selectedPeers.value.size === peerKeys.value.length,
);

watch(
  () => props.show,
  async (show) => {
    if (show) {
      await refresh();
    } else {
      selectedPeers.value = new Set();
      identityError.value = '';
      peerError.value = '';
    }
  },
);

function identityFilename(entry: IdentityKeyInfo, prefix: string, ext: string) {
  return formatIdentityFilename(entry, {
    prefix,
    ext,
    fallbackIdentity: identity.value,
  });
}

function peerFilename(certName: string) {
  const peer = peerKeys.value.find((p) => p.certName === certName);
  return formatIdentityFilename(peer ?? { keyName: certName }, {
    prefix: 'peer',
    ext: 'cert',
    fallbackIdentity: 'peer',
  });
}

function describeDeleteError(err: unknown, fallback = 'Unable to delete entry.'): string {
  if (err instanceof Error && err.message) return err.message;
  if (typeof err === 'string' && err) return err;
  return fallback;
}

function openScanner(mode: 'peer' | 'secret') {
  scanMode.value = mode;
  showScanner.value = true;
}

async function refresh(options: { showSpinner?: boolean } = {}) {
  const { showSpinner = true } = options;
  if (showSpinner) loading.value = true;
  identityError.value = '';
  peerError.value = '';
  try {
    const overview = await ndn.api.list_identity_keys();
    identity.value = overview.identity;
    localKeys.value = overview.local ?? [];
    peerKeys.value = overview.peers ?? [];
    selectedPeers.value = new Set();
  } catch (err) {
    console.error(err);
    identityError.value = 'Unable to load identity keys.';
    peerError.value = 'Unable to load peer certificates.';
  } finally {
    if (showSpinner) loading.value = false;
    action.value = null;
    busy.value = false;
  }
}

async function generateIdentityKey() {
  identityError.value = '';
  busy.value = true;
  action.value = 'generate-id';
  try {
    const entry = await ndn.api.generate_identity_key();
    upsertLocalKey(entry);
    Toast.success('Generated new identity key');
  } catch (err) {
    console.error(err);
    Toast.error('Failed to generate identity key');
  } finally {
    busy.value = false;
    action.value = null;
  }
}

async function onIdentityImport(event: Event) {
  const target = event.target as HTMLInputElement | null;
  const file = target?.files?.[0];
  if (!file) return;

  identityError.value = '';
  busy.value = true;
  action.value = 'import-id';
  try {
    const buffer = await file.arrayBuffer();
    const entry = await ndn.api.import_identity_key(new Uint8Array(buffer));
    upsertLocalKey(entry);
    Toast.success('Imported identity key');
  } catch (err) {
    console.error(err);
    const message = describeIdentityKeyImportError(err);
    identityError.value = message;
    Toast.error(`Failed to import identity key: ${message}`);
  } finally {
    busy.value = false;
    action.value = null;
    if (target) target.value = '';
  }
}

async function exportIdentityKey(key: IdentityKeyInfo) {
  busy.value = true;
  action.value = 'export-id';
  try {
    const wire = await ndn.api.export_identity_secret(key.keyName);
    downloadBytes(wire, identityFilename(key, 'identity-key', 'ndnkey'));
    Toast.success('Identity key exported');
  } catch (err) {
    console.error(err);
    Toast.error('Unable to export identity key');
  } finally {
    busy.value = false;
    action.value = null;
  }
}

async function exportIdentityCert(key: IdentityKeyInfo) {
  busy.value = true;
  action.value = 'export-id-cert';
  try {
    const wire = await ndn.api.export_identity_cert_by_name(key.certName);
    downloadBytes(wire, identityFilename(key, 'identity', 'cert'));
    Toast.success('Identity certificate exported');
  } catch (err) {
    console.error(err);
    Toast.error('Unable to export identity certificate');
  } finally {
    busy.value = false;
    action.value = null;
  }
}

async function onPeerImport(event: Event) {
  const target = event.target as HTMLInputElement | null;
  const files = Array.from(target?.files ?? []);
  if (!files.length) return;

  peerError.value = '';
  busy.value = true;
  action.value = 'import-peer';
  try {
    const buffers: Uint8Array[] = [];
    for (const file of files) {
      const buf = await file.arrayBuffer();
      buffers.push(new Uint8Array(buf));
    }
    const imported = await ndn.api.import_peer_certs(buffers);
    if (!imported.length) {
      const message = 'No valid peer certificates found.';
      peerError.value = message;
      Toast.error(`Failed to import peer certificates: ${message}`);
      return;
    }
    upsertPeerKeys(imported);
    Toast.success('Imported peer certificates');
  } catch (err) {
    console.error(err);
    const message = describePeerCertImportError(err);
    peerError.value = message;
    Toast.error(`Failed to import peer certificates: ${message}`);
  } finally {
    busy.value = false;
    action.value = null;
    if (target) target.value = '';
  }
}

async function exportPeer(certName: string) {
  busy.value = true;
  action.value = 'export-peer';
  try {
    const wires = await ndn.api.export_peer_certs([certName]);
    downloadBytes(wires[0], peerFilename(certName));
    Toast.success('Exported peer certificate');
  } catch (err) {
    console.error(err);
    Toast.error('Unable to export peer certificate');
  } finally {
    busy.value = false;
    action.value = null;
  }
}

async function exportSelectedPeers() {
  if (!selectedPeers.value.size) return;
  busy.value = true;
  action.value = 'export-peer';
  try {
    const names = Array.from(selectedPeers.value);
    const wires = await ndn.api.export_peer_certs(names);
    names.forEach((name, idx) => {
      downloadBytes(wires[idx], peerFilename(name));
    });
    Toast.success('Exported peer keys');
  } catch (err) {
    console.error(err);
    Toast.error('Unable to export selected peers');
  } finally {
    busy.value = false;
    action.value = null;
  }
}

async function deleteEntry(entry: IdentityKeyInfo) {
  if (!confirm('Delete this key?')) return;
  if (entry.source === 'peer') {
    peerError.value = '';
  } else {
    identityError.value = '';
  }
  busy.value = true;
  action.value = entry.source === 'peer' ? 'delete-peer' : 'delete-id';
  try {
    await ndn.api.delete_identity_entry(entry.certName);
    if (entry.source === 'peer') {
      removePeers([entry.certName]);
    } else {
      localKeys.value = localKeys.value.filter((key) => key.certName !== entry.certName);
    }
    Toast.success('Entry deleted');
  } catch (err) {
    console.error(err);
    const message = describeDeleteError(err, 'Failed to delete key.');
    if (entry.source === 'peer') {
      peerError.value = message;
    } else {
      identityError.value = message;
    }
    Toast.error(`Failed to delete key: ${message}`);
  } finally {
    busy.value = false;
    action.value = null;
  }
}

async function deleteSelectedPeers() {
  if (!selectedPeers.value.size) return;
  if (!confirm('Delete selected peer keys?')) return;
  peerError.value = '';
  busy.value = true;
  action.value = 'delete-peer';
  const removed: string[] = [];
  const failures: string[] = [];
  try {
    const names = Array.from(selectedPeers.value);
    for (const name of names) {
      try {
        await ndn.api.delete_identity_entry(name);
        removed.push(name);
      } catch (err) {
        console.error(err);
        failures.push(describeDeleteError(err, 'Failed to delete peer key.'));
      }
    }
    if (removed.length) removePeers(removed);
    if (failures.length) {
      const summary = `Failed to delete ${failures.length} peer${failures.length === 1 ? '' : 's'}.`;
      peerError.value = failures[0];
      Toast.error(`${summary} ${failures[0]}`);
    } else {
      Toast.success('Selected peers deleted');
    }
  } catch (err) {
    console.error(err);
    const message = describeDeleteError(err, 'Failed to delete selected peers.');
    peerError.value = message;
    Toast.error(`Failed to delete selected peers: ${message}`);
  } finally {
    busy.value = false;
    action.value = null;
  }
}

function togglePeer(certName: string) {
  const set = new Set(selectedPeers.value);
  if (set.has(certName)) set.delete(certName);
  else set.add(certName);
  selectedPeers.value = set;
}

function toggleAllPeers() {
  if (allPeersSelected.value) {
    selectedPeers.value = new Set();
  } else {
    selectedPeers.value = new Set(peerKeys.value.map((p) => p.certName));
  }
}

function removePeers(names: Iterable<string>) {
  const removeSet = new Set(names);
  peerKeys.value = peerKeys.value.filter((peer) => !removeSet.has(peer.certName));
  if (!selectedPeers.value.size) return;
  const next = new Set(selectedPeers.value);
  removeSet.forEach((name) => next.delete(name));
  selectedPeers.value = next;
}

function upsertLocalKey(entry: IdentityKeyInfo) {
  if (entry.identity) identity.value = entry.identity;
  const next = new Map<string, IdentityKeyInfo>();
  next.set(entry.certName, entry);
  localKeys.value.forEach((key) => {
    if (!next.has(key.certName)) next.set(key.certName, key);
  });
  localKeys.value = Array.from(next.values());
}

function upsertPeerKeys(entries: IdentityKeyInfo[]) {
  if (!entries.length) return;
  const next = new Map<string, IdentityKeyInfo>();
  entries.forEach((entry) => next.set(entry.certName, entry));
  peerKeys.value.forEach((peer) => {
    if (!next.has(peer.certName)) next.set(peer.certName, peer);
  });
  peerKeys.value = Array.from(next.values());
}

function downloadBytes(data: Uint8Array, filename: string) {
  const blob = new Blob([new Uint8Array(data).buffer as ArrayBuffer]);
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  URL.revokeObjectURL(url);
}


async function onScan(raw: string) {
  const mode = scanMode.value;
  showScanner.value = false;
  if (!mode) return;

  if (mode === 'peer') {
    peerError.value = '';
  } else {
    identityError.value = '';
  }
  try {
    if (mode === 'peer') {
      const bytes = decodeQrDataPayload(raw);
      const imported = await ndn.api.import_peer_certs([bytes]);
      if (!imported.length) {
        const message = 'No valid peer certificates found.';
        peerError.value = message;
        Toast.error(`Failed to import from QR code: ${message}`);
      } else {
        upsertPeerKeys(imported);
        Toast.success('Peer certificate imported');
      }
    } else if (mode === 'secret') {
      scannedSecretPayload.value = raw;
      secretPassword.value = '';
      secretPasswordError.value = '';
      showSecretPasswordModal.value = true;
    }
  } catch (err) {
    console.error(err);
    const message =
      mode === 'peer' ? describePeerCertImportError(err) : describeIdentityKeyImportError(err);
    if (mode === 'peer') {
      peerError.value = message;
    } else {
      identityError.value = message;
    }
    Toast.error(`Failed to import from QR code: ${message}`);
  } finally {
    scanMode.value = null;
  }
}

function closeSecretPasswordModal(force = false) {
  if (busy.value && !force) return;
  showSecretPasswordModal.value = false;
  scannedSecretPayload.value = null;
  secretPassword.value = '';
  secretPasswordError.value = '';
}

async function confirmSecretImport() {
  if (!scannedSecretPayload.value || !secretPassword.value) return;

  busy.value = true;
  action.value = 'import-secret';
  secretPasswordError.value = '';
  try {
    const secret = await decryptSecretPayload(scannedSecretPayload.value, secretPassword.value);
    const entry = await ndn.api.import_identity_key(secret);
    upsertLocalKey(entry);
    Toast.success('Identity secret imported');
    closeSecretPasswordModal(true);
  } catch (err) {
    console.error(err);
    const message = describeIdentityKeyImportError(err);
    secretPasswordError.value = message;
    Toast.error(`Failed to import identity secret: ${message}`);
  } finally {
    busy.value = false;
    action.value = null;
  }
}
</script>

<style scoped lang="scss">
.card-block {
  border: 1px solid #e5e5e5;
  border-radius: 8px;
  padding: 12px;
  margin-bottom: 18px;
  overflow: hidden;
}

.card-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 10px;

  .actions {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    justify-content: flex-end;

    .stack-actions {
      display: flex;
      gap: 6px;
      flex-wrap: wrap;
    }
  }
}

.wide-table {
  table-layout: fixed;
}

.wide-table th:nth-child(1),
.wide-table td:nth-child(1),
.wide-table th:nth-child(2),
.wide-table td:nth-child(2) {
  width: 40%;
}

.wide-table code {
  word-break: break-all;
  white-space: normal;
  display: inline-block;
}

:deep(.modal-content) {
  width: 95vw;
  max-width: 2200px;
  min-width: 320px;
}

:deep(.modal-content .box) {
  width: 100%;
  min-height: 70vh;
}
</style>
