import { flushSync } from 'svelte';
import { expect, test, vi } from 'vitest';

import { FakeNavigation } from '../test/fakes';
import {
  documentLoader,
  matchesPattern,
  paramsOf,
  Router,
} from './router.svelte';

const KNOWN = ['/', '/settings', '/devices/:id'];

const open = (path: string): { router: Router; bar: FakeNavigation } => {
  const bar = new FakeNavigation(path);

  return { router: new Router({ known: KNOWN, navigation: bar }), bar };
};

/**
 * Puts one anchor in the page and clicks it, and answers whether the router
 * took the click. The last listener cancels the event whatever the router
 * did, so a link the router leaves alone never navigates the test page away.
 */
const clickOn = (href: string, init: MouseEventInit = {}): boolean => {
  const anchor = document.createElement('a');
  anchor.href = href;
  anchor.textContent = href;
  document.body.append(anchor);

  let taken = false;
  const last = (event: Event): void => {
    taken = event.defaultPrevented;
    event.preventDefault();
  };
  globalThis.addEventListener('click', last);

  anchor.dispatchEvent(
    new MouseEvent('click', { bubbles: true, cancelable: true, ...init }),
  );

  globalThis.removeEventListener('click', last);
  anchor.remove();

  return taken;
};

test('it opens on the screen the address bar names', () => {
  expect(open('/settings').router.path).toBe('/settings');
});

test('a path this app does not serve keeps its address and answers no pattern', () => {
  const { router } = open('/printers/p1/files');

  expect(router.path).toBe('/printers/p1/files');
  expect(router.pattern).toBeUndefined();
  expect(router.serves('/printers/p1/files')).toBe(false);
  expect(router.serves('/settings')).toBe(true);
});

test('the setter refuses a path this app does not serve and writes nothing', () => {
  const { router, bar } = open('/');

  router.path = '/nowhere';

  expect(router.path).toBe('/');
  expect(bar.pathname).toBe('/');
  expect(bar.entries).toEqual(['/']);
});

test('an accepted path moves the state and the address bar together', () => {
  const { router, bar } = open('/');

  router.path = '/settings';

  expect(router.path).toBe('/settings');
  expect(bar.pathname).toBe('/settings');
  expect(bar.entries).toEqual(['/', '/settings']);
});

test('a replace takes the current entry, so back skips the page that decided', () => {
  const { router, bar } = open('/');

  router.path = '/settings';
  router.replace('/devices/p1');

  expect(router.path).toBe('/devices/p1');
  expect(bar.entries).toEqual(['/', '/devices/p1']);
});

test('back returns to the route before it', () => {
  const { router, bar } = open('/');
  const stop = router.listen();

  router.path = '/settings';
  router.path = '/devices/p1';
  expect(router.path).toBe('/devices/p1');

  bar.back();
  expect(router.path).toBe('/settings');
  expect(router.pattern).toBe('/settings');

  bar.back();
  expect(router.path).toBe('/');

  // The listener is released, so a page that has gone hears nothing.
  stop();
  router.path = '/settings';
  bar.back();
  expect(router.path).toBe('/settings');
});

test('a click on a link this app serves is taken by the app, not the browser', () => {
  const { router, bar } = open('/');
  const stop = router.listen();

  expect(clickOn('/settings')).toBe(true);
  expect(router.path).toBe('/settings');
  expect(bar.pathname).toBe('/settings');

  stop();
});

test('a click the browser should keep is left to the browser', () => {
  const { router } = open('/');
  const stop = router.listen();

  const kept = {
    // A path this app does not serve, so the server answers it.
    'a server address': clickOn('/api/v1/health'),
    // The ways a person asks for a second tab or a window.
    'a command click': clickOn('/settings', { metaKey: true }),
    'a control click': clickOn('/settings', { ctrlKey: true }),
    'a middle click': clickOn('/settings', { button: 1 }),
    'another origin': clickOn('https://example.test/settings'),
    'a fragment': clickOn('/settings#tokens'),
  };

  expect(kept).toEqual({
    'a server address': false,
    'a command click': false,
    'a control click': false,
    'a middle click': false,
    'another origin': false,
    'a fragment': false,
  });
  expect(router.path).toBe('/');

  stop();
});

