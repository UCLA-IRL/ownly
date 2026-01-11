export type EncryptedSecretPayload = {
  salt: string;
  iv: string;
  data: string;
};

export function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function bytesToBase64(bytes: Uint8Array): string {
  let str = '';
  bytes.forEach((b) => (str += String.fromCharCode(b)));
  return btoa(str);
}

export function decodeQrDataPayload(raw: string): Uint8Array {
  try {
    if (raw.startsWith('data:')) {
      const [, base64] = raw.split(',', 2);
      if (!base64) throw new Error('Unable to decode QR payload');
      return base64ToBytes(base64);
    }
    if (raw.startsWith('ownly-cert:')) {
      return base64ToBytes(raw.slice('ownly-cert:'.length));
    }
    return base64ToBytes(raw);
  } catch (err) {
    console.error(err);
    throw new Error('Unable to decode QR payload');
  }
}

export async function encryptSecret(
  secret: Uint8Array,
  password: string,
  iterations = 150_000,
): Promise<EncryptedSecretPayload> {
  const enc = new TextEncoder();
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const keyMat = await crypto.subtle.importKey('raw', enc.encode(password), 'PBKDF2', false, [
    'deriveKey',
  ]);
  const key = await crypto.subtle.deriveKey(
    { name: 'PBKDF2', hash: 'SHA-256', salt: new Uint8Array(salt).buffer as ArrayBuffer, iterations },
    keyMat,
    { name: 'AES-GCM', length: 256 },
    false,
    ['encrypt'],
  );

  const cipher = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv: new Uint8Array(iv).buffer as ArrayBuffer },
    key,
    new Uint8Array(secret).buffer as ArrayBuffer,
  );
  return {
    salt: bytesToBase64(salt),
    iv: bytesToBase64(iv),
    data: bytesToBase64(new Uint8Array(cipher)),
  };
}

export async function decryptSecretPayload(
  raw: string,
  password: string,
  iterations = 150_000,
): Promise<Uint8Array> {
  const prefix = 'ownly-secret:';
  const encoded = raw.startsWith(prefix) ? raw.slice(prefix.length) : raw;
  let payload: EncryptedSecretPayload;
  try {
    payload = JSON.parse(atob(encoded));
  } catch (err) {
    console.error(err);
    throw new Error('Invalid encrypted payload');
  }

  const salt = base64ToBytes(payload.salt);
  const iv = base64ToBytes(payload.iv);
  const data = base64ToBytes(payload.data);

  const enc = new TextEncoder();
  const keyMat = await crypto.subtle.importKey('raw', enc.encode(password), 'PBKDF2', false, [
    'deriveKey',
  ]);
  const key = await crypto.subtle.deriveKey(
    { name: 'PBKDF2', hash: 'SHA-256', salt: new Uint8Array(salt).buffer as ArrayBuffer, iterations },
    keyMat,
    { name: 'AES-GCM', length: 256 },
    false,
    ['decrypt'],
  );

  const plain = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv: new Uint8Array(iv).buffer as ArrayBuffer },
    key,
    new Uint8Array(data).buffer as ArrayBuffer,
  );
  return new Uint8Array(plain);
}
