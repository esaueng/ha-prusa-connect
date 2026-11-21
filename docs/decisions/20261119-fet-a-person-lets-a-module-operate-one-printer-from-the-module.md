---
title: "ADR-20261119-fet: a person lets a module operate one printer from the module's tab"
description: "How a module that acts on a printer by itself gets the right to: one grant per printer, written by a person through the core's grant routes from the module's own page."
date: 2026-11-19
status: accepted
supersedes: []
amends: []
tags:
  - adr
  - extensions
  - authorization
---
# ADR-20261119-fet: a person lets a module operate one printer from the module's tab

<!-- nav:related -->
> [!NOTE]
> Index: [decisions](README.md) | Builds on [ADR-20260827-rqc](20260827-rqc-an-extension-declares-a-permission.md) and [ADR-20260901-b2z](20260901-b2z-driver-declared-permissions.md)

A module that acts on a printer with nobody behind the call, such as a print queue that starts the next file, needs `operate` on that printer. An approved install does not give it: a module holds the viewer role on the organization and no role above it, so `operate` is written on each printer apart. Until now a person wrote that grant on the server's access page, by the module's instance name, and nothing on the module's own page said that it was missing.

## rules

1. The right of a module to act on one printer by itself is one grant: the relation `operator` on that device, to the module's own instance. There is no second record of it. The module keeps no setting of its own that says the same.
2. A module's page may offer a switch that writes this grant and withdraws it. It writes through the core's grant routes with the client of the person on the page. The core decides whether that person may, as for any grant, and the page shows a refusal.
3. A module's process never writes a grant, for itself or for anybody.
4. The page offers the switch to a person whose actions on the device include `administer`. That is a hint for what to draw. The core's answer to the write is the decision.
5. The module asks the core whether it may operate the printer each time it is about to act by itself, and does not act without it. A person who presses the button acts with their own rights, with the grant or without.
6. Withdrawing the grant stops what the module does by itself from the next check. It does not stop a print already running.
7. The page names the module's instance by `self` on its host, as ADR-20261118-5q2 rule 14 says.

## enforcement

`TestWithoutItsOwnGrantTheQueueWaitsForAPerson` holds rule 5 for the print queue. The tests of the switch in `printer-queue.svelte.test.ts` hold rules 2 and 4 there. A person who may not turn it on is left the start button. Turning it writes and withdraws the grant.

Rules 1, 3, 6 and 7 have no gate of their own.

## rejected

| option | why not |
|---|---|
| an approved install grants `operate` on every printer | the module could start prints on printers nobody looked at, and a print on a bed that is not clear breaks the machine |
| the module keeps "starts by itself" in its own database | a second answer to the same question, which would disagree once the grant is withdrawn elsewhere |
| the module's process writes its own grant when a person turns the switch | a module that can write grants can give itself any right. The person's own client makes the core check the person |
| only the access page writes the grant | the person meets the missing right on the tab, and would need the instance name to fix it elsewhere |
