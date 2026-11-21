// What a store reports when a request was refused.
//
// A store never produces an English sentence of its own. The core answers a
// refusal as an RFC 9457 problem document with a stable `code`, and that code
// is the key a screen translates (`../i18n/fault.ts`). So the store keeps the
// code, the status and the parameters, and a component decides the words.
//
// `message` is the server's own English line. It is kept beside the code
// because it names the object, the field or the printer that a registry line
// cannot. It is never a literal written in this package.

/** A refused request, as a store reports it. */
export interface DataFault {
  /** The registry code, or one of the two local codes below. */
  readonly code: string;
  /** The HTTP status, absent when no answer arrived. */
  readonly status: number | undefined;
  /** The problem document's parameters, for a message with placeholders. */
  readonly params: Readonly<Record<string, string | number | boolean>>;
  /** The server's own English line, absent when there was none. */
  readonly message: string | undefined;
  /** The id the server stamped on the refusal, for a support ticket. Absent when the request never reached it. */
  readonly traceId: string | undefined;
}

/** No answer arrived: the connection failed, was refused, or dropped. */
export const NETWORK_FAULT = 'ui.data/NETWORK';

/** Something was thrown that is neither an `ApiError` nor a `NetworkError`. */
export const UNKNOWN_FAULT = 'ui.data/UNKNOWN';

/** The core declares an operation this build's generated client has no method for. */
export const OPERATION_MISSING_FAULT = 'ui.data/OPERATION_MISSING';

/**
 * Thrown by a service whose operation the generated client does not bind yet.
 * `operation` is the operationId in the core's document, so a screen can say
 * which one and a test can assert on it.
 */
export class MissingOperation extends Error {
  override readonly name = 'MissingOperation';
  readonly code = OPERATION_MISSING_FAULT;
  readonly params: Readonly<Record<string, string>>;

  constructor(operation: string) {
    super(`the client has no method for ${operation}`);
    this.params = { operation };
  }
}

const textOf = (value: unknown): string | undefined =>
  typeof value === 'string' && value.trim() !== '' ? value : undefined;

const numberOf = (value: unknown): number | undefined =>
  typeof value === 'number' && Number.isFinite(value) ? value : undefined;

const paramsOf = (
  value: unknown,
): Readonly<Record<string, string | number | boolean>> => {
  if (typeof value !== 'object' || value === null) return {};

  const kept: Record<string, string | number | boolean> = {};
  for (const [name, given] of Object.entries(value)) {
    if (
      typeof given === 'string' ||
      typeof given === 'number' ||
      typeof given === 'boolean'
    ) {
      kept[name] = given;
    }
  }

  return kept;
};

/**
 * Reads a thrown value into a fault. It matches on shape rather than on class,
 * so an `ApiError` from the SDK, a `MissingOperation` from a service, an
 * error a module's own client threw and a fault payload an answer carries
 * (`detail`, `trace_id`) are all read the same way.
 *
 * A thrown value with no code becomes `NETWORK_FAULT` when its name says the
 * request never reached the server, and `UNKNOWN_FAULT` otherwise.
 */
export const faultOf = (thrown: unknown): DataFault => {
  const shape: {
    code?: unknown;
    status?: unknown;
    params?: unknown;
    traceId?: unknown;
    trace_id?: unknown;
    detail?: unknown;
    name?: unknown;
  } = typeof thrown === 'object' && thrown !== null ? thrown : {};

  const message =
    thrown instanceof Error
      ? textOf(thrown.message)
      : (textOf(thrown) ?? textOf(shape.detail));
  const code = textOf(shape.code);
  const network = textOf(shape.name) === 'NetworkError';

  return {
    code: code ?? (network ? NETWORK_FAULT : UNKNOWN_FAULT),
    status: numberOf(shape.status),
    params: paramsOf(shape.params),
    message,
    traceId: textOf(shape.traceId) ?? textOf(shape.trace_id),
  };
};
