<template>
  <aside
    ref="navRoot"
    :class="['menu', 'main-nav', 'has-background-primary', 'soft-if-dark', { resizing: isResizing }]"
    :style="sidebarStyle"
  >
    <div class="top-sheet">
      <div class="logo-row">
        <router-link to="/" v-slot="{ navigate }">
          <img alt="logo" class="logo" src="@/assets/logo-white.svg" @click="navigate" />
        </router-link>
        <label
          class="theme-switch"
          :aria-label="`Switch to ${effectiveTheme === 'dark' ? 'light' : 'dark'} mode`"
          :title="`Switch to ${effectiveTheme === 'dark' ? 'light' : 'dark'} mode`"
        >
          <input type="checkbox" :checked="effectiveTheme === 'dark'" @change="toggleTheme" />
          <span class="track">
            <FontAwesomeIcon class="track-icon sun" :icon="faSun" />
            <FontAwesomeIcon class="track-icon moon" :icon="faMoon" />
            <span class="knob" />
          </span>
        </label>
      </div>

      <!-- non-workspace general routes -->
      <template v-if="routeIsDashboard">
        <p class="menu-label">General</p>
        <ul class="menu-list">
          <li>
            <router-link to="/">
              <FontAwesomeIcon class="mr-1" :icon="faTableCells" size="sm" />
              Dashboard</router-link
            >
          </li>
        </ul>

        <p class="menu-label">About</p>
        <ul class="menu-list">
          <li>
            <router-link to="/about">
              <FontAwesomeIcon class="mr-1" :icon="faCircleInfo" size="sm" />
              About</router-link
            >
          </li>
          <li>
            <a href="https://github.com/pulsejet/ownly" target="_blank">
              <FontAwesomeIcon class="mr-1" :icon="faGithub" size="sm" />
              GitHub
            </a>
          </li>
        </ul>
      </template>

      <template v-if="routeIsWorkspace">
        <p class="menu-label">Projects</p>
        <ul class="menu-list project-list">
          <li v-for="proj in projects" :key="proj.uuid" class="project-item">
            <router-link :to="linkProject(proj)" class="project-link">
              <div class="link-inner project-link-inner">
                <span class="project-icon-shell">
                  <FontAwesomeIcon :icon="faLayerGroup" size="sm" />
                </span>
                <span class="project-name">{{ proj.name }}</span>
              </div>

              <ProjectTreeMenu
                v-if="activeProjectName === proj.uuid"
                class="link-button"
                :allow-new="true"
                :allow-delete="false"
                @new-file="projectTree?.[0]?.newHere('file', $event)"
                @new-folder="projectTree?.[0]?.newHere('folder')"
                @import="projectTree?.[0]?.importHere()"
                @import-zip="projectTree?.[0]?.importZipHere()"
                @export="projectTree?.[0]?.executeExport(null)"
              />
            </router-link>

            <ProjectTree
              v-if="activeProjectName === proj.uuid"
              class="outermost"
              ref="projectTree"
              :project="proj"
              :files="projectFiles"
            />
          </li>
        </ul>

        <ul class="menu-list project-actions">
          <li>
            <a @click="showProjectModal = true">
              <FontAwesomeIcon class="mr-1" :icon="faPlus" size="sm" />
              Add project
            </a>
          </li>
        </ul>

        <p class="menu-label">Discussion</p>
        <ul class="menu-list">
          <li v-for="chan in chatChannels" :key="chan.uuid">
            <router-link :to="linkDiscuss(chan)">
              <FontAwesomeIcon class="mr-1" :icon="faHashtag" size="sm" />
              {{ chan.name }}
            </router-link>
          </li>
          <li>
            <a @click="showChannelModal = true">
              <FontAwesomeIcon class="mr-1" :icon="faPlus" size="sm" />
              Add channel
            </a>
          </li>
        </ul>

        <p class="menu-label">AI Agents</p>
        <ul class="menu-list">
          <li>
            <a @click="showAgentModal = true">
              <FontAwesomeIcon class="mr-1" :icon="faRobot" size="sm" />
              Manage agents
            </a>
          </li>
        </ul>

        <p class="menu-label">Workspace</p>
        <ul class="menu-list">
          <li>
            <a @click="showInviteModal = true">
              <FontAwesomeIcon class="mr-1" :icon="faPlus" size="sm" />
              Invite people

              <FontAwesomeIcon v-show="showNotifBubble" class="mr-1" :icon="faCircleExclamation" size="sm"></FontAwesomeIcon>
            </a>
          </li>
          <li>
            <a @click="showAdvancedSettings = !showAdvancedSettings">
              <FontAwesomeIcon class="mr-1" :icon="faGear" size="sm" />
              {{ showAdvancedSettings ? 'Hide advanced settings' : 'Advanced settings' }}
            </a>
          </li>
          <li v-if="showAdvancedSettings">
            <a :class="{ 'is-disabled': isRequestingSOS }" @click="sosRequest">
              <FontAwesomeIcon class="mr-1" :icon="faArrowsRotate" size="sm" />
              {{ isRequestingSOS ? 'Broadcasting SOS...' : 'SOS' }}
            </a>
          </li>
        </ul>
      </template>

      <p class="menu-label">v{{ buildVersion() }}</p>
    </div>

    <div class="bottom-sheet">
      <div class="id-share">
        <a @click="showIdentity = true">
          <FontAwesomeIcon class="mr-1" :icon="faQrcode" size="sm" />
          Share your Identity
        </a>
      </div>

      <div class="connection">
        <template v-if="connState.connected">
          <FontAwesomeIcon class="mr-1" :icon="faWifi" size="sm" />
          {{ connState.router }}
        </template>
        <template v-else>
          <FontAwesomeIcon class="mr-1" :icon="faGhost" size="sm" />
          Offline
        </template>
      </div>
    </div>

    <div v-if="canResizeSidebar" class="sidebar-resizer" @pointerdown.prevent="startSidebarResize"></div>

    <AddChannelModal :show="showChannelModal" @close="showChannelModal = false" />
    <AddProjectModal :show="showProjectModal" @close="showProjectModal = false" />
    <InvitePeopleModal :show="showInviteModal" @close="showInviteModal = false" />
    <QRIdentityModal :show="showIdentity" @close="showIdentity = false" />

    <ModalComponent :show="showAgentModal" @close="showAgentModal = false">
      <AgentBrowser />
    </ModalComponent>
  </aside>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, useTemplateRef } from 'vue';
