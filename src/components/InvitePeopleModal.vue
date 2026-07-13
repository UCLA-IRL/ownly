<template>
  <ModalComponent :show="show && !!wksp" @close="emit('close')">
    <div class="title is-5 mb-4">People & access for {{ wksp?.metadata.label }}</div>

    <p v-if="canManageMembers">
      Share the public join link for the traditional flow, or add recipients below to generate
      per-person fast join links. The owner join link can be shared with an owner device that needs
      to rejoin this workspace. Ownly does not automatically send emails.
    </p>
    <p v-else>
      Share the public join link for the traditional flow. The owner join link can be shared with
      an owner device that needs to rejoin this workspace. A master owner device must approve access
      requests and manage per-person fast join links.
    </p>

    <div class="invite-link-box mt-3">
      <div class="title is-6 mb-2">Public join link</div>
      <div class="invite-link-row">
        <code class="select-all link">{{ inviteLink || 'Generating link...' }}</code>
        <button
          class="button invitee-list-action"
          @click="copyInviteLink"
          :disabled="!inviteLink"
          title="Copy public join link"
        >
          <FontAwesomeIcon :icon="faCopy" />
        </button>
      </div>
    </div>

    <div class="fast-invite-links mt-3">
      <div class="title is-6 mb-2">Generated invite links</div>
      <template v-if="fastInviteLinks.length > 0">
        <div class="fast-invite-link" v-for="link in fastInviteLinks" :key="link.name">
          <div class="fast-invite-recipient">
            <strong>{{ link.label || link.email || link.name }}</strong>
            <span v-if="link.label || link.email">{{ link.name }}</span>
          </div>
          <code class="select-all">{{ link.href }}</code>
          <button class="button invitee-list-action" @click="copyFastInviteLink(link)" title="Copy invite link">
            <FontAwesomeIcon :icon="faCopy" />
          </button>
        </div>
      </template>
      <p v-else class="invite-link-empty">No generated links yet.</p>
    </div>

    <template v-if="canManageMembers">
      <p class="mt-2">Enter an email address or NDN name below</p>

      <div class="field has-addons mt-2">
        <div class="control is-expanded">
          <input class="input" type="text" :placeholder="`name@example.com or /ndn/user-name`" v-model="inviteInput"
            @keydown.enter.prevent="addInvitees(inviteInput)" @paste="addInviteesOnPaste" autofocus />
        </div>
        <div class="control">
          <button class="button is-primary" @click="addInvitees(inviteInput)">
            Add
          </button>
        </div>
      </div>
    </template>

    <div class="invitee-management">
      <div class="title is-6 mb-4" v-if="canManageMembers && pendingRequests.length > 0">
        Access Requests ({{ pendingRequests.length }})
      </div>
      <DynamicScroller v-if="canManageMembers && pendingRequests.length > 0" class="scroller" :items="pendingRequests" :min-item-size="10" key-field="name">
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
                    <span class="name">{{ item.name }}</span>
                  </div>

                  <div class="email" v-if="item.email">{{ item.email }}</div>
                </div>
                <button class="button invitee-list-action" @click="acceptRequest(item)" title="Accept">
                  <FontAwesomeIcon :icon="faCheck" />
                </button>
                <button class="button invitee-list-action" @click="denyRequest(item)" title="Deny">
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
        <button v-if="canManageMembers" class="button invitee-list-action" @click="pasteInviteeList"
          title="Import invitee profiles from clipboard">
          <FontAwesomeIcon :icon="faClipboard" />
        </button>
        <button class="button invitee-list-action" @click="copyInviteeList" title="Copy invitee profiles">
          <FontAwesomeIcon :icon="faCopy" />
        </button>
      </div>
      <DynamicScroller class="scroller" :items="allInvitees" :min-item-size="10" key-field="name">
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
                    <span class="name">{{ item.name }}</span>
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
                  Access
                </div>

                <button class="button invitee-list-action" v-if="canManageMembers && !item.owner"
                  :disabled="resendingInvite === item.name" @click="resendInvite(item.name)"
                  title="Generate a new fast-join link for this person">
                  <FontAwesomeIcon :icon="faShare" />
                </button>
                <button class="button invitee-list-action" v-if="item.pending" @click="removeInvitee(item.name)"
                  title="Remove this pending invitee">
                  <FontAwesomeIcon :icon="faXmark" />
                </button>
                <button class="button invitee-list-action" v-else-if="canManageMembers && !item.owner"
                  :disabled="removingMember === item.name" @click="removeExistingMember(item.name)"
                  title="Remove access to this workspace">
                  <FontAwesomeIcon :icon="faUserMinus" />
                </button>
              </div>
            </div>
          </DynamicScrollerItem>
        </template>
      </DynamicScroller>
    </div>

    <div class="field has-text-right mt-2">
      <div class="control">
        <button class="button mr-2" @click="emit('close')">
          {{ pendingInvitees.length > 0 ? 'Cancel' : 'Close' }}
        </button>
        <button v-if="canManageMembers" class="button is-primary soft-if-dark mr-2" @click="send"
          :disabled="pendingInvitees.length == 0">
          Invite
        </button>
      </div>
    </div>

    <div class="title is-6 mb-4">Current Workspace Members</div>
    This list currenly only shows members who have published messages in discussions.
    <p v-if="members.length > 0" class="mt-4">
      <code>{{ members.join('\n') }}</code>
    </p>

  </ModalComponent>
