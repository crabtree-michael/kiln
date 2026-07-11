// Mock AssemblyAI Universal-Streaming server for the keyless e2e voice spec
// (design docs/keyless-e2e-tests-design.md §3.2). It stands in for
// wss://streaming.assemblyai.com/v3/ws so the REAL frontend voice pipeline
// (worklet → socket → commit machine → Dock) runs with no ASSEMBLYAI_API_KEY.
//
// Zero-dependency: it does the RFC 6455 upgrade by hand and writes server text
// frames directly, so the harness needs nothing beyond Node (no `ws` install).
// It speaks just enough of the v3 protocol the client needs:
//   - on connect, send a `Begin` frame so the client enters "listening";
//   - ignore the binary PCM the client streams (audio is irrelevant here);
//   - after a short delay, send one final `Turn` (end_of_turn + turn_is_formatted)
//     carrying the scripted transcript, which the commit machine commits as a
//     human.message.
//
// Config: KILN_STT_PORT (default 7071), KILN_STT_TRANSCRIPT, KILN_STT_TURN_DELAY_MS.
// Point the client at it with VITE_VOICE_WS_URL=ws://127.0.0.1:7071.
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';

const PORT = Number(process.env.KILN_STT_PORT ?? 7071);
const TRANSCRIPT = process.env.KILN_STT_TRANSCRIPT ?? 'Create a ticket to add a dark mode toggle';
const TURN_DELAY_MS = Number(process.env.KILN_STT_TURN_DELAY_MS ?? 800);
const WS_GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11';

// encodeTextFrame builds an unmasked server text frame (opcode 0x1) for payloads
// up to 64 KiB — plenty for a Begin/Turn JSON message.
function encodeTextFrame(text) {
  const payload = Buffer.from(text, 'utf8');
  const len = payload.length;
  let header;
  if (len < 126) {
    header = Buffer.from([0x81, len]);
  } else {
    header = Buffer.alloc(4);
    header[0] = 0x81;
    header[1] = 126;
    header.writeUInt16BE(len, 2);
  }
  return Buffer.concat([header, payload]);
}

const server = createServer((_req, res) => {
  res.writeHead(426, { 'Content-Type': 'text/plain' });
  res.end('Upgrade Required');
});

server.on('upgrade', (req, socket) => {
  const key = req.headers['sec-websocket-key'];
  const accept = createHash('sha1')
    .update(key + WS_GUID)
    .digest('base64');
  socket.write(
    'HTTP/1.1 101 Switching Protocols\r\n' +
      'Upgrade: websocket\r\n' +
      'Connection: Upgrade\r\n' +
      `Sec-WebSocket-Accept: ${accept}\r\n\r\n`,
  );

  const send = (obj) => {
    if (!socket.destroyed) socket.write(encodeTextFrame(JSON.stringify(obj)));
  };

  // v3 `Begin`: the session is open. The client keys off type === 'Begin'.
  send({ type: 'Begin', id: 'mock-session', expires_at: Date.now() + 600_000 });

  const timer = setTimeout(() => {
    // v3 `Turn`: a formatted, end-of-turn transcript — the committed utterance.
    send({ type: 'Turn', transcript: TRANSCRIPT, end_of_turn: true, turn_is_formatted: true, turn_order: 0 });
  }, TURN_DELAY_MS);

  // Inbound frames (masked PCM + control) are drained but not parsed; we only
  // need to stop the timer when the client goes away.
  socket.on('close', () => clearTimeout(timer));
  socket.on('error', () => clearTimeout(timer));
  socket.on('data', () => {});
});

server.listen(PORT, '0.0.0.0', () => {
  // eslint-disable-next-line no-console
  console.log(`mock-stt listening on ws://0.0.0.0:${PORT} (transcript: ${JSON.stringify(TRANSCRIPT)})`);
});
