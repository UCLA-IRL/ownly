<template>
  <div class="mdviewer" ref="mdviewer">
    <!-- Markdown Viewer -->
    <div class="content">
      <div v-html="mdHtml"></div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch, type PropType } from 'vue';

import * as Y from 'yjs';
import 'highlight.js/styles/vs.min.css';
import { renderMarkdown } from '@/utils/sanitize';

const mdHtml = ref('');
const mdText = ref('');

const props = defineProps({
  ytext: {
    type: Object as PropType<Y.Text>,
    required: true,
  },
  basename: {
    type: String,
    required: true,
  },
});

const observeText = async () => {
  mdText.value = props.ytext.toString();
  mdHtml.value = await renderMarkdown(mdText.value);
};

const create = async () => {
  props.ytext.observe(observeText);
  await observeText();
};

const destroy = () => {
  props.ytext.unobserve(observeText);
};

watch(
  () => props.ytext,
  () => {
    destroy();
    create();
  },
);
onMounted(create);
onBeforeUnmount(destroy);
</script>

<style scoped lang="scss">
.mdviewer {
  overflow-y: auto;
  padding: 1em;

  // Override bulma's overly aggressive font-weight
  // for headings in markdown
  :deep(:is(h1, h2, h3, h4, h5, h6)) {
    font-weight: 500;
  }
}
</style>
