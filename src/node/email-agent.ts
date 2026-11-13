import { fork } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import {
  calendarDate,
  renderAgenda,
  selectAgenda,
  sendAgenda,
  validateMail,
  type MailConfig,
} from './agenda-sending';

type Env = Record<string, string | undefined>;
type FetchConfig = {
  workspace: string;
  project: string;
  stateDir: string;
  timeout: number;
  agenda: string;
  env: Env;
};

const required = (env: Env, key: string) => {
  const value = env[key];
  if (!value?.trim()) {
    throw new Error(`Missing ${key}`);
  }
  return value;
};

const FETCH_WORKER = '--ownly-email-fetch-worker';
type WorkerRequest = Omit<FetchConfig, 'env'> & { deadline: number };

/** Email-owned isolation: close does not unload Go or cancel pending operations. */
async function runFetchWorker(config: WorkerRequest) {
  const remaining = () => {
    const ms = config.deadline - Date.now();
    if (ms < 1) {
      throw new Error('Ownly fetch timed out');
    }
    return ms;
  };
  const { openHeadlessProject } = await import('./headless');
  const session = await openHeadlessProject({
    workspace: config.workspace,
    project: config.project,
    stateDir: config.stateDir,
    timeoutMs: remaining(),
    refresh: true,
    seedKeychain: process.env.OWNLY_SEED_KEYCHAIN,
    env: process.env,
  });
  let markdown: string;
  try {
    markdown = new TextDecoder().decode(await session.readFile(config.agenda, remaining()));
    if (!markdown.trim()) {
      throw new Error('Ownly agenda empty');
    }
  } finally {
    await session.close();
  }
  remaining();
  if (!process.send) {
    throw new Error('Ownly fetch IPC unavailable');
  }
  process.send({ markdown, refresh: session.refresh }, (error: Error | null) =>
    process.exit(error ? 1 : 0),
  );
}

/** A bounded child, no shell, no raw child logs (bundles/WASM dumps contain secrets). */
export async function fetchOwnly(config: FetchConfig): Promise<unknown> {
  const deadline = Date.now() + config.timeout;
  const env = { ...config.env };
  for (const key of Object.keys(env))
    if (key.startsWith('SMTP_') || key.startsWith('EMAIL_')) {
      delete env[key];
    }
  return new Promise<unknown>((resolve, reject) => {
    let result: unknown;
    let timedOut = false;
    const child = fork(fileURLToPath(import.meta.url), [FETCH_WORKER], {
      env,
      stdio: ['ignore', 'ignore', 'ignore', 'ipc'],
      execArgv: [],
    });
    const timer = setTimeout(
      () => {
        timedOut = true;
        child.kill('SIGKILL');
      },
      Math.max(1, deadline - Date.now()),
    );
    child.on('error', () => {
      child.kill('SIGKILL');
      clearTimeout(timer);
      reject(new Error('Ownly fetch could not start'));
    });
    child.on('message', (message) => {
      result = message;
    });
    child.on('close', (code, signal) => {
      clearTimeout(timer);
      if (!timedOut && Date.now() < deadline && code === 0 && !signal && result) {
        resolve(result);
      } else {
        reject(new Error('Ownly fetch failed or timed out; no email sent'));
      }
    });
    child.send(
      {
        workspace: config.workspace,
        project: config.project,
        stateDir: path.resolve(config.stateDir),
        timeout: config.timeout,
        agenda: config.agenda,
        deadline,
      } satisfies WorkerRequest,
      (error) => {
        if (error) {
          child.kill('SIGKILL');
        }
      },
    );
  });
}

