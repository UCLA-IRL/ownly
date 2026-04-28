<template>
  <div class="outer">
    <div class="file-head px-3 py-1">
      <div class="file-head-left"></div>

      <div class="file-head-center">
        <button v-if="!isRenaming" class="file-title-button" @click="startRename" :title="fileTitle">
          <span class="file-title">{{ fileTitle }}</span>
        </button>

        <form v-else class="rename-form" @submit.prevent="submitRename">
          <input
            ref="renameInput"
            class="input is-small rename-input"
            v-model="renameValue"
            @keydown.esc.prevent="cancelRename"
          />
          <button class="button is-small" type="submit">Save</button>
          <button class="button is-small is-light" type="button" @click="cancelRename">Cancel</button>
        </form>
      </div>

      <div class="file-head-right">
        <button
          v-if="hasPreviewResult"
          class="button is-small is-light mobile-preview-toggle"
          type="button"
          :title="mobileActivePane === 'preview' ? 'Switch to code' : 'Switch to preview'"
          :aria-pressed="mobileActivePane === 'preview'"
          @click="toggleMobilePane"
        >
          {{ mobileActivePane === 'preview' ? 'Code' : 'Preview' }}
        </button>

        <div class="viewer-list" v-if="viewers.length">
          <div
            v-for="viewer in visibleViewers"
            :key="viewer"
            class="viewer-badge"
            tabindex="0"
            :aria-label="`Viewing ${viewer}`"
          >
            <img
              class="viewer-avatar"
              :src="utils.makeAvatar(viewer)"
              :alt="viewer"
            />
            <div class="viewer-tooltip" role="tooltip">
              <div class="viewer-tooltip-title">Viewing</div>
              <div class="viewer-tooltip-name">{{ viewer }}</div>
            </div>
          </div>
          <div
            v-if="hiddenViewerCount > 0"
            class="viewer-more"
            tabindex="0"
            :aria-label="`Also viewing: ${hiddenViewers.join(', ')}`"
          >
            +{{ hiddenViewerCount }}
            <div class="viewer-tooltip" role="tooltip">
              <div class="viewer-tooltip-title">Also viewing</div>
              <ul>
                <li v-for="viewer in hiddenViewers" :key="viewer">{{ viewer }}</li>
              </ul>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Do not kill other components even during loading
    They can likely be reused if the user is switching files. -->
    <LoadingSpinner v-if="loading" class="absolute-center" text="Loading your file ..." />

    <Suspense v-if="contentCode">
      <div
        ref="codePane"
        class="code"
        :class="{
          'has-result': hasPreviewResult,
          'is-resizing': isResizingSplit,
          'preview-collapsed': previewCollapsed,
          'mobile-preview-active': mobileActivePane === 'preview',
        }"
        :style="codeSplitStyle"
      >
        <CodeEditor
          class="editor"
          :ytext="contentCode"
          :basename="contentBasename"
          :awareness="awareness!"
        />
        <div
          v-if="hasPreviewResult"
          class="code-resizer"
          tabindex="0"
          role="separator"
          aria-orientation="vertical"
          aria-label="Resize preview panel"
          @pointerdown.prevent="startSplitResize"
          @keydown="onResizerKeydown"
        >
          <button
            class="preview-toggle"
            type="button"
            :title="previewCollapsed ? 'Click to show preview' : 'Click to hide preview'"
            @pointerdown.stop
            @click="togglePreview"
          >
            {{ previewCollapsed ? '‹' : '›' }}
          </button>

          <div class="resizer-grip grip-top" aria-hidden="true">
            <span class="grip-dots" aria-hidden="true"></span>
          </div>

          <div class="resizer-grip grip-bottom" aria-hidden="true">
            <span class="grip-dots" aria-hidden="true"></span>
          </div>
        </div>
        <PdfViewer
          v-if="contentIsLatex"
          class="result"
          :basename="`${proj?.name}.pdf`"
          :pdf="resultPdf"
          has-compile
          :compiling="resultIsCompiling"
          :error="resultError"
          @compile="compileLatex"
        />
        <MarkdownViewer
          v-if="contentIsMarkdown"
          class="result"
          :basename="contentBasename"
          :ytext="contentCode"
        />
        <RevealSlidesViewer
          v-if="contentIsRevealJS"
          class="result"
          :basename="contentBasename"
          :ytext="contentCode"
        />
      </div>

      <template #fallback>
        <LoadingSpinner class="absolute-center" text="Loading code editor ..." />
      </template>
    </Suspense>

    <Suspense v-else-if="contentExcalidrawElements !== null && contentExcalidrawFiles !== null">
      <ExcalidrawEditor
        :yeles="contentExcalidrawElements"
        :yfiles="contentExcalidrawFiles"
        :name="contentBasename"
      />

      <template #fallback>
        <LoadingSpinner class="absolute-center" text="Loading excalidraw editor ..." />
      </template>
    </Suspense>

    <Suspense v-else-if="contentMilk">
      <MilkdownEditor :yxml="contentMilk" :path="filepath" :awareness="awareness!" />

      <template #fallback>
        <LoadingSpinner class="absolute-center" text="Loading document editor ..." />
      </template>
    </Suspense>

    <BlobView
      v-else-if="contentBlob"
      :key="contentBlob.name"
      :version="contentBlob"
      :path="filepath"
      :basename="contentBasename"
    />
  </div>
