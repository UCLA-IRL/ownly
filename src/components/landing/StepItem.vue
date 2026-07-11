<template>
  <div class="step-item">
    <div class="step-content">
      <div class="step-left">
        <div class="step-number">{{ number }}</div>
      </div>
      <div class="step-right">
        <div v-if="title" class="step-title">{{ title }}</div>
        <div class="step-text">
          <slot />
        </div>
        <div v-if="$slots.visual" class="step-visual">
          <slot name="visual" />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
defineProps({
  number: {
    type: Number,
    required: true,
  },
  title: {
    type: String,
    default: '',
  },
});
</script>

<style scoped lang="scss">
.step-item {
  display: flex;
  margin-bottom: 2rem;
  width: 100%;
}

.step-content {
  display: flex;
  flex-direction: row;
  gap: 1rem;
}

.step-left {
  flex-shrink: 0;
}

.step-right {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.step-number {
  width: 28px;
  height: 28px;
  background: rgba(255, 255, 255, 0.15);
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 600;
  font-size: 0.9rem;
  color: inherit;
}

.step-title {
  font-size: 1.1rem;
  font-weight: 600;
  color: inherit;
}

.step-text {
  color: inherit;
  line-height: 1.6;
  font-size: 1rem;

  strong {
    font-weight: 600;
  }
}

.step-visual {
  display: flex;
  gap: 1rem;
  margin-top: 0.5rem;
}
</style>
