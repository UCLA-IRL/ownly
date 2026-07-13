<template>
  <div class="demo-dashboard has-background-primary soft-if-dark">
    <div class="spacelist">
      <div class="create box">
        <div class="box-content">
          <div class="block">
            <div class="media-content">
              <p class="subtitle is-5 soft-if-dark">Don't see what you are looking for?</p>
            </div>
          </div>

          <div class="content has-text-right">
            <button class="button mr-2 mb-2 is-small-caps">
              Create a new workspace
            </button>
            <button class="button mr-2 is-primary is-small-caps soft-if-dark">
              Join a workspace
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
defineProps({
  highlightCreate: {
    type: Boolean,
    default: false,
  },
  highlightJoin: {
    type: Boolean,
    default: false,
  },
});
</script>

<style scoped lang="scss">
.demo-dashboard {
  border-radius: 8px;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.25);
  overflow: hidden;
}

.spacelist {
  margin: 0;
}

.box {
  border: none;
  box-shadow: none;
}
</style>
