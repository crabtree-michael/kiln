// The words the "Kiln is …" thinking pill cycles through instead of a static
// "thinking". They read as clay-work — shaping, molding, firing — so the pill
// stays on-brand while the brain deliberates. The list lives in a plain text
// file (one word per line) so it can be edited without touching code; Vite's
// `?raw` loader hands us its contents as a string at build time.
import wordsRaw from '@/components/kiln-words.txt?raw';

/** The clay-work verbs, parsed from the text file (blank lines dropped). */
export const KILN_WORDS: readonly string[] = wordsRaw
  .split('\n')
  .map((line) => line.trim())
  .filter((line) => line.length > 0);

/** A random clay-work verb to slot into "Kiln is …". */
export function pickKilnWord(): string {
  const index = Math.floor(Math.random() * KILN_WORDS.length);
  return KILN_WORDS[index] ?? 'thinking';
}