test('a write of the path the router is already on recomputes nothing', () => {
  const router = new Router({ known: KNOWN, navigation: null });

  const root = $effect.root(() => {
    let reads = 0;
    let seen = '';
    $effect(() => {
      seen = router.path;
      reads += 1;
    });
    flushSync();
    expect(reads).toBe(1);

    router.path = '/';
    flushSync();
    expect(reads).toBe(1);

    router.path = '/settings';
    flushSync();
    expect(reads).toBe(2);
    expect(seen).toBe('/settings');
  });

  root();
});

test('a pattern takes one segment per parameter and never across a slash', () => {
  expect(matchesPattern('/devices/:id', '/devices/p1')).toBe(true);
  expect(matchesPattern('/devices/:id', '/devices/p1/files')).toBe(false);
  expect(matchesPattern('/devices/:id', '/devices/')).toBe(false);
  expect(matchesPattern('/devices/:id', '/devices')).toBe(false);
  expect(matchesPattern('/settings', '/settings')).toBe(true);
});

test('a captured segment is decoded, and a broken escape is kept as written', () => {
  expect(paramsOf('/devices/:id', '/devices/printer%2Fone')).toEqual({
    id: 'printer/one',
  });
  expect(paramsOf('/devices/:id', '/devices/%E0%A4%A')).toEqual({
    id: '%E0%A4%A',
  });
  expect(paramsOf('/devices/:id', '/settings')).toEqual({});
  expect(paramsOf('/settings', '/settings')).toEqual({});
});

test('the screen at a parameterised address is the pattern, and the id is its own', () => {
  const { router } = open('/devices/p1');

  expect(router.path).toBe('/devices/p1');
  expect(router.pattern).toBe('/devices/:id');
  expect(router.params.id).toBe('p1');
});

test('a parameterised path this app serves is accepted by the setter', () => {
  const { router, bar } = open('/');

  router.path = '/devices/p2';

  expect(router.pattern).toBe('/devices/:id');
  expect(bar.pathname).toBe('/devices/p2');

  // And one the patterns do not answer still moves nothing.
  router.path = '/devices/p2/files';
  expect(router.path).toBe('/devices/p2');
  expect(bar.pathname).toBe('/devices/p2');
});

test('an address with a query opens the screen of its path and keeps the query', () => {
  const { router } = open('/devices/p1?tag=workshop');

  expect(router.path).toBe('/devices/p1');
  expect(router.pattern).toBe('/devices/:id');
  expect(router.search).toBe('?tag=workshop');
  expect(router.hash).toBe('');
});

test('a move with a query writes the query into the address bar', () => {
  const { router, bar } = open('/');

  router.path = '/settings?tab=people#row';

  expect(router.path).toBe('/settings');
  expect(router.search).toBe('?tab=people');
  expect(router.hash).toBe('#row');
  expect(bar.entries).toEqual(['/', '/settings?tab=people#row']);
});

test('a move that only changes the query is taken', () => {
  const { router, bar } = open('/settings?a=1');

  router.replace('/settings?a=2');

  expect(router.search).toBe('?a=2');
  expect(bar.entries).toEqual(['/settings?a=2']);

  router.replace('/settings');
  expect(router.search).toBe('');
});

test('a click on a link with a query this app serves is taken by the app', () => {
  const { router } = open('/');
  const stop = router.listen();

  expect(clickOn('/settings?tab=credentials')).toBe(true);
  expect(router.path).toBe('/settings');
  expect(router.search).toBe('?tab=credentials');

  stop();
});

test('back restores the query of the entry before', () => {
  const { router, bar } = open('/settings?a=1');
  const stop = router.listen();

  router.path = '/settings?a=2';
  bar.back();

  expect(router.search).toBe('?a=1');

  stop();
});

