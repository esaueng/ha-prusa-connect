---
title: "ADR-20261124-vc5: saved profiles for STL slicing"
description: "Slice STL files with a named PrusaSlicer configuration saved by an administrator."
date: 2026-11-24
status: proposed
supersedes: []
amends: [ADR-20261026-ydf]
tags:
  - adr
---
# ADR-20261124-vc5: saved profiles for STL slicing

<!-- nav:related -->
> [!NOTE]
> Index: [decisions](README.md)

## rules

1. Take an STL file from a storage or an upload. Require the job to name a saved profile.
2. Save profiles in the module's administrator settings. Take a flat PrusaSlicer 2.x INI export for a filament printer. Return only profile names in the slicer list.
3. Reject post-processing scripts. Use the job's output path for the G-code.
4. Keep the profile name on the job. Include it in the default output name, so the same STL can be sliced for more than one printer.
5. Use the existing storage permissions, queue and process limits for STL jobs. A 3MF project continues to carry its own settings.
6. Run STL jobs with PrusaSlicer 2.x, which reads the saved INI format.

## enforcement

The settings tests check the profile input and the job tests check the model,
profile, slicer version and output path the worker uses.

## rejected

| option | why not |
|---|---|
| Choose a printer model as the profile | The model leaves out nozzle, material and print settings. |
| Require an extra 3MF for every STL job | A saved profile can serve every job that uses those settings. |
