<template>
  <div class="wrapper">
    <Transition name="fade-rtl" mode="out-in">
      <LoadingSpinner
        v-if="showLoading"
        class="has-text-white-bis has-text-centered"
        :text="loadStatus"
      />

      <div class="box anim-rtl-fade p-4" v-else-if="flowStep === 'auth'">
        <div class="header is-size-4 has-text-weight-semibold">Get started</div>
        <p class="subtitle mt-1">
          Choose the authentication method that works best for you.
        </p>

        <div class="method-toggle mt-3">
          <button
            class="toggle"
            :class="{ active: authMethod === 'email' }"
            type="button"
            :disabled="methodSwitchDisabled && authMethod !== 'email'"
            @click="selectMethod('email')"
          >
            Email
          </button>
          <button
            class="toggle"
            :class="{ active: authMethod === 'dns' }"
            type="button"
            :disabled="methodSwitchDisabled && authMethod !== 'dns'"
            @click="selectMethod('dns')"
          >
            DNS
          </button>
        </div>

        <div class="login mt-4">
          <template v-if="authMethod === 'email'">
            <div v-if="emailStep === 'input'">
              <div class="field">
                <label>First, you need an email address to verify your unique identity.</label>
                <div class="control has-icons-left has-icons-right mt-3">
                  <input
                    :class="{ input: true, 'is-danger': emailError }"
                    inputmode="email"
                    autocomplete="email"
                    type="email"
                    placeholder="name@email.com"
                    v-model="emailAddress"
                    @keyup.enter="emailSubmit"
                  />
                  <span class="icon is-small is-left">
                    <FontAwesomeIcon :icon="faEnvelope" />
                  </span>

                  <span class="icon is-small is-right" v-if="emailError">
                    <FontAwesomeIcon :icon="faExclamationTriangle" />
                  </span>
                </div>
                <p v-if="emailError" class="help is-danger">{{ emailError }}</p>
              </div>

              <button class="button mt-3 is-primary is-fullwidth" @click="emailSubmit">
                Continue
              </button>
            </div>

            <div v-else>
              <div class="field">
                <label>
                  We have sent a verification code to your email address. Enter the code below to
                  continue.
                </label>
                <div class="control has-icons-left mt-3">
                  <input
                    :class="{ input: true, 'is-danger': codeError }"
                    inputmode="numeric"
                    pattern="[0-9]{6}"
                    autocomplete="off"
                    type="text"
                    placeholder="123456"
                    maxlength="6"
                    minlength="6"
                    v-model="codeInput"
                    @keypress="disallowNonNumeric"
                    @keyup="codeSubmitHandler"
                  />
                  <span class="icon is-small is-left">
                    <FontAwesomeIcon :icon="faKey" />
                  </span>
                </div>
                <p v-if="codeError" class="help is-danger">{{ codeError }}</p>

                <a class="is-size-7 mt-3 ml-1" @click="codeCancel"> Go back to the previous step </a>
              </div>
            </div>
          </template>

          <template v-else>
            <div v-if="dnsStep === 'input'">
              <div class="field">
                <label class="mt-2">First, you need to own domain to verify your unique identity.</label>
                <div class="control has-icons-left has-icons-right mt-3">
                  <input
                    :class="{ input: true, 'is-danger': domainError }"
                    inputmode="url"
                    autocomplete="off"
                    type="text"
                    placeholder="example.com"
                    v-model="domainName"
                    @keyup.enter="domainSubmit"
                  />
                  <span class="icon is-small is-left">
                    <FontAwesomeIcon :icon="faGlobe" />
                  </span>

                  <span class="icon is-small is-right" v-if="domainError">
                    <FontAwesomeIcon :icon="faExclamationTriangle" />
                  </span>
                </div>
                <p v-if="domainError" class="help is-danger">{{ domainError }}</p>
              </div>

              <button class="button mt-3 is-primary is-fullwidth" @click="domainSubmit">
                Continue
              </button>
            </div>

            <div v-else>
              <div class="field">
                <label class="mt-2">Publish this TXT record under {{ domainName }}.</label>
                <div class="dns-record mt-3">
                  <div class="row">
                    <div>
                      <span class="label">Name</span>
                      <code>{{ dnsRecordName || '--' }}</code>
                    </div>
                    <button
                      v-if="dnsRecordName"
                      class="copy"
                      type="button"
                      @click="copyValue(dnsRecordName, 'record name')"
                    >
                      Copy
                    </button>
                  </div>
                  <div class="row mt-2">
                    <div>
                      <span class="label">Type</span>
                      <code>TXT</code>
                    </div>
                  </div>
                  <div class="row mt-2">
                    <div>
                      <span class="label">Value</span>
                      <code class="value">{{ dnsRecordValueDisplay || '--' }}</code>
                    </div>
                    <button
                      v-if="dnsRecordValueDisplay"
                      class="copy"
                      type="button"
                      @click="copyValue(dnsRecordValueDisplay, 'record value')"
                    >
                      Copy
                    </button>
                  </div>
                </div>
              </div>

              <button
                class="button mt-3 is-primary is-fullwidth"
                type="button"
                :disabled="!canConfirmDns"
                @click="dnsConfirm"
              >
                The TXT record is ready
              </button>
              <p v-if="copyNotice" class="help is-success mt-2">{{ copyNotice }}</p>
            </div>
          </template>
        </div>
      </div>

      <div class="box anim-rtl-fade p-4" v-else-if="flowStep === 'identity'">
        <div class="header is-size-4 has-text-weight-semibold">Add your identity secret</div>
        <p class="subtitle mt-1">
          Import an existing identity secret or generate a new one for <code>{{ identityName || '--' }}</code>.
        </p>

        <div class="identity-card mt-4">
          <div class="field">
            <label class="label is-small">Identity name</label>
            <code class="identity-name">{{ identityName || '--' }}</code>
          </div>

          <div class="field mt-3">
            <label>Import an existing identity secret (.ndnkey)</label>
            <div class="file has-name is-boxed mt-2">
              <label class="file-label">
                <input
                  class="file-input"
                  type="file"
                  accept=".ndnkey"
                  @change="onIdentityFile"
                />
                <span class="file-cta">
                  <span class="file-label">Choose file</span>
                </span>
                <span class="file-name">{{ identityFileName || 'No file selected' }}</span>
              </label>
            </div>
            <button
              class="button is-primary is-fullwidth mt-2 soft-if-dark"
              :disabled="!identityFile || identityBusy"
              @click="importIdentityFromFile"
            >
              {{ identityBusy && identityAction === 'import' ? 'Importing…' : 'Import existing key' }}
            </button>
            <button
              class="button is-light is-fullwidth mt-2 soft-if-dark"
              :disabled="identityBusy"
              @click="openIdentityScanner"
            >
              Scan encrypted identity secret QR
            </button>
            <p class="help is-info mt-2">
              You will be prompted for the password to decrypt the QR export.
            </p>
          </div>

          <div class="divider my-3"><span>or</span></div>

          <button
            class="button is-link is-fullwidth soft-if-dark"
            :disabled="identityBusy"
            @click="generateIdentityKey"
          >
            {{ identityBusy && identityAction === 'generate' ? 'Generating…' : 'Generate a new identity key' }}
          </button>

          <p class="help is-danger mt-2" v-if="identityError">{{ identityError }}</p>
          <p class="help is-info mt-1">
            Identity secrets are only used to authenticate you when you join a workspace. They are never used to secure application data.
          </p>
        </div>
      </div>

      <div class="anim-rtl-fade p-4 has-text-centered" v-else>
        <FontAwesomeIcon class="success" :icon="faCircleCheck" />
      </div>
    </Transition>

    <QrModal
      :show="showIdentityScanner"
      mode="scan"
      title="Scan encrypted identity QR"
      message="Point the camera at the password-protected identity QR code. You'll be asked for the password."
      @decoded="onIdentityScan"
      @close="showIdentityScanner = false"
    />

    <ModalComponent :show="showSecretPasswordModal" @close="closePasswordModal">
      <div class="title is-5 mb-3">Decrypt identity secret</div>
      <p class="mb-3">Enter the password used when generating the encrypted identity secret QR.</p>
      <div class="field">
        <label class="label is-small">Password</label>
        <div class="control">
          <input
            class="input"
            type="password"
            autocomplete="current-password"
            v-model="secretPassword"
            :disabled="identityBusy"
            @keyup.enter="confirmPasswordImport"
          />
        </div>
      </div>
      <div class="field has-text-right">
        <button class="button mr-2" type="button" :disabled="identityBusy" @click="closePasswordModal">
          Cancel
        </button>
        <button class="button is-primary" type="button" :disabled="!secretPassword || identityBusy" @click="confirmPasswordImport">
          {{ identityBusy && identityAction === 'import' ? 'Importing…' : 'Import key' }}
        </button>
      </div>
      <p class="help is-danger mt-2" v-if="identityError">{{ identityError }}</p>
    </ModalComponent>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';