test('an address is written in one form, so the same address is one entry', () => {
  const { router, bar } = open('/settings');

  router.path = '/settings?';
  router.path = '/devices/a b';
  router.path = '/devices/a%20b';

  expect(bar.entries).toEqual(['/settings', '/devices/a%20b']);
  expect(router.params.id).toBe('a b');
});

test('a move to another path starts at the top of the page', () => {
  const { router, bar } = open('/');

  router.path = '/settings';
  expect(bar.scrolledToTop).toBe(1);

  // A query on the same page keeps the reader where they are.
  router.replace('/settings?tab=people');
  expect(bar.scrolledToTop).toBe(1);

  router.replace('/devices/p1');
  expect(bar.scrolledToTop).toBe(2);
});

test('a leave check keeps the person until they agree, and a replace is never asked', () => {
  const { router, bar } = open('/settings');
  let leave: (() => void) | undefined;
  let asked = 0;
  const stop = router.checkLeave((go) => {
    asked += 1;
    if (leave !== undefined) return false;
    leave = go;

    return true;
  });

  router.path = '/settings';
  expect(asked).toBe(0);

  router.path = '/devices/p1';
  expect(router.path).toBe('/settings');
  expect(bar.entries).toEqual(['/settings']);

  leave?.();
  expect(router.path).toBe('/devices/p1');

  router.replace('/');
  expect(asked).toBe(2);
  expect(router.path).toBe('/');

  stop();
  router.path = '/settings';
  expect(asked).toBe(2);
});

test('a leave check keeps the person on the page the back button would leave', () => {
  const { router, bar } = open('/');
  const listening = router.listen();
  router.path = '/settings';
  let leave: (() => void) | undefined;
  const stop = router.checkLeave((go) => {
    if (leave !== undefined) return false;
    leave = go;

    return true;
  });

  bar.back();
  expect(router.path).toBe('/settings');
  expect(bar.pathname).toBe('/settings');

  leave?.();
  expect(router.path).toBe('/');
  expect(bar.pathname).toBe('/');

  stop();
  listening();
});

// An address the server sends with headers of its own needs a document of its
// own, so the router hands it to the browser.
const withRule = (
  path: string,
  rule: (path: string) => 'document' | 'window' | undefined,
): { router: Router; bar: FakeNavigation } => {
  const opened = open(path);
  opened.router.loadDocumentFor(rule);

  return opened;
};

const isolatedDevice = (path: string): 'document' | undefined =>
  path === '/devices/console' ? 'document' : undefined;

test('an address the document rule names is loaded by the browser and moves nothing', () => {
  const { router, bar } = withRule('/', isolatedDevice);

  router.path = '/devices/console';

  expect(bar.loads).toEqual([['/devices/console', 'assign']]);
  expect(router.path).toBe('/');
  expect(bar.entries).toEqual(['/']);
});

test('a replace the document rule names is loaded in place of the current entry', () => {
  const { router, bar } = withRule('/', isolatedDevice);

  router.replace('/devices/console');

  expect(bar.loads).toEqual([['/devices/console', 'replace']]);
  expect(router.path).toBe('/');
});

test('an address the rule sends to a window opens there and this tab stays', () => {
  const { router, bar } = withRule('/', () => 'window');

  router.path = '/settings?tab=a';

  expect(bar.loads).toEqual([['/settings?tab=a', 'open']]);
  expect(router.path).toBe('/');
  expect(bar.entries).toEqual(['/']);
});

test('an address the rule leaves alone is routed by the app', () => {
  const { router, bar } = withRule('/', isolatedDevice);

  router.path = '/devices/p1';

  expect(bar.loads).toEqual([]);
  expect(bar.entries).toEqual(['/', '/devices/p1']);
});

test('the rule is not asked for an address this app does not serve or already shows', () => {
  const asked: string[] = [];
  const { router, bar } = withRule('/settings', (path) => {
    asked.push(path);

    return 'document';
  });

  router.path = '/nowhere';
  router.path = '/settings?tab=b';

  expect(asked).toEqual([]);
  expect(bar.loads).toEqual([]);
  expect(bar.entries).toEqual(['/settings', '/settings?tab=b']);
});

