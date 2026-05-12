<template>
  <ModalComponent :show="show && !!wksp" @close="emit('close')">
    <div class="title is-5 mb-4">Invite people to {{ wksp?.metadata.label }}</div>

    <p>
      An invitation will be generated for each email address. Note that Ownly does not automatically
      send any emails &ndash; you must ask the recipients to join the workspace using the email
      address listed below using the invite link.
    </p>

    <p class="mt-1">
      <code class="select-all link">{{ inviteLink }}</code>
    </p>

    <p class="mt-2">Enter an email address or NDN name below</p>

    <div class="field mt-2">
      <input class="input" type="text" :placeholder="`name@example.com or /user-name`" v-model="inviteInput"
        :disabled="!isOwner" @keydown.enter.prevent="addInvitees(inviteInput)" @paste="addInviteesOnPaste" autofocus />
    </div>

    <div class="invitee-management">
      <div class="title is-6 mb-4" v-if="pendingRequests.length > 0">
        Access Requests ({{ pendingRequests.length }})
      </div>
      <DynamicScroller class="scroller" ref="scroller" :items="pendingRequests" :min-item-size="10" key-field="name">
        <template #default="{ item, index, active }">
          <DynamicScrollerItem :item="item" :active="active" :data-index="index" class="invitee-profile">
            <div :class="{
              'px-4': true,
              'pt-2': true,
              'pb-2': true,
              pending: item.pending,
            }">
              <div class="holder">
                <div class="avatar">
                  <img :src="utils.makeAvatar(item.name)" :key="item.name" alt="avatar" />
                </div>

                <div class="Info">
                  <div class="header">
                    <span class="name">{{ displayProfileName(item.name) }}</span>
                  </div>

                  <div class="email" v-if="item.email">{{ item.email }}</div>
                </div>
                <button class="button invitee-list-action"
                  @click="acceptRequest(item)" title="Accept">
                  <FontAwesomeIcon :icon="faCheck" />
                </button>
                <button class="button invitee-list-action"
                  @click="denyRequest(item)" title="Deny">
                  <FontAwesomeIcon :icon="faXmark" />
                </button>
              </div>
            </div>
          </DynamicScrollerItem>
        </template>
      </DynamicScroller>
      <div class="title is-6 mb-4">
        People with Access to this Workspace ({{ invitees.length }}<span v-if="pendingInvitees.length > 0"> + {{
          pendingInvitees.length }}</span>)
        <button class="button invitee-list-action" @click="pasteInviteeList"
          title="Import invitee profiles from clipboard">
          <FontAwesomeIcon :icon="faClipboard" />
        </button>
        <button class="button invitee-list-action" @click="copyInviteeList" title="Copy invitee profiles">
          <FontAwesomeIcon :icon="faCopy" />
        </button>
      </div>
      <DynamicScroller class="scroller" ref="scroller" :items="allInvitees" :min-item-size="10" key-field="name">
        <template #default="{ item, index, active }">
          <DynamicScrollerItem :item="item" :active="active" :data-index="index" class="invitee-profile">
            <div :class="{
              'px-4': true,
              'pt-2': true,
              'pb-2': true,
              pending: item.pending,
            }">
              <div class="holder">
                <div class="avatar">
                  <img :src="utils.makeAvatar(item.name)" :key="item.name" alt="avatar" />
                </div>

                <div class="Info">
                  <div class="header">
                    <span class="name">{{ displayProfileName(item.name) }}</span>
                  </div>

                  <div class="email" v-if="item.email">{{ item.email }}</div>
                </div>

                <div class="badge" v-if="item.pending">
                  Will be invited
                </div>
                <div class="badge" v-else-if="item.owner">
                  Owner
                </div>
                <div class="badge" v-else>
                  Member
                </div>

                <button class="button invitee-list-action" v-if="item.pending"
                  @click="removeInvitee(item.name)" title="Remove this pending invitee">
                  <FontAwesomeIcon :icon="faXmark" />
                </button>
                <button class="button invitee-list-action" v-else @click="() => { /*TODO: menu*/ }" title="Menu" disabled="true">
                  <FontAwesomeIcon :icon="faBars" />
                </button>
              </div>
            </div>
          </DynamicScrollerItem>
        </template>
      </DynamicScroller>
    </div>

    <p v-if="!isOwner" class="has-text-danger has-text-weight-semibold mt-2">
      You must be the owner of the workspace to invite people
    </p>

    <div class="field has-text-right mt-2">
      <div class="control">
        <button class="button mr-2" @click="emit('close')">Cancel</button>
        <button class="button is-primary soft-if-dark mr-2" @click="send"
          :disabled="!isOwner || pendingInvitees.length == 0">
          Invite
        </button>
      </div>
    </div>

    <div class="title is-6 mb-4">Current Workspace Members</div>
    This list currenly only shows members who have published messages in discussions.
    <p v-if="members.length > 0" class="mt-4">
      <code>{{ displayMembers.join('\n') }}</code>
    </p>

  </ModalComponent>