import LoadingSpinner from '@/components/LoadingSpinner.vue';
import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome';
import {
  faEnvelope,
  faExclamationTriangle,
  faKey,
  faCircleCheck,
  faGlobe,
} from '@fortawesome/free-solid-svg-icons';

import * as utils from '@/utils/index';
import ndn from '@/services/ndn';
import { Toast } from '@/utils/toast';
import QrModal from '@/components/QrModal.vue';
import ModalComponent from '@/components/ModalComponent.vue';
import { describeIdentityKeyImportError } from '@/utils/identity-errors';
import { decryptSecretPayload } from '@/utils/qr-crypto';

const emit = defineEmits(['login', 'ready']);

const showLoading = ref(true);
const flowStep = ref<'auth' | 'identity' | 'done'>('auth');

const loadStatus = ref(String());

const identityName = ref(String());
const identityFile = ref<File | null>(null);
const identityFileName = ref(String());
const identityError = ref(String());
const identityBusy = ref(false);
const identityAction = ref<null | 'import' | 'generate'>(null);
const showIdentityScanner = ref(false);
const showSecretPasswordModal = ref(false);
const secretPassword = ref('');
const scannedSecretPayload = ref<string | null>(null);

const authMethod = ref<'email' | 'dns'>('email');
const methodSwitchDisabled = computed(
  () =>
    showLoading.value || emailStep.value === 'code' || dnsConfirmResolver.value !== null,
);

