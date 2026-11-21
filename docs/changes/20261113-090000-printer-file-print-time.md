### Added

- [sdk] The answer about one print file on a printer carries the print time the slicer estimated, in whole seconds, as `estimated_seconds`. It is in `GET /api/v1/devices/{device_id}/files` for a path that names a file, in `deviceapi.File` for a module and in `connectorapi.File` for a driver. Beside it come `printer_model`, `layer_height_mm` and `material`, as the slicer wrote them. A Prusa printer with Buddy firmware reports them.
