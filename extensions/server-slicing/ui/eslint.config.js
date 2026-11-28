import { moduleConfig } from '@prusa/frontend-standards/eslint/module';

import svelteConfig from './svelte.config.js';

export default [
  // The built Storybook is output, like dist/. vendor/ holds another
  // project's files, copied unchanged and held to their hashes.
  { ignores: ['storybook-static/', 'vendor/'] },
  ...moduleConfig({ tsconfigRootDir: import.meta.dirname, svelteConfig }),
];
