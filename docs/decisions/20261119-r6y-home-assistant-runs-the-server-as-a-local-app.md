---
title: "ADR-20261119-r6y: Home Assistant runs the server as a local app"
description: "Package the existing server for Home Assistant OS and keep its state in the app's backup folder."
date: 2026-11-19
status: proposed
supersedes: []
amends: []
tags:
  - adr
---
# ADR-20261119-r6y: Home Assistant runs the server as a local app

<!-- nav:related -->
> [!NOTE]
> Index: [decisions](README.md)

## context

Home Assistant OS runs local apps as containers. It builds a local app from
its own folder and mounts a private `/data` folder for its state. The Prusa
Connect image is built from the full source tree, and the printer needs a LAN
address that it can reach without a Home Assistant session.

## rules

1. Export the server binary with its web app and the trusted certificates
   from the regular server build. Put the files for one architecture in each
   app archive. Use the marketplace to install optional extensions.
2. Open port `8080` for the web app and printer traffic. Offer an optional
   mapping for a dedicated printer listener on port `8443`.
3. Start the server as its own unprivileged user in `/data/prusa-connect`.
   Keep its default configuration, database and files there.
4. Replace the app archive to update the server. Apply the update or rebuild
   the app in Home Assistant. Use cold backups, which stop the server while
   Home Assistant copies the data.

## enforcement

The Home Assistant app workflow builds each archive and runs the app with a
mounted data folder. It checks that an administrator, a file and a printer
registration survive replacement of the container.

## rejected

| option | why not |
|---|---|
| Embedded Home Assistant integration | The fork would need its own signed releases to use this installer |
| Prebuilt image in a registry | Installing the app would depend on publishing the image before each version update |
