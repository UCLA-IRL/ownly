<template>
  <div class="pdfviewer" ref="pdfviewer">
    <!-- Loading / Compiling Spinner -->
    <LoadingSpinner v-if="!loaded && pdf && !displayError" class="absolute-center" />

    <!-- Watermark logo -->
    <img alt="logo" class="logo invert-if-dark" src="@/assets/logo-white.svg" />

    <!-- Top toolbar -->
    <div class="pdf-toolbar">
      <div class="left">
        <button
          v-if="hasCompile"
          @click="$emit('compile')"
          class="button is-primary is-small soft-if-dark"
          :disabled="compiling"
        >
          Compile
        </button>
        <button class="button is-small soft-if-dark ml-1" :disabled="!pdf" @click="download">
          Download
        </button>
      </div>
      <div class="right">
        <button
          class="button is-small soft-if-dark"
          @click="width = Math.max(width - 100, 200)"
          :disabled="!loaded || width <= 200"
        >
          <FontAwesomeIcon :icon="faMinus" />
        </button>
        <button
          class="button is-small soft-if-dark ml-1"
          @click="width = Math.min(width + 100, 5000)"
          :disabled="!loaded || width >= 5000"
        >
          <FontAwesomeIcon :icon="faPlus" />
        </button>
      </div>
    </div>

    <!-- PDF Viewer -->
    <div class="pdf-content">
      <VuePdfEmbed
        v-if="pdfCopy && width"
        annotation-layer
        text-layer
        :source="pdfCopy"
        :width="width"
        @loaded="loaded = true"
        @loading-failed="handlePdfError"
        @password-requested="handlePasswordRequest"
        @rendering-failed="handlePdfError"
      />
    </div>

    <!-- Error message -->
    <div v-if="displayError" class="error">{{ displayError }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, shallowRef, useTemplateRef, watch } from 'vue';

import LoadingSpinner from '@/components/LoadingSpinner.vue';

import VuePdfEmbed from 'vue-pdf-embed';
import 'vue-pdf-embed/dist/styles/annotationLayer.css';
import 'vue-pdf-embed/dist/styles/textLayer.css';

import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome';
import { faMinus, faPlus } from '@fortawesome/free-solid-svg-icons';

defineEmits(['compile']);

const props = defineProps({
  basename: {
    type: String,
    required: false,
    default: 'document.pdf',
  },
  hasCompile: {
    type: Boolean,
    required: false,
    default: false,
  },
  pdf: {
    type: [Uint8Array, null],
    required: false,
  },
  compiling: {
    type: Boolean,
    required: false,
    default: false,
  },
  error: {
    type: String,
    required: false,
    default: String(),
  },
});

const pdfviewer = useTemplateRef('pdfviewer');
const width = ref(0);
const loaded = ref(false);
const pdfCopy = shallowRef<Uint8Array | null>(null);
const viewerError = ref(String());
const displayError = computed(() => props.error || viewerError.value);

watch(() => props.pdf, create);

onMounted(() => {
  width.value = Math.max((pdfviewer.value?.clientWidth ?? 800) - 20, 400);
  width.value = Math.min(Math.max(width.value, 400), 1000);
  create();
});

function create() {
  loaded.value = false;
  viewerError.value = String();
  // Copy the pdf to prevent viewer from destroying the original
  // This is needed for the download button to work
  pdfCopy.value = props.pdf ? new Uint8Array(props.pdf) : null;
}

function handlePdfError(err: unknown) {
  loaded.value = true;
  viewerError.value = `Failed to render PDF: ${formatError(err)}`;
}

function handlePasswordRequest() {
  loaded.value = true;
  viewerError.value = 'Password-protected PDFs are not supported.';
}

function formatError(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

async function download() {
  if (!props.pdf) return;
  const fileStream = _o.streamSaver.createWriteStream(props.basename);
  const writer = fileStream.getWriter();
  await writer.write(props.pdf);
  await writer.close();
}
</script>

<style lang="scss">
// https://github.com/wojtekmaj/react-pdf/issues/1815
.hiddenCanvasElement {
  display: none;
}
</style>

<style scoped lang="scss">
.pdfviewer {
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100%;
  background-color: var(--bulma-scheme-main-bis);
  overflow: hidden;
  padding-left: 4px;

  .pdf-toolbar {
    display: flex;
    justify-content: space-between;
    padding: 4px 0;
    padding-right: 4px;
    width: 100%;
    > .left {
      flex: 1;
      display: flex;
      justify-content: flex-start;
    }
    > .right {
      flex: 1;
      display: flex;
      justify-content: flex-end;
    }
  }

  > .pdf-content {
    overflow-y: scroll;
    overflow-x: auto;
    flex: 1;
    text-align: center;
    margin-bottom: 10px;

    :deep(.vue-pdf-embed) {
      display: inline-block;

      .vue-pdf-embed__page {
        margin-bottom: 10px;
      }
    }
  }

  > .logo {
    width: 80%;
    max-width: 200px;
    position: absolute;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    pointer-events: none;
  }

  > .error {
    text-align: left;
    position: absolute;
    top: 40px;
    left: 0;
    background-color: rgba(100, 0, 0, 0.85);
    width: 100%;
    font-family: monospace;
    white-space: pre;
    padding: 10px;
    max-height: 80%;
    overflow: auto;
    color: white;
  }
}
</style>