</template>

<script setup lang="ts">
import { computed, ref, shallowRef, watch } from 'vue';
import { useRouter } from 'vue-router';

import { DynamicScroller, DynamicScrollerItem } from 'vue-virtual-scroller';
import ModalComponent from './ModalComponent.vue';

import * as utils from '@/utils';
import { Workspace } from '@/services/workspace';
import { Toast } from '@/utils/toast';
import type { IProfile } from '@/services/types';
import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome';
import { faCheck, faClipboard, faCopy, faShare, faUserMinus, faXmark } from '@fortawesome/free-solid-svg-icons';

const props = defineProps({
  show: {
    type: Boolean,
    required: true,
  },
});
const emit = defineEmits(['close']);
const router = useRouter();

const wksp = shallowRef<Workspace | null>(null);
const inviteLink = ref(String());
const inviteInput = ref(String())
const canManageMembers = computed(() =>
  !!wksp.value?.metadata.owner && !!wksp.value?.invite.isMasterDevice(),
);
const members = ref([] as string[]);
const invitees = ref([] as IProfile[]);
const pendingInvitees = ref([] as IProfile[]);
const pendingRequests = ref([] as IProfile[]);
const removingMember = ref<string | null>(null);
type FastInviteLink = { name: string; email?: string; label?: string; href: string };
const fastInviteLinks = ref([] as FastInviteLink[]);
const MAX_BATCH = 100;
const OWNER_JOIN_LINK_LABEL = 'Owner join link';

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

    inviteLink.value = String();
    invitees.value = wksp.value.invite.getInviteArray();
    pendingInvitees.value.length = 0; // clear pending invitees
    pendingRequests.value.length = 0;
    fastInviteLinks.value = [];
    _access_requests.forEach((requester) => {
      if (wksp.value?.metadata.name == requester[0] && !requester[2]) // requester[2] is false if request has not been dealt with yet
        addRequest(requester[1]);
    })
    inviteLink.value = await wksp.value.invite.getJoinLink(router);
    members.value = await wksp.value.getMembers();
    await refreshOwnerJoinLink();
  },
);

async function refreshOwnerJoinLink() {
  if (!wksp.value) return;

  const href = await wksp.value.invite.getJoinLink(router);
  const link = {
    name: wksp.value.metadata.name,
    label: OWNER_JOIN_LINK_LABEL,
    href,
  };
  fastInviteLinks.value = [
    link,
    ...fastInviteLinks.value.filter((existing) => existing.label !== OWNER_JOIN_LINK_LABEL),
  ];
}

