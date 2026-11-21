// A stub host: everything the module receives, built over a fetch that answers
// from a table.
//
// The client is a real `PrusaConnect` in session mode, not a fake of one. That
// is what makes the tests worth running: the request the page causes is the
// request the browser would send, so the path, the method, the body and the
// absence of an Authorization header are all readable here.
import { PrusaConnect } from '@prusa/connect';
import type { ReadonlyFleet } from '@prusa/connect-ui/domain';
import { registerCatalogue, translateFor } from '@prusa/connect-ui/i18n';
import type {
  CardEditorProps,
  CardProps,
  ModuleHost,
} from '@prusa/connect-ui/slots';
import {
  buildRegister,
  modulePageHref,
  noComponents,
} from '@prusa/connect-ui/slots';
import type { Component } from 'svelte';

import english from '../locales/en.json';

/** The namespace the module is installed under in every test. */
export const NAMESPACE = 'com.github.aux.prusa3d.parts';

/** The module's mount, which every one of its routes is under. */
export const MOUNT = `/api/v1/modules/${NAMESPACE}`;

/** One card the module registered: its component and its own editor. */
export interface RegisteredCard {
  readonly component: Component<CardProps>;
  readonly editor: Component<CardEditorProps> | undefined;
}

/** One request the module made, as the stub recorded it. */
export interface Call {
  readonly method: string;
  readonly path: string;
  readonly search: string;
  readonly authorization: string | null;
  readonly body: unknown;
}

/** One answer. A refusal carries the module's own problem document. */
export interface Answer {
  readonly status: number;
  readonly body?: unknown;
}

/**
 * A route answers from a fixed list, or from a function of the request. The
 * function may answer later, for a case that acts while a request is on its
 * way.
 */
export type Route =
  Answer | readonly Answer[] | ((call: Call) => Answer | Promise<Answer>);

export interface StubOptions {
  /** Who the host says is calling. */
  readonly principal?: string;
  /** The actions access/check allows. Absent allows every one. */
  readonly allow?: readonly string[];
  /** The answers, keyed by `METHOD path`. A key here replaces the default. */
  readonly routes?: Readonly<Record<string, Route>>;
  /** The printers the host says this person can see. */
  readonly fleet?: ReadonlyFleet;
}

export interface Stub {
  host: ModuleHost;
  calls: Call[];
  pages: Record<string, Component>;
  /** The dashboard cards the module registered, by id, with their editors. */
  cards: Record<string, RegisteredCard>;
  /** The calls made to one `METHOD path`. */
  made(key: string): Call[];
}

/** A refusal in the module's own shape. */
export const refusal = (
  status: number,
  code: string,
  detail = code,
): Answer => ({
  status,
  body: {
    type: 'about:blank',
    status,
    code,
    title: code,
    detail,
    params: {},
    retryable: false,
  },
});

/** What the overview answers for an empty catalogue. */
export const EMPTY_OVERVIEW = {
  folders: [],
  root_parts: 0,
  tags: [],
  usage: { bytes: 0, limit: 8388608 },
  storage: 'local',
  root: 'parts',
  principal: 'user:demo',
};

/** The one storage of every test, which takes any file. */
export const STORAGES = {
  items: [
    {
      name: 'local',
      display_name: 'Storage',
      allowed_extensions: [],
      browse: true,
      write: true,
      move: true,
      ranges: true,
      download_url: '',
    },
  ],
};

const noFleet: ReadonlyFleet = {
  devices: [],
  loading: false,
  loaded: true,
  error: undefined,
  device: () => undefined,
};

const bodyOf = (init: RequestInit | undefined): unknown => {
  if (typeof init?.body !== 'string') return init?.body;

  try {
    return JSON.parse(init.body) as unknown;
  } catch {
    return init.body;
  }
};

/** The address one call went to, whichever of the three shapes fetch was given. */
const addressOf = (input: RequestInfo | URL): string => {
  if (typeof input === 'string') return input;

  return input instanceof URL ? input.href : input.url;
};

/** Builds a host whose fetch answers each call from the table. */
export const stubHost = (options: StubOptions = {}): Stub => {
  registerCatalogue(NAMESPACE, 'en', english);

  const calls: Call[] = [];
  const seen = new Map<string, number>();
  const allow = options.allow;

  const defaults: Record<string, Route> = {
    'GET /api/v1/storages': { status: 200, body: STORAGES },
    'POST /api/v1/access/check': (call) => {
      const action =
        (call.body as { action?: string } | undefined)?.action ?? '';
      const allowed = allow === undefined || allow.includes(action);

      return {
        status: 200,
        body: {
          allowed,
          actions: allowed ? [action] : [],
          authorization_epoch: 1,
        },
      };
    },
    [`GET ${MOUNT}/overview`]: { status: 200, body: EMPTY_OVERVIEW },
    [`GET ${MOUNT}/parts`]: {
      status: 200,
      body: { parts: [], matched: 0, held: 0, principal: 'user:demo' },
    },
  };

  const table = { ...defaults, ...options.routes };

  const answer = (key: string, call: Call): Answer | Promise<Answer> => {
    const route = table[key];
    if (route === undefined) return refusal(404, 'test/NO_ROUTE', key);
    if (typeof route === 'function') return route(call);
    if (!Array.isArray(route)) return route as Answer;

    const list = route as readonly Answer[];
    const at = seen.get(key) ?? 0;
    seen.set(key, at + 1);

    return list[Math.min(at, list.length - 1)] ?? refusal(500, 'test/EMPTY');
  };

  const fetcher: typeof globalThis.fetch = (input, init) => {
    const url = new URL(addressOf(input), 'http://module.test');
    const method = init?.method ?? 'GET';
    const call: Call = {
      method,
      path: url.pathname,
      search: url.search,
      authorization: new Headers(init?.headers).get('authorization'),
      body: bodyOf(init),
    };
    calls.push(call);

    return Promise.resolve(answer(`${method} ${url.pathname}`, call)).then(
      (reply) =>
        new Response(
          reply.body === undefined ? null : JSON.stringify(reply.body),
          {
            status: reply.status,
            headers: {
              'content-type':
                reply.status >= 400
                  ? 'application/problem+json'
                  : 'application/json',
            },
          },
        ),
    );
  };

  const pages: Record<string, Component> = {};
  const cards: Record<string, RegisteredCard> = {};

  const host: ModuleHost = {
    client: new PrusaConnect({
      auth: { kind: 'session' },
      fetch: fetcher,
      retryDelay: 0,
    }),
    principal: { id: options.principal ?? 'user:demo', grants: [] },
    fleet: options.fleet ?? noFleet,
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
      card: (id, component, options) => {
        cards[id] = { component, editor: options?.editor };
      },
    }),
  };

  return {
    host,
    calls,
    pages,
    cards,
    made: (key) =>
      calls.filter((call) => `${call.method} ${call.path}` === key),
  };
};