</template>

<script setup lang="ts">
import { computed, ref, shallowRef, useTemplateRef, watch } from 'vue';
import { useRouter } from 'vue-router';

import { DynamicScroller, DynamicScrollerItem } from 'vue-virtual-scroller';
import ModalComponent from './ModalComponent.vue';

import * as utils from '@/utils';
import { Workspace } from '@/services/workspace';
import { Toast } from '@/utils/toast';
import type { IProfile } from '@/services/types';
import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome';
import { faBars, faCheck, faClipboard, faCopy, faXmark } from '@fortawesome/free-solid-svg-icons';

const props = defineProps({
  show: {
    type: Boolean,
    required: true,
  },
});
const emit = defineEmits(['close']);
const router = useRouter();

// Element references
const scroller = useTemplateRef<typeof DynamicScroller>('scroller');

const wksp = shallowRef<Workspace | null>(null);
const inviteLink = ref(String());
const inviteInput = ref(String())
const isOwner = computed(() => !!wksp.value?.metadata.owner);
const members = ref([] as string[]);
const invitees = ref([] as IProfile[]);
const pendingInvitees = ref([] as IProfile[]);
const pendingRequests = ref([] as IProfile[]);
const displayMembers = computed(() =>
  members.value.map((member) => utils.stripNdnPrefixForDisplay(member)),
);

const allInvitees = computed(() => {
  return [
    ...pendingInvitees.value.map(profile => ({ ...profile, pending: true })),
    ...invitees.value.map(profile => ({ ...profile, pending: false })),
  ].sort((a, b) => {
    // Pending always on top
    if (a.pending && !b.pending) return -1;
    else if (!a.pending && b.pending) return 1;

    // Within pending, sort by name
    if (a.pending && b.pending) {
      return a.name.localeCompare(b.name);
    }

    // Within non-pending, owners at the bottom
    if (a.owner && !b.owner) return 1;
    else if (!a.owner && b.owner) return -1;

    // Otherwise, sort by name
    return a.name.localeCompare(b.name);
  });
});

// Do not use the onMounted hook since this component is always mounted
// in the sidebar (the inner modal has the v-if directive)
watch(
  () => props.show,
  async () => {
    wksp.value = await Workspace.setupOrRedir(router);
    if (!wksp.value) return;

    invitees.value = wksp.value.invite.getInviteArray();
    pendingInvitees.value.length = 0; // clear pending invitees
    pendingRequests.value.length = 0;
    _access_requests.forEach((requester) => {
      if (wksp.value?.metadata.name == requester[0] && !requester[2]) // requester[2] is false if request has not been dealt with yet
        addRequest(requester[1]);
    })

    inviteLink.value = await wksp.value.invite.getJoinLink(router);
    members.value = await wksp.value.getMembers();
  },
);

