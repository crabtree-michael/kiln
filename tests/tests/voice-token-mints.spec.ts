import { expect, test, type APIRequestContext } from '@playwright/test';
import { readFile } from 'node:fs/promises';

// E2E (gated): the REAL AssemblyAI voice path (docs/specs/09 §2, §6, §8).
//
// This is the spec's real-service smoke test. It hits the real AssemblyAI
// streaming API and so is GATED behind KILN_VOICE_SMOKE=1 — it never runs in
// the default gate, per the repo's real-service test hygiene. It needs a
// running stack (the backend mints the token; ASSEMBLYAI_API_KEY lives only in
// the backend env — the test never sees the key, exactly like the browser).
//
// Two assertions, matching the trust boundary (09 §2):
//   1. Token + socket auth (always, when gated): POST /api/voice/token returns a
//      short-lived token with a future expiry, and that backend-minted token
//      opens the real AssemblyAI Universal-Streaming socket (a `Begin` frame
//      arrives). This proves the whole credential path end to end without an
//      audio asset — the API key never left the backend, yet the client's
//      socket authenticated.
//   2. Audio -> human.message (when a PCM clip is supplied): stream a canned
//      16 kHz mono PCM16 clip of speech, wait for AssemblyAI's formatted
//      end-of-turn transcript, POST it to the unchanged /api/message seam, and
//      assert a `human.message` (a user transcript row) lands with non-empty
//      text (09 §4, §8). Supply the clip via KILN_VOICE_SAMPLE=/path/to.pcm
//      (raw little-endian PCM16, mono, 16 kHz); without it this assertion is
//      skipped, since the STT path needs real speech to transcribe.
//
// Run recipe (see ../README.md): bring the stack up with ASSEMBLYAI_API_KEY set
// (repo-root .env), then `KILN_VOICE_SMOKE=1 KILN_VOICE_SAMPLE=... make e2e`.

const SMOKE = process.env.KILN_VOICE_SMOKE === '1';

// API-driven: the browser client streams audio itself, but a headless test hits
// the backend directly. Override with KILN_E2E_API_URL (default the compose backend).
const apiBase = (process.env.KILN_E2E_API_URL ?? 'http://localhost:8080').replace(/\/+$/, '');

// AssemblyAI Universal-Streaming v3 (09 §2): PCM16 mono 16 kHz, formatted turns on.
const ASSEMBLYAI_WS = 'wss://streaming.assemblyai.com/v3/ws';
const SAMPLE_RATE = 16_000;

// POST /api/voice/token's 200 body (wire.VoiceToken).
type VoiceToken = { token: string; expires_at: string };
// One transcript row from GET /api/messages (wire.Message).
type Message = { role: string; text: string };

async function mintToken(request: APIRequestContext): Promise<VoiceToken> {
  const res = await request.post(`${apiBase}/api/voice/token`);
  expect(res.status(), `POST /api/voice/token -> ${res.status()}`).toBe(200);
  return (await res.json()) as VoiceToken;
}

function streamUrl(token: string): string {
  const q = new URLSearchParams({
    sample_rate: String(SAMPLE_RATE),
    encoding: 'pcm_s16le',
    format_turns: 'true',
    token,
  });
  return `${ASSEMBLYAI_WS}?${q.toString()}`;
}

// Open the AssemblyAI socket and resolve once a `Begin` frame arrives (proves the
// minted token authenticated). Rejects on socket error or timeout.
async function assertSocketAuthenticates(token: string): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    const ws = new WebSocket(streamUrl(token));
    const timer = setTimeout(() => {
      ws.close();
      reject(new Error('no Begin frame within 10s — token did not authenticate the socket'));
    }, 10_000);
    ws.addEventListener('message', (ev: MessageEvent) => {
      const msg: unknown = JSON.parse(String(ev.data));
      if (isRecord(msg) && msg.type === 'Begin') {
        clearTimeout(timer);
        ws.send(JSON.stringify({ type: 'Terminate' }));
        setTimeout(() => {
          ws.close();
          resolve();
        }, 100);
      }
    });
    ws.addEventListener('error', () => {
      clearTimeout(timer);
      reject(new Error('AssemblyAI socket error before Begin'));
    });
  });
}

