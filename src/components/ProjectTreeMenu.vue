<template>
  <button
    ref="button"
    class="button"
    @click.stop.prevent="openFromButton"
    title="Open actions menu"
  >
    <FontAwesomeIcon :icon="faEllipsisVertical" size="xs" />

    <DropdownMenu ref="dropdown">
      <a class="dropdown-item" v-if="allowNew" @click="emit('new-folder')"> New folder </a>
      <a class="dropdown-item" v-if="allowNew" @click="emit('new-file', '')"> New blank file </a>
      <a class="dropdown-item" v-if="allowNew" @click="emit('import')"> Import files </a>
      <a class="dropdown-item" v-if="allowNew" @click="emit('import-zip')"> Import ZIP folder </a>

      <hr class="dropdown-divider" v-if="allowNew" />
      <a class="dropdown-item" v-if="allowNew" @click="emit('new-file', 'mdoc')"> New RichDoc </a>
      <a class="dropdown-item" v-if="allowNew" @click="emit('new-file', 'md')">
        New Markdown file
      </a>
      <a class="dropdown-item" v-if="allowNew" @click="emit('new-file', 'tex')"> New LaTeX file </a>
      <a class="dropdown-item" v-if="allowNew" @click="emit('new-file', 'excalidraw')">
        New Excalidraw file
      </a>
      <hr class="dropdown-divider" v-if="allowNew" />

      <a class="dropdown-item" @click="emit('export')"> Export </a>
      <a class="dropdown-item" v-if="allowRename" @click="emit('rename')"> Rename </a>
      <a class="dropdown-item" v-if="allowDelete" @click="emit('delete')"> Delete </a>
    </DropdownMenu>
  </button>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, useTemplateRef } from 'vue';

import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome';
import { faEllipsisVertical } from '@fortawesome/free-solid-svg-icons';
import DropdownMenu from './DropdownMenu.vue';

defineProps({
  allowNew: Boolean,
  allowDelete: Boolean,
  allowRename: Boolean,
});

const emit = defineEmits<{
  (e: 'new-folder'): void;
  (e: 'new-file', ext: string): void;
  (e: 'import'): void;
  (e: 'import-zip'): void;
  (e: 'export'): void;
  (e: 'rename'): void;
  (e: 'delete'): void;
}>();

const button = useTemplateRef('button');
const dropdown = useTemplateRef('dropdown');

function openFromButton() {
  if (!button.value) return;
  dropdown.value?.open(button.value);
}

function openFromContext(event: Event) {
  const mouseEvent = event as MouseEvent;
  mouseEvent.stopPropagation();
  mouseEvent.preventDefault();

  const anchor = button.value?.parentElement ?? button.value;
  if (!anchor) return;
  dropdown.value?.open(anchor);
}

// This is a bit ugly, but simplifies the parent component
// Attach to the parent's context menu event here and trigger the dropdown
onMounted(() => {
  button.value?.parentElement?.addEventListener('contextmenu', openFromContext);
});

onBeforeUnmount(() => {
  button.value?.parentElement?.removeEventListener('contextmenu', openFromContext);
});
</script>

<style lang="css" scoped></style>