const emailStep = ref<'input' | 'code'>('input');
const emailAddress = ref(String());
const emailError = ref(String());

const codeInput = ref(String());
const codeError = ref(String());
const codeSubmit = ref<() => void>(() => {});
const codeSubmitHandler = () => codeSubmit.value();

const dnsStep = ref<'input' | 'record'>('input');
const domainName = ref(String());
const domainError = ref(String());
const dnsRecordName = ref(String());
const dnsRecordValue = ref(String());
const dnsStatus = ref(String());
const dnsConfirmResolver = ref<null | ((value: string) => void)>(null);
const canConfirmDns = computed(() => dnsConfirmResolver.value !== null);
const copyNotice = ref(String());
let copyTimeout: ReturnType<typeof setTimeout> | undefined;

const dnsRecordValueDisplay = computed(() => {
  const raw = dnsRecordValue.value.trim();
  if (!raw) return '';
  const hasQuotes = raw.startsWith('"') && raw.endsWith('"');
  return hasQuotes ? raw : `"${raw}"`;
});

function selectMethod(method: 'email' | 'dns') {
  if (authMethod.value === method) return;
  if (methodSwitchDisabled.value) return;

  authMethod.value = method;

  if (method === 'email') {
    emailStep.value = 'input';
    emailError.value = '';
  } else {
    dnsStep.value = 'input';
    domainError.value = '';
  }
}

