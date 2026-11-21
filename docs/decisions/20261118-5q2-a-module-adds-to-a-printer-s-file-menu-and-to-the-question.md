---
title: "ADR-20261118-5q2: a module adds to a printer's file menu and to the question a dropped file asks"
description: "Two slots on a printer's page, the two host functions that send a module's files through that page, and what a module reads of a printer's files without waiting for the printer."
date: 2026-11-18
status: accepted
supersedes: []
amends: [ADR-20260911-4p6, ADR-20261027-wvj]
tags:
  - adr
  - extensions
  - sdk
---
# ADR-20261118-5q2: a module adds to a printer's file menu and to the question a dropped file asks

<!-- nav:related -->
> [!NOTE]
> Index: [decisions](README.md) | Amends [ADR-20260911-4p6](20260911-4p6-a-module-extends-the-ui-through-one-library-and-four-slots.md) and [ADR-20261027-wvj](20261027-wvj-a-module-extends-how-the-core-reads-a-file-and-draws-a-file.md) rule 17

A module could act on a printer and on a file in a storage, and on nothing between the two. A file on a printer had a menu only the core filled, and a file dropped on a printer's page could only be printed or copied. A module that works with a printer's files, such as a print queue, had no place to offer itself where the person already is.

A printer answers one command at a time, and a question about one file takes it seconds. A module that draws a list of a printer's files cannot wait that long for each row.

## rules

1. `device-file-action` is a slot on a printer's page, in the area `device-file-actions`, with the form of `device-action`. It is an item in the menu of each file on the Files tab, in the list and in the tiles. The host never draws one for a folder.
2. Its subject is a printer file reference, `<device id>:<path>`: the device id, a colon and the file's path on the printer, such as `usb/part.bgcode`. A device id holds no colon, so the first one splits the two.
3. `device-upload-action` has the same form and is drawn on that page too, in the area `drop-choices`. It is a choice in the question a file dropped there asks, beside Print now and Only copy to printer. A file picked with it is copied to the printer as Only copy to printer does.
4. Its subject is the printer file reference of rule 2, naming where the file lands. A send that keeps both files lands under another name than it asked for, and the reference names that one.
5. The host runs a `device-upload-action` entry as soon as this server is sending the file, and not when the file has landed. The transfer outlives the page. The file may not be on the printer yet, and the module must hold with that.
6. A name the printer already holds asks the person first. The entry runs once their answer sends the file, and on the printer's own file when they keep it.
7. An entry of either slot may name `file_types`, as ADR-20261027-wvj rule 17 says for a slot handed a file. A `device-upload-action` choice is offered only when every dropped file fits.
8. The module host offers `uploadToPrinter` and `sendToPrinter`. The first sends files from the person's computer, and the second copies files from a storage of this server. Both go through the page of the printer, which asks what it asks for a dropped file, and both run the module's own `device-upload-action` entry on each file. They are for a file a person sends while they are on that page. A module that sends a file with nobody there, such as a queue at the file's turn, calls `deviceapi.Print` with `KeepBoth` and asks nothing.
9. Both functions work only while the printer's page is open, such as from a module's tab on it. A request for a printer whose page is not open is dropped, and never waits for a later visit. A request that names a choice the module did not declare, or one an operator hid, sends nothing, and the page says so in a toast. A core without them leaves them out, so a module checks for each before it offers what needs it.
10. The page takes the requests for its printer oldest first. It sends the stored files of one request one after another, because the server takes one transfer for a printer at a time. A printer that can take no file says why in a toast, because a module's tab shows no drop zone.
11. `deviceapi.FilesParams.Cached` answers a path from what the server last read of it, at once. It never waits for the printer. A path the server holds nothing of is answered with no entry and no rows, and the read that ask starts lets a later ask find it. That read is a background one, which a person's command goes ahead of (ADR-20261112-wfc rule 8). A path whose last read failed is answered the same way, and that ask puts no read on the printer.
12. `connectorapi.File` and `deviceapi.File` carry what the slicer wrote in a print file: `EstimatedSeconds`, `PrinterModel`, `LayerHeightMM` and `Material`. The four sit on the file itself. `Material` is the slicer's own value, one material for each tool joined by a semicolon, such as `PLA;PETG`. A stored file's `storageapi.PrintInfo` names the first tool only. A driver fills them only in the answer about that one file. A file that says none, and a value of a type the driver does not expect, leave the field at zero and cost nothing else of the answer.
13. A send that keeps both files numbers the name before its extension: `part.bgcode` lands as `part[1].bgcode`, then `part[2].bgcode`, and a two-part extension such as `.gcode.gz` stays whole. The server asks the printer about one name after another up to `[99]`, and refuses the send as a taken name when the printer holds every one. `keep_both` together with `replace` is refused. A module asks for the same with `KeepBoth` on `deviceapi.PrintParams`, and `Path` on `deviceapi.Started` names the file that is printed. A module does not number names itself.
14. The module host carries `namespace`, the namespace the module was installed under, and `self`, the module's instance as grants name it, such as `sink:simple-queue`. A module never reads either out of an address or a listing.

## enforcement

`make slots-check` holds the generated table, the reference and the scaffold template for rules 1 and 3, and the every-slot test in `slot-outlets.svelte.test.ts` finds both outlets on their screen. The `printer-detail.svelte.test.ts` tests hold rule 10, and the tests of the waiting requests in `registry.svelte.test.ts` hold the dropped request of rule 9 and the order of rule 10. `TestAModuleCachedListingNeverWaitsForThePrinter` and `TestAModuleCachedListingOfAFailedPathAsksThePrinterNoMore` hold rule 11. `TestKeepBothSendsUnderTheNextFreeName`, `TestNumberedNameKeepsTheExtension`, `TestKeepBothWithEveryNumberTakenIsNameTaken` and `TestKeepBothAndReplaceTogetherAreRefused` hold rule 13, with `TestAModulePrintWithKeepBothAnswersTheFreeName` for a module. `TestAFileInfoForOneFileCarriesItsPrintTime` and `TestAFileInfoForOneFileCarriesWhatTheSlicerWrote` hold rule 12 in the Buddy driver.

The host object test in `loader.svelte.test.ts` holds rule 14.

Rules 5, 6 and 8 have no gate of their own, and neither has the half of rule 9 about a core without the functions. Rule 6 is held in part by the tests of the replace question.

## rejected

| option | why not |
|---|---|
| `print-section` for a file on a printer | a file on a printer has no print dialog. Print there starts it at once, so the section has no place to be drawn |
| run the upload entry once the file has landed | the wait would not outlive the page. A person who closed the tab would have the file and not the action |
| a module sends its own files to the printer | it would copy the question about a taken name and the choice to keep a copy, and the two would drift |
| a cached read that names an unread path as an empty folder | the path may be a file. The module could not tell a read in flight from a folder with nothing in it |
| the first tool only in `Material`, as a stored file has it | a queue would show one material of a file that needs two. A list per tool can come later |
| the slicer's facts in a folder's rows | the printer sends them for one file at a time, so every row would hold its command slot for minutes |
