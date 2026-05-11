<template>
  <Transition name="fade-2" mode="out-in">
    <!-- This view will set up WASM and make sure we have a key -->
    <LandingView v-if="showLogin" @login="showLogin = false" />

    <!-- This view will show the main app -->
    <main v-else-if="!showLogin" class="router-view">
      <!-- Main navigation menu ("bar" for historical reasons) -->
      <NavBar class="nav-bar" :class="{ show: showNav }"></NavBar>

      <!-- Backdrop for navigation on mobile -->
      <div v-if="showNav" class="nav-backdrop" @click="showNav = false"></div>

      <div class="router-view-inner">
        <!-- Top bar for mobile -->
        <div class="top-bar has-background-primary soft-if-dark">
          <button class="button is-primary soft-if-dark" @click="showNav = true">
            <FontAwesomeIcon :icon="faBars" />
          </button>
          <img alt="logo" class="logo" src="@/assets/logo-white.svg" />
        </div>

        <!-- Main content -->
        <RouterView v-slot="{ Component }">
          <Transition name="fade-2" mode="out-in">
            <component class="router-view-content" :is="Component" />
          </Transition>
        </RouterView>
      </div>
    </main>
  </Transition>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue';
import { RouterView, useRoute, useRouter } from 'vue-router';

import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome';
import { faBars } from '@fortawesome/free-solid-svg-icons';

import NavBar from '@/components/NavBar.vue';
import LandingView from '@/views/LandingView.vue';

import { GlobalBus } from '@/services/event-bus';
import { Toast } from '@/utils/toast';

import { marked } from 'marked';
import { markedHighlight } from 'marked-highlight';
import hljs from 'highlight.js';

// Global setup for marked
marked.use(
  { gfm: true },
  markedHighlight({
    emptyLangClass: 'hljs',
    langPrefix: 'hljs language-',
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    highlight(code, lang, _info) {
      const language = hljs.getLanguage(lang) ? lang : 'plaintext';
      return hljs.highlight(code, { language }).value;
    },
  }),
);

const route = useRoute();
const router = useRouter();
const showLogin = ref(true);
const showNav = ref(false);

watch(
  () => route.path,
  () => {
    // When activating the project, we don't display anything useful.
    // In that case keep the nav open.
    if (route.name === 'project') return;

    // Close the nav when changing routes
    showNav.value = false;
  },
);

onMounted(() => {
  GlobalBus.addListener('wksp-error', wkspErrorListener);
  GlobalBus.addListener('workspace-revoked', workspaceRevokedListener);
});

onUnmounted(() => {
  GlobalBus.removeListener('wksp-error', wkspErrorListener);
  GlobalBus.removeListener('workspace-revoked', workspaceRevokedListener);
});

function wkspErrorListener(error: Error) {
  Toast.error(error.toString());
}

function workspaceRevokedListener(workspace: string, error: Error) {
  void (async () => {
    if (globalThis.ActiveWorkspace?.metadata.name === workspace) {
      try {
        await globalThis.ActiveWorkspace.destroy();
      } catch (e) {
        console.error(e);
        error = new Error(`${error}\nFailed to stop revoked workspace: ${e}`);
      } finally {
        globalThis.ActiveWorkspace = null;
      }
    }

    if (router.currentRoute.value.path !== '/') {
      await router.push('/');
    }

    Toast.error(error.toString());
  })();
}
</script>

<style scoped lang="scss">
main.router-view {
  display: flex;
  flex-direction: row;
  width: 100vw;
  height: 100dvh;
  overflow-x: hidden;

  > .router-view-inner {
    flex: 1;
    overflow: hidden;

    .router-view-content {
      height: 100dvh;
    }
  }
}

/** Hide elements only for mobile */
.top-bar,
.nav-backdrop {
  display: none;
}

/** Mobile styling - hide the navbar and show with button */
@media (max-width: 1023px) {
  .nav-bar {
    position: fixed;
    z-index: calc(var(--z-navbar) + 10);
    will-change: transform;
    transform: translateX(-100%);
    transition: transform 0.2s ease-in-out;
    touch-action: manipulation;

    &.show {
      transform: translateX(0);
    }
  }

  .nav-backdrop {
    display: block;
    position: fixed;
    top: 0;
    left: 0;
    width: 100vw;
    height: 100dvh;
    background-color: rgba(0, 0, 0, 0.5);
    z-index: var(--z-navbar);
    touch-action: manipulation;
  }

  main.router-view > .router-view-inner {
    $top-bar-height: 44px;
    display: block;
    flex-direction: column;

    .top-bar {
      height: $top-bar-height;
      display: block;
      padding: 6px 4px 2px 4px;
      touch-action: manipulation;
      .logo {
        height: 26px;
        margin-left: 2px;
        margin-top: 5px;
      }
    }

    .router-view-content {
      height: calc(100dvh - #{$top-bar-height});
    }
  }
}
</style>