</template>

<script setup lang="ts">
import {
  computed,
  defineAsyncComponent,
  nextTick,
  onMounted,
  onUnmounted,
  ref,
  shallowRef,
  useTemplateRef,
  watch,
} from 'vue';
import { useRoute, useRouter } from 'vue-router';

import * as Y from 'yjs';
import * as awareProto from 'y-protocols/awareness.js';
import type { ExcalidrawElementYMap, ExcalidrawFilesYMap } from '@/services/excalidraw-types';

import LoadingSpinner from '@/components/LoadingSpinner.vue';
const CodeEditor = defineAsyncComponent({
  loader: () => import('@/components/files/CodeEditor.vue'),
});
const MilkdownEditor = defineAsyncComponent({
  loader: () => import('@/components/files/MilkdownEditor.vue'),
});
const PdfViewer = defineAsyncComponent({
  loader: () => import('@/components/files/PdfViewer.vue'),
});
const MarkdownViewer = defineAsyncComponent({
  loader: () => import('@/components/files/MarkdownViewer.vue'),
});
const ExcalidrawEditor = defineAsyncComponent({
  loader: () => import('@/components/files/ExcalidrawEditor.vue'),
});
import BlobView from '@/components/files/BlobView.vue';

import { Workspace } from '@/services/workspace';
import * as latex from '@/services/latex/index';
import * as utils from '@/utils';
import { Toast } from '@/utils/toast';

import type { WorkspaceProj } from '@/services/workspace-proj';
import type { IBlobVersion } from '@/services/types';
import RevealSlidesViewer from '@/components/files/RevealSlidesViewer.vue';

const route = useRoute();
const router = useRouter();

const loading = ref(true);
const filename = computed(() => route.params.filename as string[]);
const filepath = computed(() => '/' + filename.value.join('/'));
const fileTitle = computed(() => filename.value[filename.value.length - 1] || contentBasename.value);

const proj = shallowRef(null as WorkspaceProj | null);

const contentDoc = shallowRef<Y.Doc | null>(null);
const awareness = shallowRef<awareProto.Awareness | null>(null);

const contentCode = shallowRef<Y.Text | null>(null);
const contentMilk = shallowRef<Y.XmlFragment | null>(null);
const contentExcalidrawElements = shallowRef<ExcalidrawElementYMap | null>(null);
const contentExcalidrawFiles = shallowRef<ExcalidrawFilesYMap | null>(null);
const contentBlob = shallowRef<IBlobVersion | null>(null);

// These are refs to prevent ui glitch when switching views
const contentBasename = ref<string>(String());
const contentIsLatex = ref<boolean>(false);
const contentIsRevealJS = ref<boolean>(false);
const contentIsMarkdown = ref<boolean>(false);

const resultPdf = shallowRef<Uint8Array<ArrayBuffer> | null>(null);
const resultIsCompiling = ref(false);
const resultError = ref(String());
const viewers = ref<string[]>([]);
const visibleViewers = computed(() => viewers.value.slice(0, 5));
const hiddenViewers = computed(() => viewers.value.slice(5));
const hiddenViewerCount = computed(() => hiddenViewers.value.length);
const hasPreviewResult = computed(() => contentIsLatex.value || contentIsMarkdown.value || contentIsRevealJS.value);

