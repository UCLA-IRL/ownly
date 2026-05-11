<template>
  <ModalComponent :show="show" :loading="loading" @close="close">
    <div class="title is-5 mb-4">Leave Workspace</div>
    <div class="field">
      <p>
        You are about to leave the workspace "{{ target }}" from your dashboard. This is a local
        action, your data will not be deleted.
      </p>
    </div>

    <div class="field has-text-right">
      <div class="control">
        <button class="button is-light mr-2" @click="close">Cancel</button>
        <button class="button is-warning" @click="leave">Leave</button>
      </div>
    </div>
  </ModalComponent>
</template>

<script setup lang="ts">
import { ref } from 'vue';

import ModalComponent from '../ModalComponent.vue';

import * as utils from '@/utils';
import { Toast } from '@/utils/toast';

const props = defineProps({
  show: {
    type: Boolean,
    required: true,
  },
  target: {
    type: String,
    required: true,
  },
});

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'leave'): void;
}>();

const loading = ref(false);

async function leave() {
  try {
    loading.value = true;

    // Close workspace if active
    if (globalThis.ActiveWorkspace?.metadata.name === props.target) {
      await globalThis.ActiveWorkspace.destroy();
      globalThis.ActiveWorkspace = null;
    }

    // Remove from main list
    await _o.stats.del(props.target);

    // Remove project databases
    const slug = utils.escapeUrlName(props.target);
    await _o.ProjDb.deleteWksp(slug);
    await _o.bootState?.del(`${props.target}/32=boot`);

    emit('leave');
    emit('close');

    Toast.info(`Left workspace ${props.target}`);
  } catch (err) {
    console.error(err);
    Toast.error(`Error leaving workspace ${props.target}: ${err}`);
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
