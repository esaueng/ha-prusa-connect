import { Router } from '@prusa/connect-ui/router';
import { SlotRegistry } from '@prusa/connect-ui/slots';
import { describe, expect, it } from 'vitest';

import { FakeNavigation } from '../test/navigation';
import {
  browserIsolation,
  documentFor,
  isolatedTabAt,
  isolateModuleTabs,
} from './isolation';
import { PATTERNS } from './routes';

// A printer tab that asks for an isolated page is opened and left with a full
// page load.

const CONSOLE = 'com.github.aux.acme.console';
const TAB = `/printers/one/${CONSOLE}.console`;

const registryWith = (hidden: readonly string[] = []): SlotRegistry => {
  const registry = new SlotRegistry();
  registry.declare(
    CONSOLE,
    {
      device_tab: [
        { id: 'console', title: 'Device page', cross_origin_isolated: true },
        { id: 'log', title: 'Log' },
      ],
    },
    hidden,
  );

  return registry;
};

const page = (isolated: boolean, capable = true, framed = false) => ({
  isolated,
  capable,
  framed,
});

describe('isolatedTabAt', () => {
  it('names the printer tab that asks for an isolated page, on any printer', () => {
    const registry = registryWith();

    expect(isolatedTabAt(registry, TAB)).toBe(true);
    expect(isolatedTabAt(registry, `/printers/a%2Fb/${CONSOLE}.console`)).toBe(
      true,
    );
  });

  it('names no other address', () => {
    const registry = registryWith();

    for (const path of [
      '/',
      '/printers/one',
      '/printers/one/status',
      `/printers/one/${CONSOLE}.log`,
      `/printers/one/com.github.aux.acme.other.console`,
      `/printers/one/two/${CONSOLE}.console`,
      `/files/one/${CONSOLE}.console`,
    ]) {
      expect(isolatedTabAt(registry, path), path).toBe(false);
    }
  });

  it('names no tab its module or the operator hides, nor one of a refused module', () => {
    expect(isolatedTabAt(registryWith(['device_tab/console']), TAB)).toBe(
      false,
    );

    const refused = registryWith();
    refused.refuse({ namespace: CONSOLE, reason: 'threw', detail: 'boom' });
    expect(isolatedTabAt(refused, TAB)).toBe(false);
  });
});

describe('documentFor', () => {
  it('loads a document to enter the tab and to leave it', () => {
    const registry = registryWith();

    expect(documentFor(registry, TAB, page(false))).toBe('document');
    expect(documentFor(registry, '/printers/one/status', page(true))).toBe(
      'document',
    );
    expect(documentFor(registry, '/', page(true))).toBe('document');
  });

  it('routes in the app where the document already is what the address needs', () => {
    const registry = registryWith();

    expect(documentFor(registry, TAB, page(true))).toBeUndefined();
    expect(
      documentFor(registry, '/printers/one/status', page(false)),
    ).toBeUndefined();
  });

  it('opens the tab in a new tab when the app is in a frame of another page', () => {
    expect(documentFor(registryWith(), TAB, page(false, true, true))).toBe(
      'window',
    );
  });

  it('routes everything in the app where the browser cannot isolate', () => {
    const registry = registryWith();

    expect(documentFor(registry, TAB, page(false, false))).toBeUndefined();
    expect(
      documentFor(registry, TAB, page(false, false, true)),
    ).toBeUndefined();
  });
});

describe('browserIsolation', () => {
  it('reads an isolated top-level page in a secure context', () => {
    const scope = { crossOriginIsolated: true, isSecureContext: true };

    expect(browserIsolation({ ...scope, top: scope, self: scope })).toEqual({
      isolated: true,
      capable: true,
      framed: false,
    });
  });

  it('reads a page in a frame of another page as framed', () => {
    const scope = {
      crossOriginIsolated: false,
      isSecureContext: true,
      top: {},
      self: {},
    };

    expect(browserIsolation(scope).framed).toBe(true);
  });

  it('reads no secure context, or a browser that knows no isolation, as not capable', () => {
    const plain = { crossOriginIsolated: false, isSecureContext: false };
    const old = { isSecureContext: true };

    expect(browserIsolation(plain).capable).toBe(false);
    expect(browserIsolation(old)).toMatchObject({
      isolated: false,
      capable: false,
    });
  });

  // The test runner draws this page in a frame of its own, on localhost.
  it('reads this test page from the browser itself', () => {
    expect(browserIsolation()).toEqual({
      isolated: false,
      capable: true,
      framed: true,
    });
  });
});

describe('isolateModuleTabs', () => {
  it('hands the tab to the browser and routes every other address', () => {
    const bar = new FakeNavigation('/printers/one/status');
    const router = new Router({ known: PATTERNS, navigation: bar });
    const stop = isolateModuleTabs(router, registryWith(), () => page(false));

    router.path = TAB;
    router.path = '/printers/one/files';

    expect(bar.loads).toEqual([[TAB, 'assign']]);
    expect(router.path).toBe('/printers/one/files');

    stop();
    router.path = TAB;
    expect(bar.loads).toHaveLength(1);
  });
});
