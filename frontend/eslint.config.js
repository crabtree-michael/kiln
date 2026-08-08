// Flat ESLint config that BANS the type-system escape hatches (02 §4b): a weak
// model cannot wriggle out of types with `any`, `as`, `@ts-ignore`, non-null `!`,
// or by leaving unused symbols. `pnpm lint` runs with --max-warnings 0, so every
// one of these is a hard failure that blocks the gate.
import js from '@eslint/js';
import tseslint from 'typescript-eslint';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import prettier from 'eslint-config-prettier';
import globals from 'globals';

export default tseslint.config(
  {
    // public/push-sw.js is a hand-written static service worker served verbatim;
    // its ServiceWorkerGlobalScope globals (self/clients/registration/caches)
    // don't fit the app's browser-DOM lint program (see the file header).
    ignores: ['dist', 'dev-dist', 'coverage', 'src/schema/generated.ts', 'public/push-sw.js'],
  },
  js.configs.recommended,
  ...tseslint.configs.strictTypeChecked,
  ...tseslint.configs.stylisticTypeChecked,
  {
    languageOptions: {
      globals: { ...globals.browser, ...globals.es2022 },
      parserOptions: {
        // Allow linting config files that live outside tsconfig's include.
        projectService: {
          allowDefaultProject: ['eslint.config.js'],
        },
        tsconfigRootDir: import.meta.dirname,
      },
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],

      // --- The banned escape hatches ---
      '@typescript-eslint/no-explicit-any': 'error',
      '@typescript-eslint/no-non-null-assertion': 'error',
      '@typescript-eslint/ban-ts-comment': [
        'error',
        { 'ts-ignore': true, 'ts-nocheck': true, 'ts-expect-error': 'allow-with-description' },
      ],
      '@typescript-eslint/consistent-type-assertions': [
        'error',
        { assertionStyle: 'never' }, // no `x as T` and no `<T>x`; narrow with type guards
      ],
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrorsIgnorePattern: '^_' },
      ],
      'no-unused-vars': 'off', // handled by the typescript-eslint rule above
    },
  },
  {
    // --- The shell rule (shell-architecture plan, L4) ---
    //
    // A shell file is markup. It contains no `function` declaration and no state
    // above its component body: if it computes what to show that belongs in the
    // feed model (L2), if it remembers something in the overlay hook (L3), and if
    // it writes to the server in the ticket intents (L1).
    //
    // This is here rather than in a comment because a documented convention is
    // exactly what these three files already had, and it is how they came to hold
    // 187 hand-copied lines between them. The point of a lint rule is that it
    // fires on the FOURTH shell before anybody reviews it.
    //
    // Scope note: `Program > FunctionDeclaration` catches un-exported top-level
    // helpers — the thing that actually gets copied — and deliberately not the
    // exported component itself, which sits under an `ExportNamedDeclaration`.
    // If purity ever pushes back (it did once, on `KanbanScreenView`'s
    // `cardLabel`), the answer is to give that view its own model module, NOT to
    // push presentation copy into a module the other shells share.
    files: [
      'src/components/PrimaryScreenView.tsx',
      'src/components/desktop/DesktopScreenView.tsx',
      'src/components/desktop/KanbanScreenView.tsx',
    ],
    rules: {
      'no-restricted-syntax': [
        'error',
        {
          selector: 'Program > FunctionDeclaration',
          message:
            'A shell computes nothing. Move this into the feed model (components/feed-model.ts) or into this view’s own model module — see the shell-architecture plan, L2/L4.',
        },
        {
          selector: 'CallExpression[callee.name=/^(useState|useReducer)$/]',
          message:
            'A shell remembers nothing. Overlay state belongs in useTicketOverlay() (components/use-ticket-overlay.ts) — see the shell-architecture plan, L3.',
        },
      ],
    },
  },
  {
    // Test files may assert against the DOM in ways strict rules over-flag.
    files: ['**/*.test.ts', '**/*.test.tsx', 'vitest.setup.ts'],
    rules: {
      '@typescript-eslint/no-non-null-assertion': 'off',
    },
  },
  {
    // JS config files (eslint.config.js) aren't part of the app's typed program;
    // don't run type-aware rules on them.
    files: ['**/*.js'],
    ...tseslint.configs.disableTypeChecked,
  },
  prettier,
);
