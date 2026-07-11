<template>
  <div class="box demo-modal">
    <div class="title is-5 mb-4">Join Workspace</div>

    <div class="field">
      <label class="label">Dashboard Label</label>
      <div class="control">
        <input class="input" type="text" placeholder="Marketing Team" />
      </div>
      <p class="help">A readable label for the workspace on your dashboard</p>
    </div>

    <div class="field">
      <label class="label">NDN Name</label>
      <div class="control">
        <input class="input" type="text" placeholder="/my/awesome/workspace" />
      </div>
      <p class="help">Unique network identifier of the workspace</p>
    </div>

    <div class="field">
      <label class="label">Pre-Shared Key</label>
      <div class="control">
        <input class="input" type="text" placeholder="..." />
      </div>
      <p class="help">Ask the owner of the workspace for the key</p>
    </div>

    <div class="field has-text-right">
      <div class="control">
        <button class="button mr-2">Cancel</button>
        <button class="button is-primary">Join</button>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.demo-modal {
  width: 400px;
  max-width: 100%;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.25);
}
</style>