import { useRoute } from 'vue-router';
//import { useRouter } from 'vue-router';
import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome';
import {
  faLayerGroup,
  faPlus,
  faHashtag,
  faWifi,
  faGhost,
  faTableCells,
  faQrcode,
  faCircleInfo,
  faRobot,
  faCircleExclamation,
  faArrowsRotate,
  faGear,
  faMoon,
  faSun,
} from '@fortawesome/free-solid-svg-icons';
import { faGithub } from '@fortawesome/free-brands-svg-icons';

import ProjectTree from './ProjectTree.vue';
import ProjectTreeMenu from './ProjectTreeMenu.vue';
import AddChannelModal from './AddChannelModal.vue';
import AddProjectModal from './AddProjectModal.vue';
import AgentBrowser from './AgentBrowser.vue';
import ModalComponent from './ModalComponent.vue';

import { GlobalBus } from '@/services/event-bus';
import { Toast } from '@/utils/toast';
//import { Workspace } from '@/services/workspace';

import type { IChatChannel, IProject, IProjectFile } from '@/services/types';
import InvitePeopleModal from './InvitePeopleModal.vue';
import QRIdentityModal from './QRIdentityModal.vue';

const route = useRoute();
//const router = useRouter();
const routeIsDashboard = computed(() =>
  ['dashboard', 'join', 'about'].includes(String(route.name)),
);
const routeIsWorkspace = computed(() =>
  ['space-home', 'project', 'discuss', 'project-file'].includes(String(route.name)),
);
const canResizeSidebar = computed(() => routeIsWorkspace.value);

const showChannelModal = ref(false);
const showProjectModal = ref(false);
const showInviteModal = ref(false);
const showIdentity = ref(false);
const showAgentModal = ref(false);
const showAdvancedSettings = ref(false);
const isRequestingSOS = ref(false);

// vue-tsc chokes on this type inference
const projectTree = useTemplateRef<Array<InstanceType<typeof ProjectTree>>>('projectTree');

const channels = ref([] as IChatChannel[]);

// Use channels directly as chatChannels since they're now separate
const chatChannels = computed(() => channels.value);

const projects = ref([] as IProject[]);
const activeProjectName = ref(null as string | null);
const projectFiles = ref([] as IProjectFile[]);

const connState = ref(globalThis._ndnd_conn_state);

