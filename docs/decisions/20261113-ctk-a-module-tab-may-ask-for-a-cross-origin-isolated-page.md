---
title: "ADR-20261113-ctk: a module tab may ask for a cross-origin isolated page"
description: "A device_tab entry may carry cross_origin_isolated: true. The server then sends Cross-Origin-Opener-Policy same-origin and Cross-Origin-Embedder-Policy require-corp with the app's page at that tab's address alone, and the app enters and leaves the tab with a full page load."
date: 2026-11-13
status: proposed
supersedes: []
amends: [ADR-20260911-4p6]
tags:
  - adr
  - extensions
  - frontend
  - security
---
# ADR-20261113-ctk: a module tab may ask for a cross-origin isolated page

<!-- nav:related -->
> [!NOTE]
> Index: [decisions](README.md) | Amends [ADR-20260911-4p6](20260911-4p6-a-module-extends-the-ui-through-one-library-and-four-slots.md) | Builds on [ADR-20261110-5np](20261110-5np-a-module-entry-may-show-only-on-the-printer-the-server-runs.md)

A device this server manages may serve a web page of its own. Such a page
may need `SharedArrayBuffer`. A WebAssembly app with threads is one
example. A module shows such a page in a frame, in its tab on the
printer's page. A browser gives `SharedArrayBuffer` only to a document
that is cross-origin isolated, and a document in a frame is isolated only
when the page around the frame is isolated too.

A page is isolated when it sends two headers.
`Cross-Origin-Opener-Policy: same-origin` separates the page from any window
of another origin that it opens or that opened it. Neither can reach the
other through `window.opener` or what `window.open` returns.
`Cross-Origin-Embedder-Policy: require-corp` lets the page load a resource
of another origin only when that resource allows it with
`Cross-Origin-Resource-Policy` (CORP) or CORS. That is the cost. It applies
to every part of the page, other modules' parts included.

Both headers act on one document, not on the origin. So the page of the one
tab that frames the device is isolated, and every other page of the app stays
as it is. The tab says so in its module's manifest, next to the `devices`
selector of ADR-20261110-5np, and no operator has to set anything. That
adds a key to the `device_tab` entries of ADR-20260911-4p6 rule 13. Rule 13
below adds `frame-src` to the policy of its rule 22.

ADR-20260911-4p6 rejected an iframe per module as the way to keep a module's
code apart from the app, because each frame costs a renderer of its own.
That stays rejected. A device's own page is a different case. It comes from
the device's origin and the app's library cannot draw it, so a frame is the
only way to show it. It costs one renderer while its tab is open.

## rules

1. An entry of `device_tab` may carry `cross_origin_isolated`, true or false.
   No other slot takes the key, and an absent key is false.
2. The server sends `Cross-Origin-Opener-Policy: same-origin` and
   `Cross-Origin-Embedder-Policy: require-corp` with the app's entry document
   at `/printers/<device id>/<namespace>.<tab id>`. It does so when an
   enabled, running module of that namespace declares that tab with
   `cross_origin_isolated: true`, and neither the module nor the operator
   hides it. That holds for a 200, a 304 and HEAD, compressed or not.
3. No other answer carries either header. That includes every other address
   of the app, a file under `assets/`, a module's file and `/docs`. When no
   module declares such a tab, every answer is exactly the answer of a server
   that does not implement this record.
4. The server reads no caller and no device record to decide, so the entry
   document is the same for every viewer. A tab's address is isolated on
   every printer, even where a `devices` selector keeps the tab off it. It is
   isolated even when the web app refused the module and shows none of its
   tabs, because the server does not know of that refusal.
5. A client that has not signed in may read from the headers whether a
   module's tab asks for isolation and whether it is hidden. That a module
   is installed is readable already from `/api/v1/modules/<namespace>/`.
6. The entity tags of the entry document at an isolated address carry
   `-isolated` after the digest. A 304 replaces only the stored headers it
   carries (RFC 9111 §4.3.4), and a 304 for a page that is not isolated
   carries no isolation header. With one tag, an address would stay isolated
   after its module stops asking.
7. The app enters and leaves such a tab with a full page load, because the
   headers come only with a document. While a file for a printer is still
   uploading from this browser, or waits there to be sent, the browser asks
   the person before it leaves the page.
8. In a frame of another page, the app opens the tab in a new browser tab,
   because a frame inside a page that is not isolated is never isolated. A
   page that frames the app in a sandboxed frame gives it `allow-popups` and
   `allow-popups-to-escape-sandbox`, or the tab cannot open in a new browser
   tab.
9. Without a secure context, which is HTTPS or `localhost`, the app routes
   the tab as any other, because nothing can be isolated there.
10. Where the browser can isolate and the tab strip holds such a tab, every
    tab in the strip opens on Enter, Space or a click. The arrow keys only
    move the focus, so moving along the strip loads no page.
