---
title: "ADR-20261118-fys: a module reads a device's state change and what it takes in its state"
description: "The event a device's state change is told with, and the two members of a module's device record that say which verbs the device takes now and whether it is ready."
date: 2026-11-18
status: accepted
supersedes: []
amends: []
tags:
  - adr
  - devices
  - events
  - sdk
---
# ADR-20261118-fys: a module reads a device's state change and what it takes in its state

<!-- nav:related -->
> [!NOTE]
> Index: [decisions](README.md) | Builds on [ADR-20261025-z7t](20261025-z7t-a-dialect-declares-its-states-and-each-argument-says-its.md) rules 1 to 4 and [ADR-20260831-c7b](20260831-c7b-an-adapter-declares-the-states-and-the-permissions-and-the-host-places-them.md) rules 6 to 8 | Reads with [ADR-20260826-5yj](20260826-5yj-a-device-is-the-object-and-a-printer-is-the-type.md)

A module that waits for a printer to take a print, such as a print queue, had two questions to answer by itself. It learned that a state changed only from `device.telemetry.sampled`, which carries every measurement of every device, or by asking each printer on a timer. And it knew that a printer takes a print only by comparing the state with words it had written down, such as `IDLE` and `READY`. Those are one connector's words, so a printer of another connector was never free for it.

The host already holds both answers. It keeps the state of each device, and each connector declares which verbs every state accepts (ADR-20261025-z7t rule 4).

## rules

### the event

1. The host publishes `com.prusa.connect.device.state.changed`, of class state, when the value a device holds at `/state` changes. A device that reports the state it is in publishes nothing.
2. The frame carries `device_id`, `driver`, `from` and `to`. The two states are the machine's own words, as the device-state vocabulary spells them.
3. `from` is empty for the first state of a device the host holds none for. A device that goes away publishes no frame of this type, because its state did not change: `com.prusa.connect.device.departed` says that.

### the record

4. `deviceapi.Device` carries `accepts`: the verbs of the device's capability set that its connector's state set accepts in the state the device is in, sorted. The host works it out from the placed state set (ADR-20260831-c7b rule 8) and never asks the device.
5. `accepts` is nil for a device whose connector declares no accepted verbs, and empty for a device that takes nothing. A device that is away takes nothing. A module reads nil as "not said" and decides for itself.
6. `accepts` is a hint for deciding what to offer. The device's answer to a command stays the decision, as it does for the vocabulary the list is read from.
7. `deviceapi.Device` carries `ready`: the device is connected and in a state that a transition caused by `core.command.set_printer_ready` enters. A device whose connector declares no such transition is never ready.
8. `ready` stays on the device record. The record is the one place a module reads the state of a device, and the member is filled by a mechanism every kind shares: a declared transition. Its verb is a printer's, as every verb inside the capability set is (ADR-20260826-5yj rule 3).

## enforcement

`TestAStateChangeIsOneFrameAndARepeatIsNone` holds rules 1 to 3. `TestModuleDevice_acceptsAndReady` holds rules 4, 5 and 7, and `TestStatesByInstance_keysEveryInstance` holds that a device finds its state set by the instance it names. `make event-types-check` holds the generated table and the TypeScript types of the event, and `make sdk-reference-check` the reference of the record.

Rules 6 and 8 have no gate of their own.

## rejected

| option | why not |
|---|---|
| a module filters `device.telemetry.sampled` for `/state` | it reads every measurement of the fleet to find the few frames that change a state |
| the table of each verb's states on every device record | it is the same for every device of a connector, and the module would still join it with the state |
| a module reads the device-state vocabulary | it holds the table but does not answer "now", which is what a module asks. A call for it can come later |
| no `ready`, and a module reads the state named `READY` | that is one connector's word again |
| `ready` on a record of its own for printers | a module would make a second call per printer for one bit |