const SPLIT_RATIO_KEY = 'ownly.preview.splitRatio';
const PREVIEW_COLLAPSED_KEY = 'ownly.preview.collapsed';
const MOBILE_ACTIVE_PANE_KEY = 'ownly.preview.mobilePane';
const MIN_SPLIT_RATIO = 0.28;
const MAX_SPLIT_RATIO = 0.72;
const splitRatio = ref(0.5);
const previewCollapsed = ref(false);
const mobileActivePane = ref<'code' | 'preview'>('code');
const isResizingSplit = ref(false);
const splitLeft = ref(0);
const splitWidth = ref(0);
const codePane = useTemplateRef<HTMLElement>('codePane');

const codeSplitStyle = computed(() => ({
  '--editor-width': previewCollapsed.value
    ? 'calc(100% - var(--resizer-width))'
    : `calc((100% - var(--resizer-width)) * ${splitRatio.value})`,
  '--preview-width': previewCollapsed.value
    ? '0px'
    : `calc((100% - var(--resizer-width)) * ${1 - splitRatio.value})`,
}));
const localViewerName = ref('');
const isRenaming = ref(false);
const renameValue = ref('');
const renameInput = useTemplateRef<HTMLInputElement>('renameInput');

let awarenessListener: ((event: { added: number[]; updated: number[]; removed: number[] }, source: 'local' | 'remote') => void) | null = null;
onMounted(create);
watch(filename, create);
watch(() => route.params.project, create);

watch(filename, () => {
  isRenaming.value = false;
});
onUnmounted(() => void destroy());

const savedSplit = Number(globalThis.localStorage?.getItem(SPLIT_RATIO_KEY));
if (Number.isFinite(savedSplit)) {
  splitRatio.value = Math.max(MIN_SPLIT_RATIO, Math.min(MAX_SPLIT_RATIO, savedSplit));
}

previewCollapsed.value = globalThis.localStorage?.getItem(PREVIEW_COLLAPSED_KEY) === '1';
mobileActivePane.value = globalThis.localStorage?.getItem(MOBILE_ACTIVE_PANE_KEY) === 'preview'
  ? 'preview'
  : 'code';

async function create() {
  // If something fails, we should destroy these
  let newDoc: Y.Doc | null = null;
  let newAwareness: awareProto.Awareness | null = null;
  let newContentCode: Y.Text | null = null;
  let newContentMilk: Y.XmlFragment | null = null;
  let newContentBlob: IBlobVersion | null = null;
  let newContentExcalidrawElements: ExcalidrawElementYMap | null = null;
  let newContentExcalidrawFiles: ExcalidrawFilesYMap | null = null;

  try {
    loading.value = true;

    const wksp = await Workspace.setupOrRedir(router);
    if (!wksp) throw new Error('Workspace not found');

    localViewerName.value = wksp.username ?? '';

    let newProj;

    if (wksp.proj.active) newProj = wksp.proj.active;
    else {
      // No active project, try to get it from the URL
      const projName = router.currentRoute.value.params.project as string;
      if (!projName) throw new Error('No project name provided');

      newProj = await wksp.proj.get(projName);
      await newProj.activate();
    }

    if (proj.value?.uuid !== newProj.uuid) await destroy();
    proj.value = newProj;

    // Load file metadata
    const metadata = proj.value.getFileMeta(filepath.value);
    if (!metadata) throw new Error(`File not found: ${filepath.value}`);

    // Update tab name
    document.title = utils.formTabName(wksp.metadata.label);

    // Get file path attributes
    const basename = filename.value[filename.value.length - 1];

    // Load file content
    if (metadata.is_blob) {
      // Blob file, the doc contains the version list
      newDoc = await proj.value.getFile(filepath.value);
      newContentBlob = newDoc.getArray<IBlobVersion>('blobs').get(0);
      if (!newContentBlob) Toast.warning('Empty blob file opened');
    } else if (utils.isExtensionType(basename, 'code')) {
      // Text file content
      newDoc = await proj.value.getFile(filepath.value);
      newAwareness = await proj.value.getAwareness(filepath.value);
      newContentCode = newDoc.getText('text');
    } else if (utils.isExtensionType(basename, 'milkdown')) {
      // Milkdown XML fragment content
      newDoc = await proj.value.getFile(filepath.value);
      newAwareness = await proj.value.getAwareness(filepath.value);
      newContentMilk = newDoc.getXmlFragment('milkdown');
    } else if (utils.isExtensionType(basename, 'excalidraw')) {
      // Excalidraw JSON content. We handle elements and files, but leave appstate.
      newDoc = await proj.value.getFile(filepath.value);
      newContentExcalidrawElements = newDoc.getMap('elements');
      newContentExcalidrawFiles = newDoc.getMap('files');
    } else {
      throw new Error(`Unsupported content extension: ${basename}`);
    }

    // Always update these, since the filename might have changed
    contentBasename.value = basename;
    contentIsLatex.value = utils.isExtensionType(basename, 'latex');
    if (utils.isExtensionType(basename, 'markdown')) {
      contentIsRevealJS.value = basename.endsWith('slides.md');
      contentIsMarkdown.value = !contentIsRevealJS.value;
    } else {
      contentIsRevealJS.value = false;
      contentIsMarkdown.value = false;
    }

    // If the content doc is the same, then do not reset the doc
    // The provider returns the same doc if the file is already loaded
    // This will change if we support multiple open handles (e.g. ref counting)
    //
    // Test in case: rename an open file and open it - the path changes but
    // the underlying document is the same
    if (contentDoc.value?.guid === newDoc.guid) return;

    // Do the reset synchronously so that the UI does not refresh
    // This means that there will be no flicker and the PDF will stay.
    resetDoc();
    contentDoc.value = newDoc;
    awareness.value = newAwareness;
    contentCode.value = newContentCode;
    contentMilk.value = newContentMilk;
    contentExcalidrawElements.value = newContentExcalidrawElements;
    contentExcalidrawFiles.value = newContentExcalidrawFiles;
    contentBlob.value = newContentBlob;
  } catch (err) {
    console.error(err);
    Toast.error(`Failed to load file: ${err}`);

    // Destroy the new doc if it was created
    // This automatically destroys the children
    newDoc?.destroy();
  } finally {
    loading.value = false;
  }
}

