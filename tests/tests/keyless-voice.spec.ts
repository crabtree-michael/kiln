import { expect, test, type APIRequestContext } from '@playwright/test';
import { mintSession } from '../session';
import { apiBase } from '../keyless';

// KEYLESS E2E — the full voice path in a real browser (spec 09), run with NO
// provider keys (design docs/keyless-e2e-tests-design.md §Test 4). The keyless
// twin of voice-mic-to-brain, minus AssemblyAI and its KILN_VOICE_SMOKE gate: the
// backend mints a canned token (KILN_VOICE_MODE=mock) and the client's voice
// socket is pointed at the mock STT server (VITE_VOICE_WS_URL), which replays a
// scripted transcript. The real frontend pipeline — fake mic → worklet → socket →
// commit machine → Dock — runs, the committed utterance lands as a human.message,
// and the scripted brain answers with a create_ticket + say. Kiln does not speak
// (no TTS), so the assertion is on-screen text.
//
// Runs in the Playwright `voice` project (Chromium + fake mic). The mock STT
// transcript is set by docker-compose.keyless.yml (KILN_STT_TRANSCRIPT) to a
// dark-mode request, which matches the voice rule in tests/fixtures/brain/keyless.json.

type Message = { message_id: number; role: string; text: string };

async function messages(request: APIRequestContext): Promise<Message[]> {
  const res = await request.get(`${apiBase}/api/messages?limit=50`);
  if (!res.ok()) return [];
  return (await res.json()) as Message[];
}

test('@keyless a spoken request becomes on-screen text and Kiln runs a turn', async ({ page, request }) => {
  test.setTimeout(60_000);
  await mintSession(request, { base: apiBase });
  await mintSession(page.request);

  const before = await messages(request);
  const baselineMaxId = before.reduce((m, r) => Math.max(m, r.message_id), 0);

  // Open the app and tap the mic. The fake mic supplies the audio stream (its
  // content is irrelevant — the mock STT scripts the transcript), the user gesture
  // starts the AudioContext, and the whole real pipeline runs against the mock socket.
  await page.goto('/');
  const talk = page.getByRole('button', { name: 'Talk' });
  await expect(talk).toBeVisible();
  await talk.click();
  await expect(talk).toHaveAttribute('data-dock-state', 'listening');

  // 1. The scripted transcript is committed as a human.message.
  let userRow: Message | undefined;
  await expect
    .poll(
      async () => {
        userRow = (await messages(request)).find(
          (m) => m.role === 'user' && m.message_id > baselineMaxId && /dark mode/i.test(m.text),
        );
        return userRow !== undefined;
      },
      { message: 'the spoken (mock-STT) utterance never landed as a human.message' },
    )
    .toBe(true);

  // 2. The scripted brain runs a turn in response — a kiln reply lands after it.
  const userId = userRow?.message_id ?? baselineMaxId;
  await expect
    .poll(
      async () => (await messages(request)).some((m) => m.role === 'kiln' && m.message_id > userId),
      { message: 'the brain never ran a turn in response to the voice utterance (no kiln reply)' },
    )
    .toBe(true);
});
