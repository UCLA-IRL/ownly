type ErrorDetails = {
  raw: string;
  lower: string;
  name: string;
};

function normalizeError(err: unknown): ErrorDetails {
  let raw = 'Unknown error';
  let name = '';

  if (err instanceof Error) {
    raw = err.message || raw;
    name = err.name || name;
  } else if (typeof err === 'string') {
    raw = err;
  } else if (err && typeof err === 'object') {
    if ('message' in err && typeof (err as { message?: unknown }).message === 'string') {
      raw = (err as { message: string }).message || raw;
    }
    if ('name' in err && typeof (err as { name?: unknown }).name === 'string') {
      name = (err as { name: string }).name || name;
    }
  }

  if (!raw || raw === 'Unknown error') {
    try {
      const json = JSON.stringify(err);
      if (json) raw = json;
    } catch {}
  }

  return { raw, lower: raw.toLowerCase(), name: name.toLowerCase() };
}

function isQrFormatError(lower: string): boolean {
  return (
    lower.includes('unable to decode qr payload') ||
    lower.includes('invalidcharactererror') ||
    lower.includes('not correctly encoded') ||
    lower.includes('invalid qr')
  );
}

export function describeIdentityKeyImportError(err: unknown): string {
  const { raw, lower, name } = normalizeError(err);

  if (name === 'operationerror' || lower.includes('operationerror')) {
    return 'Password incorrect.';
  }
  if (lower.includes('invalid encrypted payload')) {
    return 'Invalid encrypted payload format.';
  }
  if (lower.includes('empty keychain entry')) {
    return 'Key file is empty.';
  }
  if (lower.includes('no valid keychain entry found')) {
    return 'Invalid key format.';
  }
  if (isQrFormatError(lower)) {
    return 'Invalid QR format.';
  }
  if (lower.includes('identity key already exists')) {
    return 'Identity key already exists.';
  }
  if (lower.includes('key name already exists as peer cert')) {
    return 'Key already exists in peer certificates.';
  }
  if (lower.includes('identity key must use')) {
    const match = raw.match(/identity key must use (.+)$/i);
    return match ? `Identity name mismatch. Expected ${match[1]}.` : 'Identity name mismatch.';
  }
  if (lower.includes('no signing key found')) {
    return 'No signing key found in key file.';
  }
  if (lower.includes('no usable identity key found')) {
    return 'No usable identity key found in key file.';
  }
  if (lower.includes('no testbed key')) {
    return 'No testbed key available. Connect to the testbed first.';
  }
  if (lower.includes('unknown file format') || lower.includes('unsupported file format')) {
    return 'Invalid key format.';
  }

  return raw || 'Unknown error';
}

export function describePeerCertImportError(err: unknown): string {
  const { raw, lower } = normalizeError(err);

  if (lower.includes('invalid certificate content type')) {
    const match = raw.match(/invalid certificate content type for (.+)$/i);
    return match ? `Invalid certificate format: ${match[1]}.` : 'Invalid certificate format.';
  }
  if (lower.includes('key name already exists as identity key')) {
    return 'Key already exists in identity keys.';
  }
  if (lower.includes('peer certificate already exists')) {
    return 'Peer certificate already exists.';
  }
  if (lower.includes('peer key already exists')) {
    return 'Peer key already exists.';
  }
  if (lower.includes('empty keychain entry')) {
    return 'Certificate file is empty.';
  }
  if (lower.includes('no valid keychain entry found')) {
    return 'Invalid certificate format.';
  }
  if (lower.includes('certificate is expired')) {
    const match = raw.match(/certificate is expired: (.+)$/i);
    return match ? `Certificate is expired: ${match[1]}.` : 'Certificate is expired.';
  }
  if (lower.includes('not self-signed')) {
    const match = raw.match(/certificate (.+) is not self-signed/i);
    return match ? `Certificate is not self-signed: ${match[1]}.` : 'Certificate is not self-signed.';
  }
  if (isQrFormatError(lower)) {
    return 'Invalid QR format.';
  }
  if (lower.includes('unknown file format') || lower.includes('unsupported file format')) {
    return 'Invalid certificate format.';
  }

  return raw || 'Unknown error';
}
