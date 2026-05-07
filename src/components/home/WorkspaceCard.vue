<template>
  <div class="workspace card">
    <div class="card-content">
      <div class="media">
        <div class="media-left">
          <figure class="image is-64x64 mr-1">
            <img :src="avatar" alt="Placeholder image" />
          </figure>
        </div>

        <div class="media-content">
          <p class="title is-4 has-text-weight-semibold">{{ metadata.label }}</p>
          <p class="subtitle is-6 mt-1">{{ metadata.name }}</p>
        </div>

        <button
          ref="button"
          class="button circle-button"
          @click.stop.prevent="wkspMenu?.open($event)"
        >
          <FontAwesomeIcon :icon="faEllipsisV" size="sm" />
          <DropdownMenu ref="wkspMenu">
            <a class="dropdown-item" @click="showLeave = true"> Leave </a>
          </DropdownMenu>
        </button>
      </div>

      <div class="content has-text-right">
        <button class="button is-primary mr-2 is-small-caps soft-if-dark" @click="launch">
          Launch Workspace
        </button>
      </div>
    </div>

    <LeaveWorkspaceModal
      :show="showLeave"
      :target="metadata.name"
      @close="showLeave = false"
      @leave="emit('leave')"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, useTemplateRef } from 'vue';

import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome';
import { faEllipsisV } from '@fortawesome/free-solid-svg-icons';

import DropdownMenu from '@/components/DropdownMenu.vue';
import LeaveWorkspaceModal from '@/components/home/LeaveWorkspaceModal.vue';

import * as utils from '@/utils';

import type { PropType } from 'vue';
import type { IWkspStats } from '@/services/types';

const showLeave = ref(false);

const props = defineProps({
  metadata: {
    type: Object as PropType<IWkspStats>,
    required: true,
  },
});

const emit = defineEmits(['open', 'leave']);

const avatar = ref<string>(utils.makeAvatar(props.metadata.name ?? 'wksp', 'shapes'));
const wkspMenu = useTemplateRef('wkspMenu');

function launch() {
  emit('open');

  // Take this chance to request persistent storage
  navigator.storage?.persist?.().then((persisted) => {
    console.log(`Storage persistance state: ${persisted}`);
  });
}
</script>

<style scoped lang="scss">
.card,
.card-content,
.media-content {
  overflow: hidden;
  box-shadow: none;
}

.media img {
  border-radius: 4px;
}
</style>
