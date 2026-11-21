// A stub host, on the model of packages/svelte/src/test/fakes.ts: everything
// the module receives, built over a fetch that answers from a script.
//
// The client is a real `PrusaConnect` in session mode, not a fake of one. That
// is what makes the test worth running: the request the page causes is the
// request the browser would send, so the path, the method and the absence of
// an Authorization header are all readable here.
import { PrusaConnect } from '@prusa/connect';
import { registerCatalogue, translateFor } from '@prusa/connect-ui/i18n';
import type { ModuleHost, SectionProps } from '@prusa/connect-ui/slots';
import {
  buildRegister,
  noComponents,
  modulePageHref,
} from '@prusa/connect-ui/slots';
import type { Component } from 'svelte';

import english from '../locales/en.json';

/** One request the module made, as the stub recorded it. */
export interface Call {
  readonly url: string;
  readonly method: string;
  readonly credentials: RequestCredentials | undefined;
  readonly authorization: string | null;
  readonly body: string | undefined;
}

/** What the module registered, so a test can render it. */
export interface Registered {
  pages: Record<string, Component>;
  components: Record<string, Component<never>>;
  deviceCard: Record<string, Component<SectionProps['device-card']>>;
  settings: Record<string, Component<SectionProps['settings']>>;
}

export interface Stub {
  host: ModuleHost;
  calls: Call[];
  registered: Registered;
}

/** One scripted answer. A refusal carries the module's own problem document. */
export interface Answer {
  readonly status: number;
  readonly body: unknown;
  /** Defaults to application/json, and a refusal sends application/problem+json. */
  readonly contentType?: string;
}

/** The address one call went to, whichever of the three shapes fetch was given. */
const addressOf = (input: RequestInfo | URL): string => {
  if (typeof input === 'string') return input;

  return input instanceof URL ? input.href : input.url;
};

const answered = ({ status, body, contentType }: Answer): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': contentType ?? 'application/json' },
  });

/**
 * A host whose fetch answers each call from `answers`, in order, and repeats
 * the last one after that. Everything sent is recorded in `calls`.
 */
export const stubHost = (
  answers: readonly Answer[],
  principal = 'user:demo',
  namespace = 'com.github.aux.prusa.spools',
): Stub => {
  // What the loader does before it imports a bundle: it reads the module's
  // own `locales/en.json` and merges it under the namespace
  // (frontend-i18n.md rule 7).
  registerCatalogue(namespace, 'en', english);

  const calls: Call[] = [];
  let made = 0;

  const fetcher: typeof globalThis.fetch = (input, init) => {
    const headers = new Headers(init?.headers);
    calls.push({
      // The transport builds the address itself, so `input` is a string here.
      // A `Request` or a `URL` is written out rather than stringified blindly.
      url: addressOf(input),
      method: init?.method ?? 'GET',
      credentials: init?.credentials,
      authorization: headers.get('authorization'),
      body: typeof init?.body === 'string' ? init.body : undefined,
    });

    const next = answers[Math.min(made, answers.length - 1)];
    made += 1;

    return Promise.resolve(answered(next ?? { status: 200, body: {} }));
  };

  const registered: Registered = {
    pages: {},
    components: {},
    deviceCard: {},
    settings: {},
  };

  const host: ModuleHost = {
    client: new PrusaConnect({
      auth: { kind: 'session' },
      fetch: fetcher,
      retryDelay: 0,
    }),
    principal: { id: principal, grants: [] },
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
    // The loader hands a module `translateFor(<its namespace>)`, so the stub
    // hands the same function under the namespace this module is mounted at.
    // A key no catalogue holds reads back as the key, which is what a test
    // asserting on rendered text compares against.
    t: translateFor(namespace),
    // A test never ends the module's time on the page, so nothing aborts it.
    signal: new AbortController().signal,
    namespace,
    self: 'sink:test',
    pageHref: (path) => modulePageHref(namespace, path),
    // This module draws no other module's component.
    components: noComponents,
    register: buildRegister({
      page: (path, component) => {
        registered.pages[path] = component;
      },
      section: (slot, id, component) => {
        // The generic parameter narrows on the value but not the props type
        // it indexes; the check above is what makes each cast true.
        if (slot === 'device-card') {
          registered.deviceCard[id] = component as Component<
            SectionProps['device-card']
          >;
        } else {
          registered.settings[id] = component as Component<
            SectionProps['settings']
          >;
        }
      },
      action: () => undefined,
      component: (name, component) => {
        registered.components[name] = component;
      },
    }),
  };

  return { host, calls, registered };
};
