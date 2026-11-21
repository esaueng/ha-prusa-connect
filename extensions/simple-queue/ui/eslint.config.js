import { moduleConfig } from '@prusa/frontend-standards/eslint/module';

import svelteConfig from './svelte.config.js';

export default [
  // The built Storybook is output, like dist/.
  { ignores: ['storybook-static/'] },
  ...moduleConfig({ tsconfigRootDir: import.meta.dirname, svelteConfig }),
];
