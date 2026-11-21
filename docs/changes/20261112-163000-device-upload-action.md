### Added

- [sdk] A module can add an item to the menu of each file on a printer's Files tab with the `device_file_action` contribution. It gets the reference `<device id>:<path>` and may name the file types it offers the item for.
- [sdk] A module can offer a choice for a file dropped on a printer's page with the `device_upload_action` contribution. The file is copied to the printer, and the module's action gets the reference `<device id>:<path>` as soon as the copy is on its way. An entry may name the file types it is offered for.
