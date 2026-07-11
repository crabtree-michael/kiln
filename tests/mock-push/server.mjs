// Mock Web Push service for the keyless e2e notification spec (design
// docs/keyless-e2e-tests-design.md §Test 2). A real browser push subscription
// points at a real push service (FCM/Mozilla); keyless, the test registers a
// subscription whose endpoint is THIS server, so when a notify.send fires the
// backend's real push.Sender (RFC 8291 encryption + VAPID signing) POSTs the
// encrypted push here and we record it. The body is encrypted (aes128gcm) and
// deliberately not decrypted — the assertion is that a signed push was
// *delivered* to the endpoint, which proves the whole transport ran.
//
// Zero-dependency plain HTTP:
//   POST /push/:id   — record a delivered push (headers only); responds 201.
//   GET  /_pushes    — JSON array of everything recorded (optionally ?id=).
//   POST /_reset     — clear the record (per-test isolation).
//
// Config: KILN_PUSH_PORT (default 7072).
import { createServer } from 'node:http';

const PORT = Number(process.env.KILN_PUSH_PORT ?? 7072);

/** @type {{id:string, headers:Record<string,string|string[]|undefined>, bytes:number, at:number}[]} */
const pushes = [];

const server = createServer((req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`);

  if (req.method === 'POST' && url.pathname.startsWith('/push/')) {
    const id = url.pathname.slice('/push/'.length);
    let bytes = 0;
    req.on('data', (chunk) => {
      bytes += chunk.length;
    });
    req.on('end', () => {
      pushes.push({ id, headers: req.headers, bytes, at: Date.now() });
      res.writeHead(201).end();
    });
    return;
  }

  if (req.method === 'GET' && url.pathname === '/_pushes') {
    const id = url.searchParams.get('id');
    const out = id ? pushes.filter((p) => p.id === id) : pushes;
    res.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify(out));
    return;
  }

  if (req.method === 'POST' && url.pathname === '/_reset') {
    pushes.length = 0;
    res.writeHead(204).end();
    return;
  }

  res.writeHead(404).end();
});

server.listen(PORT, '0.0.0.0', () => {
  // eslint-disable-next-line no-console
  console.log(`mock-push listening on http://0.0.0.0:${PORT}`);
});
