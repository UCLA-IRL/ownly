<template>
  <Teleport to="body" v-if="opened">
    <div class="dropdown-backdrop" @click.stop.prevent="close" @contextmenu.stop.prevent="close">
      <div
        class="dropdown is-active"
        :class="{ 'is-positioning': !positioned }"
        :style="{ top: `${coords.y}px`, left: `${coords.x}px` }"
      >
        <div ref="menuRef" class="dropdown-menu" role="menu">
          <div class="dropdown-content">
            <slot></slot>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { nextTick, ref } from 'vue';

const emit = defineEmits(['close']);
defineExpose({ open });

const coords = ref({ x: 0, y: 0 });
const opened = ref(false);
const menuRef = ref<HTMLElement | null>(null);
const positioned = ref(false);

function open(anchor: MouseEvent | HTMLElement) {
  let x = 200;
  let y = 0;

  if (anchor instanceof MouseEvent) {
    anchor.stopPropagation();
    anchor.preventDefault();

    const el = (anchor.currentTarget as HTMLElement | null) ?? null;
    if (el) {
      const rect = el.getBoundingClientRect();
      x = rect.right + 6;
      y = rect.top;
    } else {
      x = Math.max(anchor.clientX + 6, 200);
      y = anchor.clientY;
    }
  } else {
    const rect = anchor.getBoundingClientRect();
    x = rect.right + 6;
    y = rect.top;
  }

  coords.value = { x, y };
  positioned.value = false;
  opened.value = true;

  void nextTick(() => {
    const menu = menuRef.value;
    if (!menu) {
      coords.value = { x, y };
      positioned.value = true;
      return;
    }

    const padding = 8;
    const menuRect = menu.getBoundingClientRect();
    const maxX = Math.max(padding, window.innerWidth - menuRect.width - padding);
    const maxY = Math.max(padding, window.innerHeight - menuRect.height - padding);

    coords.value = {
      x: Math.min(Math.max(x, padding), maxX),
      y: Math.min(Math.max(y, padding), maxY),
    };
    positioned.value = true;
  });
}

function close() {
  opened.value = false;
  positioned.value = false;
  emit('close');
}
</script>

<style lang="scss" scoped>
.dropdown-backdrop {
  position: fixed;
  top: 0;
  left: 0;
  height: 100vh;
  width: 100vw;
  z-index: var(--z-dropdown);

  .dropdown {
    z-index: calc(var(--z-dropdown) + 1);

    &.is-positioning {
      visibility: hidden;
    }
  }

  .dropdown-content {
    user-select: none;
    transform: none;
    box-shadow: 0px 0px 7px 2px rgba(0, 0, 0, 0.5);
  }
}
</style>