const busListeners = {
  'project-list': (projs: IProject[]) => (projects.value = projs),
  'project-files': (name: string, files: IProjectFile[]) => {
    activeProjectName.value = name;
    projectFiles.value = files;
  },
  'chat-channels': (chans: IChatChannel[]) => (channels.value = chans),
  'conn-change': () => {
    connState.value = globalThis._ndnd_conn_state;
    if (!connState.value.connected) {
      Toast.info('Disconnected - you are offline');
    }
  },
};

const showNotifBubble = ref(false);

const SIDEBAR_WIDTH_KEY = 'ownly.sidebar.width';
const SIDEBAR_DEFAULT_WIDTH = 230;
const SIDEBAR_MIN_WIDTH = 220;
const SIDEBAR_MAX_WIDTH = 420;

const navRoot = useTemplateRef<HTMLElement>('navRoot');
const sidebarWidth = ref(SIDEBAR_DEFAULT_WIDTH);
const isResizing = ref(false);
const sidebarLeft = ref(0);

const sidebarStyle = computed(() => ({
  width: `${sidebarWidth.value}px`,
  minWidth: `${sidebarWidth.value}px`,
  flex: `0 0 ${sidebarWidth.value}px`,
}));

const THEME_KEY = 'ownly.theme';
const preferredDark = globalThis.matchMedia?.('(prefers-color-scheme: dark)');
const systemTheme = ref<'dark' | 'light'>(preferredDark?.matches ? 'dark' : 'light');
const userTheme = ref<'dark' | 'light' | null>(
  (globalThis.localStorage?.getItem(THEME_KEY) as 'dark' | 'light' | null) ??
  (document.documentElement.getAttribute('data-theme') as 'dark' | 'light' | null),
);
const effectiveTheme = computed<'dark' | 'light'>(() => userTheme.value ?? systemTheme.value);

let interval: ReturnType<typeof setInterval> ;

onMounted(async () => {
  const savedWidth = Number(globalThis.localStorage?.getItem(SIDEBAR_WIDTH_KEY));
  if (Number.isFinite(savedWidth)) {
    sidebarWidth.value = Math.max(SIDEBAR_MIN_WIDTH, Math.min(SIDEBAR_MAX_WIDTH, savedWidth));
  }

  GlobalBus.addListener('project-list', busListeners['project-list']);
  GlobalBus.addListener('project-files', busListeners['project-files']);
  GlobalBus.addListener('chat-channels', busListeners['chat-channels']);
  GlobalBus.addListener('conn-change', busListeners['conn-change']);
  interval = setInterval(() => {
    setNotification();
  },
  250);

  preferredDark?.addEventListener('change', onThemeMediaChange);
});

onUnmounted(() => {
  stopSidebarResize();

  GlobalBus.removeListener('project-list', busListeners['project-list']);
  GlobalBus.removeListener('project-files', busListeners['project-files']);
  GlobalBus.removeListener('chat-channels', busListeners['chat-channels']);
  GlobalBus.removeListener('conn-change', busListeners['conn-change']);
  clearInterval(interval);
  preferredDark?.removeEventListener('change', onThemeMediaChange);
});

function onThemeMediaChange(event: MediaQueryListEvent) {
  systemTheme.value = event.matches ? 'dark' : 'light';
}

function commitTheme(theme: 'dark' | 'light') {
  document.documentElement.setAttribute('data-theme', theme);
  userTheme.value = theme;
  globalThis.localStorage?.setItem(THEME_KEY, theme);
}

let pendingToggle: { theme: 'dark' | 'light'; curtain: HTMLElement; timer: ReturnType<typeof setTimeout> } | null = null;

function cleanupToggle() {
  if (!pendingToggle) return;
  clearTimeout(pendingToggle.timer);
  commitTheme(pendingToggle.theme);
  pendingToggle.curtain.remove();
  pendingToggle = null;
}

function toggleTheme() {
  // If a transition is in-flight, finish it immediately so we start clean
  cleanupToggle();

  const nextTheme = effectiveTheme.value === 'dark' ? 'light' : 'dark';

  const curtain = document.createElement('div');
  curtain.className = 'theme-curtain';
  curtain.style.background = nextTheme === 'dark' ? '#111' : '#fff';
  document.body.appendChild(curtain);

  // Force layout so the browser sees opacity:0 before we trigger fade-in
  curtain.getBoundingClientRect();
  curtain.classList.add('visible');

  const TIMEOUT = 800;
  const timer = setTimeout(() => cleanupToggle(), TIMEOUT);
  pendingToggle = { theme: nextTheme, curtain, timer };

  curtain.addEventListener('transitionend', function onFadeIn(e) {
    if (e.propertyName !== 'opacity') return;
    curtain.removeEventListener('transitionend', onFadeIn);

    // Apply the theme behind the opaque curtain
    commitTheme(nextTheme);

    // Brief hold for repaint, then fade out
    setTimeout(() => {
      curtain.classList.add('out');
      curtain.classList.remove('visible');
      curtain.addEventListener('transitionend', () => {
        clearTimeout(timer);
        curtain.remove();
        if (pendingToggle?.curtain === curtain) pendingToggle = null;
      }, { once: true });
    }, 120);
  });
}

