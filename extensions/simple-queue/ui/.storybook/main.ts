// The Storybook the print queue is looked at in: one story file beside each
// component, written in Svelte through `@storybook/addon-svelte-csf`, as the
// library's own Storybook does (packages/svelte/.storybook/main.ts).
//
// vitest.config.ts carries the plugins a component needs, Tailwind and
// Svelte, and no library build, so it is the Vite config read here.
import type { StorybookConfig } from '@storybook/svelte-vite';

// The props tables are read from each component by svelte2tsx, which a build
// does for every component at once. The gate builds the pages and leaves the
// tables to the dev server.
const building = process.argv.includes('build');

const config: StorybookConfig = {
  // A self-hosted open-source tool must not phone home on every build.
  core: {
    disableTelemetry: true,
    builder: {
      name: '@storybook/builder-vite',
      options: { viteConfigPath: 'vitest.config.ts' },
    },
  },
  stories: ['../src/**/*.stories.svelte'],
  addons: ['@storybook/addon-svelte-csf'],
  framework: {
    name: '@storybook/svelte-vite',
    options: { docgen: !building },
  },
};

export default config;