// Copy invitee list (including pending ones) to clipboard
// Use comma as delimiters
async function copyInviteeList() {
  navigator.clipboard.writeText(
    allInvitees.value.map((profile) => {
      if (profile.email) {
        return `${profile.email}`;
      } else {
        return utils.stripNdnPrefixForDisplay(profile.name);
      }
    }).toString()
  )
  Toast.success(`Copied ${allInvitees.value.length} users to clipboard!`);
}

// Paste invitee list from clipboard, overwriting existing pending invitees
async function pasteInviteeList() {
  const clipboardText = await navigator.clipboard.readText();
  addInvitees(clipboardText);
  Toast.success(`Added ${pendingInvitees.value.length} users to invitation list`);
}

// Remove an invitee from the pending list
function removeInvitee(name: string) {
  const index = pendingInvitees.value.findIndex((profile) => profile.name === name);
  if (index !== -1) {
    pendingInvitees.value.splice(index, 1);
  }
}

// Wrapper for addInvitees - allows quick paste into the input field
function addInviteesOnPaste(event: ClipboardEvent) {
  const pasted = event.clipboardData?.getData('text') || '';
  addInvitees(pasted);
  event.preventDefault();
}

// Add invitees to the pending list
// Wrapper for addInvitee - allows multiple invitees to be added at once
function addInvitees(input: string) {
  const entries = input.trim().split(/[\s,]+/);
  for (const entry of entries) {
    addInvitee(entry); // add the current entry
  }
  inviteInput.value = String(); // clear the input field
}

// Add an invitee to the pending list
function addInvitee(invitee: string) {
  // Check maximum invitees per invitation
  if (pendingInvitees.value.length >= 100) {
    Toast.error("Maximum of 100 invitees allowed in one time")
    return;
  }

  // Transform the entry to a name
  // Check validity and ignore blank
  const entry = invitee.trim()
  if (!entry) return; // blank line

  let new_profile: IProfile;

  // Check if it is an NDN name
  if (entry.startsWith('/')) {
    new_profile = { name: utils.restoreNdnPrefixFromDisplay(entry) };
  } else {
    // Validate the email address
    if (!utils.validateEmail(entry)) {
      Toast.error(`Invalid email address: ${entry}`);
      return;
    }

    // Convert email to NDN name
    const ndnName = utils.convertEmailToNameLegacy(entry);

    // Form profile
    new_profile = { name: ndnName, email: entry };
  }

  // Check repetition
  if (allInvitees.value.some((profile) => profile.name === new_profile.name)) {
    Toast.error(`${displayProfileName(new_profile.name)} already in the invitation list`);
    return;
  }

  // Add to pending invitee list
  pendingInvitees.value.push(new_profile);
}

// Add an access request to the pending list
function addRequest(invitee: string) {
  // Check maximum invitees per invitation
  if (pendingRequests.value.length >= 100) {
    Toast.error("Maximum of 100 requests allowed in one time")
    return;
  }

  // Transform the entry to a name
  // Check validity and ignore blank
  const entry = invitee.trim()
  if (!entry) return; // blank line

  let new_profile: IProfile;

  // Check if it is an NDN name
  if (entry.startsWith('/')) {
    new_profile = { name: utils.restoreNdnPrefixFromDisplay(entry) };
  } else {
    // Validate the email address
    if (!utils.validateEmail(entry)) {
      Toast.error(`Invalid email address: ${entry}`);
      return;
    }

    // Convert email to NDN name
    const ndnName = utils.convertEmailToNameLegacy(entry);

    // Form profile
    new_profile = { name: ndnName, email: entry };
  }

  // Check if already invited/pending
  if (allInvitees.value.some((profile) => profile.name === new_profile.name)) {
    console.log("Received access request from already added member");

    // Remove from local list to prevent accidental duplicates, and mark in global list as already dealt with
    const idx = _access_requests.findIndex(a => a[0] == wksp.value?.metadata.name && a[1] == new_profile.name)
    if (idx != -1)
      _access_requests[idx][2] = true
    pendingRequests.value = pendingRequests.value.filter(a => a.name != new_profile.name)
    return;
  }

  // Check for repetition
  if (pendingRequests.value.some((profile) => profile.name === new_profile.name)) {
    console.log("Received duplicate access request");

    // Remove from local list to prevent accidental duplicates, and mark in global list as already dealt with
    const idx = _access_requests.findIndex(a => a[0] == wksp.value?.metadata.name && a[1] == new_profile.name)
    if (idx != -1)
      _access_requests[idx][2] = true
    pendingRequests.value = pendingRequests.value.filter(a => a.name != new_profile.name)
    return;
  }

  // Add to pending requests list
  pendingRequests.value.push(new_profile);
}

