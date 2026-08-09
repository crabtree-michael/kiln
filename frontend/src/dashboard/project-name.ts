// What a new project is CALLED, derived from the repository it is pointed at
// (auto-name from repository).
//
// Creating a project used to open on a name field — before the repo had even
// been picked, which is the wrong order: the repo is the decision, and the name
// was the repo's own name essentially every time anyone filled it in. So the
// create flows ask for the repository and take the name from it, and this is the
// one place that derivation lives (the settings modal, the app-native create
// step and first-run onboarding all read it, so they cannot drift into naming
// the same repo three ways).
//
// Renaming an EXISTING project is untouched: `ProjectFields` still offers the
// name field when it is handed a project, and nothing here runs on that path — a
// board the user deliberately renamed must not snap back to the repo's name on
// the next unrelated save.

/** The repository's own name — the last path segment of its URL, with the
 * trailing `/` and `.git` hand-typed values carry stripped (the same three
 * spellings `sameRepo` normalizes). `https://github.com/Crabtree-Michael/Pac-Man`
 * → `Pac-Man`: the org is dropped on purpose, because it is the same word for
 * every project a user has and says nothing about which board this is.
 *
 * '' in, '' out — the create flows use that to mean "no repo picked yet", and
 * their submit is blocked on the repo anyway. */
export function projectNameFromRepoUrl(repoUrl: string): string {
  const trimmed = repoUrl
    .trim()
    .replace(/\/+$/, '')
    .replace(/\.git$/i, '');
  return trimmed.split('/').at(-1) ?? '';
}