// Stream a raw PCM16 clip and resolve with the first non-empty formatted
// end-of-turn transcript (09 §4's commit trigger: end_of_turn && turn_is_formatted).
async function transcribeClip(token: string, pcm: Buffer): Promise<string> {
  return await new Promise<string>((resolve, reject) => {
    const ws = new WebSocket(streamUrl(token));
    let settled = false;
    const finish = (fn: () => void): void => {
      if (settled) return;
      settled = true;
      fn();
    };
    const timer = setTimeout(() => {
      ws.close();
      finish(() => reject(new Error('no formatted end-of-turn transcript within 30s')));
    }, 30_000);

    ws.addEventListener('open', () => {
      // Pace ~50 ms frames (1600 samples * 2 bytes) so AssemblyAI sees a
      // realtime-ish stream, then signal end-of-input with Terminate.
      const frameBytes = 1600 * 2;
      let offset = 0;
      const pump = (): void => {
        if (settled) return;
        if (offset >= pcm.length) {
          ws.send(JSON.stringify({ type: 'Terminate' }));
          return;
        }
        const end = Math.min(offset + frameBytes, pcm.length);
        ws.send(pcm.subarray(offset, end));
        offset = end;
        setTimeout(pump, 50);
      };
      pump();
    });

    ws.addEventListener('message', (ev: MessageEvent) => {
      const msg: unknown = JSON.parse(String(ev.data));
      if (!isRecord(msg) || msg.type !== 'Turn') return;
      const text = typeof msg.transcript === 'string' ? msg.transcript.trim() : '';
      if (msg.end_of_turn === true && msg.turn_is_formatted === true && text !== '') {
        clearTimeout(timer);
        ws.send(JSON.stringify({ type: 'Terminate' }));
        setTimeout(() => ws.close(), 100);
        finish(() => resolve(text));
      }
    });

    ws.addEventListener('error', () => {
      clearTimeout(timer);
      finish(() => reject(new Error('AssemblyAI socket error while streaming audio')));
    });
  });
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

test('backend-minted AssemblyAI token authenticates the real streaming socket', async ({
  request,
}) => {
  test.skip(!SMOKE, 'gated real-service test: set KILN_VOICE_SMOKE=1 to run (09 §8)');

  const { token, expires_at } = await mintToken(request);
  expect(token, 'token should be non-empty').not.toBe('');
  expect(
    new Date(expires_at).getTime(),
    'expires_at should be in the future',
  ).toBeGreaterThan(Date.now());

  await assertSocketAuthenticates(token);
});

test('spoken audio becomes a human.message via STT + /api/message', async ({ request }) => {
  test.skip(!SMOKE, 'gated real-service test: set KILN_VOICE_SMOKE=1 to run (09 §8)');
  const samplePath = process.env.KILN_VOICE_SAMPLE;
  test.skip(
    !samplePath,
    'set KILN_VOICE_SAMPLE=/path/to/clip.pcm (raw PCM16 mono 16 kHz) to exercise the STT path',
  );
  test.setTimeout(90_000);

  const pcm = await readFile(samplePath as string);
  const { token } = await mintToken(request);

  // 1. Real STT: the canned clip -> a non-empty formatted transcript (09 §4).
  const transcript = await transcribeClip(token, pcm);
  expect(transcript, 'AssemblyAI returned an empty transcript for the clip').not.toBe('');

  // 2. The client commits the utterance through the unchanged /api/message seam,
  //    exactly like a typed message (09 §4). Tag it so we assert on our own row.
  const tag = `VOICE-${Date.now().toString(36).toUpperCase()}`;
  const tagged = `${transcript} ${tag}`;
  const post = await request.post(`${apiBase}/api/message`, { data: { text: tagged } });
  expect(post.status(), `POST /api/message -> ${post.status()}`).toBe(202);

  // 3. A human.message (a user transcript row) lands with the non-empty text.
  await expect
    .poll(
      async () => {
        const res = await request.get(`${apiBase}/api/messages?limit=50`);
        if (!res.ok()) return false;
        const rows = (await res.json()) as Message[];
        return rows.some((m) => m.role === 'user' && m.text.includes(tag));
      },
      { message: `the voice utterance tagged ${tag} never landed as a human.message` },
    )
    .toBe(true);
});
