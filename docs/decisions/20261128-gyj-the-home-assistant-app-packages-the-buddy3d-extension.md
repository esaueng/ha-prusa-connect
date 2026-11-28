---
title: "ADR-20261128-gyj: the Home Assistant app packages the Buddy3D extension"
description: "Include the Buddy3D extension in the Home Assistant app archive so a camera can pair on the local network."
date: 2026-11-28
status: proposed
supersedes: []
amends: [ADR-20261119-r6y]
tags:
  - adr
---
# ADR-20261128-gyj: the Home Assistant app packages the Buddy3D extension

<!-- nav:related -->
> [!NOTE]
> Index: [decisions](README.md) | Amends [ADR-20261119-r6y](20261119-r6y-home-assistant-runs-the-server-as-a-local-app.md)

The Buddy3D camera extension lets a camera pair without a Prusa account.
Prusa's core release builds the extension from source. The extension is
absent from the marketplace and the Home Assistant app archive.

## rules

1. Build the Buddy3D camera extension from a pinned Prusa Connect commit and include it in each Home Assistant app archive at `/opt/modules/buddy3d-camera`.
2. Offer Buddy3D camera in **Settings > Extensions** through `core.modules_local`. Include the entry in the default configuration. Operators add it to existing configurations.
3. Let the operator approve the extension and its camera listener on TCP port `9443`. The camera reaches that port on the Home Assistant host.

## enforcement

The Home Assistant archive test installs the extension through the API. It
checks the camera type, browser bundle and TCP listener before and after
replacing the container.

## rejected

| option | why not |
|---|---|
| Use the marketplace for Buddy3D | The marketplace has no Buddy3D release to install |
