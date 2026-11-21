// A stub host: everything the module receives, built over a fetch that answers
// from a script.
//
// The client is a real `PrusaConnect` in session mode, not a fake of one. That
// is what makes the test worth running: the request the page causes is the
// request the browser would send, so the path, the method and the absence of
// an Authorization header are all readable here.
import { PrusaConnect } from '@prusa/connect';
import type { PrincipalGrant, ReadonlyFleet } from '@prusa/connect-ui/domain';
import { registerCatalogue, translateFor } from '@prusa/connect-ui/i18n';
import type { ModuleHost, SectionProps } from '@prusa/connect-ui/slots';
import {
  buildRegister,
  modulePageHref,
  noComponents,
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
  deviceCard: Record<string, Component<SectionProps['device-card']>>;
  deviceTab: Record<string, Component<SectionProps['device-tab']>>;
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
  /** Holds this one answer back until it settles. */
  readonly held?: Promise<unknown>;
}

/** What a stub host is built with. Every field has a default a test can keep. */
export interface StubOptions {
  /** Who the host says is calling. */
  readonly principal?: string;
  /** What the core answers when asked whether that person may write. */
  readonly mayOperate?: boolean;
  /**
   * The answers, matched by the path the request went to. A path with no entry
   * falls to `answers`, in order.
   */
  readonly routed?: Readonly<Record<string, readonly Answer[]>>;
  /**
   * A gate every answer waits behind. A test that has to see the page while a
   * read is in flight holds it and opens it when it has looked.
   */
  readonly until?: Promise<unknown>;
  /** The printers the host says this person can see. The default is none, read. */
  readonly fleet?: ReadonlyFleet;
}

/** A gate a test opens when it has finished looking at the loading state. */
export interface Gate {
  readonly held: Promise<unknown>;
  open(): void;
}

/** A gate that is shut until somebody opens it. */
export const gate = (): Gate => {
  let open = (): void => undefined;
  const held = new Promise<unknown>((resolve) => {
    open = () => {
      resolve(undefined);
    };
  });

  return {
    held,
    open: () => {
      open();
    },
  };
};

/**
 * A holding as the core lists it: device actions only. Whether the person may
 * write is the access check's answer, never read off this.
 */
export const OPERATOR: PrincipalGrant = {
  device: 'device:demo',
  kind: 'printer',
  actions: ['observe', 'operate'],
  verbs: [],
};

/** Where the client asks the core whether the person may do something. */
const ACCESS_CHECK = '/api/v1/access/check';

/** The address one call went to, whichever of the three shapes fetch was given. */
const addressOf = (input: RequestInfo | URL): string => {
  if (typeof input === 'string') return input;

  return input instanceof URL ? input.href : input.url;
};

const answered = ({ status, body, contentType }: Answer): Response =>
  // A 204 has no body, and the Response constructor refuses one.
  new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'content-type': contentType ?? 'application/json' },
  });

/** The path of one address, without the search the query parameters wrote. */
const pathOf = (url: string): string =>
  new URL(url, 'http://module.test').pathname;

/**
 * A host whose fetch answers each call from `answers`, in order, and repeats
 * the last one after that. A path named in `routed` takes its answers from
 * that list instead, which is what lets a test script the material list and the
 * materials separately however many times the page reads them.
 *
 * Everything sent is recorded in `calls`.
 */
export const stubHost = (
  answers: readonly Answer[],
  options: StubOptions = {},
  namespace = 'com.github.aux.prusa3d.materials',
): Stub => {
  // What the loader does before it imports a bundle: it reads the module's
  // own `locales/en.json` and merges it under the namespace
  // (frontend-i18n.md rule 7).
  registerCatalogue(namespace, 'en', english);

  const calls: Call[] = [];
  const made = new Map<string, number>();
  let plain = 0;

  /** The next answer for one path, repeating the last once the list runs out. */
  const next = (url: string): Answer => {
    const routed = options.routed?.[pathOf(url)];
    if (routed !== undefined && routed.length > 0) {
      const at = made.get(pathOf(url)) ?? 0;
      made.set(pathOf(url), at + 1);

      return (
        routed[Math.min(at, routed.length - 1)] ?? { status: 200, body: {} }
      );
    }

    const at = plain;
    plain += 1;

    return (
      answers[Math.min(at, answers.length - 1)] ?? { status: 200, body: {} }
    );
  };

  const fetcher: typeof globalThis.fetch = async (input, init) => {
    const headers = new Headers(init?.headers);
    const url = addressOf(input);
    // The core's answer, not the module's: it is left out of `calls`, which
    // records what the module itself was asked.
    if (pathOf(url) === ACCESS_CHECK) {
      return answered({
        status: 200,
        body: { allowed: options.mayOperate ?? true },
      });
    }

    calls.push({
      // The transport builds the address itself, so `input` is a string here.
      // A `Request` or a `URL` is written out rather than stringified blindly.
      url,
      method: init?.method ?? 'GET',
      credentials: init?.credentials,
      authorization: headers.get('authorization'),
      body: typeof init?.body === 'string' ? init.body : undefined,
    });

    // The call is recorded before the gate, so a test can count what is in
    // flight while it holds the answer back.
    if (options.until !== undefined) await options.until;

    const reply = next(url);
    if (reply.held !== undefined) await reply.held;

    return answered(reply);
  };

  const registered: Registered = {
    pages: {},
    deviceCard: {},
    deviceTab: {},
    settings: {},
  };

  const host: ModuleHost = {
    client: new PrusaConnect({
      auth: { kind: 'session' },
      fetch: fetcher,
      retryDelay: 0,
    }),
    principal: {
      id: options.principal ?? 'user:demo',
      grants: [OPERATOR],
    },
    fleet: options.fleet ?? {
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
        } else if (slot === 'device-tab') {
          registered.deviceTab[id] = component as Component<
            SectionProps['device-tab']
          >;
        } else {
          registered.settings[id] = component as Component<
            SectionProps['settings']
          >;
        }
      },
      action: () => undefined,
    }),
  };

  return { host, calls, registered };
};
