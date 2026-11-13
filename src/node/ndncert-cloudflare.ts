import Cloudflare from 'cloudflare';
import { Resolver } from 'node:dns/promises';
import { setTimeout as delay } from 'node:timers/promises';

type EnrollmentApi = {
  has_testbed_key(): Promise<boolean>;
  connect_testbed(): Promise<void>;
  ndncert_dns(
    domain: string,
    confirm: (name: string, value: string, status: string) => Promise<string>,
  ): Promise<void>;
};

// SDK errors include response bodies; report status without leaking credentials.
function safeError(error: unknown): Error {
  if (error instanceof Cloudflare.APIError) {
    return new Error(`Cloudflare request failed${error.status ? ` (HTTP ${error.status})` : ''}`);
  }
  return error instanceof Error ? error : new Error('DNS enrollment failed');
}

export async function ensureTestbedCertificate(
  api: EnrollmentApi,
  env: NodeJS.ProcessEnv = process.env,
) {
  if (await api.has_testbed_key()) {
    return;
  }
  const domain = env.OWNLY_NDNCERT_DOMAIN;
  if (!domain) {
    return;
  }
  if (!env.CLOUDFLARE_API_TOKEN || !env.CLOUDFLARE_ZONE_ID) {
    throw new Error('DNS enrollment requires CLOUDFLARE_API_TOKEN and CLOUDFLARE_ZONE_ID');
  }
  const client = new Cloudflare({ apiToken: env.CLOUDFLARE_API_TOKEN });
  const zone = { zone_id: env.CLOUDFLARE_ZONE_ID };
  const resolver = new Resolver({ timeout: 2_000, tries: 1 });
  let deadline = 0;
  let recordId: string | undefined;
  let callbackError: Error | undefined;
  try {
    await api.connect_testbed();
    await api.ndncert_dns(domain, async (recordName, content, status) => {
      try {
        const name = recordName.replace(/\.$/, '');
        if (name.toLowerCase() !== `_ndncert-challenge.${domain}`.toLowerCase()) {
          throw new Error('Unexpected DNS challenge record');
        }
        if (!recordId) {
          const record = await client.dns.records.create({
            ...zone,
            type: 'TXT',
            name,
            content,
            ttl: 60,
          });
          recordId = record.id;
          // Fail normally while there is still time to delete the challenge record.
          deadline = Date.now() + 30_000;
        }
        if (status === 'wrong-record') {
          await delay(2_000);
        }
        while (Date.now() < deadline) {
          const answers = await resolver.resolveTxt(name).catch(() => []);
          if (answers.some((chunks) => chunks.join('') === content)) {
            return 'ready';
          }
          await delay(2_000);
        }
        throw new Error('DNS challenge propagation timed out');
      } catch (error) {
        // The WASM bridge replaces callback rejection with an empty confirmation.
        callbackError ??= safeError(error);
        throw callbackError;
      }
    });
  } catch (error) {
    throw callbackError ?? safeError(error);
  } finally {
    if (recordId) {
      try {
        await client.dns.records.delete(recordId, zone);
      } catch (error) {
        throw new Error(`TXT cleanup failed: ${safeError(error).message}`);
      }
    }
  }
}