function resetDoc() {
  if (awareness.value && awarenessListener) {
    awareness.value.off('update', awarenessListener);
  }
  awarenessListener = null;

  contentCode.value = null;
  contentMilk.value = null;
  contentExcalidrawElements.value = null;
  contentExcalidrawFiles.value = null;
  contentBlob.value = null;
  awareness.value = null;

  contentDoc.value?.destroy();
  contentDoc.value = null;
  viewers.value = [];
}

async function destroy() {
  stopSplitResize();

  resetDoc();
  resultIsCompiling.value = false;
  resultPdf.value = null;
  resultError.value = String();
}

function startSplitResize(event: PointerEvent) {
  if (previewCollapsed.value) {
    previewCollapsed.value = false;
    globalThis.localStorage?.setItem(PREVIEW_COLLAPSED_KEY, '0');
  }

  const pane = codePane.value;
  if (!pane) return;

  const rect = pane.getBoundingClientRect();
  splitLeft.value = rect.left;
  splitWidth.value = rect.width;
  isResizingSplit.value = true;
  document.body.style.userSelect = 'none';
  document.body.style.cursor = 'col-resize';

  onSplitResize(event);
  window.addEventListener('pointermove', onSplitResize);
  window.addEventListener('pointerup', stopSplitResize, { once: true });
}

function togglePreview() {
  previewCollapsed.value = !previewCollapsed.value;
  globalThis.localStorage?.setItem(PREVIEW_COLLAPSED_KEY, previewCollapsed.value ? '1' : '0');
}

function toggleMobilePane() {
  mobileActivePane.value = mobileActivePane.value === 'preview' ? 'code' : 'preview';
  globalThis.localStorage?.setItem(MOBILE_ACTIVE_PANE_KEY, mobileActivePane.value);
}

function onResizerKeydown(event: KeyboardEvent) {
  if (!hasPreviewResult.value) return;

  const key = event.key;
  if (key !== 'ArrowLeft' && key !== 'ArrowRight' && key !== 'Home' && key !== 'End') return;

  event.preventDefault();

  if (previewCollapsed.value) {
    previewCollapsed.value = false;
    globalThis.localStorage?.setItem(PREVIEW_COLLAPSED_KEY, '0');
  }

  const step = event.shiftKey ? 0.05 : 0.02;
  if (key === 'ArrowLeft') {
    splitRatio.value = Math.max(MIN_SPLIT_RATIO, splitRatio.value - step);
  } else if (key === 'ArrowRight') {
    splitRatio.value = Math.min(MAX_SPLIT_RATIO, splitRatio.value + step);
  } else if (key === 'Home') {
    splitRatio.value = MIN_SPLIT_RATIO;
  } else if (key === 'End') {
    splitRatio.value = MAX_SPLIT_RATIO;
  }

  globalThis.localStorage?.setItem(SPLIT_RATIO_KEY, String(splitRatio.value));
}

