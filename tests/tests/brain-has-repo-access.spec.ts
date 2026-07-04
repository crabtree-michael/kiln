import { expect, test, type APIRequestContext } from '@playwright/test';

// E2E: the brain can actually reach the project repository (design
// docs/superpowers/specs/2026-07-04-brain-repo-bash-tool-design.md). The brain
// has a `bash` tool over a local clone the backend makes at boot; this proves
// the whole path is live — clone + auth + git/gh under the restricted PATH —
// by asking the brain a question it can ONLY answer by running git/gh against
// the real repo, then checking its chat reply.
//
// It is API-driven (like ready-kicks-off-amika-run): there is no UI affordance
// for "ask the brain to inspect the repo", so it POSTs to /api/message and reads
// the transcript back. POST /api/message -> durable queue -> brain -> real LLM
// -> the brain calls `bash` (git/gh in the clone) -> `say` -> a `kiln` row in
// GET /api/messages. It never creates a ticket or reaches Developing, so it
// spins up no Amika sandbox and leaves nothing to clean up.
//
// The proof must be UNFAKEABLE by the model: we assert the reply carries either
// the latest commit SHA on the default branch (fetched here straight from the
// GitHub API — the model cannot know it without a live `git log`) or the repo's
// `owner/name` slug (the owner login isn't derivable from the model's priors).
// Either one only comes from the clone actually working.
//
// Requires the backend stack to be up with GITHUB_REPO_URL + GITHUB_AUTH_TOKEN
// set (docker-compose passes them through from the repo-root .env), on the cheap
// model (KILN_BRAIN_MODEL=claude-haiku-4-5-...). See ../README.md.

// API-driven: hit the backend directly (the vite proxy at :5173 is for the
// browser client). Override with KILN_E2E_API_URL.
const apiBase = (process.env.KILN_E2E_API_URL ?? 'http://localhost:8080').replace(/\/+$/, '');

// The repo the backend was told to clone, from the same .env docker-compose reads.
const repoURL = process.env.GITHUB_REPO_URL ?? '';
const token = process.env.GITHUB_AUTH_TOKEN ?? '';

type Message = { message_id: number; role: 'user' | 'kiln'; text: string };

async function getMessages(request: APIRequestContext): Promise<Message[]> {
  const res = await request.get(`${apiBase}/api/messages?limit=100`);
  expect(res.ok(), `GET /api/messages -> ${res.status()}`).toBeTruthy();
  return (await res.json()) as Message[];
}

// parseSlug pulls owner/name out of an https or scp-style GitHub URL, dropping a
// trailing .git — e.g. https://github.com/crabtree-michael/kiln(.git) ->
// { owner: 'crabtree-michael', repo: 'kiln' }.
function parseSlug(url: string): { owner: string; repo: string } | null {
  const m = url.match(/github\.com[:/]+([^/]+)\/([^/]+?)(?:\.git)?\/?$/i);
  return m ? { owner: m[1], repo: m[2] } : null;
}

// latestDefaultBranchSha asks the GitHub API for the HEAD commit of the repo's
// default branch — the independent source of truth the brain's answer must
// match. Absolute URL, so the request fixture's frontend baseURL doesn't apply.
async function latestDefaultBranchSha(
  request: APIRequestContext,
  owner: string,
  repo: string,
): Promise<string> {
  const headers = {
    Authorization: `Bearer ${token}`,
    Accept: 'application/vnd.github+json',
    'X-GitHub-Api-Version': '2022-11-28',
  };
  const repoRes = await request.get(`https://api.github.com/repos/${owner}/${repo}`, { headers });
  expect(repoRes.ok(), `GitHub GET /repos/${owner}/${repo} -> ${repoRes.status()}`).toBeTruthy();
  const defaultBranch = ((await repoRes.json()) as { default_branch: string }).default_branch;

  const commitRes = await request.get(
    `https://api.github.com/repos/${owner}/${repo}/commits/${defaultBranch}`,
    { headers },
  );
  expect(commitRes.ok(), `GitHub GET commits/${defaultBranch} -> ${commitRes.status()}`).toBeTruthy();
  return ((await commitRes.json()) as { sha: string }).sha;
}

test('the brain can reach the project repository via its bash tool', async ({ request }) => {
  test.setTimeout(150_000); // a real-LLM turn plus a bash round-trip in the clone

  // This test verifies the REAL repo-inspection path, so it needs the repo
  // configured — a missing URL/token is a misconfigured run, not a pass. (The
  // backend must also have them; docker-compose forwards the same .env vars.)
  test.skip(
    !repoURL || !token,
    'GITHUB_REPO_URL / GITHUB_AUTH_TOKEN unset (repo-root .env) — this test verifies the ' +
      'brain reaching the real repo; set them and bring the stack up with them',
  );
  const slug = parseSlug(repoURL);
  expect(slug, `GITHUB_REPO_URL is not a recognizable GitHub URL: ${repoURL}`).not.toBeNull();
  const { owner, repo } = slug as { owner: string; repo: string };

  // Independent source of truth: the current HEAD SHA of the default branch. A
  // hard error here (bad token, unreachable) fails fast rather than as a mystery
  // timeout on the poll below.
  const sha = await latestDefaultBranchSha(request, owner, repo);
  const shortSha = sha.slice(0, 7);

  // Baseline the transcript so we only accept a reply to THIS request, not a
  // leftover row from an earlier run on the persistent stack.
  const baselineId = (await getMessages(request)).reduce((max, m) => Math.max(max, m.message_id), 0);

  // Ask a question answerable only by running git/gh in the clone. The brain is
  // tuned to minimize output — it will run the tool and end the turn silently
  // unless told, unequivocally, to report back via `say`. So the instruction
  // names the `say` tool explicitly, forbids the alternatives it would otherwise
  // default to (post_update, a ticket, silence), and scopes it to a read-only
  // check so it never touches the board.
  const post = await request.post(`${apiBase}/api/message`, {
    data: {
      text:
        `Capability check. Use your bash tool to run git/gh in the project repository clone ` +
        `and find two facts: the repository's full name in "owner/name" form, and the full ` +
        `SHA of the latest commit on the default branch. Then you MUST call the \`say\` tool ` +
        `to report both values back to me in the chat, including the owner/name and the full ` +
        `commit SHA verbatim. This is a direct order: reply with \`say\`. Do NOT create, ` +
        `change, or accept any ticket; do NOT post an update; do NOT end your turn until you ` +
        `have called \`say\` with the answer.`,
    },
  });
  expect(post.status(), `POST /api/message -> ${post.status()}`).toBe(202);

  // The reply must carry a fact only obtainable from the live repo: the commit
  // SHA (unknowable to the model) or the owner/name slug (the owner login isn't
  // in the model's priors). Poll the transcript for a NEW kiln row (the brain's
  // `say`) that carries it.
  const slugLower = `${owner}/${repo}`.toLowerCase();
  const proves = (text: string) =>
    text.includes(shortSha) || text.toLowerCase().includes(slugLower);
  await expect
    .poll(
      async () => {
        const msgs = await getMessages(request);
        return msgs.some((m) => m.role === 'kiln' && m.message_id > baselineId && proves(m.text));
      },
      {
        message:
          `the brain never reported a repo fact only obtainable from live git/gh access ` +
          `(commit ${shortSha} or slug ${owner}/${repo}). Check the backend has GITHUB_REPO_URL / ` +
          `GITHUB_AUTH_TOKEN set, git+gh in the image, and that the boot clone succeeded ` +
          `(look for "repo.shell.ready" / "repo.shell.disabled" in the backend logs).`,
      },
    )
    .toBe(true);
});
