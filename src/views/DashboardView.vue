<template>
  <div class="outer has-background-primary soft-if-dark">
    <div class="inner">
      <section class="hero">
        <div class="hero-body pb-1">
          <p class="title has-text-white">Dashboard</p>
          <p class="subtitle mt-2 has-text-white">
            You can create or join one or more workspaces below
          </p>
          <div class="mt-3">
            <button class="button identity-button is-small-caps" @click="showIdentityManager = true">
              <span class="icon">
                <FontAwesomeIcon :icon="faKey" />
              </span>
              <span>Manage identity keys</span>
            </button>
          </div>
        </div>
      </section>

      <div class="spacelist">
        <template v-for="ws in workspaces" :key="ws.name">
          <!-- Create workspace card -->
          <div class="create card" v-if="ws.name === '__create__'">
            <div class="card-content">
              <div class="block">
                <div class="media-content">
                  <p class="subtitle is-5">Don't see what you are looking for?</p>
                </div>
              </div>

              <div class="content has-text-right">
                <button class="button mr-2 mb-2 is-small-caps" @click="showCreate = true">
                  Create a new workspace
                </button>
                <button
                  class="button mr-2 is-primary is-small-caps soft-if-dark"
                  @click="showJoin = true"
                >
                  Join a workspace
                </button>
              </div>
            </div>
          </div>

          <!-- Normal workspace card -->
          <WorkspaceCard v-else :metadata="ws" @open="open(ws)" @leave="refreshList" />
        </template>
      </div>
    </div>

    <CreateWorkspaceModal :show="showCreate" @close="showCreate = false" @create="openByName" />
    <JoinWorkspaceModal :show="showJoin" @close="showJoin = false" @join="openByName" />
    <InviteWorkspaceModal :show="showInvite" @close="showInvite = false" />
    <IdentityKeyManager :show="showIdentityManager" @close="showIdentityManager = false" />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome';
import { faKey } from '@fortawesome/free-solid-svg-icons';

import CreateWorkspaceModal from '@/components/home/CreateWorkspaceModal.vue';
import JoinWorkspaceModal from '@/components/home/JoinWorkspaceModal.vue';
import InviteWorkspaceModal from '@/components/InviteWorkspaceModal.vue';
import WorkspaceCard from '@/components/home/WorkspaceCard.vue';
import IdentityKeyManager from '@/components/home/IdentityKeyManager.vue';

import * as utils from '@/utils';
import type { IWkspStats } from '@/services/types';

const router = useRouter();
const route = useRoute();

const showCreate = ref(false);
const showJoin = ref(false);
const showInvite = ref(false);
const showIdentityManager = ref(false);
const workspaces = ref([] as IWkspStats[]);

async function refreshList() {
  workspaces.value = await _o.stats.list();
  workspaces.value.sort((a, b) => (b.lastAccess ?? 0) - (a.lastAccess ?? 0));

  // Insert a create workspace card
  workspaces.value.splice(1, 0, {
    name: '__create__',
  } as IWkspStats);
}

onMounted(async () => {
  // Update tab name
  document.title = utils.formTabName('Dashboard');

  await refreshList();

  if (route.name === 'join') {
    // Check if already joined this workspace, then skip
    const space = utils.unescapeUrlName((route.params.space as string) || String());
    if (!(await openByName(space))) {
      showJoin.value = true;
    }
  } else if (route.query.invite) {
    showInvite.value = true;
  }
});

function open(ws: IWkspStats) {
  router.push({
    name: 'space-home',
    params: {
      space: utils.escapeUrlName(ws.name),
    },
  });
}

async function openByName(name: string): Promise<boolean> {
  await refreshList();
  const ws = workspaces.value.find((w) => w.name === name);
  if (ws) open(ws);
  return !!ws;
}
</script>

<style scoped lang="scss">
.outer {
  height: 100%;
  width: auto;
  overflow-y: auto;

  .inner {
    max-width: 900px;
    margin: 0 auto;
  }

  .spacelist {
    margin: 40px;
  }
}

.card.create {
  box-shadow: none;
}

@media (max-width: 1023px) {
  .outer .spacelist {
    margin: 20px;
  }
}

.identity-button {
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.18);
  border: 1px solid rgba(255, 255, 255, 0.45);
  color: #fff;
  padding: 0.55rem 1.1rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  box-shadow: 0 6px 16px rgba(0, 0, 0, 0.18);
}

.identity-button:hover {
  background: rgba(255, 255, 255, 0.28);
  border-color: rgba(255, 255, 255, 0.6);
  color: #fff;
}

.identity-button .icon {
  margin-right: 0.35rem;
}
</style>
