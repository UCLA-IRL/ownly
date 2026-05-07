<template>
  <ModalComponent :show="show" :loading="loading" @close="close">
    <div class="title is-5 mb-4">Create Workspace</div>

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
      <label class="label">Name Identifier</label>
      <div class="control">
        <input class="input" type="text" placeholder="marketing-team" v-model="opts.name" />
      </div>
      <p class="help">
        A unique identifier for the workspace, without spaces or special characters
      </p>
    </div>

    <div>
      Your workspace will have the network name &ndash;<br />
      <code>{{ fullName }}</code>
    </div>

    <div class="field mt-2 has-text-right">
      <div class="control">
        <button class="button is-light mr-2" :disabled="loading" @click="close">Cancel</button>
        <button class="button is-primary" :disabled="loading" @click="create">Create</button>
      </div>
    </div>
  </ModalComponent>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';

import ModalComponent from '@/components/ModalComponent.vue';

import ndn from '@/services/ndn';
import { Workspace } from '@/services/workspace';
import { Toast } from '@/utils/toast';

defineProps({
  show: {
    type: Boolean,
    required: true,
  },
});

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'create', name: string): void;
}>();

const loading = ref(false);
const idName = ref(String());
const opts = ref({
  label: String(),
  name: String(),
});

// Name we intend to give the workspace
const fullName = computed(() => `${idName.value}/${opts.value.name.trim()}`);

// No need to reset these values on show
onMounted(async () => {
  idName.value = await ndn.api.get_identity_name();
});

async function create() {
  try {
    loading.value = true;

    // Validate inputs
    const label = opts.value.label.trim();
    const name = opts.value.name.trim();
    if (!label || !name) {
      throw new Error('Please fill in all the fields');
    }
    if (!/^[a-z0-9-_]+$/.test(name)) {
      throw new Error('Name identifier contains invalid characters');
    }

    // Join the workspace with attempt to create
    const finalName = await Workspace.join(label, fullName.value, true, null);

    emit('create', finalName);
    emit('close');

    Toast.success('Created workspace successfully!');
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
