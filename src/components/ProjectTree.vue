<template>
  <ul :class="['menu-list', 'project-tree', { nested: props.depth > 0, outermost: props.depth === 0 }]">
    <li v-for="entry in tree" :key="getEntryPath(entry)" class="tree-node">
      <!-- Rename -->
      <ProjectTreeInput
        v-if="renameEntry?.key === getEntryPath(entry)"
        class="tree-input"
        :name="renameEntry.name"
        @cancel="renameEntry = null"
        @done="executeRename"
      />

      <!-- Folder row -->
      <div
        v-else-if="entry.is_folder"
        class="tree-row"
        :class="{ open: isFolderOpen(entry) }"
      >
        <button
          type="button"
          class="tree-disclosure"
          :aria-label="isFolderOpen(entry) ? 'Collapse folder' : 'Expand folder'"
          :aria-expanded="isFolderOpen(entry)"
          :aria-controls="getFolderControlsId(entry)"
          @click.stop="toggleFolder(entry)"
        >
          <FontAwesomeIcon
            class="disclosure-icon"
            :icon="isFolderOpen(entry) ? faChevronDown : faChevronRight"
            size="sm"
          />
        </button>

        <button type="button" class="tree-hit" :title="entry.name" @click="toggleFolder(entry)">
          <span class="tree-label">
            <FontAwesomeIcon class="tree-icon" :icon="chooseIcon(entry)" size="sm" />
            <span class="tree-name">{{ entry.name }}</span>
          </span>
        </button>

        <ProjectTreeMenu
          class="tree-actions"
          :allow-new="true"
          :allow-delete="true"
          :allow-rename="true"
          @new-file="onSubtree(entry, (t) => t.newHere('file', $event))"
          @new-folder="onSubtree(entry, (t) => t.newHere('folder', $event))"
          @import="onSubtree(entry, (t) => t.importHere())"
          @import-zip="onSubtree(entry, (t) => t.importZipHere())"
          @export="executeExport(entry)"
          @rename="renameEntry = { ...entry, key: getEntryPath(entry) }"
          @delete="executeDelete(entry)"
        />
      </div>

      <!-- File row -->
      <div v-else class="tree-row file-row" :class="{ active: isFileActive(entry) }">

        <router-link class="tree-hit" :to="linkToFile(entry)" :title="entry.name">
          <span class="tree-label">
            <FontAwesomeIcon class="tree-icon" :icon="chooseIcon(entry)" size="sm" />
            <span class="tree-name">{{ entry.name }}</span>
          </span>
        </router-link>

        <ProjectTreeMenu
          class="tree-actions"
          :allow-new="false"
          :allow-delete="true"
          :allow-rename="true"
          @export="executeExport(entry)"
          @rename="renameEntry = { ...entry, key: getEntryPath(entry) }"
          @delete="executeDelete(entry)"
        />
      </div>

      <!-- Children -->
      <ProjectTree
        ref="subtrees"
        v-if="isFolderOpen(entry)"
        :id="getFolderControlsId(entry)"
        :project="props.project"
        :files="[]"
        :rtree="entry.children ?? []"
        :path="`${path}${entry.name}/`"
        :parent="entry.name"
        :depth="props.depth + 1"
      />
    </li>

    <ProjectTreeInput
      v-if="newFile.show"
      class="tree-input"
      @cancel="newFile.show = false"
      @done="executeNew"
    />
  </ul>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, useTemplateRef, watch, type PropType } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import * as zip from '@zip.js/zip.js';

import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome';
import {
  faFolder,
  faFolderOpen,
  faFile,
  faFileCode,
  faFileWord,
  faFilePowerpoint,
  faFileExcel,
  faFilePdf,
  faFileImage,
  faChevronRight,
  faChevronDown,
} from '@fortawesome/free-solid-svg-icons';

import ProjectTreeInput from './ProjectTreeInput.vue';
import ProjectTreeMenu from './ProjectTreeMenu.vue';

import { Workspace } from '@/services/workspace';
import * as utils from '@/utils';
import { Toast } from '@/utils/toast';