async function acceptRequest(invitee: IProfile) {
  console.log(invitee);
  if (!wksp.value) return;

  // Remove from local list to prevent accidental duplicates, and mark in global list as already dealt with
  console.log(_access_requests)
  const idx = _access_requests.findIndex(a => a[0] == wksp.value?.metadata.name && a[1] == invitee.name)
  if (idx != -1)
    _access_requests[idx][2] = true
  pendingRequests.value = pendingRequests.value.filter(a => a.name != invitee.name)

  // Publish invitation
  try {
    // Generate and publish invitation to sync
    await wksp.value.invite.tryInvite(invitee);
  } catch (err) {
    Toast.error(`Failed to invite ${displayProfileName(invitee.name)}: ${err}`);
    return; // rare
  }

  invitees.value.push(invitee);

  // Finish
  Toast.success(`Invited ${displayProfileName(invitee.name)} to workspace!`);
}

function denyRequest(invitee: IProfile) {
  if (!wksp.value) return;

  // Remove from local list to prevent accidental duplicates, and mark in global list as already dealt with
  console.log(_access_requests)
  const idx = _access_requests.findIndex(a => a[0] == wksp.value?.metadata.name && a[1] == invitee.name)
  if (idx != -1)
    _access_requests[idx][2] = true
  pendingRequests.value = pendingRequests.value.filter(a => a.name != invitee.name)
}

// Sign the invitations and send them to the server
async function send() {
  if (!wksp.value) return;

  // Publish invitations
  for (const invitee of pendingInvitees.value) {
    try {
      // Generate and publish invitation to sync
      await wksp.value.invite.tryInvite(invitee);
    } catch (err) {
      Toast.error(`Failed to invite ${displayProfileName(invitee.name)}: ${err}`);
      return; // rare
    }
  }

  // Finish
  Toast.success(`Invited ${pendingInvitees.value.length} users to workspace!`);
  emit('close');
}

function displayProfileName(name: string): string {
  return utils.stripNdnPrefixForDisplay(name);
}

</script>

<style scoped lang="scss">
.textarea {
  resize: none;
}

.invitee-management {
  .invitee-list-action {
    float: right;
    width: 2rem;
    height: 2rem;
    border-radius: 50%;
    align-self: center;
  }

  .scroller {
    flex: 1;
    overflow-y: auto;
    max-height: 150px;

    .invitee-profile {
      &:hover {
        background-color: rgba(0, 0, 0, 0.03);
      }

      .pending {
        .Info {
          margin-bottom: 0%;
        }
      }

      .holder {
        display: flex;
        flex-direction: row;

        .avatar {
          min-width: 36px;
          max-height: 36px;
          object-fit: cover;
          border-radius: 5px;
          overflow: hidden;
          margin-right: 10px;
          transform: translateY(4px); // visual hack

          >img {
            width: 36px;
            height: 36px;
          }
        }

        .Info {
          flex: 1;
          display: flex;
          flex-direction: column;
          font-size: 14px;
          line-height: 1.5;

          .name {
            font-weight: bold;
          }

          .email {
            white-space: normal;
          }
        }

        .badge {
          margin-left: auto;
          padding-right: 4%;
          align-content: center;
          font-style: italic;
        }
      }
    }
  }
}
</style>