test('a link to an address the rule names is taken and handed to the browser', () => {
  const { router, bar } = withRule('/', isolatedDevice);
  const stop = router.listen();

  expect(clickOn('/devices/console')).toBe(true);
  expect(bar.loads).toEqual([['/devices/console', 'assign']]);
  expect(router.path).toBe('/');

  stop();
});

test('the call loadDocumentFor returns stops asking the rule', () => {
  const { router, bar } = open('/');
  const stop = router.loadDocumentFor(isolatedDevice);

  stop();
  router.path = '/devices/console';

  expect(bar.loads).toEqual([]);
  expect(router.path).toBe('/devices/console');
});

test('one press reported twice hands the address over once', () => {
  const { router, bar } = withRule('/', isolatedDevice);

  router.path = '/devices/console';
  router.path = '/devices/console';

  expect(bar.loads).toEqual([['/devices/console', 'assign']]);
});

test('a window the rule asks for opens once for one press, and again for a later one', () => {
  vi.useFakeTimers();
  try {
    const { router, bar } = withRule('/', () => 'window');

    router.path = '/settings';
    router.path = '/settings';
    vi.advanceTimersByTime(5000);
    router.path = '/settings';

    expect(bar.loads).toEqual([
      ['/settings', 'open'],
      ['/settings', 'open'],
    ]);
  } finally {
    vi.useRealTimers();
  }
});

test('a press after the app moved elsewhere hands the address over again', () => {
  const { router, bar } = withRule('/', (path) =>
    path === '/settings' ? 'window' : undefined,
  );

  router.path = '/settings';
  router.path = '/devices/p1';
  router.path = '/settings';

  expect(bar.loads).toEqual([
    ['/settings', 'open'],
    ['/settings', 'open'],
  ]);
});

test('a press after back hands the address over again', () => {
  const bar = new FakeNavigation('/');
  const router = new Router({ known: KNOWN, navigation: bar });
  router.path = '/devices/p1';
  router.loadDocumentFor((path) =>
    path === '/settings' ? 'window' : undefined,
  );
  const stop = router.listen();

  router.path = '/settings';
  bar.back();
  router.path = '/settings';

  expect(router.path).toBe('/');
  expect(bar.loads).toEqual([
    ['/settings', 'open'],
    ['/settings', 'open'],
  ]);

  stop();
});

test('back to an entry the rule names reloads that entry in place', () => {
  const bar = new FakeNavigation('/');
  const router = new Router({ known: KNOWN, navigation: bar });
  router.path = '/devices/console';
  router.path = '/settings';
  router.loadDocumentFor(isolatedDevice);
  const stop = router.listen();

  bar.back();

  expect(bar.loads).toEqual([['/devices/console', 'replace']]);
  expect(router.path).toBe('/settings');

  stop();
});

test('back never opens a window: the entry is routed in the app', () => {
  const bar = new FakeNavigation('/');
  const router = new Router({ known: KNOWN, navigation: bar });
  router.path = '/devices/p1';
  router.path = '/settings';
  router.loadDocumentFor(() => 'window');
  const stop = router.listen();

  bar.back();

  expect(bar.loads).toEqual([]);
  expect(router.path).toBe('/devices/p1');

  stop();
});

test('the browser loader assigns, replaces, and opens a tab without a handle', () => {
  const calls: string[] = [];
  const load = documentLoader(
    {
      assign: (url: string | URL) => calls.push(`assign ${String(url)}`),
      replace: (url: string | URL) => calls.push(`replace ${String(url)}`),
    },
    (url, target, features) => calls.push(`open ${url} ${target} ${features}`),
  );

  load('/a', 'assign');
  load('/b', 'replace');
  load('/c', 'open');

  expect(calls).toEqual(['assign /a', 'replace /b', 'open /c _blank noopener']);
});