import type { IProject, IProjectFile } from '@/services/types';

type TreeEntry = {
  name: string;
  children?: TreeEntry[];
  is_folder?: boolean;
};

type RenameEntry = TreeEntry & { key: string };

const props = defineProps({
  project: {
    type: Object as PropType<IProject>,
    required: true,
  },
  files: {
    type: Array as PropType<IProjectFile[]>,
    required: true,
  },
  rtree: {
    type: Array as PropType<TreeEntry[]>,
    required: false,
  },
  path: {
    type: String,
    required: false,
    default: '/',
  },
  parent: {
    type: String,
    required: false,
    default: '',
  },
  depth: {
    type: Number,
    required: false,
    default: 0,
  },
});

const route = useRoute();
const router = useRouter();

// vue-tsc is currently broken for the type inference
const subtrees = useTemplateRef<Array<any>>('subtrees');

const newFile = ref({
  show: false,
  type: 'file' as 'file' | 'folder',
  ext: null as string | null,
});

const renameEntry = ref<RenameEntry | null>(null);

defineExpose({ newHere, importHere, importZipHere, executeExport, parent: props.parent });
onMounted(checkRoute);
watch(() => route.params.filename, checkRoute);

const splitPath = computed(() => props.path.split('/').filter(Boolean));

/**
 * Tree computes the tree structure from the flat files list.
 * If this component is recursive, then rtree should be passed and is used instead.
 */
const tree = computed<TreeEntry[]>(() => {
  if (props.rtree) return props.rtree;

  const tree: TreeEntry[] = [];

  const fillPrefix = (path: string) => {
    const parts = path.split('/');
    let current = tree;

    for (let i = 0; i < parts.length; i++) {
      const part = parts[i];
      if (!part) continue;
      const isFolder = i !== parts.length - 1;

      const existing = current.find((e) => e.name === part);
      if (existing) {
        if (isFolder) {
          existing.is_folder = true;
          existing.children = existing.children ?? [];
          current = existing.children;
        }
      } else {
        const newEntry: TreeEntry = {
          name: part,
          is_folder: isFolder,
        };

        if (isFolder) {
          newEntry.children = [];
        }

        current.push(newEntry);
        if (isFolder) {
          current = newEntry.children!;
        }
      }
    }
  };

  for (const file of props.files) {
    fillPrefix(file.path);
  }

  const sortTree = (t: TreeEntry[]) => {
    t.sort((a, b) => {
      if (a.is_folder && !b.is_folder) return -1;
      if (!a.is_folder && b.is_folder) return 1;
      return a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true });
    });
    for (const entry of t) {
      if (entry.children) sortTree(entry.children);
    }
  };
  sortTree(tree);

  return tree;
});

// Auto-open child folders for the current route
function checkRoute() {
  if (!route.params.filename) return;

  const parts = route.params.filename as string[];
  if (parts.length <= 1) return;

  const myparts = splitPath.value;
  if (parts.length <= myparts.length) return;

  if (parts.slice(0, myparts.length).every((p, i) => p === myparts[i])) {
    const folder = parts[myparts.length];
    const entry = tree.value.find((e) => e.name === folder);
    if (entry) toggleFolder(entry, true);
  }
}

/** Get router link to a file */
function linkToFile(entry: TreeEntry) {
  return {
    name: 'project-file',
    params: {
      space: route.params.space as string,
      project: props.project.name,
      filename: splitPath.value.concat(entry.name),
    },
  };
}

function isFileActive(entry: TreeEntry) {
  const routeFile = route.params.filename;
  const routeParts = Array.isArray(routeFile) ? routeFile : routeFile ? [routeFile] : [];
  const entryParts = splitPath.value.concat(entry.name);
  return (
    routeParts.length === entryParts.length && routeParts.every((part, index) => part === entryParts[index])
  );
}

/** Get the project path to an entry */
function getEntryPath(entry: TreeEntry) {
  return `${props.path}${entry.name}${entry.is_folder ? '/' : ''}`;
}

