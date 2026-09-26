<template>
  <ModalComponent :show="show" content-class="workspace-members-modal" @close="emit('close')">
    <div class="title is-5 mb-1">Workspace members</div>
    <p class="mb-4">
      Members of the active workspace are listed below. A workspace key (wkspKey)
      can be revoked to drop the member's Sync publications; the underlying identity
      key (idKey) is never revoked by the application. To remove a member from MLS,
      use the People &amp; access panel.
    </p>

    <div v-if="loading" class="has-text-centered my-4">
      <LoadingSpinner text="Loading revocations ..." />
    </div>

    <div v-else>
      <article class="card-block">
        <header class="card-head">
          <div>
            <p class="is-size-6 has-text-weight-semibold">Revoked workspace keys</p>
            <p class="is-size-7">
              {{ revocations.length === 0
                ? 'No revoked wkspKeys are stored locally.'
                : `${revocations.length} wkspKey${revocations.length === 1 ? '' : 's'} revoked.` }}
            </p>
          </div>
        </header>

        <table v-if="revocations.length" class="table is-fullwidth is-hoverable wide-table">
          <thead>
            <tr>
              <th>Workspace key name</th>
              <th>Reason</th>
              <th>Invalidity</th>
              <th>Cert hash</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="rec in revocations" :key="rec.certName">
              <td>
                <code :title="rec.certName || 'unknown'">
                  {{ shortName(rec.certName) || '(unknown cert)' }}
                </code>
              </td>
              <td>{{ reasonLabel(rec.reason) }}</td>
              <td>{{ formatInvalidity(rec.invalidityTime) }}</td>
              <td><code class="hash-cell" :title="rec.certHash">{{ shortHash(rec.certHash) }}</code></td>
            </tr>
          </tbody>
        </table>
        <p v-else class="has-text-grey">No revocations yet.</p>
      </article>

      <article v-if="canRevoke" class="card-block">
        <header class="card-head">
          <div>
            <p class="is-size-6 has-text-weight-semibold">Revoke a workspace key</p>
            <p class="is-size-7">
              Paste the full NDN name of the wkspKey to revoke. The name must contain
              <code>/wksp/</code> and <code>/KEY/</code>; identity keys cannot be
              revoked from here.
            </p>
          </div>
        </header>

        <div class="field">
          <label class="label is-small">wkspKey name</label>
          <div class="control">
            <input
              ref="certNameInput"
              v-model="certNameInput"
              class="input"
              type="text"
              placeholder="/alice@example.com/wksp/alice@example.com/KEY/&lt;kid&gt;/anchor/v=1"
              :disabled="busy"
            />
          </div>
        </div>

        <div class="field">
          <label class="label is-small">Reason</label>
          <div class="control">
            <div class="select is-small">
              <select v-model.number="reasonInput" :disabled="busy">
                <option :value="ReasonCode.PrivilegeWithdrawn">Revoked by owner</option>
                <option :value="ReasonCode.KeyCompromise">Key compromise</option>
                <option :value="ReasonCode.CessationOfOperation">Cessation of operation</option>
                <option :value="ReasonCode.Unspecified">Unspecified</option>
              </select>
            </div>
          </div>
        </div>

        <div class="field is-grouped is-grouped-right">
          <p class="control">
            <button
              class="button is-warning"
              :class="{ 'is-loading': busy }"
              :disabled="!canSubmit || busy"
              @click="onRevoke"
            >
              Publish revocation
            </button>
          </p>
        </div>
        <p v-if="revokeError" class="help is-danger mt-2">{{ revokeError }}</p>
      </article>

      <article v-else class="card-block">
        <p class="has-text-grey is-size-7">
          You must be the master device of this workspace to publish revocations.
        </p>
      </article>
    </div>
  </ModalComponent>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';

