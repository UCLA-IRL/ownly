import fs from 'node:fs';
import { devNull } from 'node:os';
import { createServer, type ServerResponse } from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { marked } from 'marked';
import sanitizeHtml from 'sanitize-html';
import type { Doc } from 'yjs';
import type { HeadlessProject } from './headless';

const HELP = `Ownly whole-agenda HTTP consumer
Usage: node dist/headless/agenda-http.js [--host 127.0.0.1] [--port 8080]
--host <host>   Listener address (default: 127.0.0.1)
--port <port>   Listener port, 1–65535 (default: 8080)
--help          Show help without opening Ownly
Required: OWNLY_WORKSPACE, OWNLY_PROJECT, absolute OWNLY_STATE_DIR, AGENDA_PATH (.md).
OWNLY_TIMEOUT_MS bounds startup (default: 180000).
Optional: OWNLY_FAST_JOIN_BUNDLE, OWNLY_PSK_HEX, OWNLY_SEED_KEYCHAIN;
existing OWNLY_NDNCERT_DOMAIN, CLOUDFLARE_API_TOKEN, CLOUDFLARE_ZONE_ID enrollment.
Use one process per state directory. Relative workspace attachments are not hosted.`;

function required(key: string): string {
  const value = process.env[key];
  if (!value?.trim()) {
    throw new Error('Invalid configuration');
  }
  return value;
}

function integer(value: string, max: number): number {
  const number = Number(value);
  if (!/^\d+$/.test(value) || !Number.isSafeInteger(number) || number < 1 || number > max) {
    throw new Error('Invalid configuration');
  }
  return number;
}

function respond(
  response: ServerResponse,
  method: string | undefined,
  status: number,
  body: string,
  contentType = 'text/plain',
) {
  response.writeHead(status, {
    'Cache-Control': 'no-store',
    'Content-Type': `${contentType}; charset=utf-8`,
    'Content-Length': Buffer.byteLength(body),
    ...(status === 405 ? { Allow: 'GET, HEAD' } : {}),
  });
  response.end(method === 'HEAD' ? undefined : body);
}

async function renderAgenda(session: HeadlessProject, agenda: string): Promise<string> {
  const before = session.project.getFileMeta(agenda);
  if (before?.is_blob) {
    throw new Error('Agenda unavailable');
  }
  const bytes = await session.readFile(agenda, 5000);
  const after = session.project.getFileMeta(agenda);
  if (!after || after.is_blob || (before && before.uuid !== after.uuid)) {
    throw new Error('Agenda unavailable');
  }
  const markdown = new TextDecoder().decode(bytes);
  if (!markdown.trim()) {
    throw new Error('Agenda unavailable');
  }
  return sanitizeHtml(marked.parse(markdown, { async: false }));
}