function getFolderControlsId(entry: TreeEntry) {
  return `project-tree-${encodeURIComponent(getEntryPath(entry))}`;
}

/** Choose an icon for a given entry */
function chooseIcon(entry: TreeEntry) {
  if (entry.is_folder) {
    return isFolderOpen(entry) ? faFolderOpen : faFolder;
  }

  switch (utils.getExtensionType(entry.name)) {
    case 'code':
    case 'latex':
      return faFileCode;
    case 'word':
    case 'milkdown':
      return faFileWord;
    case 'ppt':
      return faFilePowerpoint;
    case 'excel':
      return faFileExcel;
    case 'pdf':
      return faFilePdf;
    case 'image':
    case 'excalidraw':
      return faFileImage;
    default:
      return faFile;
  }
}

/** Map of open folders (keyed by full path to avoid name collisions) */
const foldersOpen = ref<Record<string, boolean>>({});

function isFolderOpen(entry: TreeEntry) {
  if (!entry.is_folder) return false;
  return !!foldersOpen.value[getEntryPath(entry)];
}

function toggleFolder(entry: TreeEntry, val?: boolean) {
  if (!entry.is_folder) return;
  const k = getEntryPath(entry);
  foldersOpen.value[k] = val !== undefined ? val : !foldersOpen.value[k];
}

/** Run the callback on a subtree component */
function onSubtree(subfolder: TreeEntry, callback: (subtree: any) => void) {
  if (!subfolder.is_folder) return;
  toggleFolder(subfolder, true);

  nextTick(() => {
    const subtree = subtrees.value?.find((t) => t?.parent === subfolder.name);
    if (subtree) callback(subtree);
  });
}

/** Create a new file in the current folder */
async function newHere(type: 'file' | 'folder', ext?: string) {
  newFile.value = {
    show: true,
    type: type,
    ext: ext || String(),
  };
}

/** Create a new file or folder */
async function executeNew(name: string) {
  if (name.length < 1 || name.length > 40) {
    Toast.error('File and folder name must be between 1 and 40 characters');
    return;
  }

  if (name.includes('/')) {
    Toast.error('File and folder name cannot contain slashes');
    return;
  }

  let path = `${props.path}${name}`;
  if (newFile.value.type === 'folder') {
    path += '/';
  }

  const ext = newFile.value.ext;
  if (ext && !path.endsWith(ext)) {
    path += `.${ext}`;
  }

  try {
    const proj = await Workspace.setupAndGetActiveProj(router);
    await proj.newFile(path);
  } catch (err) {
    console.error(err);
    Toast.error(`Error creating ${name}: ${err}`);
    return;
  }

  newFile.value.show = false;
}

/** Delete a file or folder */
async function executeDelete(entry: TreeEntry) {
  const path = getEntryPath(entry);

  // TODO: confirmation prompt
  try {
    const proj = await Workspace.setupAndGetActiveProj(router);
    await proj.deleteFile(path);
  } catch (err) {
    console.error(err);
    Toast.error(`Error deleting ${path}: ${err}`);
  }
}

/** Import files from user */
async function importHere() {
  const proj = await Workspace.setupAndGetActiveProj(router);

  const files = await utils.selectFiles({ multiple: true });
  if (!files.length) return;

  for (const file of files) {
    try {
      const path = `${props.path}${file.name}`;
      await Toast.promise(proj.importFile(path, file.stream()), {
        pending: `Importing ${file.name}`,
        success: `Imported ${file.name}`,
        error: `Could not import ${file.name}`,
      });
    } catch (err) {
      console.warn(err);
    }
  }
}

