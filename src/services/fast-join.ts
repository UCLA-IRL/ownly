export type FastJoinInvitation = {
  owner_cert: Uint8Array;
  ephemeral_secret: Uint8Array;
  ephemeral_cert: Uint8Array;
  invitee_identity: string;
};

export type FastJoinBundle = {
  v: 5;
  label: string;
  wksp: string;
  psk: string;
  // The NDN name the inviter designated for the invitee. Surfaced in the
  // join modal so the user can see who they are joining as before clicking
  // through; also used to verify the bundle's ephemeral cert chains to the
  // expected identity.
  inviteeIdentity: string;
  ownerCert: Uint8Array;
  ephemeralSecret: Uint8Array;
  ephemeralCert: Uint8Array;
};

type JsonFastJoinBundle = Omit<
  FastJoinBundle,
  'ownerCert' | 'ephemeralSecret' | 'ephemeralCert'
> & {
  ownerCert: string;
  ephemeralSecret: string;
  ephemeralCert: string;
};

function bytesToBase64Url(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary)
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/g, '');
}

function base64UrlToBytes(input: string): Uint8Array {
  const padded = input
    .replace(/-/g, '+')
    .replace(/_/g, '/')
    .padEnd(Math.ceil(input.length / 4) * 4, '=');
  const binary = atob(padded);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    out[i] = binary.charCodeAt(i);
  }
  return out;
}

export function serializeFastJoinBundle(bundle: FastJoinBundle): string {
  const json: JsonFastJoinBundle = {
    ...bundle,
    ownerCert: bytesToBase64Url(bundle.ownerCert),
    ephemeralSecret: bytesToBase64Url(bundle.ephemeralSecret),
    ephemeralCert: bytesToBase64Url(bundle.ephemeralCert),
  };
  return bytesToBase64Url(new TextEncoder().encode(JSON.stringify(json)));
}

export function parseFastJoinBundle(input: string): FastJoinBundle {
  const json = JSON.parse(new TextDecoder().decode(base64UrlToBytes(input))) as JsonFastJoinBundle;
  if (json.v !== 5) {
    throw new Error('Unsupported fast join link version');
  }
  if (!json.label || !json.wksp || !json.psk) {
    throw new Error('Incomplete fast join link');
  }
  if (!json.inviteeIdentity) {
    throw new Error('Fast join link is missing invitee identity');
  }
  if (!json.ownerCert) {
    throw new Error('Incomplete fast join trust bundle');
  }
  if (!json.ephemeralSecret || !json.ephemeralCert) {
    throw new Error('Incomplete fast join identity bundle');
  }

  return {
    ...json,
    ownerCert: base64UrlToBytes(json.ownerCert),
    ephemeralSecret: base64UrlToBytes(json.ephemeralSecret),
    ephemeralCert: base64UrlToBytes(json.ephemeralCert),
  };
}

/**
 * Extracts the identity prefix from a fast-join ephemeral certificate's
 * name. The cert name is `<identity>/KEY/<kid>/ephemeral/<ver>`. Returns
 * the identity prefix (`/a/b/c`) or null if the cert is malformed.
 *
 * Used at login time to verify that the identity the NDNCERT step would
 * produce matches the identity the inviter designated for the fast-join
 * invitation.
 */
export function extractCertIdentityPrefix(cert: Uint8Array): string | null {
  // NDN-TLV uses BER-style length encoding:
  //   0x00-0xFC → 1-byte literal length
  //   0xFD      → 2-byte big-endian length follows
  //   0xFE      → 4-byte big-endian length follows
  //   0xFF      → 8-byte big-endian length follows
  const NDN_TLV_DATA = 0x06;
  const NDN_TLV_NAME = 0x07;
  const NDN_TLV_GENERIC = 0x08;

  let off = 0;
  if (cert.length === 0 || cert[off] !== NDN_TLV_DATA) return null;
  off += 1;
  const [dataLen, dlb] = readNat(cert, off);
  off += dlb;
  if (off + dataLen > cert.length) return null;
  if (cert[off] !== NDN_TLV_NAME) return null;
  off += 1;
  const [nameLen, nlb] = readNat(cert, off);
  off += nlb;
  const nameEnd = off + nameLen;
  if (nameEnd > cert.length) return null;
  const comps: string[] = [];
  while (off < nameEnd) {
    const t = cert[off];
    off += 1;
    if (t === undefined) return null;
    const [cl, clb] = readNat(cert, off);
    off += clb;
    if (off + cl > nameEnd) return null;
    if (t !== NDN_TLV_GENERIC) return null;
    comps.push(
      new TextDecoder('utf-8', { fatal: false }).decode(
        cert.subarray(off, off + cl),
      ),
    );
    off += cl;
  }
  const keyIdx = comps.indexOf('KEY');
  if (keyIdx < 1) return null;
  return '/' + comps.slice(0, keyIdx).join('/');
}

function readNat(bytes: Uint8Array, offset: number): [number, number] {
  const b = bytes[offset];
  if (b === undefined) throw new Error('truncated length');
  if (b < 0xfd) return [b, 1];
  let width: number;
  if (b === 0xfd) width = 2;
  else if (b === 0xfe) width = 4;
  else if (b === 0xff) width = 8;
  else throw new Error('invalid length marker');
  if (offset + 1 + width > bytes.length) throw new Error('truncated length');
  let value = 0;
  for (let i = 0; i < width; i++) {
    value = (value << 8) | bytes[offset + 1 + i];
  }
  return [value, 1 + width];
}

/**
 * Parses a fast-join bundle from the current URL (either `route.query.fj`
 * or the URL fragment) without throwing. Returns null when no bundle is
 * present or when parsing fails.
 */
export function parseFastJoinBundleFromLocation(
  queryFj: unknown,
  hash: string,
): FastJoinBundle | null {
  const candidate =
    typeof queryFj === 'string' && queryFj
      ? queryFj
      : new URLSearchParams(hash.slice(1)).get('fj');
  if (typeof candidate !== 'string' || !candidate) return null;
  try {
    return parseFastJoinBundle(candidate);
  } catch {
    return null;
  }
}