export async function runEmailAgent(args: string[], env: Env = process.env) {
  const { values } = parseArgs({
    args,
    options: {
      'dry-run': { type: 'boolean', default: false },
      help: { type: 'boolean' },
    },
  });
  if (values.help) {
    console.log(HELP);
    return;
  }
  const dryRun = values['dry-run'];
  const config: FetchConfig = {
    workspace: required(env, 'OWNLY_WORKSPACE'),
    project: required(env, 'OWNLY_PROJECT'),
    stateDir: path.resolve(required(env, 'OWNLY_STATE_DIR')),
    timeout: Number(env.OWNLY_TIMEOUT_MS ?? '180000'),
    agenda: required(env, 'AGENDA_PATH'),
    env,
  };
  if (
    !Number.isSafeInteger(config.timeout) ||
    config.timeout < 1 ||
    config.timeout > 2_147_483_647
  ) {
    throw new Error('Invalid OWNLY_TIMEOUT_MS');
  }
  const today = calendarDate(new Date(), env.AGENDA_TIMEZONE ?? 'UTC');
  let mail: MailConfig | undefined;
  if (!dryRun) {
    mail = {
      from: required(env, 'EMAIL_FROM'),
      to: required(env, 'EMAIL_TO')
        .split(',')
        .map((s) => s.trim()),
      subject: env.EMAIL_SUBJECT ?? 'Ownly agenda',
      smtp: {
        host: required(env, 'SMTP_HOST'),
        port: Number(env.SMTP_PORT ?? '465'),
        security: (env.SMTP_SECURITY ?? 'tls') as MailConfig['smtp']['security'],
        user: env.SMTP_USER,
        pass: env.SMTP_PASS,
      },
    };
  }
  if (mail) {
    validateMail(mail);
  }
  const raw = await fetchOwnly(config);
  if (!raw || typeof raw !== 'object') {
    throw new Error('Invalid Ownly agenda result');
  }
  const result = raw as { markdown?: string; refresh?: string };
  if (
    !['acknowledged', 'unacknowledged', 'not-requested'].includes(result.refresh ?? '') ||
    typeof result.markdown !== 'string' ||
    !result.markdown.trim()
  ) {
    throw new Error('Ownly agenda empty or invalid refresh status; no email sent');
  }
  const selection = selectAgenda(result.markdown, today);
  if (dryRun) {
    return {
      status: 'dry-run' as const,
      refresh: result.refresh,
      dates: selection.dates,
      staleFallback: selection.staleFallback,
      markdown: selection.markdown,
      html: renderAgenda(selection.markdown),
    };
  }
  const sent = await sendAgenda(selection.markdown, mail!);
  return {
    ...sent,
    dates: selection.dates,
    staleFallback: selection.staleFallback,
    refresh: result.refresh,
  };
}

const HELP = `Ownly agenda email agent — fetch, select, render and SEND by default.
Usage: node dist/headless/email-agent.js [--dry-run]
Required: OWNLY_WORKSPACE, OWNLY_PROJECT, OWNLY_STATE_DIR, AGENDA_PATH
Sending: EMAIL_FROM, EMAIL_TO (comma-separated), SMTP_HOST
Optional: EMAIL_SUBJECT, AGENDA_TIMEZONE (UTC), OWNLY_TIMEOUT_MS (180000),
OWNLY_FAST_JOIN_BUNDLE, OWNLY_PSK_HEX, OWNLY_SEED_KEYCHAIN,
SMTP_PORT (465), SMTP_SECURITY (tls|starttls|plain), SMTP_USER, SMTP_PASS.
--dry-run fetches and prints selected Markdown without opening SMTP.
Peer refresh is best-effort (at most 5000 ms); nonempty available agenda data is required.
EMAIL RESULT reports refresh=acknowledged|unacknowledged|not-requested.
Refresh is not proof of globally latest peer state.`;

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  if (process.argv.length === 3 && process.argv[2] === FETCH_WORKER && process.send) {
    process.once('disconnect', () => process.exit(1));
    process.once('message', (message) => {
      runFetchWorker(message as WorkerRequest).catch(() => process.exit(1));
    });
  } else {
    runEmailAgent(process.argv.slice(2))
      .then((result) => {
        if (!result) {
          return;
        }
        if (result.status === 'dry-run') {
          console.log(result.markdown);
        }
        // Print no raw child logs, transport response or credentials.
        console.log(
          'EMAIL RESULT: ' +
            JSON.stringify({
              status: result.status,
              messageId: 'messageId' in result ? result.messageId : undefined,
              dates: result.dates,
              staleFallback: result.staleFallback,
              refresh: result.refresh,
            }),
        );
      })
      .catch((error) => {
        console.error('FATAL: ' + error.message);
        process.exitCode = 1;
      });
  }
}
