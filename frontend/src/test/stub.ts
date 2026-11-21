// A `PrusaConnect` built on a fetch that answers from a table and records what
// it was asked, so a test reads the request the SDK built and never opens a
// socket. It is packages/typescript/test/stub.ts keyed by request rather than
// by ordinal: a screen makes its calls in whatever order its effects settle,
// and a script by position turns that into a flaky test.
//
// The real client and the real transport are under it, so a test here exercises
// the URL the SDK builds, the credential mode it sends, and the event-stream
// parser, and not a hand-written double of any of them.
import { PrusaConnect } from '@prusa/connect';

export interface Call {
  readonly method: string;
  readonly path: string;
  /** The query the SDK built, decoded, such as `tag=night shift`. Empty for none. */
  readonly query: string;
  readonly body: string | undefined;
  readonly credentials: RequestCredentials | undefined;
  /** The `If-Match` header, when the request carried one. */
  readonly ifMatch?: string | undefined;
}

export type Answer = Response | Error;

export type Handler = (call: Call) => Answer | Promise<Answer>;

/** Keyed `"<METHOD> <path>"`, such as `"GET /api/v1/devices"`. */
export type Routes = Record<string, Handler | Answer>;

export interface Stub {
  readonly calls: Call[];
  readonly client: PrusaConnect;
  /** How many times one route was asked for. */
  count(key: string): number;
}

export const json = (status: number, body: unknown): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });

export const problem = (
  status: number,
  code: string,
  params: Record<string, string | number | boolean> = {},
): Response =>
  new Response(
    JSON.stringify({
      type: 'about:blank',
      status,
      code,
      title: 'refused',
      detail: `the server refused with ${code}`,
      params,
      retryable: false,
      trace_id: 'trace-1',
    }),
    { status, headers: { 'content-type': 'application/problem+json' } },
  );

export const empty = (status: number): Response =>
  new Response(null, { status });

/**
 * An event stream the test writes into after handing the response over.
 * `push` delivers one frame as the socket would and `end` closes it the way a
 * server that went away does.
 */
export interface Stream {
  readonly response: Response;
  push(chunk: string): void;
  end(): void;
}

export const stream = (): Stream => {
  const encoder = new TextEncoder();
  let control: ReadableStreamDefaultController<Uint8Array> | undefined;
  let open = true;
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      control = controller;
    },
    cancel() {
      open = false;
    },
  });

  return {
    response: new Response(body, {
      status: 200,
      headers: { 'content-type': 'text/event-stream' },
    }),
    push: (chunk) => {
      if (open) control?.enqueue(encoder.encode(chunk));
    },
    end: () => {
      if (open) control?.close();
      open = false;
    },
  };
};

/** One server-sent frame carrying a CloudEvents envelope, as the core writes it. */
export const frame = (
  id: string,
  type: string,
  envelope: Record<string, unknown>,
): string =>
  `id: ${id}\nevent: ${type}\ndata: ${JSON.stringify({ specversion: '1.0', id, type, source: 'test', time: new Date().toISOString(), hops: 0, evclass: 'state', ...envelope })}\n\n`;

// What fetch was asked for, as text. The transport always passes a string,
// and the other two shapes are here because the signature allows them.
const addressOf = (input: RequestInfo | URL): string => {
  if (typeof input === 'string') return input;
  return input instanceof URL ? input.href : input.url;
};

/** The fetch under a stub, for a page that reaches the core through the app's own client. */
export interface StubFetch {
  readonly calls: Call[];
  readonly fetch: typeof globalThis.fetch;
  /** How many times one route was asked for. */
  readonly count: (key: string) => number;
}

export const stubFetch = (routes: Routes): StubFetch => {
  const calls: Call[] = [];

  const fetcher: typeof globalThis.fetch = async (input, init) => {
    // Decoded, so a route whose path parameter is a principal such as
    // `user:demo` is keyed as it is written rather than as `user%3Ademo`.
    const url = new URL(addressOf(input), 'http://core.test');
    const path = decodeURIComponent(url.pathname);
    const method = init?.method ?? 'GET';
    const call: Call = {
      method,
      path,
      query: decodeURIComponent(url.search.replace(/^\?/, '')),
      body: typeof init?.body === 'string' ? init.body : undefined,
      credentials: init?.credentials,
      ifMatch:
        new Headers(
          init?.headers ??
            (typeof input === 'object' && 'headers' in input
              ? input.headers
              : undefined),
        ).get('if-match') ?? undefined,
    };
    calls.push(call);

    const route = routes[`${method} ${path}`];
    // A route the table does not name is a 404 rather than a thrown error:
    // the transport retries a GET whose connection failed, and a test that
    // forgot a route would then wait out the backoff before failing.
    if (route === undefined) return problem(404, 'core.api/NOT_FOUND');

    const answer = typeof route === 'function' ? await route(call) : route;
    if (answer instanceof Error) throw answer;

    // A route may be asked more than once and a Response body is read once, so
    // an answer is handed over as a copy. An event stream is the exception:
    // cloning tees it, and the branch nobody reads holds every frame in
    // memory until the test ends.
    const type = answer.headers.get('content-type') ?? '';
    return type.includes('text/event-stream') ? answer : answer.clone();
  };

  return {
    calls,
    fetch: fetcher,
    count: (key) =>
      calls.filter((call) => `${call.method} ${call.path}` === key).length,
  };
};

export const stubClient = (routes: Routes): Stub => {
  const stubbed = stubFetch(routes);
  const { calls } = stubbed;

  return {
    calls,
    client: new PrusaConnect({
      auth: { kind: 'session' },
      fetch: stubbed.fetch,
      retries: 0,
    }),
    count: (key) => stubbed.count(key),
  };
};