// Copy invitee list (including pending ones) to clipboard
// Use comma as delimiters
async function copyInviteeList() {
  navigator.clipboard.writeText(
    allInvitees.value.map((profile) => {
      if (profile.email) {
        return `${profile.email}`;
      } else {
        return `${profile.name}`;
      }
    }).toString()
  )
  Toast.success(`Copied ${allInvitees.value.length} users to clipboard!`);
}

async function copyFastInviteLink(link: FastInviteLink) {
  await navigator.clipboard.writeText(link.href);
  Toast.success(`Copied invite link for ${link.label || link.email || link.name}`);
}

async function copyInviteLink() {
  if (!inviteLink.value) return;
  await navigator.clipboard.writeText(inviteLink.value);
  Toast.success('Public join link copied to clipboard!');
}

const resendingInvite = ref<string | null>(null);

async function resendInvite(name: string) {
  if (!wksp.value) return;
  if (resendingInvite.value) return;
  resendingInvite.value = name;
  try {
    const href = await wksp.value.invite.resendFastInvite(name, router);
    await navigator.clipboard.writeText(href);
    Toast.success(`New fast-join link for ${name} copied to clipboard`);
  } catch (err: any) {
    Toast.error(`Failed to resend invite for ${name}: ${err?.message || err}`);
  } finally {
    resendingInvite.value = null;
  }
}

// Paste invitee list from clipboard, overwriting existing pending invitees
async function pasteInviteeList() {
  const clipboardText = await navigator.clipboard.readText();
  addInvitees(clipboardText);
  Toast.success(`Added ${pendingInvitees.value.length} users to invitation list`);
}

// Mark an access request as handled in the global list, and remove it from the local pending list
function markRequestHandled(name: string) {
  const idx = _access_requests.findIndex(
    a => a[0] == wksp.value?.metadata.name && a[1] == name,
  )
  if (idx != -1) _access_requests[idx][2] = true
  pendingRequests.value = pendingRequests.value.filter(a => a.name != name)
}

// Remove an invitee from the pending list
function removeInvitee(name: string) {
  const index = pendingInvitees.value.findIndex((profile) => profile.name === name);
  if (index !== -1) {
    pendingInvitees.value.splice(index, 1);
  }
}