function onSplitResize(event: PointerEvent) {
  if (!isResizingSplit.value || splitWidth.value <= 0) return;
  const ratio = (event.clientX - splitLeft.value) / splitWidth.value;
  splitRatio.value = Math.max(MIN_SPLIT_RATIO, Math.min(MAX_SPLIT_RATIO, ratio));
}

function stopSplitResize() {
  if (!isResizingSplit.value) {
    window.removeEventListener('pointermove', onSplitResize);
    window.removeEventListener('pointerup', stopSplitResize);
    return;
  }

  isResizingSplit.value = false;
  document.body.style.userSelect = '';
  document.body.style.cursor = '';
  window.removeEventListener('pointermove', onSplitResize);
  window.removeEventListener('pointerup', stopSplitResize);
  globalThis.localStorage?.setItem(SPLIT_RATIO_KEY, String(splitRatio.value));
}

function startRename() {
  renameValue.value = fileTitle.value;
  isRenaming.value = true;
  nextTick(() => {
    renameInput.value?.focus();
    renameInput.value?.select();
  });
}

function cancelRename() {
  isRenaming.value = false;
}

async function submitRename() {
  const nextName = renameValue.value.trim();
  if (!nextName) {
    Toast.error('File name cannot be empty');
    return;
  }
  if (nextName.includes('/')) {
    Toast.error('File name cannot include /');
    return;
  }
  if (!proj.value) return;

  const parts = filepath.value.split('/').filter(Boolean);
  if (!parts.length) return;
  if (nextName === parts[parts.length - 1]) {
    isRenaming.value = false;
    return;
  }

  parts[parts.length - 1] = nextName;
  const nextPath = '/' + parts.join('/');

  try {
    await proj.value.moveFile(filepath.value, nextPath);
    isRenaming.value = false;
    await router.replace({
      name: 'project-file',
      params: {
        space: route.params.space,
        project: route.params.project,
        filename: parts,
      },
    });
  } catch (err) {
    console.error(err);
    Toast.error(`Failed to rename file: ${err}`);
  }
}

function updateViewers() {
  if (!awareness.value) {
    viewers.value = [];
    return;
  }

  const names = new Set<string>();
  for (const state of awareness.value.getStates().values()) {
    const user = (state as { user?: { name?: string } }).user;
    const name = user?.name?.trim();
    if (name) names.add(name);
  }
  viewers.value = Array.from(names.values()).sort((a, b) => {
    if (a === localViewerName.value && b !== localViewerName.value) return -1;
    if (b === localViewerName.value && a !== localViewerName.value) return 1;
    return a.localeCompare(b, undefined, { sensitivity: 'base' });
  });
}

async function compileLatex() {
  if (resultIsCompiling.value) return;
  try {
    resultIsCompiling.value = true;
    resultPdf.value = await latex.compile(proj.value!);
    resultError.value = String();
  } catch (err) {
    resultError.value = `Failed to compile LaTeX\n\n ${err}`;
  } finally {
    resultIsCompiling.value = false;
  }
}

watch(awareness, (newAwareness, oldAwareness) => {
  if (oldAwareness && awarenessListener) {
    oldAwareness.off('update', awarenessListener);
  }

  if (!newAwareness) {
    awarenessListener = null;
    viewers.value = [];
    return;
  }

  awarenessListener = () => updateViewers();
  newAwareness.on('update', awarenessListener);
  updateViewers();
});
</script>

<style scoped lang="scss">
.outer {
  position: relative;
  height: 100%;
  width: 100%;
  display: flex;
  flex-direction: column;
}

.file-head {
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  align-items: center;
  gap: 0.75rem;
}

.file-head-left {
  min-width: 0;
}

.file-head-center {
  justify-self: center;
  min-width: 0;
}

