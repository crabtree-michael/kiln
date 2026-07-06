import { expect, test, type APIRequestContext } from "@playwright/test";

// E2E (gated): the FULL voice path in a real browser — fake mic → STT → brain.
//
// This is the end-to-end proof for spec 09. The Playwright `voice` project
// launches Chromium with a fake microphone fed by a canned clip that says
// "This is a test" (tests/fixtures/this-is-a-test.wav). Opening the app runs the
// REAL frontend pipeline with no mocks. The mic only starts on an explicit tap
// (never automatically), so the test taps the "Talk" mic control; the
// VoiceProvider then fetches a token, opens the real AssemblyAI socket, the
// audio worklet streams PCM, AssemblyAI transcribes, and on end-of-turn the
// commit machine POSTs the transcript to /api/message (09 §4). That enqueues a
// `human.message`, which wakes the brain to run one turn.
//
// Assertions:
//   1. The spoken utterance lands as a `human.message` (a `user` transcript row
//      whose text is the transcription of the clip — contains "test").
//   2. The brain runs a turn in response — a `kiln` reply appears after it.
//
// GATED behind KILN_VOICE_SMOKE=1 (real AssemblyAI + real LLM; never in the
// default gate). Bring the stack up first with ASSEMBLYAI_API_KEY + a cheap
// brain model (see tests/README.md), then:
//   KILN_VOICE_SMOKE=1 pnpm exec playwright test --project=voice

const SMOKE = process.env.KILN_VOICE_SMOKE === "1";

// The test reads the transcript over the backend API directly (the browser is
// busy driving the mic). Override with KILN_E2E_API_URL.
const apiBase = (
  process.env.KILN_E2E_API_URL ?? "http://localhost:8080"
).replace(/\/+$/, "");

// One transcript row from GET /api/messages (wire.Message).
type Message = { message_id: number; role: string; text: string };

async function messages(request: APIRequestContext): Promise<Message[]> {
  const res = await request.get(`${apiBase}/api/messages?limit=50`);
  if (!res.ok()) return [];
  return (await res.json()) as Message[];
}

test('speaking "This is a test" into the mic lands a human.message and the brain runs a turn', async ({
  page,
  request,
}) => {
  test.skip(
    !SMOKE,
    "gated real-service test: set KILN_VOICE_SMOKE=1 to run (09 §8)",
  );
  test.setTimeout(120_000);

  // Baseline: the highest message id before we speak, so we assert on rows that
  // arrive AFTER (a persistent stack may already hold prior transcript rows).
  const before = await messages(request);
  const baselineMaxId = before.reduce((m, r) => Math.max(m, r.message_id), 0);

  // Open the app. The mic is off at rest ("Tap to talk") — it never starts on its
  // own — so tap the mic control to begin listening against the fake mic, which
  // starts the whole pipeline (token → socket → worklet → STT → commit).
  await page.goto("/");
  const talk = page.getByRole("button", { name: "Talk" });
  await expect(talk).toBeVisible();
  await expect(talk).toHaveAttribute("data-dock-state", "paused");
  await talk.click();
  // The tap moved the dock into Listening (amber, mic on).
  await expect(talk).toHaveAttribute("data-dock-state", "listening");

  // 1. The spoken clip is transcribed and committed as a `human.message`.
  let userRow: Message | undefined;
  await expect
    .poll(
      async () => {
        userRow = (await messages(request)).find(
          (m) =>
            m.role === "user" &&
            m.message_id > baselineMaxId &&
            /test/i.test(m.text),
        );
        return userRow !== undefined;
      },
      {
        message:
          "the spoken utterance never landed as a human.message (STT → /api/message)",
      },
    )
    .toBe(true);
  // eslint-disable-next-line no-console
  console.log(
    `voice utterance committed as user message: ${JSON.stringify(userRow?.text)}`,
  );

  // 2. The brain runs a turn in response — a `kiln` reply lands after the user row.
  const userId = userRow?.message_id ?? baselineMaxId;
  let brainReply: Message | undefined;
  await expect
    .poll(
      async () => {
        brainReply = (await messages(request)).find(
          (m) => m.role === "kiln" && m.message_id > userId,
        );
        return brainReply !== undefined;
      },
      {
        message:
          "the brain never ran a turn in response to the voice utterance (no kiln reply)",
      },
    )
    .toBe(true);
  // eslint-disable-next-line no-console
  console.log(`brain ran a turn; reply: ${JSON.stringify(brainReply?.text)}`);
});
