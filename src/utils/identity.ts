type IdentityNameSource = { identity?: string; keyName?: string } | null | undefined;

function sanitizeFilenamePart(value: string, fallback: string): string {
  const sanitized = value
    .replace(/^[\\/]+/, '')
    .replace(/[\\/:*?"<>|]+/g, '-')
    .replace(/\s+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-+|-+$/g, '');
  return sanitized || fallback;
}

export function deriveIdentityFromKeyName(keyName?: string): string {
  if (!keyName) return '';
  const parts = keyName.split('/').filter(Boolean);
  if (!parts.length) return '';

  const keyIndex = parts.findIndex((part) => part.toLowerCase() === 'key');
  if (keyIndex > 0) {
    return '/' + parts.slice(0, keyIndex).join('/');
  }

  if (parts.length > 1) {
    return '/' + parts.slice(0, -1).join('/');
  }

  return '/' + parts[0];
}

export function extractKeyIdFromName(name?: string): string {
  if (!name) return '';
  const parts = name.split('/').filter(Boolean);
  if (!parts.length) return '';

  const keyIndex = parts.findIndex((part) => part.toLowerCase() === 'key');
  if (keyIndex !== -1 && keyIndex + 1 < parts.length) {
    return parts[keyIndex + 1];
  }

  return parts[parts.length - 1];
}

export function formatIdentityFilename(
  source: IdentityNameSource,
  options: { prefix: string; ext: string; fallbackIdentity?: string; fallbackKeyId?: string },
): string {
  const identityRaw =
    source?.identity ?? deriveIdentityFromKeyName(source?.keyName) ?? options.fallbackIdentity ?? '';
  const identityPart = sanitizeFilenamePart(identityRaw, 'identity');

  const keyIdRaw = extractKeyIdFromName(source?.keyName);
  const keyId = sanitizeFilenamePart(keyIdRaw, options.fallbackKeyId ?? 'key');

  const parts = [options.prefix, identityPart, keyId].filter(Boolean);
  return `${parts.join('-')}.${options.ext}`;
}
