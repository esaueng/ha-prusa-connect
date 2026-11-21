### Added
- A send refused with `core.printer/FILE_NAME_TAKEN` carries `read_only` true when the printer will not let its file be replaced now.

### Fixed
- A file on the printer that cannot be replaced is now said so before the person picks Replace. The printer lists such a file read-only, as a Buddy printer does while the file is open on its screen, and the question about its name then offers no Replace.
- A file sent to a printer now says it is checking the printer before its transfer starts. The server asks the printer about the name first, which can take a few seconds.
- A file that replaces one on the printer now says it is deleting the printer's file before its transfer starts.