import LoadingSpinner from '@/components/LoadingSpinner.vue';
import ModalComponent from '@/components/ModalComponent.vue';
import ndn from '@/services/ndn';
import {
  ReasonCode,
  recordRevocation,
  reasonLabel,
  registerOnCertRevoked,
  type ReasonCodeValue,
  type RevocationRecord,
} from '@/services/revocation';
import { Toast } from '@/utils/toast';

const props = defineProps({
  show: {
    type: Boolean,
    required: true,
  },
});

const emit = defineEmits(['close']);

const loading = ref(true);
const busy = ref(false);
const revokeError = ref('');
const certNameInput = ref('');
const reasonInput = ref<number>(ReasonCode.PrivilegeWithdrawn);
const isMasterDevice = computed(
  () => !!globalThis.ActiveWorkspace?.invite?.isMasterDevice(),
);

const revocations = ref<RevocationRecord[]>([]);

// The callback delivers new records; list_revocations rebuilds the view from
// ndnd's durable keychain state.
async function rehydrate() {
  try {
    const existing = await ndn.api.list_revocations();
    const out: RevocationRecord[] = [];
    for (const r of existing) {
      if (!r.cert_name && !r.cert_hash) continue;
      const rec: RevocationRecord = {
        reason: r.reason as ReasonCodeValue,
        invalidityTime: r.invalidity_time,
        certHash: r.cert_hash,
        certName: r.cert_name,
      };
      recordRevocation(rec);
      out.push(rec);
    }
    revocations.value = out;
  } catch (err) {
    console.error('Failed to load revocations', err);
  }
}

const unregisterCertRevoked = registerOnCertRevoked((rec) => {
  recordRevocation(rec);
  rehydrate();
});

watch(
  () => props.show,
  async (open) => {
    if (open) {
      loading.value = true;
      await rehydrate();
      loading.value = false;
    } else {
      revokeError.value = '';
      certNameInput.value = '';
    }
  },
);

onUnmounted(() => {
  unregisterCertRevoked();
});

const canRevoke = computed(() => isMasterDevice.value);

const canSubmit = computed(() => {
  const name = certNameInput.value.trim();
  if (!name) return false;
  if (!name.includes('/wksp/') || !name.includes('/KEY/')) return false;
  return true;
});

function shortName(name: string): string {
  if (!name) return '';
  if (name.length <= 56) return name;
  return name.slice(0, 28) + '...' + name.slice(-24);
}

function shortHash(hash: string): string {
  if (!hash) return '';
  if (hash.length <= 16) return hash;
  return hash.slice(0, 8) + '...' + hash.slice(-8);
}

function formatInvalidity(t: number): string {
  if (!t) return 'now';
  const d = new Date(t);
  if (Number.isNaN(d.getTime())) return 'now';
  return d.toISOString().slice(0, 10);
}

async function onRevoke() {
  const name = certNameInput.value.trim();
  if (!name) return;
  if (!confirm(`Publish a revocation for ${name}? Peers in the workspace will drop future Sync publications from this wkspKey.`)) {
    return;
  }
  busy.value = true;
  revokeError.value = '';
  try {
    await ndn.api.revoke_cert(name, reasonInput.value, 0);
    Toast.success(`Revocation published for ${shortName(name)}`);
    certNameInput.value = '';
    await rehydrate();
  } catch (err) {
    revokeError.value = String((err as Error)?.message ?? err);
    Toast.error(`Failed to revoke: ${revokeError.value}`);
  } finally {
    busy.value = false;
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
  font-size: 1.05rem;
}

.card-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}

.wide-table {
  table-layout: auto;
}

.wide-table code {
  word-break: break-all;
  white-space: normal;
  display: inline-block;
}

.wide-table th {
  text-align: center !important;
}

.hash-cell {
  font-size: 0.85em;
  color: #4a4a4a;
}

:global(.workspace-members-modal) {
  width: 100vw;
  max-width: 1600px;
  min-width: 720px;
}

:global(.workspace-members-modal .box) {
  width: 100%;
  min-height: 50vh;
}
</style>
