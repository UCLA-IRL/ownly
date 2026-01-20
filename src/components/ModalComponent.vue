<template>
  <Teleport to="body">
    <Transition name="fade-2">
      <div class="modal is-active anim-fade" v-if="show || loading">
        <div class="modal-background"></div>
        <div class="modal-content" :class="contentClass">
          <LoadingSpinner v-if="loading" class="fixed-center" />

          <div class="box" v-if="show">
            <slot></slot>
          </div>
        </div>

        <button class="modal-close is-large" aria-label="close" @click="emit('close')"></button>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { nextTick, watch } from 'vue';

import LoadingSpinner from '@/components/LoadingSpinner.vue';

const props = defineProps({
  show: {
    type: Boolean,
    required: true,
  },
  loading: {
    type: Boolean,
    default: false,
  },
  contentClass: {
    type: [String, Array, Object],
    default: undefined,
  },
});

const emit = defineEmits(['close']);

watch(
  () => props.show,
  async (show) => {
    if (show) {
      await nextTick();

      // Find autofocus element and focus it
      const elem = document.querySelector<HTMLInputElement>('.modal-content input[autofocus]');
      elem?.focus();
    }
  },
);
</script>

<style scoped lang="scss">
.modal-content {
  padding: 20px;
}
.modal {
  --bulma-modal-z: var(--z-modal);
}
</style>
