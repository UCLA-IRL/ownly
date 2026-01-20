type IdentityNameSource = { identity?: string; keyName?: string } | null | undefined;

export function deriveIdentityFromKeyName(keyName?: string): string {
  if (!keyName) return '';
  const parts = keyName.split('/').filter(Boolean);
  if (!parts.length) return '';

  const keyIndex = parts.findIndex((part) => part.toLowerCase() === 'key');
  if (keyIndex > 0) return '/' + parts.slice(0, keyIndex).join('/');
  if (parts.length > 1) return '/' + parts.slice(0, -1).join('/');
  return '/' + parts[0];
}

export function extractKeyIdFromName(name?: string): string {
  if (!name) return '';
  const parts = name.split('/').filter(Boolean);
  if (!parts.length) return '';

  const keyIndex = parts.findIndex((part) => part.toLowerCase() === 'key');
  if (keyIndex !== -1 && keyIndex + 1 < parts.length) return parts[keyIndex + 1];
  return parts[parts.length - 1];
}

export function describeKeyId(keyName?: string): string {
  const raw = extractKeyIdFromName(keyName);
  if (!raw) return '';

  const bytes: number[] = [];
  for (let i = 0; i < raw.length; ) {
    if (raw[i] === '%' && i + 2 < raw.length) {
      const hex = raw.slice(i + 1, i + 3);
      const byte = Number.parseInt(hex, 16);
      if (Number.isNaN(byte)) {
        bytes.push(raw.charCodeAt(i));
        i += 1;
      } else {
        bytes.push(byte);
        i += 3;
      }
    } else {
      bytes.push(raw.charCodeAt(i));
      i += 1;
    }
  }

  const decoded = new Uint8Array(bytes);
  if (!decoded.length) return raw;

  let utf8 = '';
  try {
    utf8 = new TextDecoder().decode(decoded);
  } catch {
    utf8 = '';
  }
  if (utf8 && /^[\x20-\x7E]+$/.test(utf8)) return utf8;

  const hex = Array.from(decoded)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
  return hex || raw;
}

export function certNameToDate(certName?: string): Date | null {
  if (!certName) return null;
  const parts = certName.split('/').filter(Boolean);
  if (!parts.length) return null;

  const raw = parts[parts.length - 1];
  if (!raw) return null;

  let versionStr = raw;
  try {
    versionStr = decodeURIComponent(raw);
  } catch {
    versionStr = raw;
  }

  if (!versionStr.startsWith('v=')) return null;
  const numStr = versionStr.slice(2);
  if (!numStr) return null;

  const numeric = Number.parseInt(numStr, 10);
  if (!Number.isFinite(numeric) || numeric <= 0) return null;

  const millis = numeric >= 1e12 ? numeric : numeric * 1000;

  const date = new Date(millis);
  return Number.isNaN(date.getTime()) ? null : date;
}

export function formatIdentityFilename(
  source: IdentityNameSource,
  options: { prefix: string; ext: string; fallbackIdentity?: string; fallbackKeyId?: string },
): string {
  const identityRaw =
    source?.identity ?? deriveIdentityFromKeyName(source?.keyName) ?? options.fallbackIdentity ?? '';
  const keyIdRaw = extractKeyIdFromName(source?.keyName);

  const sanitize = (value: string, fallback: string) =>
    value
      .replace(/^[\\/]+/, '')
      .replace(/[\\/:*?"<>|]+/g, '-')
      .replace(/\s+/g, '-')
      .replace(/-+/g, '-')
      .replace(/^-+|-+$/g, '') || fallback;

  const identityPart = sanitize(identityRaw, 'identity');
  const keyIdPart = sanitize(keyIdRaw, options.fallbackKeyId ?? 'key');

  const parts = [options.prefix, identityPart, keyIdPart].filter(Boolean);
  return `${parts.join('-')}.${options.ext}`;
}
