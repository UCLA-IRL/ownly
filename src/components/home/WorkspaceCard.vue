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

      <div class="content">
        <div class="field mb-3">
          <div class="control">
            <div class="field is-grouped is-grouped-left">
              <div class="control">
                <div class="toggle-switch" @click="toggleIgnoreValidity">
                  <input type="checkbox" :checked="ignoreValidity" class="toggle-input" />
                  <span class="toggle-slider" :class="{ active: ignoreValidity }"></span>
                </div>
              </div>
              <div class="control">
                <span class="is-size-7">{{ ignoreValidity ? 'Magic ON' : 'Magic OFF' }}</span>
              </div>
            </div>
          </div>
        </div>

        <div class="has-text-right">
          <button class="button is-primary mr-2 is-small-caps soft-if-dark" @click="launch">
            Launch Workspace
          </button>
        </div>
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
import { onMounted, ref, useTemplateRef } from 'vue';

import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome';
import { faEllipsisV } from '@fortawesome/free-solid-svg-icons';

import DropdownMenu from '@/components/DropdownMenu.vue';
import LeaveWorkspaceModal from '@/components/home/LeaveWorkspaceModal.vue';

import * as utils from '@/utils';

import type { PropType } from 'vue';
import type { IWkspStats } from '@/services/types';

const showLeave = ref(false);
const ignoreValidity = ref(false); // Default to off

const props = defineProps({
  metadata: {
    type: Object as PropType<IWkspStats>,
    required: true,
  },
});

const emit = defineEmits(['open', 'leave']);

const avatar = ref<string>(utils.makeAvatar(props.metadata.name ?? 'wksp', 'shapes'));
const wkspMenu = useTemplateRef('wkspMenu');

onMounted(() => {
  try {
    // Initialize the toggle with the stored preference
    ignoreValidity.value = props.metadata.ignore ?? false;
    console.log('WorkspaceCard mounted:', props.metadata.name, 'ignore:', props.metadata.ignore);
  } catch (err) {
    console.error('Error mounting WorkspaceCard:', err);
    ignoreValidity.value = false;
  }
});

function toggleIgnoreValidity() {
  ignoreValidity.value = !ignoreValidity.value;
  updateIgnorePreference();
}

async function updateIgnorePreference() {
  // Update the ignore preference in metadata when toggle changes
  try {
    const metadata = { ...props.metadata, ignore: ignoreValidity.value };
    await _o.stats.put(metadata.name, metadata);
  } catch (err) {
    console.error('Failed to update ignore preference:', err);
  }
}

async function launch() {
  // Update metadata with current ignore preference before launching
  try {
    const metadata = { ...props.metadata, ignore: ignoreValidity.value };
    await _o.stats.put(metadata.name, metadata);
  } catch (err) {
    console.error('Failed to update ignore preference before launch:', err);
  }

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

.toggle-switch {
  position: relative;
  display: inline-block;
  width: 48px;
  height: 24px;
  cursor: pointer;
}

.toggle-input {
  opacity: 0;
  width: 0;
  height: 0;
}

.toggle-slider {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background-color: #ccc;
  border-radius: 24px;
  transition: 0.3s;

  &:before {
    position: absolute;
    content: "";
    height: 18px;
    width: 18px;
    left: 3px;
    bottom: 3px;
    background-color: white;
    border-radius: 50%;
    transition: 0.3s;
  }

  &.active {
    background-color: #2196f3;

    &:before {
      transform: translateX(24px);
    }
  }
}

.toggle-switch:hover .toggle-slider {
  background-color: #b3b3b3;

  &.active {
    background-color: #1976d2;
  }
}
</style>