function startSidebarResize(event: PointerEvent) {
  sidebarLeft.value = navRoot.value?.getBoundingClientRect().left ?? 0;
  isResizing.value = true;
  document.body.style.userSelect = 'none';
  document.body.style.cursor = 'col-resize';

  onSidebarResize(event);
  window.addEventListener('pointermove', onSidebarResize);
  window.addEventListener('pointerup', stopSidebarResize, { once: true });
}

function onSidebarResize(event: PointerEvent) {
  if (!isResizing.value) return;

  const next = Math.max(
    SIDEBAR_MIN_WIDTH,
    Math.min(SIDEBAR_MAX_WIDTH, Math.round(event.clientX - sidebarLeft.value)),
  );
  sidebarWidth.value = next;
}

function stopSidebarResize() {
  if (!isResizing.value) {
    window.removeEventListener('pointermove', onSidebarResize);
    window.removeEventListener('pointerup', stopSidebarResize);
    return;
  }

  isResizing.value = false;
  document.body.style.userSelect = '';
  document.body.style.cursor = '';
  window.removeEventListener('pointermove', onSidebarResize);
  window.removeEventListener('pointerup', stopSidebarResize);
  globalThis.localStorage?.setItem(SIDEBAR_WIDTH_KEY, String(sidebarWidth.value));
}

function buildVersion() {
  return __BUILD_VERSION__;
}

/** Link to project home page */
function linkProject(project: IProject) {
  return {
    name: 'project',
    params: {
      space: route.params.space,
      project: project.name,
    },
  };
}

/** Link to discussion channel */
function linkDiscuss(channel: IChatChannel) {
  return {
    name: 'discuss',
    params: {
      space: route.params.space,
      channel: channel.name,
    },
  };
}


function setNotification() {
  let wkspName = "/" + route.params.space as string
  while (wkspName.replace("-","/") != wkspName) { // convert dashes to slashes
    wkspName = wkspName.replace("-","/")
  }
  if (_access_requests.filter(a => a[0] == wkspName && a[2] == false).length > 0)
    showNotifBubble.value = true;
  else
    showNotifBubble.value = false;
}

async function sosRequest() {
  if (isRequestingSOS.value) return;

  const wksp = globalThis.ActiveWorkspace;
  if (!wksp) {
    Toast.error('No active workspace');
    return;
  }

  isRequestingSOS.value = true;
  const progress = Toast.loading('Broadcasting SOS refresh request...');
  try {
    const { requestId, responder } = await wksp.sosRequest();
    await progress.success(`SOS request sent to ${responder}`);
  } catch (err) {
    await progress.error(`Failed to send SOS request: ${err}`);
  } finally {
    isRequestingSOS.value = false;
  }
}
</script>

<style scoped lang="scss">
@use '@/assets/navbar-item.scss';