11. A module that frames a device's page under isolation gives the frame
    `allow="cross-origin-isolated"`. The device's page sends
    `Cross-Origin-Embedder-Policy: require-corp` and
    `Cross-Origin-Resource-Policy: cross-origin`.
12. On HTTPS, the device's page is HTTPS too, with a certificate the browser
    trusts. A browser blocks a frame served over plain HTTP inside a page
    served over HTTPS. So the frame works on `localhost`, or with
    certificates the browser trusts on both sides. A browser also blocks a
    frame with a certificate it has not accepted, and offers no way to accept
    it there. So the module offers a link that opens the device's page in a
    window of its own.
13. The Content-Security-Policy of ADR-20260911-4p6 rule 22, when the web app
    sends one, lists in `frame-src` the origins of the device pages that
    modules frame.

## enforcement

| rule | gate |
|---|---|
| 1 | `TestAPrinterTabMayAskForAnIsolatedPage` in `sdk/manifest` |
| 1 | `TestOnlyAPrinterTabMayAskForAnIsolatedPage` in `sdk/manifest` |
| 2 | `TestTheTabsPageIsSentIsolated` in `internal/transport/http/httpapi` |
| 2 | `TestTheTabAddressIsReadAsTheAppRoutesIt` in `internal/transport/http/httpapi` |
| 2 | `TestTheServerReadsATabAddressAsTheAppWritesIt` in `internal/arch` |
| 2, 4 | `TestTheLookupIsolatesADeclaredTabThatAsksAndIsNotHidden` in `internal/app` |
| 2 | `TestAHiddenTabIsNotIsolated` in `internal/app` |
| 2, 3, 4 | `TestATabThatAsksForIsolationIsolatesItsOwnPageAlone` in `internal/app` |
| 3 | `TestEveryOtherAnswerIsTheOneAServerWithNoSuchTabSends` in `internal/transport/http/httpapi` |
| 2, 3 | `TestTheReleaseBundlesTabPageIsIsolatedInEveryForm` in `internal/transport/http/httpapi` |
| 3 | `TestTheReferenceIsNotIsolated` in `internal/transport/http/httpapi` |
| 6 | `TestATabsPageIsTaggedApartFromThePlainPage` in `internal/transport/http/httpapi` |
| 7, 8, 9 | `isolation.svelte.test.ts` in `frontend/src/lib` |
| 7 | `app.svelte.test.ts` in `frontend/src` |
| 7 | `sends-indicator.svelte.test.ts` in `packages/svelte/src/shell/sends-indicator` |
| 7, 8 | `router.svelte.test.ts` in `packages/svelte/src/router` |
| 10 | `printer-detail.svelte.test.ts` in `frontend/src/lib/screens` |
| 10 | `detail-tabs.svelte.test.ts` in `packages/svelte/src/primitives/detail-tabs` |

Rule 5, the sandbox flags of rule 8 and rules 11 to 13 have no gate. Rule 3 has no gate for a module's
file. A module's files are served by a handler of their own, which sends
neither header.

## rejected

| option | why not |
|---|---|
| a configuration key that isolates the whole app and `/docs` | every page then loses each resource of another origin without CORP, for one tab, through a permanent key |
| `Document-Isolation-Policy` on the device's page alone | only Chromium supports it, and the app runs in Firefox and Safari too |
| opening the device's page in a new tab instead of a frame | the device's own page belongs inside the printer's page, and a link to a new tab stays for when the frame cannot load |
| `credentialless` as the embedder policy | Safari does not support it, and it would not change what the device's page in the frame has to send |
| the headers on every file of the app | they act on a document and on a worker script alone, and the app starts no worker |
| entering the tab without a new document | the headers come only with a document, so the page would stay as it was |
| one entity tag for both states of an address | a page stored while the tab asked for isolation keeps both policies after it stops asking |
| a warning the core shows when a tab's page is not isolated | the module shows the device's page and a link to it, so the module knows what the person can do next |

## open items

- A frame of a module's own HTML file, or a worker started from a module's URL, is blocked on an isolated page, because module files carry no embedder policy.
- The web app sends no Content-Security-Policy yet, so rule 13 has nothing to apply to.
- A link to the tab that a person follows before the app has read the modules' declarations opens in the current page, which is not isolated. The return from a sign-in through a provider to the tab's address may be such a case. The tab is isolated only after the next full load. It is fixed when the app checks the current address once the declarations arrive.
- When a page frames the app and blocks new tabs, a press on such a tab does nothing, or opens a browser error page. A tab opened with `noopener` cannot tell the app that it did not open. It is fixed when the router learns whether the tab opened and otherwise routes it in the frame, so the module can say what is missing. A page that allows new tabs but not their escape from the sandbox still opens the error page, which the router cannot tell from a tab that opened.
