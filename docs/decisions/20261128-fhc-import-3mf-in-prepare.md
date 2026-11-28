---
title: "ADR-20261128-fhc: Import 3MF in Prepare"
description: "Read 3MF geometry in the browser and save it as a placed STL."
date: 2026-11-28
status: proposed
supersedes: []
amends: [ADR-20261124-pcs]
tags:
  - adr
  - slicing
---

# ADR-20261128-fhc: Import 3MF in Prepare

<!-- nav:related -->
> [!NOTE]
> Index: [decisions](README.md) | Amends [ADR-20261124-pcs](20261124-pcs-prepare-a-print-plate-in-the-browser.md) rules 2 and 3

## rules

1. Prepare takes one STL or 3MF file. It checks the extension and file signature before reading the file in the browser. The selected profile supplies the rectangular print volume's bounds and height.
2. Each reader returns triangle meshes in millimetres with their instance transforms. The 3MF reader follows the build items and components and keeps their positions from the file.
3. Prepare edits one model. It combines the transformed meshes and keeps their positions relative to each other. A new STL starts in the centre of the bed. A 3MF below the bed moves up to the bed without changing its XY position. Reset placement centres the model and puts its lowest point on the bed.
4. The 3MF import keeps printable solid geometry. It uses PrusaSlicer 2 and Bambu part roles to skip modifiers, negative volumes and support tools. If a project supplies part roles Prepare cannot determine, it asks for a geometry export. The slicing job uses the saved profile selected in Prepare.
5. Prepare accepts files up to 20 MiB and 300,000 placed triangles. It reads a 3MF archive only when its expanded size is at most 64 MiB.

## enforcement

Chromium tests check file signatures, units, nested and mirrored instances, malformed files and import limits. They also check project part roles and placement below the bed. The Prepare tests check the placed STL and the job profile.

## rejected

| option | why ruled out |
|---|---|
| Send the source file to a module import route | The module mount accepts 1 MiB per request. Prepare allows files up to 20 MiB. |
