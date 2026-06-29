<template>
  <ModalComponent :show="show" :loading="loading" @close="close">
    <div class="title is-5 mb-4">Join Workspace</div>

    <div class="field">
      <label class="label">Dashboard Label</label>
      <div class="control">
        <input
          class="input"
          type="text"
          placeholder="Marketing Team"
          v-model="opts.label"
          autofocus
        />
      </div>
      <p class="help">A readable label for the workspace on your dashboard</p>
    </div>

    <div v-if="fastJoin" class="fast-join-summary mb-4">
      <div class="fast-join-row">
        <span>Joining as</span>
        <code>{{ fastJoin.inviteeIdentity }}</code>
      </div>
    </div>

    <div v-else class="field">
      <label class="label">NDN Name</label>
      <div class="control">
        <input class="input" type="text" placeholder="/my/awesome/workspace" v-model="opts.name" />
      </div>
      <p class="help">Unique network identifier of the workspace</p>
    </div>

    <div v-if="!fastJoin" class="field">
      <label class="label">Pre-Shared Key</label>
      <div class="control">
        <input class="input" type="text" placeholder="..." v-model="opts.psk" />
      </div>
      <p class="help">Ask the owner of the workspace for the key</p>
    </div>

    <div class="field has-text-right">
      <div class="control">
        <button class="button is-light mr-2" :disabled="loading" @click="close">Cancel</button>
        <button class="button is-primary" :disabled="loading" @click="join">Join</button>
      </div>
    </div>
  </ModalComponent>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import { useRoute } from 'vue-router';

import ModalComponent from '@/components/ModalComponent.vue';

import * as utils from '@/utils';
import { Toast } from '@/utils/toast';
import { Workspace } from '@/services/workspace';
import ndn from '@/services/ndn';
import { parseFastJoinBundle, type FastJoinBundle } from '@/services/fast-join';

const props = defineProps({
  show: {
    type: Boolean,
    required: true,
  },
});

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'join', name: string): void;
}>();

const route = useRoute();

const loading = ref(false);

const opts = ref({
  label: String(),
  name: String(),
  psk: String(),
});
const fastJoin = ref<FastJoinBundle | null>(null);

watch(
  () => props.show,
  (show) => {
    if (!show) return;

    opts.value.label = String();
    opts.value.name = String();
    opts.value.psk = String();
    fastJoin.value = null;

    // Check if URL specifies a workspace
    if (route.name === 'join') {
      const space = route.params.space as string;
      opts.value.name = utils.unescapeUrlName(space || String());
      opts.value.label = (route.query.label as string) || String();
      opts.value.psk = (route.query.psk as string) || String();
      const fast = route.query.fj ?? new URLSearchParams(route.hash.slice(1)).get('fj');
      if (typeof fast === 'string' && fast) {
        fastJoin.value = parseFastJoinBundle(fast);
        opts.value.name = fastJoin.value.wksp;
        opts.value.label = fastJoin.value.label;
        opts.value.psk = fastJoin.value.psk;
      }
    }
  },
);

async function join() {
  try {
    loading.value = true;

    // Validate the inputs
    const label = opts.value.label.trim();
    const name = opts.value.name.trim();
    const psk = opts.value.psk.trim();
    if (!label || !name || (!fastJoin.value && !psk)) {
      throw new Error('Please fill in all the fields');
    }
    if (fastJoin.value && !psk) {
      throw new Error('Fast join link is missing the workspace key');
    }

    if (fastJoin.value) {
      // Fast join requires the participant to already have a self-signed
      // IDCERT — the owner will receive it via the BootJoin payload and SVS
      // boot group so it surfaces in their Authenticated Peers UI. If the
      // user has no IDCERT yet, prompt them to generate one before joining.
      const overview = await ndn.api.list_identity_keys();
      if (!overview.local?.length) {
        const confirmed = window.confirm(
          'You need an Identity Key before joining a fast-join workspace. ' +
            'Generate one now?',
        );
        if (!confirmed) {
          throw new Error('Identity Key required to join via fast-join link');
        }
        await ndn.api.generate_identity_key();
      }

      await ndn.api.import_fast_join_identity(
        fastJoin.value.ephemeralSecret,
        fastJoin.value.ephemeralCert,
        fastJoin.value.ownerCert,
      );
    }

    // Join the workspace without attempting create
    const pskBuf = utils.fromHex(psk);
    const finalName = await Workspace.join(
      label,
      name,
      false,
      false,
      pskBuf,
      null,
    );

    emit('join', finalName);
    emit('close');

    Toast.success('Joined workspace successfully!');
  } catch (err) {
    console.error(err);
    Toast.error(`${err}`);
  } finally {
    loading.value = false;
  }
}

function close() {
  loading.value = false;
  emit('close');
}
</script>

<style scoped lang="scss">
.fast-join-summary {
  border: 1px solid rgba(127, 127, 127, 0.24);
  border-radius: 6px;
  padding: 0.75rem 0.875rem;
}

.fast-join-row {
  display: grid;
  grid-template-columns: minmax(6rem, 8rem) minmax(0, 1fr);
  gap: 0.75rem;
  align-items: baseline;

  span {
    color: var(--bulma-text-weak, #888);
    font-size: 0.875rem;
  }

  code {
    overflow-wrap: anywhere;
    white-space: normal;
  }
}
</style>