<template>
  <div class="box soft-if-dark demo-modal">
    <div class="title is-5 mb-4">{{ title }}</div>
    <p>{{ description }}</p>
    <div class="invite-link-box mt-1">
      <code class="select-all link">{{ link }}</code>
      <button class="button invitee-list-action soft-if-dark">
        <FontAwesomeIcon :icon="faCopy" />
      </button>
    </div>
    <p class="mt-2">{{ inputLabel }}</p>
    <div class="field has-addons mt-2">
      <div class="control is-expanded">
        <input class="input soft-if-dark" type="text" :placeholder="placeholder" disabled />
      </div>
      <div class="control">
        <button class="button is-primary soft-if-dark" disabled>Add</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome';
import { faCopy } from '@fortawesome/free-solid-svg-icons';

defineProps({
  title: {
    type: String,
    default: 'Invite people',
  },
  description: {
    type: String,
    default: 'An invitation will be generated for each email address.',
  },
  link: {
    type: String,
    default: 'https://app.ownly.dev/join/my-workspace...',
  },
  inputLabel: {
    type: String,
    default: 'Enter an email address or NDN name below',
  },
  placeholder: {
    type: String,
    default: 'name@example.com or /user-name',
  },
});
</script>

<style scoped lang="scss">
.demo-modal {
  width: 450px;
  max-width: 100%;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.25);
}

.invite-link-box {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem;
  border-radius: 6px;
  background: rgba(0, 0, 0, 0.05);
  overflow-x: auto;

  code {
    background: transparent;
    padding: 0;
    word-break: break-all;
    font-size: 0.85rem;
    flex: 1;
  }

  .invitee-list-action {
    flex-shrink: 0;
    width: 2rem;
    height: 2rem;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
  }
}
</style>
