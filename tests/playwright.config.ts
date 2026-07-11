import { defineConfig, devices } from '@playwright/test';
import dotenv from 'dotenv';

// Load the repo-root .env (where the provider keys already live — ANTHROPIC_API_KEY,
// AMIKA_API_KEY). Playwright's cwd is tests/, so it isn't picked up automatically; the
// global teardown needs AMIKA_API_KEY to destroy the sandboxes a Developing run leaves
// behind. Shell env wins over the file (no `override`), so an explicit export still rules.
dotenv.config({ path: new URL('../.env', import.meta.url).pathname });

// The e2e suite drives the REAL web client against a running stack (02 §4a):
// no fakes at this level — the brain hits the real LLM. Target the frontend the
// docker-compose stack serves by default; KILN_E2E_BASE_URL overrides it (e.g. a
// deployed client). See ./README.md for the run recipe (cheap model, keys).
const baseURL = process.env.KILN_E2E_BASE_URL ?? 'http://localhost:5173';

// The canned speech clip fed to the browser's fake microphone in the voice
// project (09 §8). `--use-file-for-fake-audio-capture` makes getUserMedia return
// this WAV as the mic stream, so the REAL frontend pipeline (worklet → socket →
// commit machine → Dock) runs against real AssemblyAI. `%noloop` plays it once.
const voiceSample = new URL('./fixtures/this-is-a-test.wav', import.meta.url).pathname;
// Specs that need the fake-mic Chromium (the `voice` project): the key-gated
// real-service spec and its keyless twin (design §Test 4). Both are excluded from
// the default `chromium` project and matched by the `voice` project below.
const voiceSpec = /(voice-mic-to-brain|keyless-voice)\.spec\.ts/;

export default defineConfig({
  testDir: './tests',
  // Any test that reaches Developing spins up a real Amika turn; this destroys every
  // kiln-worker-* sandbox afterwards (auto_delete is off by design — 05 D6).
  globalTeardown: './global-teardown.ts',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: 1,
  reporter: [['list']],
  // A real-LLM turn is slow; give the whole test room and each assertion a long
  // poll window (the ticket arrives over SSE once the brain finishes its turn).
  timeout: 120_000,
  expect: { timeout: 90_000 },
  use: {
    baseURL,
    trace: 'retain-on-failure',
  },
  projects: [
    // The default suite: everything except the fake-mic voice spec (which needs
    // the browser launched with fake-audio flags — see the `voice` project).
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
      testIgnore: voiceSpec,
    },
    // The voice project: Chromium with a fake microphone fed by the canned clip.
    // The mic starts on an explicit tap (the spec clicks "Talk"), which supplies
    // the user gesture the AudioContext needs; `--autoplay-policy=no-user-gesture-
    // required` stays as a harmless belt-and-suspenders. The fake-ui flag +
    // granted permission auto-accept the getUserMedia prompt.
    {
      name: 'voice',
      testMatch: voiceSpec,
      use: {
        ...devices['Desktop Chrome'],
        permissions: ['microphone'],
        launchOptions: {
          args: [
            '--use-fake-device-for-media-stream',
            '--use-fake-ui-for-media-stream',
            '--autoplay-policy=no-user-gesture-required',
            `--use-file-for-fake-audio-capture=${voiceSample}%noloop`,
          ],
        },
      },
    },
  ],
});
