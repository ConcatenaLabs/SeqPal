import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'

// One rule that matters, and the reason for it is a bug that shipped: a call to
// `api.health()` in a file that imports `health` by name. That is a
// ReferenceError the moment the code runs, and `vite build` compiled it without
// a word, because a bundler only resolves what crosses a module boundary.
// Nothing in this repo would have caught it before a person did.
//
// Deliberately narrow. A style sweep over a codebase this size would bury the
// one rule that catches something that actually breaks. react-hooks is
// registered but silent: the code carries disable directives naming it, and a
// directive for a rule that does not exist is itself an error.
export default [
  {
    // `eslint .` walks everything under the repo root, and this project's own
    // workflow puts whole copies of the checkout in .claude/worktrees. Linting
    // those reports errors from code that is not in this checkout, takes longer
    // the more worktrees exist, and fails the gate for reasons the person
    // running it cannot see in their own files.
    ignores: ['.claude/**', 'dist/**', 'node_modules/**'],
  },
  {
    files: ['src/**/*.js', 'src/**/*.jsx', 'scripts/**/*.mjs', 'test/**/*.js'],
    plugins: { 'react-hooks': reactHooks },
    languageOptions: {
      ecmaVersion: 2023,
      sourceType: 'module',
      parserOptions: { ecmaFeatures: { jsx: true } },
      globals: { ...globals.browser, ...globals.node },
    },
    // The code carries disable directives naming exhaustive-deps. The rule is
    // off, which makes every one of them "unused" and puts eight warnings on
    // every run of the gate. A gate that always prints something is a gate
    // people stop reading, and the directives are worth keeping as a record of
    // where a dependency list was considered and settled.
    linterOptions: { reportUnusedDisableDirectives: 'off' },
    rules: {
      'no-undef': 'error',
      'react-hooks/exhaustive-deps': 'off',
    },
  },
]
