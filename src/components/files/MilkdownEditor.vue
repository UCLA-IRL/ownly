<template>
  <div class="editor-shell">
    <LoadingSpinner v-if="loading" class="absolute-center" text="Loading document editor ..." />
    <div ref="outer" class="outer" :class="{ 'is-loading': loading }"></div>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, useTemplateRef, watch, type PropType } from 'vue';
import { useRouter } from 'vue-router';

import * as Y from 'yjs';
import type { Awareness } from 'y-protocols/awareness.js';

import { editorViewCtx } from '@milkdown/core';
import { Crepe } from '@milkdown/crepe';
import { collab, CollabService, collabServiceCtx } from '@milkdown/plugin-collab';
import '@milkdown/crepe/theme/common/style.css';
import milkdownFrameLight from '@milkdown/crepe/theme/frame.css?url';
import milkdownFrameDark from '@milkdown/crepe/theme/frame-dark.css?url';

import { Workspace } from '@/services/workspace';
import * as opfs from '@/services/opfs';
import * as utils from '@/utils';
import { useThemeWatch } from '@/utils';
import type { WorkspaceProj } from '@/services/workspace-proj';
import * as pathjs from 'path-browserify';
import LoadingSpinner from '@/components/LoadingSpinner.vue';

const props = defineProps({
  yxml: {
    type: Object as PropType<Y.XmlFragment>,
    required: true,
  },
  awareness: {
    type: Object as PropType<Awareness>,
    required: true,
  },
  path: {
    type: String,
    required: true,
  },
});

const outer = useTemplateRef('outer');
const router = useRouter();

let crepe: Crepe | null = null;
let collabService: CollabService | null = null;
let opfsPath: string | null = null;
let proj: WorkspaceProj | null = null;
const loading = ref(true);
const objectURLs: Map<string, string> = new Map();
let unwatchTheme: (() => void) | null = null;
const MILKDOWN_THEME_LINK_ID = 'ownly-milkdown-theme';

function timingLabel(action: string) {
  return `[MilkdownEditor] ${action} ${props.path}`;
}

function logDuration(label: string, started: number) {
  console.debug(`${label}: ${(performance.now() - started).toFixed(1)}ms`);
}

watch(
  () => props.yxml,
  async () => {
    await destroy();
    await create();
  },
);
onMounted(async () => {
  await create();
});
onBeforeUnmount(() => {
  unwatchTheme?.();
  unwatchTheme = null;
  void destroy();
});

const onUpload = async (file: File): Promise<string> => {
  const started = performance.now();
  const label = timingLabel(`upload ${file.name}`);
  const parts = props.path.split('/').filter(Boolean);
  const baseFolder = parts.slice(0, -1).join('/');
  const url = `${baseFolder}/${file.name}`;
  try {
    await proj?.importFile(url, file.stream());
    await new Promise((r) => setTimeout(r, 100)); // Otherwise the image won't load
    await proj?.syncFs({ path: url });
    return url;
  } finally {
    logDuration(label, started);
  }
};

const proxyDomURL = async (url: string): Promise<string> => {
  const started = performance.now();
  const label = timingLabel(`image sync ${url}`);
  const existingUrl = objectURLs.get(url);
  if (existingUrl) {
    logDuration(`${label} cache hit`, started);
    return existingUrl;
  }

  if (!proj || !opfsPath) {
    throw new Error('Project filesystem is not ready');
  }

  await proj.syncFs({ path: url });
  const handle = await opfs.getFileHandle(pathjs.join(opfsPath!, url));
  const file = await handle.getFile();
  const ret = URL.createObjectURL(file);
  objectURLs.set(url, ret);
  logDuration(label, started);
  return ret;
};

