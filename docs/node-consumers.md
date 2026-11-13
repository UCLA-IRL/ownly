# Node consumers

The Node consumers require Node 23+ (`node:sqlite`); the unified layout was
verified with Node 26.13.0. Build from the locked dependencies:

```sh
npm ci
npm run type-check
npx --no-install tsc -p tsconfig.headless.json
npm run build:headless
```

`build:email` is an alias for `build:headless`. All Node entries and their shared
modules, `main.wasm` and `wasm_exec.js` live in `dist/headless/`. Keep the installed
dependencies and complete output tree. A browser build replaces `dist/`, so build
Node consumers last.

### Generic headless API and export CLI

Call `openHeadlessProject` from `dist/headless/headless.js` to open a workspace
session. Each session exposes `project`, `filesRoot`, `refresh`,
`readFile(entryPath, timeoutMs?)` (bytes), and an idempotent async `close()`:

```js
import { openHeadlessProject } from './dist/headless/headless.js';

const session = await openHeadlessProject({
  workspace: process.env.OWNLY_WORKSPACE,
  project: process.env.OWNLY_PROJECT,
  stateDir: process.env.OWNLY_STATE_DIR,
  refresh: true,
});
try {
  const bytes = await session.readFile('agenda.md', 5000);
  // The caller owns decoding, rendering and empty-content policy.
} finally {
  await session.close();
}
```

Only one active headless session is supported per process. The Go runtime remains
loaded after closing a workspace; executable consumers own process termination.
Use one process per complete persistent state directory. Stop its owner before
moving state/identity; do not share live SQLite/keychain directories between
HTTP and email processes or run a second copy of the same live device identity.

The standalone generic project-export CLI retains `--wksp`, `--proj`, positional
workspace/project, state/keychain/join/timeout/refresh options. See `--help`.

### Agenda email

```sh
# After loading a private environment:
node dist/headless/email-agent.js --dry-run
```

Normal execution **sends email**; `--dry-run` opens no SMTP connection. Required:
`OWNLY_WORKSPACE`, `OWNLY_PROJECT`, `OWNLY_STATE_DIR`, `AGENDA_PATH`.
Sending also requires `EMAIL_FROM`, comma-separated `EMAIL_TO`, and `SMTP_HOST`;
see `--help` for SMTP and selection settings. Fetching uses the generic API in an
email-owned child to isolate backend logs and enforce one total fetch deadline.
Selection and sending start only after fetching and cleanup succeed.

### Whole-agenda HTTP service

```sh
# After loading a private environment:
/usr/bin/node dist/headless/agenda-http.js --host 127.0.0.1 --port 8080
```

Use the absolute Node executable path on your host (`/usr/bin/node` was tested).
The current Go runtime can select its unsupported native network transport when
launched with bare `node`; this also affects `agenda:http` on such environments.
The email fetch worker already launches via the absolute `process.execPath`.

Required environment: `OWNLY_WORKSPACE`, `OWNLY_PROJECT`, absolute
`OWNLY_STATE_DIR`, and `AGENDA_PATH` ending in `.md`. `OWNLY_TIMEOUT_MS` bounds
startup (default 180000). Existing enrollment/join inputs, only when needed:
`OWNLY_FAST_JOIN_BUNDLE`, `OWNLY_PSK_HEX`, `OWNLY_SEED_KEYCHAIN`,
`OWNLY_NDNCERT_DOMAIN`, `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ZONE_ID`.
Keep credentials and complete state outside the checkout and HTTP document root.
Use Node's `--env-file=/absolute/private/agenda.env` to load private `KEY=value`
settings.

The HTTP process keeps one session open with best-effort startup refresh.
`GET /` embeds the **whole** available nonempty Markdown file as sanitized HTML.
The browser opens one Server-Sent Events connection to `GET /updates`. The
server sends the current sanitized HTML immediately, then pushes it again when
it changes. `EventSource` handles reconnecting. The HTTP consumer subscribes to
`updateV2` on the existing Yjs document returned by `session.project.getFile()`
and reuses the bounded file-read/render path when it changes. The stream stays
open between updates.

`HEAD /` returns corresponding headers without a body. Missing, empty, blob,
incomplete or failed reads return generic `503` before streaming; an active
stream closes on a failed read so the browser can reconnect. Other paths return
`404`, other methods `405`. Every response is `Cache-Control: no-store`.
Backend stdout/stderr are suppressed for the HTTP process lifetime
to keep raw diagnostics private; monitor exit status and HTTP availability.
SIGINT/SIGTERM close event streams, the listener, and the workspace with a
five-second shutdown bound.

Run under your existing supervisor with automatic restart and proxy a dedicated
HTTPS hostname, preserving paths, to `http://127.0.0.1:8080`. Disable proxy/CDN
caching. The page reflects this participant's available view. Relative Ownly
attachments require separate hosting.
