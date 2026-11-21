// The page, in a real Chromium, in front of a server that is really running.
//
// Every page test under `src` is driven against a scripted fetch, which
// proves what the page does with an answer. This file proves the answer: the
// module is installed in the server, the page reads it over the session the
// browser holds, a person writes a comment on the page, and the server has it
// afterwards.
//
// `extensions/parts/scripts/check.sh` starts the server, adds a part with one version,
// and runs this with PCL_CORE_URL, PCL_DEMO_USER, PCL_DEMO_PASSWORD and
// PCL_PART_ID in the environment. The requests go to `/api/...` on this test
// server's own origin, and Vite forwards them to the core, so the page is
// same-origin and the sign-in cookie is on the origin the page is on.
import { PrusaConnect } from '@prusa/connect';
import { registerCatalogue, translateFor } from '@prusa/connect-ui/i18n';
import type { ModuleHost } from '@prusa/connect-ui/slots';
import {
  buildRegister,
  modulePageHref,
  noComponents,
} from '@prusa/connect-ui/slots';
import type { Component } from 'svelte';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { page, userEvent } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import setup from '../index';
import english from '../locales/en.json';
import { NAMESPACE } from '../test/host';

/** The values the check script puts in the environment, typed. */
const environment = import.meta.env as Record<string, string | undefined>;

const WHO = environment.PCL_DEMO_USER ?? 'demo';
const PASSWORD = environment.PCL_DEMO_PASSWORD ?? '';
const PART = environment.PCL_PART_ID ?? '1';

/**
 * The comment the page writes. The check script reads it back after it
 * restarts the server, so it is fixed here rather than made up per run.
 */
const COMMENT = 'written from the page';

/** The one client every call in this file goes through. */
const local = new PrusaConnect({ auth: { kind: 'session' }, retryDelay: 0 });

const start = window.location.href;

beforeAll(async () => {
  registerCatalogue(NAMESPACE, 'en', english);
  await local.passwordLogin({ body: { username: WHO, password: PASSWORD } });
});

afterAll(() => {
  window.history.replaceState(null, '', start);
});

/** Runs the module's setup against the live host and answers its page. */
const started = async (): Promise<Component> => {
  const pages: Record<string, Component> = {};
  const who = await local.getPrincipal();

  const host: ModuleHost = {
    client: local,
    principal: { id: who.principal, grants: [] },
    fleet: {
      devices: [],
      loading: false,
      loaded: true,
      error: undefined,
      device: () => undefined,
    },
    store: {
      get: () => undefined,
      set: () => undefined,
      delete: () => undefined,
      keys: () => [],
    },
    t: translateFor(NAMESPACE),
    signal: new AbortController().signal,
    namespace: NAMESPACE,
    self: 'sink:test',
    pageHref: (path) => modulePageHref(NAMESPACE, path),
    // This module draws no other module's component.
    components: noComponents,
    register: buildRegister({
      page: (path, component) => {
        pages[path] = component;
      },
      section: () => undefined,
      action: () => undefined,
    }),
  };

  await setup(host);
  const Page = pages.parts;
  if (Page === undefined) throw new Error('the module registered no page');

  return Page;
};

describe('the parts page against a running server', () => {
  it('lists the part the script added, under its folder', async () => {
    window.history.replaceState(null, '', window.location.pathname);
    await render(await started());

    await expect
      .element(page.getByTestId('folder-tree').getByText('Garage'))
      .toBeVisible();
    await userEvent.click(page.getByTestId('folder-tree').getByText('Garage'));
    await expect
      .element(page.getByTestId('cards').getByText('Bike hook'))
      .toBeVisible();
  });

  it('opens the part, reads its files, and writes a comment the server keeps', async () => {
    window.history.replaceState(
      null,
      '',
      `${window.location.pathname}?part=${PART}`,
    );
    await render(await started());

    await expect
      .element(page.getByRole('heading', { level: 1, name: 'Bike hook' }))
      .toBeVisible();

    await userEvent.click(page.getByRole('tab', { name: /Files/ }));
    await expect
      .element(page.getByTestId('file').getByText('hook.gcode'))
      .toBeVisible();

    await userEvent.click(page.getByRole('tab', { name: /Comments/ }));
    await userEvent.fill(page.getByLabelText('Add a comment'), COMMENT);
    await userEvent.click(page.getByTestId('post-comment'));
    await expect
      .element(page.getByTestId('comment').getByText(COMMENT))
      .toBeVisible();
  });
});
