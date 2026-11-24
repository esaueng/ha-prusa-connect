---
title: "ADR-20261124-pcs: Prepare a print plate in the browser"
description: "Place an STL in a browser and slice it with the saved placement."
date: 2026-11-24
status: proposed
supersedes: []
amends: [ADR-20261124-vc5]
tags:
  - adr
---
# ADR-20261124-pcs: Prepare a print plate in the browser

<!-- nav:related -->
> [!NOTE]
> Index: [decisions](README.md)

## rules

1. Draw the Prepare workspace in the Server Slicing extension. Keep the slicing job and storage access in the existing API.
2. Take one STL and show a rectangular print volume from a saved profile. Expose only its bounds and height from the profile.
3. Apply translation, rotation and scale to the STL in the browser. Put the lowest point of the model on the bed.
4. Save the placed STL as a new file in Storage. Slice it through the stored-file job route so its size can exceed the module upload limit.
5. Honor `keep_placement` on a stored STL job. Pass `--dont-arrange` to PrusaSlicer 2.x so the toolpaths use the placement the person chose.

## enforcement

The browser tests check placement bounds and the STL sent to Storage.
The route test checks that the flag reaches the slicer.

## rejected

| option | why not |
|---|---|
| Send the placed STL through the module upload | The module mount takes only 1 MiB in one request. |
| Put the editor in the core | The extension already has the profiles and runs the jobs. |
