<template>
  <ModalComponent :show="show" @close="emit('close')">
    <div class="title is-5 mb-4">
      Invite <code>{{ displayName }}</code>
    </div>

    <div v-if="joinLink" class="my-2">
      The user has been invited to the workspace. Share the link below or QR code to join
      automatically.

      <p class="mt-2">
        <code class="select-all">{{ joinLink }}</code>
      </p>

      <img class="qr" v-if="joinLinkQr" :src="joinLinkQr" />
    </div>

    <div class="spacelist my-2" v-else>
      <p>Select a workspace to invite the user to.</p>

      <span v-if="workspaces.length == 0">Join or create workspaces before inviting users.</span>

      <template v-else v-for="ws in workspaces" :key="ws.name">
        <div class="wksp p-2" @click="invite(ws)">
          <p class="has-text-weight-bold">{{ ws.label }}</p>
          <p>{{ utils.stripNdnPrefixForDisplay(ws.name) }}</p>
        </div>
      </template>
    </div>

    <div class="field has-text-right">
      <div class="control">
        <button class="button mr-2" @click="close">Close</button>
      </div>
    </div>
  </ModalComponent>
</template>

<script setup lang="ts">
import { computed, ref, shallowRef, watch } from 'vue';
import { useRouter, useRoute } from 'vue-router';

import QRCode from 'qrcode';

import ModalComponent from '@/components/ModalComponent.vue';

import { Workspace } from '@/services/workspace';
import { Toast } from '@/utils/toast';
import * as utils from '@/utils';

import type { IWkspStats } from '@/services/types';

const props = defineProps({
  show: {
    type: Boolean,
    required: true,
  },
});

const emit = defineEmits(['close']);

const router = useRouter();
const route = useRoute();

const name = ref(String());
const displayName = computed(() => utils.stripNdnPrefixForDisplay(name.value));
const joinLink = ref(String());
const joinLinkQr = ref(String());
const workspaces = shallowRef([] as IWkspStats[]);

watch(
  () => props.show,
  (show) => show && create(),
);

async function create() {
  workspaces.value = [];
  joinLink.value = String();

  name.value = utils.restoreNdnPrefixFromDisplay((route.query.invite as string) || String());
  if (!name.value) {
    close();
    return;
  }

  workspaces.value = await _o.stats.list();
  workspaces.value.sort((a, b) => (b.lastAccess ?? 0) - (a.lastAccess ?? 0));
}

async function invite(ws: IWkspStats) {
  try {
    if (!ws.owner) {
      throw new Error('You do not have permission to invite users to this workspace');
    }

    const wksp = await Workspace.setup(ws.name);
    if (!wksp) return;

    await wksp.invite.invite(name.value);
    Toast.success(`Invited user to workspace ${ws.label}`);

    joinLink.value = await wksp.invite.getJoinLink(router);
    joinLinkQr.value = await QRCode.toDataURL(joinLink.value, { scale: 6 });
  } catch (e) {
    console.error(e);
    Toast.error(`Failed to invite user: ${e}`);
  }
}

function close() {
  router.push({ query: {} });
  emit('close');
}
</script>

<style scoped lang="scss">
.spacelist {
  max-height: 400px;
  overflow-y: auto;
  font-size: 0.95em;

  .wksp {
    cursor: pointer;
    border-radius: 4px;
    &:hover {
      background-color: rgba(255, 255, 255, 0.15);
    }
  }
}

img.qr {
  display: block;
  margin: 10px auto;
}
</style>
