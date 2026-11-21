---
title: "ADR-20261121-qk5: The Home Assistant app uses the host network"
description: "Run the Home Assistant app on the host network so browsers can reach its live camera streams."
date: 2026-11-21
status: proposed
supersedes: []
amends: [ADR-20261119-r6y]
tags:
  - adr
---
# ADR-20261121-qk5: The Home Assistant app uses the host network

<!-- nav:related -->
> [!NOTE]
> Index: [decisions](README.md)

## context

Prusa Connect sends live H.264 camera video to the browser over WebRTC. The
server advertises its network addresses to the browser and sends video over
UDP. A container with a separate network advertises container addresses.
Publishing the web port gives the browser access to the web page, while
the video connection still needs an address and UDP port it can reach.

## rules

1. Run the Home Assistant app on the host network. The server advertises
   its available addresses at run time.
2. Use the server's listener ports directly. Keep `8080` for the web app
   and default printer connection. Use `8443` when configuring the optional
   dedicated printer listener. These rules replace rule 2 of
   [ADR-20261119-r6y](20261119-r6y-home-assistant-runs-the-server-as-a-local-app.md).

## enforcement

The Home Assistant app workflow checks the network mode in each packaged
archive before starting its app smoke test.

## rejected

| option | why not |
|---|---|
| Publish a UDP port from the container | A fixed mapping also needs a fixed server port and the host address in the WebRTC answer. |
