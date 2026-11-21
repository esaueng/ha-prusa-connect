// What every story shares: the theme a host page gives the bundle, and the
// language of the library's own texts.
import '../src/test/theme.css';

import type { Decorator, Preview } from '@storybook/svelte';

import { locale } from '@prusa/connect-ui/i18n';

// The library's components draw a few texts of their own, such as the label
// of a trace id. English is set so a story never draws a bare key.
locale.set('en');

// The host page decides light or dark with the library's `dark` class on the
// root, so a story does the same, and paints the page the way the host does.
const withTheme: Decorator = (story, context) => {
  const globals: Record<string, string | undefined> = context.globals;
  const root = document.documentElement;
  root.classList.toggle('dark', globals.theme === 'dark');
  document.body.style.background = 'var(--background)';
  document.body.style.color = 'var(--foreground)';

  return story();
};

const preview: Preview = {
  decorators: [withTheme],
  initialGlobals: { theme: 'light' },
  globalTypes: {
    theme: {
      description: 'Colour theme',
      toolbar: {
        icon: 'circlehollow',
        items: [
          { value: 'light', title: 'Light' },
          { value: 'dark', title: 'Dark' },
        ],
      },
    },
  },
  parameters: {
    // The narrowest phone the app is checked at.
    viewport: {
      options: {
        phone: {
          name: 'Phone, 390 wide',
          styles: { width: '390px', height: '844px' },
          type: 'mobile',
        },
      },
    },
  },
};

export default preview;