/** Validate email and move to step 2 */
function emailSubmit() {
  if (emailStep.value !== 'input') return;

  if (!emailAddress.value) {
    emailError.value = 'Email address is required';
    return;
  }

  if (!utils.validateEmail(emailAddress.value)) {
    emailError.value = 'Invalid email address';
    return;
  }

  emailError.value = '';
  startEmailChallenge();
}

/** Cancel code verification and go back to email step */
function codeCancel() {
  if (methodSwitchDisabled.value && emailStep.value !== 'code') return;
  codeError.value = '';
  codeInput.value = '';
  emailStep.value = 'input';
}

async function startEmailChallenge() {
  showLoading.value = true;

  try {
    loadStatus.value = 'Connecting to NDN testbed ...';
    await ndn.api.connect_testbed();

    loadStatus.value = 'Starting NDNCERT challenge ...';
    await ndn.api.ndncert_email(emailAddress.value, (status) => {
      codeError.value = '';
      codeInput.value = '';

      switch (status) {
        case 'need-code':
          break;
        case 'wrong-code':
          codeError.value = 'Invalid verification code';
          break;
        default:
          codeError.value = 'Verification error: ' + status;
          break;
      }

      showLoading.value = false;
      emailStep.value = 'code';

      return new Promise((resolve) => {
        codeSubmit.value = () => {
          if (codeInput.value.length !== 6) return;

          showLoading.value = true;
          loadStatus.value = 'Completing challenge ...';
          resolve(codeInput.value);
        };
      });
    });

    loadStatus.value = 'Certified!';
    await proceedToIdentitySetup();
  } catch (err) {
    Toast.error('Failed to complete challenge');
    console.error(err);
    showLoading.value = false;
    emailStep.value = 'input';
  } finally {
    codeSubmit.value = () => {};
  }
}

function validateDomain(value: string) {
  const trimmed = value.trim().toLowerCase();
  if (!trimmed) return { valid: false, formatted: trimmed };
  const pattern = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$/i;
  return { valid: pattern.test(trimmed) && trimmed.length <= 253, formatted: trimmed };
}

function domainSubmit() {
  if (dnsStep.value !== 'input') return;

  const { valid, formatted } = validateDomain(domainName.value);
  if (!formatted) {
    domainError.value = 'Domain is required';
    return;
  }
  if (!valid) {
    domainError.value = 'Invalid domain name';
    return;
  }

  domainError.value = '';
  domainName.value = formatted;
  startDnsChallenge();
}

async function startDnsChallenge() {
  showLoading.value = true;
  dnsStatus.value = '';
  dnsRecordName.value = '';
  dnsRecordValue.value = '';

  try {
    loadStatus.value = 'Connecting to NDN testbed ...';
    await ndn.api.connect_testbed();

    loadStatus.value = 'Starting NDNCERT challenge ...';
    await ndn.api.ndncert_dns(domainName.value, (recordName, recordValue, status) => {
      dnsStatus.value = status;
      dnsRecordName.value = recordName;
      dnsRecordValue.value = recordValue;

      showLoading.value = false;
      dnsStep.value = 'record';

      return new Promise((resolve) => {
        dnsConfirmResolver.value = (value: string) => {
          resolve(value);
          dnsConfirmResolver.value = null;
        };
      });
    });

    loadStatus.value = 'Certified!';
    await proceedToIdentitySetup();
  } catch (err) {
    Toast.error('Failed to complete challenge');
    console.error(err);
    showLoading.value = false;
    dnsStep.value = 'input';
  } finally {
    dnsConfirmResolver.value = null;
  }
}

function dnsConfirm() {
  if (!dnsConfirmResolver.value) return;

  showLoading.value = true;
  loadStatus.value =
    dnsStatus.value === 'wrong-record' ? 'Retrying DNS verification ...' : 'Completing challenge ...';

  const resolver = dnsConfirmResolver.value;
  dnsConfirmResolver.value = null;
  resolver('ready');
}