.file-title {
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.file-title-button {
  border: none;
  background: transparent;
  padding: 0;
  max-width: 360px;
  cursor: pointer;
}

.rename-form {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}

.rename-input {
  width: 260px;
}

.file-head-right {
  justify-self: end;
  display: flex;
  align-items: center;
  gap: 0.5rem;
  min-height: 30px;
}

.viewer-list {
  display: flex;
  align-items: center;
  height: 28px;
}

.viewer-avatar {
  display: block;
  width: 24px;
  height: 24px;
  border-radius: 999px;
  border: 2px solid var(--bulma-scheme-main);
  background: var(--bulma-scheme-main);
}

.viewer-badge {
  position: relative;
  width: 24px;
  height: 24px;
  margin-left: -6px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 999px;

  &:first-child {
    margin-left: 0;
  }

  &:hover .viewer-tooltip,
  &:focus .viewer-tooltip {
    opacity: 1;
    transform: translate(0, 0);
    pointer-events: auto;
  }
}

.viewer-more {
  position: relative;
  flex: 0 0 24px;
  width: 24px;
  height: 24px;
  border-radius: 999px;
  margin-left: -6px;
  background: var(--bulma-primary);
  color: var(--bulma-white);
  font-size: 0.7rem;
  font-weight: 700;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 2px solid var(--bulma-scheme-main);

  &:hover .viewer-tooltip,
  &:focus .viewer-tooltip {
    opacity: 1;
    transform: translate(0, 0);
    pointer-events: auto;
  }
}

.viewer-tooltip {
  position: absolute;
  top: calc(100% + 8px);
  right: 0;
  left: auto;
  transform: translate(0, -4px);
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.12s ease, transform 0.12s ease;
  z-index: 4;

  min-width: 140px;
  max-width: min(220px, calc(100vw - 16px));
  padding: 8px 10px;
  border-radius: 8px;
  background: var(--bulma-scheme-main-bis);
  color: var(--bulma-text);
  border: 1px solid var(--bulma-border);
  box-shadow: var(--bulma-shadow);

  .viewer-tooltip-title {
    font-size: 0.72rem;
    font-weight: 700;
    margin-bottom: 4px;
    opacity: 0.85;
  }

  ul {
    margin: 0;
    padding-left: 14px;
  }

  li {
    font-size: 0.78rem;
    line-height: 1.25rem;
  }

  .viewer-tooltip-name {
    font-size: 0.8rem;
    line-height: 1.2rem;
    font-weight: 600;
  }
}

.code {
  height: 100%;
  overflow: hidden;
  display: flex;
  flex-direction: row;
  width: 100%;

  --resizer-width: 14px;
  --editor-width: 100%;
  --preview-width: 0%;

  .editor {
    width: 100%;
    min-width: 0;
  }

  &.has-result > .editor {
    width: var(--editor-width);
  }

  > .result {
    width: var(--preview-width);
    min-width: 0;
  }

  &.preview-collapsed > .result {
    display: none;
  }

  .code-resizer {
    width: var(--resizer-width);
    flex: 0 0 var(--resizer-width);
    position: relative;
    touch-action: none;
    cursor: col-resize;

    .preview-toggle {
      position: absolute;
      left: 0;
      right: 0;
      top: 50%;
      margin: 0 auto;
      transform: translateY(-50%);
      width: var(--resizer-width);
      height: 34px;
      border-radius: 0;
      border: 0;
      padding: 0;
      font-size: 12px;
      line-height: 1;
      color: var(--bulma-text);
      background: color-mix(in srgb, var(--bulma-text) 12%, transparent);
      cursor: pointer;
      z-index: 3;
      transition: background-color 0.12s ease;

      &:hover,
      &:focus-visible {
        background: color-mix(in srgb, var(--bulma-text) 24%, transparent);
      }
    }

    .resizer-grip {
      position: absolute;
      left: 50%;
      transform: translateX(-50%);
      width: 14px;
      height: 22px;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      z-index: 2;
      pointer-events: none;
    }

    .grip-top {
      top: 25%;
      transform: translate(-50%, -50%);
    }

    .grip-bottom {
      top: 75%;
      transform: translate(-50%, -50%);
    }

    .grip-dots {
      width: 4px;
      height: 4px;
      border-radius: 999px;
      background: color-mix(in srgb, var(--bulma-text) 55%, transparent);
      box-shadow:
        0 -6px 0 color-mix(in srgb, var(--bulma-text) 55%, transparent),
        0  6px 0 color-mix(in srgb, var(--bulma-text) 55%, transparent);
      transition: background-color 0.12s ease, box-shadow 0.12s ease;
    }

  }

  @media (max-width: 1023px) {
    .code-resizer {
      display: none;
    }

    &.has-result {
      > .editor,
      > .result {
        width: 100%;
      }

      > .result {
        display: none;
      }

      &.mobile-preview-active > .editor {
        display: none;
      }

      &.mobile-preview-active > .result {
        display: block;
      }
    }
  }

}

.mobile-preview-toggle {
  display: none;
}

@media (max-width: 1023px) {
  .mobile-preview-toggle {
    display: inline-flex;
  }
}
</style>