async function removeExistingMember(name: string) {
  if (!wksp.value) return;
  if (!canManageMembers.value) {
    Toast.error('Only the master owner device can remove members');
    return;
  }
  if (!globalThis.confirm(`Remove workspace access for ${name}?`)) return;
  if (removingMember.value) return;

  removingMember.value = name;
  const progress = Toast.loading(`Removing access for ${name}...`);
  try {
    await wksp.value.invite.removeMember(name);
    invitees.value = invitees.value.filter((profile) => profile.name !== name);
    members.value = await wksp.value.getMembers();
    await progress.success(`Removed access for ${name}`);
  } catch (err) {
    await progress.error(`Failed to remove access for ${name}: ${err}`);
  } finally {
    removingMember.value = null;
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
  if (pendingInvitees.value.length >= MAX_BATCH) {
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
    new_profile = { name: entry };
  } else {
    // Validate the email address
    if (!utils.validateEmail(entry)) {
      Toast.error(`Invalid email address: ${entry}`);
      return;
    }

    // Convert email to NDN name
    const ndnName = utils.convertEmailToName(entry);

    // Form profile
    new_profile = { name: ndnName, email: entry };
  }

  // Check repetition
  if (allInvitees.value.some((profile) => profile.name === new_profile.name)) {
    Toast.error(`${new_profile.name} already in the invitation list`);
    return;
  }

  // Add to pending invitee list
  pendingInvitees.value.push(new_profile);
}

// Add an access request to the pending list
function addRequest(invitee: string) {
  // Check maximum invitees per invitation
  if (pendingRequests.value.length >= MAX_BATCH) {
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
    new_profile = { name: entry };
  } else {
    // Validate the email address
    if (!utils.validateEmail(entry)) {
      Toast.error(`Invalid email address: ${entry}`);
      return;
    }

    // Convert email to NDN name
    const ndnName = utils.convertEmailToName(entry);

    // Form profile
    new_profile = { name: ndnName, email: entry };
  }

  // Check if already invited/pending
  if (allInvitees.value.some((profile) => profile.name === new_profile.name)) {
    // Remove from local list to prevent accidental duplicates, and mark in global list as already dealt with
    markRequestHandled(new_profile.name);
    return;
  }

  // Check for repetition
  if (pendingRequests.value.some((profile) => profile.name === new_profile.name)) {
    // Remove from local list to prevent accidental duplicates, and mark in global list as already dealt with
    markRequestHandled(new_profile.name);
    return;
  }

  // Add to pending requests list
  pendingRequests.value.push(new_profile);
}

async function acceptRequest(invitee: IProfile) {
  if (!wksp.value) return;
  if (!canManageMembers.value) {
    Toast.error('Only the master owner device can invite members');
    return;
  }

  // Remove from local list to prevent accidental duplicates, and mark in global list as already dealt with
  markRequestHandled(invitee.name);

  // Publish invitation
  try {
    // Generate and publish invitation to sync
    await wksp.value.invite.tryInvite(invitee);
  } catch (err) {
    Toast.error(`Failed to invite ${invitee.name}: ${err}`);
    return; // rare
  }

  invitees.value.push(invitee);

  // Finish
  Toast.success(`Invited ${invitee.name} to workspace!`);
}

function denyRequest(invitee: IProfile) {
  if (!wksp.value) return;
  if (!canManageMembers.value) {
    Toast.error('Only the master owner device can manage access requests');
    return;
  }

  // Remove from local list to prevent accidental duplicates, and mark in global list as already dealt with
  markRequestHandled(invitee.name);
}

// Sign the invitations and send them to the server
async function send() {
  if (!wksp.value) return;
  if (!canManageMembers.value) {
    Toast.error('Only the master owner device can invite members');
    return;
  }

  const generatedLinks: FastInviteLink[] = [];

  // Publish invitations and create per-invitee fast join links.
  for (const invitee of pendingInvitees.value) {
    try {
      const href = await wksp.value.invite.tryInviteWithFastJoin(invitee, router);
      generatedLinks.push({ name: invitee.name, email: invitee.email, href });
      invitees.value.push(invitee);
    } catch (err) {
      Toast.error(`Failed to invite ${invitee.name}: ${err}`);
      return; // rare
    }
  }

  fastInviteLinks.value = [
    ...fastInviteLinks.value.filter((link) => link.label === OWNER_JOIN_LINK_LABEL),
    ...generatedLinks,
  ];
  pendingInvitees.value = [];

  try {
    await navigator.clipboard.writeText(
      generatedLinks.map((link) => `${link.email || link.name}: ${link.href}`).join('\n'),
    );
    Toast.success(`Generated ${generatedLinks.length} invite links and copied them to clipboard!`);
  } catch {
    Toast.success(`Generated ${generatedLinks.length} invite links!`);
  }
}

</script>

<style scoped lang="scss">
.textarea {
  resize: none;
}

.invite-link-box {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding: 0.75rem;
  border-radius: 6px;
  background: rgba(0, 0, 0, 0.05);

  .invite-link-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 2rem;
    gap: 0.5rem;
    align-items: center;
  }

  code {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    background: transparent;
    padding: 0;
  }
}

.invitee-list-action {
  width: 2rem;
  height: 2rem;
  border-radius: 50%;
  align-self: center;
}

.invitee-management {
  .invitee-list-action {
    float: right;
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

.fast-invite-links {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;

  .fast-invite-link {
    display: grid;
    grid-template-columns: minmax(8rem, 12rem) minmax(0, 1fr) 2rem;
    gap: 0.5rem;
    align-items: center;

    code {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }

  .fast-invite-recipient {
    display: flex;
    flex-direction: column;
    min-width: 0;
    line-height: 1.25;

    strong,
    span {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    span {
      color: var(--bulma-text-weak);
      font-size: 0.8rem;
    }
  }

  .invite-link-empty {
    color: var(--bulma-text-weak);
    margin-bottom: 0;
  }
}
</style>
