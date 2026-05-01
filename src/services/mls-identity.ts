const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

export type ParsedMlsIdentity = {
  accountId: string;
  deviceId: string;
};

function normalizeAccountId(accountId: string): string {
  const trimmed = accountId.trim();
  if (!trimmed.startsWith('/')) {
    throw new Error(`Invalid MLS account ID: ${accountId}`);
  }
  return trimmed !== '/' ? trimmed.replace(/\/+$/, '') : trimmed;
}

export function encodeMlsIdentity(accountId: string, deviceId: string): string {
  const normalizedAccountId = normalizeAccountId(accountId);
  const trimmedDeviceId = deviceId.trim();
  if (!trimmedDeviceId) {
    throw new Error('Missing MLS device ID');
  }
  if (trimmedDeviceId.includes('/')) {
    throw new Error(`Invalid MLS device ID: ${deviceId}`);
  }
  return `${normalizedAccountId}/${trimmedDeviceId}`;
}

export function parseMlsIdentity(identity: string): ParsedMlsIdentity {
  const normalized = normalizeAccountId(identity.trim());
  const idx = normalized.lastIndexOf('/');

  if (idx <= 0) {
    throw new Error(`Invalid MLS identity: ${identity}`);
  }

  const deviceId = normalized.slice(idx + 1);
  if (!UUID_RE.test(deviceId)) {
    throw new Error(`Invalid MLS identity: ${identity}`);
  }

  return {
    accountId: normalized.slice(0, idx),
    deviceId,
  };
}

export function accountIdentityPrefix(accountId: string): string {
  return `${normalizeAccountId(accountId)}/`;
}
