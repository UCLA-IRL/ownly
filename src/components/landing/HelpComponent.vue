<template>
  <div class="content">
    <section id="creating-workspace" class="help-section">
      <h2>Creating a Workspace</h2>

      <StepItem :number="1" title="Go to Dashboard">
        Click on <strong>Dashboard</strong> in the sidebar to access your workspaces.
        <template #visual>
          <DemoSidebarHome :active-item="'dashboard'" />
        </template>
      </StepItem>

      <StepItem :number="2" title="Create Workspace">
        Click the <strong>Create a new workspace</strong> button.
        <template #visual>
          <DemoDashboard />
        </template>
      </StepItem>

      <StepItem :number="3" title="Fill in Details">
        Fill in the <strong>Dashboard Label</strong> and <strong>Name Identifier</strong> fields.
        <template #visual>
          <DemoCreateWorkspaceModal />
        </template>
      </StepItem>

      <StepItem :number="4" title="Start Collaborating">
        Your workspace is ready. Invite team members or create projects and channels.
      </StepItem>
    </section>

    <section id="joining-workspace" class="help-section">
      <h2>Joining a Workspace</h2>

      <StepItem :number="1" title="Go to Dashboard">
        Click on <strong>Dashboard</strong> in the sidebar to access your workspaces.
        <template #visual>
          <DemoSidebarHome :active-item="'dashboard'" />
        </template>
      </StepItem>

      <StepItem :number="2" title="Join Workspace">
        Click the <strong>Join a workspace</strong> button.
        <template #visual>
          <DemoDashboard />
        </template>
      </StepItem>

      <StepItem :number="3" title="Fill in Details">
        Fill in the <strong>Dashboard Label</strong>, <strong>NDN Name</strong>, and <strong>Pre-Shared Key</strong> fields.
        <template #visual>
          <DemoJoinWorkspaceModal />
        </template>
      </StepItem>

      <StepItem :number="4" title="Start Collaborating">
        You're now part of the workspace. Create projects and channels to collaborate.
      </StepItem>
    </section>

    <section id="inviting-others" class="help-section">
      <h2>Inviting Others</h2>
      <p>Here's how to invite someone to your workspace:</p>

      <StepItem :number="1" title="Join a Workspace (first)">
        The <strong>Invite people</strong> menu only appears when you're in a workspace.
      </StepItem>

      <StepItem :number="2" title="Open Invite Modal">
        Click <strong>Invite people</strong> in the sidebar under the Workspace section.
        <template #visual>
          <DemoSidebarWorkspace :active-item="'invite'" />
        </template>
      </StepItem>

      <StepItem :number="3" title="Copy the Invite Link">
        Click the <strong>Copy Invite Link</strong> button in the modal.
        <template #visual>
          <DemoModal
            title="Invite people to My Workspace"
            link="https://app.ownly.dev/join/my-workspace..."
          />
        </template>
      </StepItem>

      <StepItem :number="4" title="Send to Your Teammate">
        Share the link with your teammate via chat, email, or any method you prefer.
      </StepItem>

      <StepItem :number="5" title="They Join">
        Your teammate opens the link and clicks <strong>Join</strong> on the dashboard.
      </StepItem>
    </section>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue';
import DemoSidebarHome from './DemoSidebarHome.vue';
import DemoSidebarWorkspace from './DemoSidebarWorkspace.vue';
import DemoDashboard from './DemoDashboard.vue';
import DemoCreateWorkspaceModal from './DemoCreateWorkspaceModal.vue';
import DemoJoinWorkspaceModal from './DemoJoinWorkspaceModal.vue';
import DemoModal from './DemoModal.vue';
import StepItem from './StepItem.vue';
import { GlobalBus } from '@/services/event-bus';

const tocIds = ['creating-workspace', 'joining-workspace', 'inviting-others'];
let isScrolling = false;

function handleScroll() {
  if (isScrolling) return;

  const sections = tocIds.map(id => document.getElementById(id)).filter(Boolean);
  const scrollPos = window.scrollY + 100;

  for (let i = sections.length - 1; i >= 0; i--) {
    const section = sections[i];
    if (section && section.offsetTop <= scrollPos) {
      const id = section.id;
      window.location.hash = id;
      GlobalBus.emit('help-toc-active', id);
      break;
    }
  }
}

function handleHashChange() {
  const hash = window.location.hash.slice(1);
  if (hash && tocIds.includes(hash)) {
    GlobalBus.emit('help-toc-active', hash);
  }
}

onMounted(() => {
  window.addEventListener('scroll', handleScroll);
  window.addEventListener('hashchange', handleHashChange);
  handleHashChange();
});

onUnmounted(() => {
  window.removeEventListener('scroll', handleScroll);
  window.removeEventListener('hashchange', handleHashChange);
});
</script>

<style scoped lang="scss">
.content {
  flex: 1;
  min-width: 0;
}

.help-section {
  padding: 1.5rem 0;
  border-bottom: 1px solid rgba(255, 255, 255, 0.1);

  h2 {
    font-size: 1.5rem;
    font-weight: 600;
    color: inherit;
    margin-bottom: 1rem;
  }

  > p {
    font-size: 1.15rem;
    font-weight: 300;
    color: inherit;
    line-height: 1.6;
    margin-bottom: 0.5rem;
  }

  strong {
    color: inherit;
    font-weight: 600;
  }

  code {
    background: rgba(255, 255, 255, 0.15);
    padding: 0.15em 0.4em;
    border-radius: 4px;
    font-size: 0.9em;
    color: inherit;
  }
}
</style>
