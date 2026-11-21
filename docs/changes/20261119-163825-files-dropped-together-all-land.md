### Added
- Dropping several files on a printer page can now print one of them and copy the rest. The file picked to print goes to the printer first.

### Fixed
- Several files dropped on a printer page at once now all reach the printer, one after another. Before, only the first one did and the rest vanished without a message; a file waiting for its turn now reads "Waiting for the printer" and can be cancelled.
- A file sent to a printer from its page now waits for a transfer another tab or person started to that printer, instead of failing as busy.
- A replace the printer refuses, for example because the file is still open on its screen, now says why instead of failing without a message.