/** Import a ZIP file from user */
async function importZipHere() {
  const proj = await Workspace.setupAndGetActiveProj(router);

  const files = await utils.selectFiles({
    accept: '.zip',
    multiple: false,
  });

  const zipFile = files[0] ?? null;
  if (!zipFile) return;

  const reader = new zip.ZipReader(new zip.BlobReader(zipFile));
  const progress = Toast.loading(`Importing files from ${zipFile.name}`);

  let importedCount = 0;
  for await (const entry of reader.getEntriesGenerator()) {
    try {
      if (entry.directory) continue;

      await progress.msg(`Importing ${entry.filename} from ${zipFile.name}`);

      const writer = new zip.BlobWriter();
      await entry.getData?.(writer);
      const content = await writer.getData();

      const path = `${props.path}${entry.filename}`;
      await proj.importFile(path, content.stream());
      importedCount++;
    } catch (err) {
      console.warn(err);
      Toast.warning(`Could not import ${entry.filename}: ${err}`);
    }
  }

  if (importedCount > 0) {
    progress.success(`Imported ${importedCount} files from ${zipFile.name}`);
  } else {
    progress.warning(`No files imported from ${zipFile.name}`);
  }
}

/** Export a file or folder */
async function executeExport(entry: TreeEntry | null) {
  const path = entry ? getEntryPath(entry) : props.path;
  try {
    const proj = await Workspace.setupAndGetActiveProj(router);
    await proj.download(path);
  } catch (err) {
    console.error(err);
    Toast.error(`Error exporting ${path}: ${err}`);
  }
}

/** Rename a file or folder */
async function executeRename(name: string) {
  if (!renameEntry.value) return;

  try {
    const proj = await Workspace.setupAndGetActiveProj(router);

    const oldPath = getEntryPath(renameEntry.value);
    const newPath = getEntryPath({ ...renameEntry.value, name });
    await proj.moveFile(oldPath, newPath);

    renameEntry.value = null;
  } catch (err) {
    console.error(err);
    Toast.error(`Error renaming ${renameEntry.value!.name}: ${err}`);
  }
}
</script>

<style lang="scss" scoped>
/* Overleaf-like file tree look (compact rows, chevrons, subtle guides, active bar) */
.project-tree {
  --tree-row-height: 28px;
  --tree-font-size: 13px;

  --tree-pad-x: 8px;
  --tree-action-size: 22px;
  --tree-chevron-size: 18px;

  --tree-line-left-gap: 12px;
  --tree-line-right-gap: 10px;
  --tree-indent-step: calc(var(--tree-line-left-gap) + 1px + var(--tree-line-right-gap));

  --tree-guide: rgba(255, 255, 255, 0.12);
  --tree-hover: rgba(255, 255, 255, 0.06);
  --tree-active-bg: rgba(255, 255, 255, 0.08);
  --tree-active-bar: var(--sidebar-highlight-bg);
  --tree-connector-x: var(--tree-line-left-gap);

  list-style: none;
  margin: 0;
  padding: 0;
  width: 100%;
  box-sizing: border-box;
  position: relative;

  font-size: var(--tree-font-size);
  color: white;

  > li {
    margin: 0;
    padding: 0;
    width: 100%;
    box-sizing: border-box;
  }

  &.nested {
    border-left: 0 !important;
    margin-top: 0 !important;
    margin-bottom: 0 !important;
    --tree-connector-x: calc(-1 * var(--tree-indent-step) + var(--tree-line-left-gap));
    margin-left: var(--tree-indent-step);
    width: calc(100% - var(--tree-indent-step));

    > li {
      position: relative;

      &::before {
        content: '';
        position: absolute;
        left: var(--tree-connector-x);
        top: 0;
        bottom: 0;
        width: 1px;
        background: var(--tree-guide);
        pointer-events: none;
      }

      &::after {
        content: '';
        position: absolute;
        left: calc(var(--tree-connector-x) + 1px);
        top: calc(var(--tree-row-height) / 2);
        width: calc(var(--tree-line-right-gap) - 1px);
        height: 1px;
        background: var(--tree-guide);
        pointer-events: none;
      }

      &:last-child::before {
        bottom: calc(100% - (var(--tree-row-height) / 2) - 1px);
      }
    }
  }

  &.outermost {
    border-inline-start: 0 !important;
    margin-inline-start: 0 !important;
    padding-inline-start: 0 !important;
  }
}

