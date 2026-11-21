// The whole router, and no meta-framework (ADR-20260911-4p6 rule 34). It reads
// `location.pathname` and writes the address bar with `pushState`, so every
// screen has a real address: a middle click opens it in a new tab, a copied
// address opens the same screen, and the back button works.
//
// That needs a server which answers the entry document for an address that
// names no file, and the core does (`internal/transport/http/httpapi/appui.go`,
// ADR-20260911-4p6 rule 33). Vite's dev server answers the same way, because the app
// is a single-page app to it.

/** What the router needs of `location`. Nothing else of it is read. */
export interface RouterLocation {
  readonly pathname: string;
  /** The query, `?` included, or empty. */
  readonly search: string;
  /** The fragment, `#` included, or empty. */
  readonly hash: string;
  /** The page's own origin, which is what tells an in-app link from any other. */
  readonly origin: string;
}

/** What the router needs of `history`. */
export interface RouterHistory {
  pushState(data: unknown, unused: string, url: string): void;
  replaceState(data: unknown, unused: string, url: string): void;
  /** Puts the page back at its top, as a browser does on a new page. */
  scrollToTop(): void;
  /**
   * Loads one address outside the app's routing. `assign` loads it as a new
   * document in this tab, `replace` does the same in place of the current
   * history entry, and `open` opens it in a new tab. Without it, the router
   * routes every address itself and never asks the rule `loadDocumentFor` set.
   */
  load?(url: string, how: DocumentLoad): void;
}

/** How `RouterHistory.load` leaves the app's routing for an address. */
export type DocumentLoad = 'assign' | 'replace' | 'open';

/**
 * Where an address has to be shown when the app cannot route it in the
 * current document. `document` is a new document in this tab, `window` a new
 * tab, and `undefined` lets the app route it. A page the server sends with
 * headers of its own needs this, because `history.pushState` fetches no
 * document.
 */
export type DocumentRule = (path: string) => 'document' | 'window' | undefined;

/**
 * The two events the router hears: `popstate` for back and forward, and
 * `click` for a link inside the page.
 */
export interface RouterTarget {
  addEventListener(
    type: 'popstate' | 'click',
    listener: (event: Event) => void,
  ): void;
  removeEventListener(
    type: 'popstate' | 'click',
    listener: (event: Event) => void,
  ): void;
}

/**
 * The address bar, the history and the events in one object. The browser
 * keeps them in three, and `browserNavigation()` joins them; a test hands in
 * one fake instead of three.
 */
export interface RouterNavigation
  extends RouterLocation, RouterHistory, RouterTarget {}

/**
 * How the browser hands an address over, for `RouterHistory.load`: a new
 * document in this tab, one in place of the current history entry, or a new
 * tab opened with `noopener`, which cannot reach this one through
 * `window.opener`.
 */
export const documentLoader =
  (
    address: Pick<Location, 'assign' | 'replace'>,
    open: (url: string, target: string, features: string) => unknown,
  ): ((url: string, how: DocumentLoad) => void) =>
  (url, how) => {
    if (how === 'open') {
      open(url, '_blank', 'noopener');
    } else if (how === 'replace') {
      address.replace(url);
    } else {
      address.assign(url);
    }
  };

/**
 * Within this time, a second hand-over of the same address with no move in
 * between is dropped. A strip with automatic activation reports one press
 * twice, on focus and on click.
 */
const REPEATED_HANDOVER_MS = 1000;

// The DOM lib declares all three as always present, and in a Node test none
// of them is.
const browser: {
  location?: Location;
  history?: History;
  addEventListener?: unknown;
} = globalThis;

/** The browser's own address bar, history and events, or `null` outside one. */
const browserNavigation = (): RouterNavigation | null => {
  const address = browser.location;
  const history = browser.history;
  if (!address || !history) return null;
  if (typeof browser.addEventListener !== 'function') return null;

  return {
    // A getter: the address changes under the router on every navigation.
    get pathname(): string {
      return address.pathname;
    },
    get search(): string {
      return address.search;
    },
    get hash(): string {
      return address.hash;
    },
    origin: address.origin,
    pushState: (data, unused, url) => {
      history.pushState(data, unused, url);
    },
    replaceState: (data, unused, url) => {
      history.replaceState(data, unused, url);
    },
    scrollToTop: () => {
      globalThis.scrollTo(0, 0);
    },
    load: documentLoader(address, (url, target, features) =>
      globalThis.open(url, target, features),
    ),
    addEventListener: (type, listener) => {
      globalThis.addEventListener(type, listener);
    },
    removeEventListener: (type, listener) => {
      globalThis.removeEventListener(type, listener);
    },
  };
};

