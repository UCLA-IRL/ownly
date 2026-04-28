<template>
  <div ref="outer" class="outer"></div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, useTemplateRef, watch, type PropType } from 'vue';

import * as monaco from 'monaco-editor';
import editorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker';
import jsonWorker from 'monaco-editor/esm/vs/language/json/json.worker?worker';

import * as Y from 'yjs';
import { MonacoBinding } from 'y-monaco';
import type { Awareness } from 'y-protocols/awareness.js';

import * as utils from '@/utils';
import { useThemeWatch } from '@/utils';
import { monacoRegister } from '@/utils/monaco';

self.MonacoEnvironment = {
  getWorker(_: any, label: string) {
    if (label === 'json') return new jsonWorker();
    return new editorWorker();
  },
};

const props = defineProps({
  ytext: {
    type: Object as PropType<Y.Text>,
    required: true,
  },
  awareness: {
    type: Object as PropType<Awareness>,
    required: true,
  },
  basename: {
    type: String,
    required: true,
  },
});

const outer = useTemplateRef('outer');

let editor: monaco.editor.IStandaloneCodeEditor | null = null;
let ybinding: MonacoBinding | null = null;
let unwatchTheme: (() => void) | null = null;

watch(
  () => props.ytext,
  () => {
    destroy();
    create();
  },
);
onMounted(create);
onBeforeUnmount(destroy);

function create() {
  monacoRegister();

  const ext = props.basename.split('.').pop()?.toLocaleLowerCase();
  let language = 'plaintext';
  switch (ext) {
    case 'md':
      language = 'markdown';
      break;
    case 'json':
      language = 'json';
      break;
    case 'tex':
    case 'latex':
    case 'sty':
    case 'cls':
      language = 'latex';
      break;
    case 'bib':
      language = 'bibtex';
      break;
  }

  editor = monaco.editor.create(outer.value!, {
    value: String(),
    language: language,
    theme: utils.themeIsDark() ? 'vs-dark' : 'vs',
    automaticLayout: true,
    scrollBeyondLastLine: false,
    minimap: { enabled: false },
    suggest: { showWords: false },
    wordWrap: 'on',
    padding: { top: 20, bottom: 10 },
  });

  applyEditorTheme();
  unwatchTheme = useThemeWatch(applyEditorTheme);

  ybinding = new MonacoBinding(
    props.ytext,
    editor!.getModel()!,
    new Set([editor]),
    props.awareness,
  );
}

function destroy() {
  unwatchTheme?.();
  unwatchTheme = null;
  ybinding?.destroy();
  editor?.getModel()?.dispose();
  editor?.dispose();
}

function applyEditorTheme() {
  monaco.editor.setTheme(utils.themeIsDark() ? 'vs-dark' : 'vs');
}
</script>

<style scoped lang="scss">
.outer {
  height: 100%;
}
</style>

<style lang="scss">
.yRemoteSelection {
  background-color: orange;
  opacity: 0.4;
}
.yRemoteSelectionHead {
  position: absolute;
  border-left: 2px solid black;
  border-color: orange;
  height: 100%;
  transition: opacity 0.1s ease;
}
.yRemoteSelectionHead::after {
  position: absolute;
  content: '';
  background-color: orange;
  transform: translateY(-100%) translateX(-2px);
  border-radius: 3px;
  padding: 0 3px;
  font-size: 10pt;
  color: var(--bulma-text-strong);
  animation: fade90 4s forwards;
}
</style>
