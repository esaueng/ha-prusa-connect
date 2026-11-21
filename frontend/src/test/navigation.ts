import type { DocumentLoad, RouterNavigation } from '@prusa/connect-ui/router';

/**
 * A stand-in for the browser's address bar and history, so a test can
 * navigate without writing the page's real one. `back()` does what the back
 * button does: it moves the address and then fires `popstate`.
 *
 * The events are the page's own. A click on a link bubbles to the window, and
 * a router that listened anywhere else would never hear one, so this fake
 * registers every listener there and fires `popstate` there too. Only the
 * address bar and the history are pretended.
 */
export class FakeNavigation implements RouterNavigation {
  readonly origin = globalThis.location.origin;
  #entries: readonly string[];
  #at: number;

  constructor(path = '/') {
    this.#entries = [path];
    this.#at = 0;
  }

  get pathname(): string {
    return (this.#entries[this.#at] ?? '/').replace(/[?#].*$/, '');
  }

  get search(): string {
    const entry = (this.#entries[this.#at] ?? '').replace(/#.*$/, '');
    const at = entry.indexOf('?');

    return at < 0 ? '' : entry.slice(at);
  }

  get hash(): string {
    const entry = this.#entries[this.#at] ?? '';
    const at = entry.indexOf('#');

    return at < 0 ? '' : entry.slice(at);
  }

  /** How many times a move asked for the top of the page. */
  scrolledToTop = 0;

  scrollToTop(): void {
    this.scrolledToTop += 1;
  }

  /** Every address this fake holds, oldest first. */
  get entries(): readonly string[] {
    return this.#entries;
  }

  pushState(_data: unknown, _unused: string, url: string): void {
    // A push after a back drops what was ahead, which is what a browser does.
    this.#entries = [...this.#entries.slice(0, this.#at + 1), url];
    this.#at = this.#entries.length - 1;
  }

  replaceState(_data: unknown, _unused: string, url: string): void {
    this.#entries = this.#entries.map((entry, index) =>
      index === this.#at ? url : entry,
    );
  }
  /** Every address handed to the browser as a document of its own, with how. */
  readonly loads: [string, DocumentLoad][] = [];

  load(url: string, how: DocumentLoad): void {
    this.loads.push([url, how]);
  }

  back(): void {
    if (this.#at === 0) return;
    this.#at -= 1;
    globalThis.dispatchEvent(new Event('popstate'));
  }

  addEventListener(
    type: 'popstate' | 'click',
    listener: (event: Event) => void,
  ): void {
    globalThis.addEventListener(type, listener);
  }

  removeEventListener(
    type: 'popstate' | 'click',
    listener: (event: Event) => void,
  ): void {
    globalThis.removeEventListener(type, listener);
  }
}