.main-nav {
  width: 230px;
  min-width: 230px;
  height: 100dvh;
  overflow-y: hidden;
  position: relative;

  display: flex;
  flex-direction: column;
  .top-sheet {
    padding: 10px;
    flex: 1;
    overflow-y: auto;
  }
  .bottom-sheet {
    padding: 8px 10px;
    font-size: 0.9rem;

    a {
      color: #ccc;
    }

    .id-share {
      padding: 2px 12px;
    }

    .connection {
      border-radius: 50px;
      padding: 5px 12px;
      margin-top: 2px;
      color: #ddd;
      cursor: pointer;

      transition: background-color 0.2s ease;
      background-color: rgba(255, 255, 255, 0.05);
      &:hover {
        background-color: rgba(255, 255, 255, 0.1);
      }
    }
  }

  .logo {
    display: block;
    height: 35px;
    margin: 5px 0;
  }

  .menu-list a.is-disabled {
    opacity: 0.65;
    pointer-events: none;
  }

  .logo-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin: 0 5px 15px;
  }

  .theme-switch {
    position: relative;
    display: inline-flex;
    align-items: center;
    cursor: pointer;

    input {
      position: absolute;
      opacity: 0;
      width: 0;
      height: 0;
    }

    .track {
      position: relative;
      width: 40px;
      height: 20px;
      border-radius: 10px;
      background: rgba(255, 255, 255, 0.1);
      transition: background 0.25s ease;
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 0 5px;
      box-sizing: border-box;
    }

    .track-icon {
      font-size: 10px;
      color: rgba(255, 255, 255, 0.35);
      transition: color 0.25s ease;
      z-index: 1;
    }

    .knob {
      position: absolute;
      top: 2px;
      left: 2px;
      width: 16px;
      height: 16px;
      border-radius: 50%;
      background: rgba(255, 255, 255, 0.5);
      transition: left 0.25s ease, background 0.25s ease;
    }

    input:checked ~ .track .knob {
      left: 22px;
    }

    &:hover .track {
      background: rgba(255, 255, 255, 0.18);
    }

    &:hover .track-icon {
      color: rgba(255, 255, 255, 0.7);
    }

    &:hover .knob {
      background: rgba(255, 255, 255, 0.8);
    }
  }

  .menu-label {
    color: #bbb;
  }

  :deep(li > a) {
    background-color: transparent;
    color: white;
    display: flex;
    align-items: center;
    //justify-content: space-between;

    &:hover {
      background-color: rgba(255, 255, 255, 0.1);
    }

    &.is-active,
    &.router-link-active {
      background: rgba(255, 255, 255, 0.08);
      color: white;
      position: relative;

      &::before {
        content: '';
        position: absolute;
        left: 0;
        top: 0;
        bottom: 0;
        width: 3px;
        border-radius: 6px 0 0 6px;
        background: var(--sidebar-highlight-bg);
        pointer-events: none;
      }
    }
    .link-inner {
      flex: 1;
      display: flex;
      align-items: center;
    }

    .link-button {
      background: none;
      border: none;
      color: rgba(255, 255, 255, 0.6);
      cursor: pointer;
      width: 20px;
      height: 20px;
      padding: 0;
      border-radius: 4px;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      transition: all 0.2s ease;
      opacity: 0;

      &:hover {
      background-color: rgba(255, 255, 255, 0.14);
      color: #fff;
      }
    }

    &:hover .link-button {
      opacity: 1;
    }

  }

  :deep(.project-item > a.router-link-active .link-button),
  :deep(.project-item > a.is-active .link-button) {
    opacity: 1;
  }

  :deep(.project-item > a.project-link) {
    min-height: 30px;
    border-radius: 6px;
    border: 0;
    background-color: transparent;

    &.router-link-active,
    &.is-active {
      background: rgba(255, 255, 255, 0.08);
    }
  }

  :deep(.project-item > a.project-link:hover) {
    background-color: rgba(255, 255, 255, 0.1);
  }

  :deep(.project-item > a.project-link .project-link-inner) {
    gap: 7px;
  }

  :deep(.project-item > a.project-link .project-icon-shell) {
    width: 16px;
    height: 16px;
    border-radius: 4px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: 0 0 auto;
  }

  :deep(.project-item > a.project-link .project-name) {
    font-weight: 600;
    letter-spacing: 0.01em;
  }

  :deep(.project-item > .outermost) {
    margin-top: 3px;
    margin-bottom: 8px;
  }

  :deep(.project-list .project-item:last-child > .outermost) {
    margin-bottom: 0;
  }

  .project-item + .project-item {
    position: relative;
    margin-top: 6px;
    padding-top: 6px;
  }

  .project-item + .project-item::before {
    content: '';
    position: absolute;
    left: 10px;
    right: 10px;
    top: 0;
    height: 1px;
    background: rgba(255, 255, 255, 0.08);
  }

  .project-actions {
    margin-top: 6px;
  }

  .sidebar-resizer {
    position: absolute;
    top: 0;
    right: -3px;
    width: 6px;
    height: 100%;
    cursor: col-resize;
    z-index: 30;
    touch-action: none;

    &::before {
      content: '';
      position: absolute;
      top: 0;
      bottom: 0;
      left: 50%;
      width: 1px;
      transform: translateX(-50%);
      background: rgba(255, 255, 255, 0);
      transition: background-color 0.15s ease;
    }
  }

  &:hover .sidebar-resizer::before,
  &.resizing .sidebar-resizer::before {
    background: rgba(255, 255, 255, 0.22);
  }
}
</style>
