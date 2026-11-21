// A stub host: what the module receives, built over a fetch that answers
// from a table. The client is a real `PrusaConnect` in session mode, so the
// request a view causes is the request the browser would send.
import { PrusaConnect } from '@prusa/connect';
import type { ReadonlyFleet } from '@prusa/connect-ui/domain';
import { translateFor } from '@prusa/connect-ui/i18n';
import {
  buildRegister,
  type ModuleHost,
  modulePageHref,
  noComponents,
} from '@prusa/connect-ui/slots';
import type { Component } from 'svelte';

import { NAMESPACE } from '../i18n';

/** The module's mount, which every one of its routes is under. */
export const MOUNT = `/api/v1/modules/${NAMESPACE}`;

/** One request the module made, as the stub recorded it. */
export interface Call {
  readonly method: string;
  readonly path: string;
  readonly query: URLSearchParams;
}

/** One answer, by `METHOD path`. */
export interface Answer {
  readonly status: number;
  readonly body?: unknown;
}

export interface Stub {
  readonly host: ModuleHost;
  readonly calls: Call[];
  /** The file views the module registered, by id. */
  readonly fileViews: Record<string, Component<never>>;
  /** The components the module handed other modules, by name. */
  readonly components: Record<string, Component<never>>;
  /** Stops the module, as a person leaving does. */
  readonly leave: () => void;
}

const noFleet: ReadonlyFleet = {
  devices: [],
  loading: false,
  loaded: true,
  error: undefined,
  device: () => undefined,
};

const addressOf = (input: RequestInfo | URL): string => {
  if (typeof input === 'string') return input;

  return input instanceof URL ? input.href : input.url;
};

/** Builds a host whose fetch answers each call from the table. */
export const stubHost = (
  routes: Readonly<Record<string, Answer>> = {},
): Stub => {
  const calls: Call[] = [];
  const fetcher: typeof globalThis.fetch = (input, init) => {
    const url = new URL(addressOf(input), 'http://module.test');
    const method = init?.method ?? 'GET';
    calls.push({ method, path: url.pathname, query: url.searchParams });
    const reply = routes[`${method} ${url.pathname}`] ?? {
      status: 404,
      body: {
        type: 'about:blank',
        status: 404,
        code: 'test/NO_ROUTE',
        title: 'no route',
        detail: url.pathname,
        params: {},
        retryable: false,
      },
    };

    return Promise.resolve(
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

  const fileViews: Record<string, Component<never>> = {};
  const components: Record<string, Component<never>> = {};
  const leaving = new AbortController();

  const host: ModuleHost = {
    client: new PrusaConnect({
      auth: { kind: 'session' },
      fetch: fetcher,
      retryDelay: 0,
    }),
    principal: { id: 'user:demo', grants: [] },
    fleet: noFleet,
    store: {
      get: () => undefined,
      set: () => undefined,
      delete: () => undefined,
      keys: () => [],
    },
    t: translateFor(NAMESPACE),
    signal: leaving.signal,
    namespace: NAMESPACE,
    self: 'sink:test',
    pageHref: (path) => modulePageHref(NAMESPACE, path),
    components: noComponents,
    register: buildRegister({
      page: () => undefined,
      section: (slot, id, component) => {
        if (slot === 'file-view') fileViews[id] = component;
      },
      action: () => undefined,
      card: () => undefined,
      component: (name, component) => {
        components[name] = component;
      },
    }),
  };

  return {
    host,
    calls,
    fileViews,
    components,
    leave: () => {
      leaving.abort();
    },
  };
};
