<template>
  <div class="revealviewer" ref="revealviewer" v-html="mdHtml"></div>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, useTemplateRef, watch, type PropType } from 'vue';

import * as Y from 'yjs';
import Reveal from 'reveal.js';
import Markdown from 'reveal.js/plugin/markdown/markdown.esm.js';
import Highlight from 'reveal.js/plugin/highlight/highlight.esm.js';
// import 'highlight.js/styles/vs.min.css';
import 'reveal.js/dist/reveal.css';
import 'reveal.js/dist/theme/solarized.css';
import { debounce } from 'lodash-es';
import { escapeHtml, sanitizeHtml } from '@/utils/sanitize';

const mdText = ref('');
const mdHtml = ref('');
const revealDeck = ref(null as Reveal.Api | null);
const revealviewer = useTemplateRef('revealviewer');
let active = false;
let renderSeq = 0;

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

const refreshDeck = async (seq: number) => {
  if (!active || seq !== renderSeq || !revealviewer.value) return;

  const state = revealDeck.value?.getState();
  revealDeck.value?.destroy();
  const deck = new Reveal({
    embedded: true,
    slideNumber: true,
    plugins: [Markdown, Highlight],
    markdown: {
      sanitize: true,
      sanitizer: sanitizeHtml,
    },
    transition: 'none',
  });
  revealDeck.value = deck;
  await deck.initialize();
  if (!active || seq !== renderSeq) {
    deck.destroy();
    return;
  }

  // Scroll to current slide
  if (state) {
    deck.setState(state);
  }
};

const renderSlides = async () => {
  const seq = ++renderSeq;
  mdText.value = props.ytext.toString();
  mdHtml.value = `<div class="reveal">
      <div class="slides">
        <section data-markdown>
          <textarea data-template>${escapeHtml(mdText.value)}</textarea>
        </section>
      </div>
    </div>`;
  await nextTick();
  await refreshDeck(seq);
};

const observeText = debounce(() => {
  void renderSlides();
}, 250);

const create = async () => {
  active = true;
  props.ytext.observe(observeText);
  await renderSlides();
};

const destroy = () => {
  active = false;
  renderSeq += 1;
  observeText.cancel();
  props.ytext.unobserve(observeText);
  revealDeck.value?.destroy();
  revealDeck.value = null;
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
