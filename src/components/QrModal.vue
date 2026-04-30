<template>
  <ModalComponent :show="show" @close="emit('close')">
    <template v-if="isScan">
      <div class="title is-5 mb-2">{{ scanTitle }}</div>
      <p class="mb-3">{{ scanMessage }}</p>

      <div v-if="scanError" class="notification is-danger is-light">{{ scanError }}</div>

      <div class="scanner" v-else>
        <video ref="videoEl" class="video" autoplay playsinline muted></video>
        <canvas ref="canvasEl" class="hidden-canvas"></canvas>
      </div>

      <div class="field has-text-right mt-3">
        <button class="button" @click="emit('close')">Close</button>
      </div>
    </template>

    <template v-else>
      <div class="title is-5 mb-4">Share Your Primary Identity Certificate</div>

      <p>
        This is your global Ownly identity. Share the identity certificate via QR or download it as a file so
        others can configure it as a trusted key in their Ownly profiles. (It is only used to authenticates you
        when you invite or take invitations; workspace data are secured by different keys.)
      </p>

      <p class="my-1">
        <code class="select-all">{{ name }}</code>
      </p>

      <img class="qr" v-if="certQrimg" :src="certQrimg" />

      <div class="mt-4">
        <label class="label is-small">Encrypt Identity Secret for sharing</label>
        <p class="is-size-7 mb-2">
          Create a password-protected QR code containing your Identity Secret. Share the
          password separately.
        </p>
        <div class="field has-addons">
          <div class="control is-expanded">
            <input
              class="input"
              type="password"
              placeholder="Enter password to protect the secret"
              v-model="secretPassword"
            />
          </div>
          <div class="control">
            <button class="button is-primary" :disabled="!identitySecret?.length || !secretPassword" @click="buildSecretQr">
              Generate QR
            </button>
          </div>
        </div>

        <img class="qr" v-if="secretQrimg" :src="secretQrimg" />
      </div>

      <div class="field has-text-right mt-4 buttons">
        <button class="button mr-2" @click="downloadCert" :disabled="!identityCert?.length">
          Download certificate
        </button>
        <button class="button is-primary" @click="emit('close')">Close</button>
      </div>
    </template>
  </ModalComponent>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';

import QRCode from 'qrcode';

import ModalComponent from '@/components/ModalComponent.vue';
import ndn, { type IdentityKeyInfo } from '@/services/ndn';
import { bytesToBase64, encryptSecret } from '@/utils/qr-crypto';
import { deriveIdentityFromKeyName, formatIdentityFilename } from '@/utils/identity';

const props = defineProps<{
  show: boolean;
  mode?: 'share' | 'scan';
  title?: string;
  message?: string;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'decoded', value: string): void;
}>();

const isScan = computed(() => props.mode === 'scan');
const scanTitle = computed(() => props.title?.trim() || 'Scan QR code');
const scanMessage = computed(
  () => props.message?.trim() || 'Align the QR code within the frame to scan.',
);

const name = ref(String());
const identityKeyName = ref(String());
const identityEntry = ref<IdentityKeyInfo | null>(null);
const certQrimg = ref(String());
const secretQrimg = ref(String());
const identityCert = ref<Uint8Array | null>(null);
const identitySecret = ref<Uint8Array | null>(null);
const secretPassword = ref(String());

const videoEl = ref<HTMLVideoElement | null>(null);
const canvasEl = ref<HTMLCanvasElement | null>(null);
const scanError = ref('');
let stream: MediaStream | null = null;
let rafId: number | null = null;
let detector: any = null;

watch(
  [() => props.show, isScan],
  ([show, scan]) => {
    if (!show) {
      stopScan();
      return;
    }

    if (scan) {
      startScan();
    } else {
      stopScan();
      void createShare();
    }
  },
);

onBeforeUnmount(stopScan);

async function createShare() {
  const testbedKey = await ndn.api.get_testbed_key();
  name.value = deriveIdentityFromKeyName(testbedKey);
  identityKeyName.value = testbedKey;

  identityCert.value = await ndn.api.export_identity_cert();
  const overview = await ndn.api.list_identity_keys();
  const localWithKey = overview.local?.find((k) => k.hasPrivate);
  identityEntry.value = localWithKey ?? overview.local?.[0] ?? null;
  if (localWithKey?.keyName) {
    identitySecret.value = await ndn.api.export_identity_secret(localWithKey.keyName);
  } else {
    identitySecret.value = null;
  }

  const encoded = bytesToBase64(identityCert.value);
  const dataUrl = `data:application/octet-stream;base64,${encoded}`;
  certQrimg.value = await QRCode.toDataURL(dataUrl, { scale: 6 });
  secretQrimg.value = '';
  secretPassword.value = '';
}

function downloadCert() {
  if (!identityCert.value?.length) return;
  const source = identityEntry.value ?? { identity: name.value, keyName: identityKeyName.value };
  const filename = formatIdentityFilename(source, {
    prefix: 'identity',
    ext: 'cert',
    fallbackIdentity: name.value,
  });
  const view = new Uint8Array(identityCert.value);
  const blob = new Blob([new Uint8Array(view).buffer as ArrayBuffer], {
    type: 'application/octet-stream',
  });
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  URL.revokeObjectURL(url);
}

async function buildSecretQr() {
  if (!identitySecret.value?.length) return;
  if (!secretPassword.value) return;

  const payload = await encryptSecret(identitySecret.value, secretPassword.value);
  const encoded = btoa(JSON.stringify(payload));
  secretQrimg.value = await QRCode.toDataURL(`ownly-secret:${encoded}`, { scale: 6 });
}


async function startScan() {
  scanError.value = '';
  if (!(window as any).BarcodeDetector) {
    scanError.value = 'QR scanning is not supported on this device.';
    return;
  }

  try {
    detector = new (window as any).BarcodeDetector({ formats: ['qr_code'] });
    stream = await navigator.mediaDevices.getUserMedia({
      video: { facingMode: 'environment' },
      audio: false,
    });
    if (videoEl.value) {
      videoEl.value.srcObject = stream;
      await videoEl.value.play();
      scanLoop();
    }
  } catch (err) {
    console.error(err);
    scanError.value = 'Unable to access the camera.';
  }
}

function stopScan() {
  if (rafId !== null) cancelAnimationFrame(rafId);
  rafId = null;
  if (stream) {
    stream.getTracks().forEach((t) => t.stop());
    stream = null;
  }
}

function scanLoop() {
  if (!videoEl.value || !canvasEl.value || !detector) return;
  const video = videoEl.value;
  const canvas = canvasEl.value;
  const ctx = canvas.getContext('2d');
  if (!ctx) return;

  canvas.width = video.videoWidth;
  canvas.height = video.videoHeight;
  ctx.drawImage(video, 0, 0, canvas.width, canvas.height);

  detector
    .detect(canvas)
    .then((codes: Array<{ rawValue: string }>) => {
      if (codes?.length) {
        stopScan();
        emit('decoded', codes[0].rawValue);
      } else {
        rafId = requestAnimationFrame(scanLoop);
      }
    })
    .catch(() => {
      rafId = requestAnimationFrame(scanLoop);
    });
}
</script>

<style scoped lang="scss">
img.qr {
  display: block;
  margin: 10px auto;
}

.scanner {
  position: relative;
  width: 100%;
  padding-top: 56.25%;
  background: #111;
  border-radius: 8px;
  overflow: hidden;
  border: 1px solid #ddd;
}

.video {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.hidden-canvas {
  display: none;
}
</style>