async function proceedToIdentitySetup() {
  try {
    loadStatus.value = 'Preparing identity key ...';
    const overview = await ndn.api.list_identity_keys();
    identityName.value = overview.identity;
    showLoading.value = false;

    if (overview.local?.length) {
      finishLogin();
      return;
    }

    flowStep.value = 'identity';
    emit('ready');
  } catch (err) {
    console.error(err);
    identityError.value = 'Unable to load identity details. Please try again.';
    showLoading.value = false;
    flowStep.value = 'identity';
  }
}

function finishLogin() {
  flowStep.value = 'done';
  showLoading.value = false;
  setTimeout(() => emit('login'), 800);
}

function onIdentityFile(event: Event) {
  const target = event.target as HTMLInputElement | null;
  const file = target?.files?.[0] ?? null;
  identityFile.value = file;
  identityFileName.value = file?.name ?? '';
  identityError.value = '';
}

async function importIdentityFromFile() {
  if (!identityFile.value) {
    identityError.value = 'Select a key file to import';
    return;
  }

  identityBusy.value = true;
  identityAction.value = 'import';
  identityError.value = '';
  try {
    const buffer = await identityFile.value.arrayBuffer();
    await ndn.api.import_identity_key(new Uint8Array(buffer));
    finishLogin();
  } catch (err) {
    console.error(err);
    const message = describeIdentityKeyImportError(err);
    identityError.value = `Failed to import identity key: ${message}`;
  } finally {
    identityBusy.value = false;
    identityAction.value = null;
  }
}

async function generateIdentityKey() {
  identityBusy.value = true;
  identityAction.value = 'generate';
  identityError.value = '';
  try {
    await ndn.api.generate_identity_key();
    finishLogin();
  } catch (err) {
    console.error(err);
    identityError.value = 'Unable to generate identity key. Please try again.';
  } finally {
    identityBusy.value = false;
    identityAction.value = null;
  }
}

function openIdentityScanner() {
  identityError.value = '';
  showIdentityScanner.value = true;
}

async function onIdentityScan(raw: string) {
  showIdentityScanner.value = false;
  scannedSecretPayload.value = raw;
  secretPassword.value = '';
  showSecretPasswordModal.value = true;
}

function closePasswordModal() {
  if (identityBusy.value) return;
  showSecretPasswordModal.value = false;
  scannedSecretPayload.value = null;
  secretPassword.value = '';
}

async function confirmPasswordImport() {
  if (!scannedSecretPayload.value || !secretPassword.value) return;

  identityBusy.value = true;
  identityAction.value = 'import';
  identityError.value = '';
  try {
    const secret = await decryptSecretPayload(scannedSecretPayload.value, secretPassword.value);
    await ndn.api.import_identity_key(secret);
    closePasswordModal();
    finishLogin();
  } catch (err) {
    console.error(err);
    const message = describeIdentityKeyImportError(err);
    identityError.value = `Failed to import identity key: ${message}`;
  } finally {
    identityBusy.value = false;
    identityAction.value = null;
  }
}

async function setup() {
  try {
    loadStatus.value = 'Setting up NDN service ...';
    await ndn.setup();

    await ndn.api.connect_testbed();

    if (await ndn.api.has_testbed_key()) {
      const isExpiringSoon = await ndn.api.is_testbed_cert_expiring_soon();
      if (isExpiringSoon) {
        console.log('latest certificate is expiring soon');
        showLoading.value = false;
        flowStep.value = 'auth';
        authMethod.value = 'email';
        emailStep.value = 'input';
        emit('ready');
        return;
      }

      await proceedToIdentitySetup();
      return;
    }

    showLoading.value = false;
    flowStep.value = 'auth';
    emailStep.value = 'input';
    dnsStep.value = 'input';
    authMethod.value = 'email';
    emit('ready');
  } catch (err) {
    console.error(err);
  }
}

onMounted(setup);

onUnmounted(() => {
  if (copyTimeout) clearTimeout(copyTimeout);
});