/** One value a route pattern captures, keyed by the name the pattern gave it. */
export type RouteParams = Readonly<Record<string, string>>;

const PARAM = ':';

/**
 * Whether a pattern answers a path. A segment written `:name` takes any one
 * segment that is not empty, and every other segment matches itself. Nothing
 * here matches across a slash: a printer's page is one segment deep, and a
 * pattern that could swallow several would let `/devices/a/b` open it.
 */
export const matchesPattern = (pattern: string, path: string): boolean => {
  const want = pattern.split('/');
  const got = path.split('/');
  if (want.length !== got.length) return false;

  return want.every((segment, at) =>
    segment.startsWith(PARAM) ? (got[at] ?? '') !== '' : segment === got[at],
  );
};

/**
 * What a pattern captured from a path it matches. An empty record for a
 * pattern with no parameter, and for one that does not match.
 *
 * A segment is decoded, because the address bar holds it encoded. A segment
 * that is not a valid escape sequence is taken as it was written: a malformed
 * id names a printer this server does not have, which is an empty page, and
 * never a thrown error that takes the screen.
 */
export const paramsOf = (pattern: string, path: string): RouteParams => {
  if (!matchesPattern(pattern, path)) return {};

  const want = pattern.split('/');
  const got = path.split('/');
  const params: Record<string, string> = {};
  want.forEach((segment, at) => {
    if (!segment.startsWith(PARAM)) return;
    params[segment.slice(PARAM.length)] = decodeSegment(got[at] ?? '');
  });

  return params;
};

const decodeSegment = (value: string): string => {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
};

/** An address split into its path, its query and its fragment. */
interface Address {
  path: string;
  search: string;
  hash: string;
}

// Any origin serves to parse a path. The router never reads it back.
const PARSE_BASE = 'http://router.invalid';

/**
 * An address in the one form the browser writes it. A raw space becomes
 * `%20` and a bare `?` or `#` is dropped, so one address is one entry.
 */
const addressOf = (address: string): Address => {
  const url = new URL(address, PARSE_BASE);

  return { path: url.pathname, search: url.search, hash: url.hash };
};

const written = (address: Address): string =>
  `${address.path}${address.search}${address.hash}`;

const anchorOf = (target: EventTarget | null): HTMLAnchorElement | null => {
  if (!(target instanceof Element)) return null;

  // An `<a>` inside an SVG is an `SVGAElement`, which has no `origin` and no
  // `pathname`, so it is not a link this app can take.
  const found = target.closest('a[href]');

  return found instanceof HTMLAnchorElement ? found : null;
};

/**
 * The address a click asks this app for, and `undefined` when the browser
 * should take the click instead.
 *
 * The browser keeps: a middle click, a click with a modifier held, a link
 * that opens elsewhere or downloads, a link to another origin, and a link
 * carrying a `#fragment`, which points into the page the browser already
 * shows. Every one of those is a person asking for the browser's behaviour.
 * A link with a `?query` is taken, and the screen reads the query.
 */
const pathOfClick = (event: MouseEvent, origin: string): string | undefined => {
  if (event.defaultPrevented || event.button !== 0) return undefined;
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
    return undefined;
  }

  const anchor = anchorOf(event.target);
  if (anchor === null) return undefined;
  if (anchor.target !== '' || anchor.hasAttribute('download')) return undefined;
  if (anchor.origin !== origin) return undefined;
  if (anchor.hash !== '') return undefined;

  return anchor.pathname + anchor.search;
};

/**
 * Asked before the person leaves the address on screen, with the call that
 * leaves it. True keeps the person where they are, and the screen calls `go`
 * once they agree to leave, such as after a dialog that asks about unsaved
 * changes. False lets the navigation through at once.
 */
export type LeaveCheck = (go: () => void) => boolean;

