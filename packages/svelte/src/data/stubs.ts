// The stubs the store tests under `src/data` are built from.
//
// Nothing here opens a socket. The SDK's own tests fake `fetch` under the
// transport (`packages/typescript/test/stub.ts`); this package has no
// transport, so what it fakes is the operations themselves.
//
// It is not exported from `./index.ts`, so nothing outside a test imports it
// and no chunk of the published package contains it.
import type {
  PrusaConnect,
  ContinuityLost,
  EventSubscription,
  EventType,
  OriginWithdrawn,
  Operation,
  OperationRequest,
  StreamEvent,
} from '@prusa/connect';

import type { Timers } from './timers';

/** A test clock. `advance(ms)` runs the tasks whose delay has elapsed. */
export interface FakeClock extends Timers {
  advance(ms: number): void;
  readonly pending: number;
}

/** Builds a test clock. Due tasks run soonest first. */
export const fakeClock = (): FakeClock => {
  let now = 0;
  let next = 1;
  const tasks = new Map<number, { at: number; task: () => void }>();

  return {
    set: (task, ms) => {
      const handle = next;
      next += 1;
      tasks.set(handle, { at: now + ms, task });

      return handle;
    },
    clear: (handle) => {
      tasks.delete(handle);
    },
    advance: (ms) => {
      now += ms;
      const due = [...tasks]
        .filter(([, entry]) => entry.at <= now)
        .sort(([, left], [, right]) => left.at - right.at);
      for (const [handle, entry] of due) {
        tasks.delete(handle);
        entry.task();
      }
    },
    get pending() {
      return tasks.size;
    },
  };
};

/**
 * One frame as the stream carries it: the envelope the core writes, and the
 * payload under it. `subject` is the origin in its `kind:id` spelling.
 */
export const hubEvent = (
  type: Exclude<EventType, ContinuityLost | OriginWithdrawn>,
  payload: unknown,
  subject?: string,
): StreamEvent => ({
  type,
  id: '1',
  payload,
  envelope: {
    specversion: '1.0',
    id: '1',
    source: `urn:prusa:connect:${subject ?? 'core'}`,
    type,
    time: '2026-09-12T00:00:00Z',
    ...(subject === undefined ? {} : { subject }),
    hops: 0,
    evclass: 'state',
  },
});

/** A subscription a test pushes events into. */
export interface FakeStream {
  /** What a store's `events()` returns. */
  readonly subscription: EventSubscription;
  /** Hands one event to the consumer and waits for it to be applied. */
  push(event: StreamEvent): Promise<void>;
  /** True once the store closed it. */
  readonly closed: boolean;
}

/**
 * A subscription backed by a queue. The cast names the generic signature a
 * `PrusaConnect` declares; a test drives one event type at a time, so the
 * narrowing the real client does is not reproduced here.
 */
export const fakeStream = (): FakeStream => {
  const waiting: StreamEvent[] = [];
  let wake: (() => void) | undefined;
  let closed = false;

  const subscription: EventSubscription = {
    lastEventId: undefined,
    close: () => {
      closed = true;
      wake?.();
    },
    async *[Symbol.asyncIterator]() {
      while (!closed) {
        const event = waiting.shift();
        if (event === undefined) {
          await new Promise<void>((resolve) => {
            wake = resolve;
          });
          continue;
        }
        yield event;
      }
    },
  };

  return {
    subscription,
    push: async (event) => {
      waiting.push(event);
      wake?.();
      // Two turns of the microtask queue: one to leave the await above, one
      // for the store's own await inside `apply`.
      await Promise.resolve();
      await Promise.resolve();
    },
    get closed() {
      return closed;
    },
  };
};

/**
 * The `events` member of a client, answering one queue. The cast names the
 * generic signature `PrusaConnect` declares, which a test cannot write out:
 * it drives one concrete event type at a time.
 */
export const fakeEvents = (stream: FakeStream): PrusaConnect['events'] =>
  (() => stream.subscription) as unknown as PrusaConnect['events'];

/**
 * Lets every promise a fake clock's tick started settle.
 *
 * A read that goes through `load()` resolves a few microtasks after the read
 * itself, because the dedupe wraps it. Counting those turns by hand in a test
 * ties the test to the wrapping, so the test waits for the queue to empty
 * instead.
 */
export const settled = async (): Promise<void> => {
  for (let turn = 0; turn < 8; turn += 1) await Promise.resolve();
};

/** A client whose `events` throws. A store that never listens gets this one. */
export const noEvents: PrusaConnect['events'] = () => {
  throw new Error('this stub opens no stream');
};

/**
 * A refusal shaped like the SDK's `ApiError`: a registry code, a status and
 * the problem document's parameters. `faultOf` reads it by shape.
 */
export class StubApiError extends Error {
  override readonly name = 'ApiError';
  readonly code: string;
  readonly status: number;
  readonly params: Readonly<Record<string, string | number | boolean>>;

  constructor(
    code: string,
    status = 403,
    params: Readonly<Record<string, string | number | boolean>> = {},
  ) {
    super(`stub refusal ${code}`);
    this.code = code;
    this.status = status;
    this.params = params;
  }
}

/** A transport that fills a path template, for the camera URLs. */
export const stubUrlBuilder = (
  base = 'https://printer.example',
): { urlOf(operation: Operation, request: OperationRequest): string } => ({
  urlOf: (operation, request) => {
    const path = operation.path.replace(
      /\{([^}]+)\}/g,
      (_whole, name: string) =>
        encodeURIComponent(String(request.path?.[name])),
    );
    const query = new URLSearchParams();
    for (const [name, value] of Object.entries(request.query ?? {})) {
      if (typeof value !== 'string' && typeof value !== 'number') continue;
      query.set(name, String(value));
    }
    const search = query.toString();

    return base + path + (search === '' ? '' : `?${search}`);
  },
});
