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

    <div class="field">
      <label class="label">NDN Name</label>
      <div class="control">
        <input class="input" type="text" placeholder="/my/awesome/workspace" v-model="opts.name" />
      </div>
      <p class="help">Unique network identifier of the workspace</p>
    </div>

    <div class="field">
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

watch(
  () => props.show,
  (show) => {
    if (!show) return;

    opts.value.label = String();
    opts.value.name = String();

    // Check if URL specifies a workspace
    if (route.name === 'join') {
      const space = route.params.space as string;
      opts.value.name = utils.unescapeUrlName(space || String());
      opts.value.label = (route.query.label as string) || String();
      opts.value.psk = (route.query.psk as string) || String();
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
    if (!label || !name || !psk) {
      throw new Error('Please fill in all the fields');
    }

    // Join the workspace without attempting create
    const pskBuf = utils.fromHex(psk);
    const finalName = await Workspace.join(label, name, false, false, pskBuf);

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

<style scoped lang="scss"></style>