export interface RouterOptions {
  /**
   * The route patterns this app serves. A path no pattern answers opens the
   * not-found screen. The array is read on every navigation and never copied,
   * so a module page pushed into it after the listing is read is a path the
   * router will take.
   */
  known: readonly string[];
  /** `null` reads no address bar and hears nothing, which is what a store test wants. */
  navigation?: RouterNavigation | null | undefined;
}

/**
 * A shared store, so a class (svelte-5.md). The address bar, the history and
 * the event target come in through the constructor, which is what lets a test
 * drive it without a browser.
 */
export class Router {
  // An array rather than a Set: the list is short, it is written once by the
  // app, and a `Set` in a `.svelte.ts` file is refused because a reactive one
  // is what a rune module usually wants.
  readonly #known: readonly string[];
  readonly #navigation: RouterNavigation | null;
  #path = $state('/');
  #search = $state('');
  #hash = $state('');
  #pattern = $state<string | undefined>(undefined);
  // ponytail: one check at a time, for the one screen that holds a draft.
  // A list of checks when two screens on one page each hold one.
  #leaveCheck: LeaveCheck | undefined;
  #documentRule: DocumentRule | undefined;
  #handedOver: { url: string; how: DocumentLoad; at: number } | undefined;

  constructor({ known, navigation = browserNavigation() }: RouterOptions) {
    this.#known = known;
    this.#navigation = navigation;
    this.#pattern = this.#patternFor(this.#path);
    this.read();
  }

  /** The path of the address, with a parameter filled in and no query. */
  get path(): string {
    return this.#path;
  }