async function create() {
  const label = timingLabel('create');
  const started = performance.now();
  loading.value = true;
  try {
    let stepStarted = performance.now();
    proj = await Workspace.setupAndGetActiveProj(router);
    logDuration(`${label} active project`, stepStarted);

    stepStarted = performance.now();
    opfsPath = proj.getFsBasePath();
    logDuration(`${label} OPFS base path`, stepStarted);

    stepStarted = performance.now();
    crepe = new Crepe({
      root: outer.value!,
      features: {
        [Crepe.Feature.ImageBlock]: true,
      },
      featureConfigs: {
        [Crepe.Feature.ImageBlock]: {
          onUpload: onUpload,
          proxyDomURL: proxyDomURL,
        },
      },
    });
    crepe.editor.use(collab);
    logDuration(`${label} crepe configure`, stepStarted);

    stepStarted = performance.now();
    await crepe.create();
    logDuration(`${label} crepe create`, stepStarted);

    stepStarted = performance.now();
    crepe.editor.action((ctx) => {
      // Connect to the collab service
      collabService = ctx.get(collabServiceCtx);
      collabService.bindXmlFragment(props.yxml).setAwareness(props.awareness).connect();

      // Add a space after pasting a link. This prevents the link from being written
      // over when you type after the link. Do the same thing when pressing space after link.
      const view = ctx.get(editorViewCtx);

      const handleLinkSpace = async (event?: KeyboardEvent) => {
        await nextTick();

        const { $from } = view.state.selection;
        if ($from.marks().some((mark) => mark.type.name === 'link')) {
          // Insert a new element after the link
          view.dispatch(view.state.tr.insert($from.pos, view.state.schema.text(' ')));

          // Prevent the space from being written
          event?.preventDefault();
        }
      };

      view.dom.addEventListener('paste', () => handleLinkSpace());
      view.dom.addEventListener('keydown', (e) => e.key === ' ' && handleLinkSpace(e));
    });

    applyThemeToEditor();
    unwatchTheme = useThemeWatch(applyThemeToEditor);
    logDuration(`${label} collab bind`, stepStarted);
    logDuration(`${label} ready`, started);
  } finally {
    loading.value = false;
  }
}

async function destroy() {
  unwatchTheme?.();
  unwatchTheme = null;

  collabService?.disconnect();
  await crepe?.destroy();
  crepe = null;
  collabService = null;

  // Remove the injected <link> so it doesn't leak when the editor is closed
  document.getElementById(MILKDOWN_THEME_LINK_ID)?.remove();

  for (const url of objectURLs.values()) {
    URL.revokeObjectURL(url);
  }
  objectURLs.clear();
}

function applyThemeToEditor() {
  const href = utils.themeIsDark() ? milkdownFrameDark : milkdownFrameLight;
  let link = document.getElementById(MILKDOWN_THEME_LINK_ID) as HTMLLinkElement | null;

  if (!link) {
    link = document.createElement('link');
    link.id = MILKDOWN_THEME_LINK_ID;
    link.rel = 'stylesheet';
    document.head.appendChild(link);
  }

  if (link.getAttribute('href') !== href) {
    link.href = href;
  }
}
</script>

<style scoped lang="scss">
.editor-shell {
  position: relative;
  height: 100%;
}

.outer {
  height: 100%;

  &.is-loading {
    opacity: 0;
  }
}

.outer :deep(.milkdown) {
  height: 100%;
  overflow-y: scroll;
  overflow-x: hidden;
}
</style>

<style lang="scss">
// Fix overlap with side panel of milkdown itself (strange)
// If you select text the + button will overlap with the toolbar
milkdown-toolbar,
milkdown-latex-inline-edit {
  z-index: 20;
}

// Workaround bug in milkdown, this should be destroyed
body > milkdown-slash-menu {
  display: none !important;
}

@media (max-width: 1023px) {
  .milkdown .ProseMirror {
    touch-action: manipulation;
    padding-left: 20px !important;
    padding-right: 20px !important;
  }
}

.ProseMirror-yjs-cursor {
  position: relative;
  border-left: 2px solid black;
  margin-left: -1px !important;
  margin-right: -1px !important;
  border-color: orange;
  word-break: normal;
  pointer-events: none;
}

.ProseMirror-yjs-cursor > div {
  position: absolute;
  top: calc(-1rem - 1px);
  left: -3px;
  font-size: 10pt;
  background-color: orange;
  border-radius: 3px;
  padding: 1px 4px;
  font-style: normal;
  font-weight: normal;
  line-height: normal;
  user-select: none;
  color: black;
  white-space: nowrap;
  animation: fade90 4s forwards;
}
</style>