function page(content: string): string {
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Ownly agenda</title>
</head>
<body>
<p>This page shows this Ownly participant's available agenda. Updates appear automatically.</p>
<main>${content}</main>
<script>
const updates = new EventSource('/updates');
updates.onmessage = event => {
  document.querySelector('main').innerHTML = JSON.parse(event.data);
};
</script>
</body>
</html>`;
}

// The CLI owns this process. Backend diagnostics can contain private material;
// Go writes directly through fs as well as JS console/streams. Keep both output
// channels silent for the whole backend lifetime, including shutdown/panics.
function silenceBackendOutput() {
  const sink = fs.openSync(devNull, 'w');
  const output = fs.createWriteStream(devNull, { fd: sink, autoClose: false });
  process.stdout.write = output.write.bind(output) as typeof process.stdout.write;
  process.stderr.write = output.write.bind(output) as typeof process.stderr.write;
  const write = fs.write;
  const writeSync = fs.writeSync;
  fs.write = ((fd: number, ...args: unknown[]) =>
    Reflect.apply(write, fs, [fd === 1 || fd === 2 ? sink : fd, ...args])) as typeof fs.write;
  fs.writeSync = ((fd: number, ...args: unknown[]) =>
    Reflect.apply(writeSync, fs, [
      fd === 1 || fd === 2 ? sink : fd,
      ...args,
    ])) as typeof fs.writeSync;
}

async function main(args: string[]) {
  const { values } = parseArgs({
    args,
    options: {
      host: { type: 'string', default: '127.0.0.1' },
      port: { type: 'string', default: '8080' },
      help: { type: 'boolean' },
    },
  });
  if (values.help) {
    console.log(HELP);
    return;
  }
  const workspace = required('OWNLY_WORKSPACE');
  const project = required('OWNLY_PROJECT');
  const stateDir = required('OWNLY_STATE_DIR');
  const agenda = required('AGENDA_PATH');
  const timeoutMs = integer(process.env.OWNLY_TIMEOUT_MS ?? '180000', 2_147_483_647);
  const port = integer(values.port, 65535);
  const host = values.host;
  if (!host.trim() || !path.isAbsolute(stateDir) || !agenda.endsWith('.md')) {
    throw new Error('Invalid configuration');
  }
  const seedKeychain = process.env.OWNLY_SEED_KEYCHAIN;
  const seed = seedKeychain ? path.resolve(seedKeychain) : undefined;
  silenceBackendOutput();

  let session: HeadlessProject | undefined;
  let opening: Promise<HeadlessProject> | undefined;
  let stopping = false;
  let latest = '';
  let reading: Promise<string> | undefined;
  let agendaDoc: Doc | undefined;
  const clients = new Set<ServerResponse>();
  const snapshot = () => {
    reading ??= (async () => {
      if (stopping || !session) {
        throw new Error('Agenda unavailable');
      }
      const doc = await session.project.getFile(agenda);
      if (stopping) {
        throw new Error('Agenda unavailable');
      }
      if (doc !== agendaDoc) {
        agendaDoc?.off('updateV2', onUpdate);
        agendaDoc = doc;
        agendaDoc.on('updateV2', onUpdate);
      }
      const html = await renderAgenda(session, agenda);
      if (stopping) {
        throw new Error('Agenda unavailable');
      }
      if (html !== latest) {
        latest = html;
        for (const response of clients) response.write(`data: ${JSON.stringify(html)}\n\n`);
      }
      return latest;
    })().finally(() => {
      reading = undefined;
    });
    return reading;
  };
  const onUpdate = () => {
    // An update arriving during a read must trigger another read afterward.
    void (reading ?? Promise.resolve()).then(snapshot).catch(() => {
      for (const response of clients) response.end();
      clients.clear();
    });
  };
  const server = createServer((request, response) => {
    void (async () => {
      let url: URL;
      try {
        url = new URL(request.url ?? '/', 'http://localhost');
      } catch {
        respond(response, request.method, 404, 'Not found\n');
        return;
      }
      if (url.pathname !== '/' && url.pathname !== '/updates') {
        respond(response, request.method, 404, 'Not found\n');
        return;
      }
      if (request.method !== 'GET' && request.method !== 'HEAD') {
        respond(response, request.method, 405, 'Method not allowed\n');
        return;
      }
      try {
        const html = await snapshot();
        if (url.pathname === '/') {
          respond(response, request.method, 200, page(html), 'text/html');
        } else if (request.method === 'HEAD') {
          respond(response, 'HEAD', 200, '', 'text/event-stream');
        } else if (!response.destroyed) {
          response.writeHead(200, {
            'Content-Type': 'text/event-stream; charset=utf-8',
            'Cache-Control': 'no-store',
          });
          clients.add(response);
          response.once('close', () => clients.delete(response));
          response.write(`data: ${JSON.stringify(latest)}\n\n`);
        }
      } catch {
        respond(response, request.method, 503, 'Agenda unavailable\n');
      }
    })().catch(() => response.destroy());
  });

  const shutdown = async (code: number) => {
    if (stopping) {
      return;
    }
    stopping = true;
    clearTimeout(startupTimer);
    agendaDoc?.off('updateV2', onUpdate);
    for (const response of clients) response.end();
    clients.clear();
    const forceExit = setTimeout(() => process.exit(1), 5000);
    try {
      await Promise.all([
        new Promise<void>((resolve) => {
          if (server.listening) {
            server.close(() => resolve());
          } else {
            resolve();
          }
        }),
        (async () => {
          const opened = session ?? (await opening);
          await opened?.close();
        })(),
      ]);
    } catch {
      code = 1;
    }
    clearTimeout(forceExit);
    process.exit(code);
  };
  const startupTimer = setTimeout(() => {
    console.error('Ownly HTTP startup timed out');
    void shutdown(1);
  }, timeoutMs);
  process.once('SIGINT', () => void shutdown(0));
  process.once('SIGTERM', () => void shutdown(0));
  server.on('error', () => {
    console.error('Ownly HTTP listener failed');
    void shutdown(1);
  });

  try {
    const { openHeadlessProject } = await import('./headless');
    if (stopping) {
      return;
    }
    opening = openHeadlessProject({
      workspace,
      project,
      stateDir,
      timeoutMs,
      refresh: true,
      seedKeychain: seed,
      env: process.env,
    });
    session = await opening;
    if (stopping) {
      return;
    }
    server.listen(port, host, () => clearTimeout(startupTimer));
  } catch {
    console.error('Ownly HTTP startup failed');
    await shutdown(1);
  }
}

const [, entrypoint, ...args] = process.argv;
if (entrypoint && path.resolve(entrypoint) === fileURLToPath(import.meta.url)) {
  main(args).catch(() => {
    console.error('Ownly HTTP configuration failed; use --help');
    process.exit(1);
  });
}