  /**
   * Rejects a path this app does not serve: the state stays and the address
   * bar is not written. Ask `serves()` first when the answer matters, such as
   * a link click that the browser should take instead.
   */
  set path(next: string) {
    if (
      this.#kept(next, () => {
        this.path = next;
      })
    ) {
      return;
    }
    if (this.#loaded(next, 'assign')) return;
    const moved = this.#move(next);
    if (moved === undefined) return;
    this.#navigation?.pushState(null, '', moved.url);
    if (moved.newPath) this.#navigation?.scrollToTop();
  }

  /**
   * `path`, but in place of the current history entry: a screen that only
   * decides where to go leaves no entry for the back button to return to.
   * The leave check is not asked: a replace is the app deciding, such as
   * after a sign-out, and never a person leaving.
   */
  replace(next: string): void {
    if (this.#loaded(next, 'replace')) return;
    const moved = this.#move(next);
    if (moved === undefined) return;
    this.#navigation?.replaceState(null, '', moved.url);
    if (moved.newPath) this.#navigation?.scrollToTop();
  }

  /** The query of the address, `?` included, or empty. */
  get search(): string {
    return this.#search;
  }

  /** The fragment of the address, `#` included, or empty. */
  get hash(): string {
    return this.#hash;
  }

  /** The pattern that answered the address, and `undefined` for one this app does not serve. */
  get pattern(): string | undefined {
    return this.#pattern;
  }

  /** What the pattern captured. Empty for a screen that takes no parameter. */
  get params(): RouteParams {
    return this.#pattern === undefined
      ? {}
      : paramsOf(this.#pattern, this.#path);
  }

  /**
   * Asks `check` before a link, `path` or the back and forward buttons leave
   * the address on screen. Returns the call that stops asking.
   */
  checkLeave(check: LeaveCheck): () => void {
    this.#leaveCheck = check;

    return () => {
      if (this.#leaveCheck === check) this.#leaveCheck = undefined;
    };
  }

  /**
   * Asks `rule` before the app routes an address itself, and hands the
   * address to the browser where it answers `document` or `window`. Returns
   * the call that stops asking.
   */
  loadDocumentFor(rule: DocumentRule): () => void {
    this.#documentRule = rule;

    return () => {
      if (this.#documentRule === rule) this.#documentRule = undefined;
    };
  }

  /** Whether this app has a screen at an address. A query and a fragment are ignored. */
  serves(address: string): boolean {
    return this.#patternFor(addressOf(address).path) !== undefined;
  }

  /**
   * Takes the path from the address bar. A path this app does not serve is
   * kept as it was asked for, so the not-found screen shows the address the
   * person typed and a reload asks the same question again.
   */
  read(): void {
    if (this.#navigation === null) return;
    const asked = this.#navigation.pathname;
    this.#path = asked;
    this.#search = this.#navigation.search;
    this.#hash = this.#navigation.hash;
    this.#pattern = this.#patternFor(asked);
    this.#handedOver = undefined;
  }

  // Moves the state to an address and answers what to write, or undefined
  // when the address is not served or is the one already shown. `newPath`
  // is false when only the query or the fragment changed.
  #move(next: string): { url: string; newPath: boolean } | undefined {
    const address = addressOf(next);
    const pattern = this.#patternFor(address.path);
    if (pattern === undefined) return undefined;
    const newPath = address.path !== this.#path;
    if (
      !newPath &&
      address.search === this.#search &&
      address.hash === this.#hash
    ) {
      return undefined;
    }
    this.#path = address.path;
    this.#search = address.search;
    this.#hash = address.hash;
    this.#pattern = pattern;
    this.#handedOver = undefined;

    return { url: written(address), newPath };
  }

  /**
   * True when the address went to the browser as a document of its own. The
   * router's state stays as it is, because the page unloads or this tab keeps
   * showing what it showed. An address this app does not serve, or the one
   * already shown, is never handed over, and a repeat with no move in between
   * is taken as done. Back and forward pass `traversal`, and the entry is then
   * reloaded in place, never opened in a new tab.
   */
  #loaded(next: string, how: 'assign' | 'replace', traversal = false): boolean {
    const rule = this.#documentRule;
    const navigation = this.#navigation;
    if (rule === undefined || navigation?.load === undefined) return false;
    const address = addressOf(next);
    if (this.#patternFor(address.path) === undefined) return false;
    if (address.path === this.#path) return false;
    const where = rule(address.path);
    if (where === undefined || (traversal && where === 'window')) return false;
    const url = written(address);
    const load: DocumentLoad = where === 'window' ? 'open' : how;
    const now = performance.now();
    const last = this.#handedOver;
    if (
      last?.url === url &&
      last.how === load &&
      now - last.at < REPEATED_HANDOVER_MS
    ) {
      return true;
    }
    this.#handedOver = { url, how: load, at: now };
    navigation.load(url, load);

    return true;
  }

  /** True when the leave check keeps the person off an address that differs from this one. */
  #kept(next: string, go: () => void): boolean {
    const check = this.#leaveCheck;
    if (check === undefined) return false;
    const address = addressOf(next);
    const same =
      address.path === this.#path &&
      address.search === this.#search &&
      address.hash === this.#hash;

    return !same && check(go);
  }

  /** The pattern that answers a path, or `undefined` when this app serves none. */
  #patternFor(path: string): string | undefined {
    return this.#known.find((pattern) => matchesPattern(pattern, path));
  }

  /**
   * Starts listening for navigation and returns the call that stops it. A
   * component calls this in an `$effect` and returns the result, so nothing
   * outlives the page (svelte-testing.md).
   *
   * Two listeners: `popstate` for the back and forward buttons, and `click`
   * for a link this app serves. The click is cancelled only when the app took
   * it, so a link to anywhere else still reaches the browser.
   */
  listen(): () => void {
    const navigation = this.#navigation;
    if (navigation === null) return () => undefined;

    const onPopState = (): void => {
      const asked = `${navigation.pathname}${navigation.search}${navigation.hash}`;
      const shown = written({
        path: this.#path,
        search: this.#search,
        hash: this.#hash,
      });
      // The browser has already moved the address bar, so a kept person gets
      // the address on screen pushed back, and leaving later pushes the one
      // they asked for. ponytail: that leaves one extra history entry; the
      // exact step back needs the entry's index kept in the history state.
      if (
        this.#kept(asked, () => {
          this.path = asked;
        })
      ) {
        navigation.pushState(null, '', shown);

        return;
      }
      if (this.#loaded(asked, 'replace', true)) return;
      this.read();
    };

    const onClick = (event: Event): void => {
      if (!(event instanceof MouseEvent)) return;
      const asked = pathOfClick(event, navigation.origin);
      if (asked === undefined || !this.serves(asked)) return;

      event.preventDefault();
      this.path = asked;
    };

    navigation.addEventListener('popstate', onPopState);
    navigation.addEventListener('click', onClick);

    return () => {
      navigation.removeEventListener('popstate', onPopState);
      navigation.removeEventListener('click', onClick);
    };
  }
}