.tree-row {
  display: flex;
  align-items: center;
  width: 100%;
  min-height: var(--tree-row-height);
  border-radius: 6px;
  box-sizing: border-box;
  margin: 0;
  padding-right: calc(var(--tree-action-size) + 8px);
  color: inherit;

  position: relative;

  &:hover,
  &:focus-within {
    background: var(--tree-hover);
  }

  /* Active file: subtle background + left bar like Overleaf */
  &.active {
    background: var(--tree-active-bg);

    &::before {
      content: '';
      position: absolute;
      left: 0;
      top: 0;
      bottom: 0;
      width: 3px;
      border-radius: 6px 0 0 6px;
      background: var(--tree-active-bar);
      z-index: 0;
      pointer-events: none;
    }
  }

  &.active:hover,
  &.active:focus-within {
    background: var(--tree-active-bg);
  }
}

.tree-row.file-row {
  padding-left: 2px;
}

.tree-row.file-row .tree-hit {
  padding-left: 12px;
}

.tree-disclosure {
  flex: 0 0 var(--tree-chevron-size);
  width: var(--tree-chevron-size);
  height: var(--tree-row-height);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  margin-left: 3px;
  border: 0;
  background: transparent;
  color: inherit !important;
  opacity: 1;
  cursor: pointer;
  position: relative;
  z-index: 5;
  overflow: visible;

  &.spacer {
    visibility: hidden;
    cursor: default;
  }

  &:hover {
    opacity: 1;
  }

  &:focus-visible {
    outline: 2px solid rgba(255, 255, 255, 0.25);
    outline-offset: 2px;
    border-radius: 4px;
  }
}

.disclosure-icon {
  color: inherit !important;
  opacity: 1;
  font-size: 0.85rem;
}

.tree-disclosure :deep(svg) {
  display: block;
  color: inherit !important;
  opacity: 1;
}

.tree-hit {
  flex: 1 1 auto;
  min-width: 0;
  max-width: 100%;
  height: var(--tree-row-height);
  padding: 0 var(--tree-pad-x);
  display: flex;
  align-items: center;
  gap: 8px;

  border-radius: 6px;
  color: inherit;
  text-decoration: none;
  background: transparent;
  border: 0;
  cursor: pointer;
  user-select: none;
  position: relative;
  z-index: 1;

  /* Make anchor and button look identical */
  &:focus-visible {
    outline: 2px solid rgba(255, 255, 255, 0.25);
    outline-offset: 2px;
  }
}

.tree-label {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  width: 100%;
}

.tree-icon {
  flex: 0 0 auto;
  opacity: 1;
}

.tree-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: inherit;
  opacity: 1;
}

/* Actions: behave like Overleaf (hidden until hover, always on active row) */
.tree-actions {
  width: var(--tree-action-size);
  height: var(--tree-action-size);
  margin: 0;
  padding: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;

  position: absolute;
  right: 4px;
  top: 50%;
  transform: translateY(-50%);

  background: transparent !important;
  color: inherit;
  transition: background-color 0.12s ease;

  opacity: 0;
  pointer-events: none;
}

.tree-row:hover .tree-actions,
.tree-row:focus-within .tree-actions,
.tree-row.active .tree-actions {
  opacity: 1;
  pointer-events: auto;
}

.tree-row:hover .tree-actions,
.tree-row:focus-within .tree-actions {
  background-color: rgba(0, 0, 0, 0.18) !important;
}

.tree-row:hover .tree-actions:hover,
.tree-row:focus-within .tree-actions:hover,
.tree-row:hover .tree-actions:focus-visible,
.tree-row:focus-within .tree-actions:focus-visible {
  background-color: rgba(0, 0, 0, 0.3) !important;
}

:deep(.tree-input input) {
  width: 100%;
  height: var(--tree-row-height);
  min-height: var(--tree-row-height);
  box-sizing: border-box;
  margin: 1px 0;
  border-radius: 6px;
  padding: 0 var(--tree-pad-x);
  font-size: var(--tree-font-size);
  line-height: 1;
}
</style>