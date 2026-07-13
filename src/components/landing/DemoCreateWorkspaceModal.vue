<template>
  <div class="box soft-if-dark demo-modal">
    <div class="title is-5 mb-4">Create Workspace</div>

    <div class="field">
      <label class="label">Dashboard Label</label>
      <div class="control">
        <input class="input" type="text" placeholder="Marketing Team" />
      </div>
      <p class="help">A readable label for the workspace on your dashboard</p>
    </div>

    <div class="field">
      <label class="label">Name Identifier</label>
      <div class="control">
        <input class="input" type="text" placeholder="marketing-team" />
      </div>
      <p class="help">A unique identifier for the workspace, without spaces or special characters</p>
    </div>

    <div>
      Your workspace will have the network name &ndash;<br />
      <code>/ndn/username/marketing-team</code>
    </div>

    <div class="field mt-2 has-text-right">
      <div class="control">
        <button class="button mr-2">Cancel</button>
        <button class="button is-primary">Create</button>
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