function disallowNonNumeric(event: KeyboardEvent) {
  if (!/^\d+$/.test(event.key) && !['Backspace', 'Delete'].includes(event.key)) {
    event.preventDefault();
  }
}

async function copyValue(value: string, label: string) {
  if (!value) return;

  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
    } else {
      const textArea = document.createElement('textarea');
      textArea.value = value;
      textArea.setAttribute('readonly', '');
      textArea.style.position = 'absolute';
      textArea.style.left = '-9999px';
      document.body.appendChild(textArea);
      textArea.select();
      document.execCommand('copy');
      document.body.removeChild(textArea);
    }

    if (copyTimeout) clearTimeout(copyTimeout);
    copyNotice.value = `Copied ${label}!`;
    copyTimeout = setTimeout(() => {
      copyNotice.value = '';
    }, 2500);
  } catch (err) {
    console.error(err);
    Toast.error('Unable to copy to clipboard');
  }
}
</script>

<style scoped lang="scss">
.wrapper {
  display: flex;
  flex-direction: column;
}

.method-toggle {
  display: flex;
  gap: 0.75rem;
}

.method-toggle .toggle {
  flex: 1;
  border: 1px solid rgba(255, 255, 255, 0.2);
  background: rgba(255, 255, 255, 0.08);
  color: #fff;
  border-radius: 6px;
  padding: 0.5rem 0.75rem;
  font-size: 0.9rem;
  transition: background 0.2s ease;
}

.method-toggle .toggle.active {
  background: #3273dc;
  border-color: #3273dc;
}

.method-toggle .toggle:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.dns-record {
  background: rgba(50, 115, 220, 0.08);
  border-radius: 6px;
  padding: 1rem;
}

.dns-record .row {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 1rem;
}

.dns-record .copy {
  background: rgba(255, 255, 255, 0.1);
  border: 1px solid rgba(255, 255, 255, 0.2);
  border-radius: 4px;
  color: #fff;
  padding: 0.25rem 0.75rem;
  font-size: 0.8rem;
  transition: background 0.2s ease;
}

.dns-record .copy:hover {
  background: rgba(255, 255, 255, 0.2);
}

.dns-record .label {
  display: block;
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: rgba(255, 255, 255, 0.7);
}

.dns-record code {
  display: inline-block;
  margin-top: 0.2rem;
  font-size: 0.95rem;
  word-break: break-word;
}

.identity-card {
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 8px;
  padding: 1rem;
}

.identity-name {
  display: inline-block;
  padding: 6px 10px;
  background: rgba(255, 255, 255, 0.08);
  border-radius: 4px;
  word-break: break-all;
}

.divider {
  position: relative;
  text-align: center;
  color: rgba(255, 255, 255, 0.7);
  font-size: 0.85rem;
}

.divider::before,
.divider::after {
  content: '';
  position: absolute;
  top: 50%;
  width: 42%;
  height: 1px;
  background: rgba(255, 255, 255, 0.2);
}

.divider::before {
  left: 0;
}

.divider::after {
  right: 0;
}

.divider span {
  padding: 0 8px;
  background: transparent;
}

.dns-record code.value {
  word-break: break-all;
}

.step-chip {
  display: inline-flex;
  align-items: center;
  font-size: 0.75rem;
  letter-spacing: 0.05em;
  text-transform: uppercase;
  border: 1px solid rgba(255, 255, 255, 0.2);
  border-radius: 999px;
  padding: 0.15rem 0.75rem;
  background: rgba(255, 255, 255, 0.08);
  color: rgba(255, 255, 255, 0.9);
}


.success {
  color: white;
  font-size: 5rem;
}

@media (min-width: 1024px) {
  .wrapper {
    margin: auto;
    margin-right: 10vw;
    min-width: 350px;
  }
}

@media (max-width: 1023px) {
  .wrapper.login {
    max-width: 400px;
    margin: 0 auto;
  }
}
</style>
