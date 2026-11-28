# Server slicing

This module turns a 3MF project into G-code on this server, with the command
line of the public PrusaSlicer. A person, a page or another program on the
network asks for a job. The job names a project and where the G-code goes. The
project is in one of the server's storages, or it comes with the request. A
worker runs PrusaSlicer in a separate process and checks that it wrote a file.
For an STL file the job selects a saved PrusaSlicer profile. The module reads
the print time, the filament and the objects out of the G-code, and
writes the file into the storage. A printer then prints it like any other
file there.

The module ships two programs. This directory holds the Go program, which runs
the jobs and serves the routes listed below. `ui/` holds the browser bundle,
which draws the Slicing page. The page opens on a build plate. Its ⋯ menu leads to a list of the PrusaSlicer versions, a list of the jobs, and an API page drawn from `openapi.json`. Each operation there names the token scope it needs in `x-scope` and the codes it refuses with in `x-codes`. Its refusal responses are written from `internal/routes`, and `openapi_test.go` holds all of it to the routes.

## Links

| what | where |
|---|---|
| the rules of this module and its API | [ADR-20261026-ydf](../../docs/decisions/20261026-ydf-the-slicer-extension-slices-with-the-public-prusaslicer.md) |
| why it is named server-slicing | [ADR-20261112-ej7](../../docs/decisions/20261112-ej7-the-slicing-extension-is-named-server-slicing.md) |
| how a module writes a file into a storage | [ADR-20261026-c7m](../../docs/decisions/20261026-c7m-a-module-writes-a-file-into-a-storage.md) |
| how a project is read and rewritten | [ADR-20261026-w6a](../../docs/decisions/20261026-w6a-a-3mf-project-is-read-by-a-core-reader-and-one-dialect-per.md) |
| the PrusaSlicer releases | [github.com/prusa3d/PrusaSlicer/releases](https://github.com/prusa3d/PrusaSlicer/releases) |

## Commands

| command | does |
|---|---|
| `make -C extensions check EXT=server-slicing` | test both halves, pack a release, install it into a demo and slice a project with a stand-in slicer |
| `make test` | build, vet and test the Go half, in this folder |
| `SLICER_INTEGRATION=1 go test -run TestTheRealSlicer -v .` | install PrusaSlicer 2.9.6 and 3.0.0-alpha12 from GitHub and slice small projects with them, on macOS |
| `pnpm run check` in `ui/` | lint, type-check and test the browser half in Chromium |
| `pnpm run storybook` in `ui/` | draw every component of the page in every state |

| variable | required | default | description |
|---|---|---|---|
| `SLICER_INTEGRATION` | no | empty | `1` runs the integration test, which downloads about 280 MB |
| `SLICER_INTEGRATION_CACHE` | no | a temporary folder | where the integration test keeps the download, named by its SHA-256 |

| path | purpose |
|---|---|
| `main.go` | the handshake with the server, the settings, the wiring |
| `internal/catalog/` | the versions offered, the manifest format, and which GitHub asset fits this host |
| `internal/feed/` | the GitHub Releases API reader and the copy of its answer on disk |
| `internal/install/` | the download, the digest check, the unpacking and the installed versions |
| `internal/runner/` | one run of the slicer, with its bounds |
| `internal/gcodemeta/` | what a G-code file says about its print |
| `internal/project/` | the project prepared for one selection, with the reader of the threemf module |
| `internal/fixture/` | the small projects the tests slice |
| `internal/jobs/` | the queue, the workers and the record of each job |
| `internal/routes/` | the HTTP routes and their refusal codes |
| `testdata/fake-slicer.sh` | the stand-in slicer the tests run |

## Configuration

| key | type | default | summary |
|---|---|---|---|
| `max_jobs` | int | `1` | How many projects slice at the same time. |
| `max_queued` | int | `16` | How many jobs may wait. One more is refused until a job finishes. |
| `timeout_minutes` | int | `30` | How long one slicer run may take before it is stopped. |
| `threads` | int | `0` | The threads one slicer run may use. 0 lets the slicer choose. |
| `max_project_mib` | int | `512` | The largest project a job takes, in MiB. |
| `keep_jobs` | int | `200` | How many finished jobs the module remembers. |
| `github_releases` | bool | `true` | Offer the PrusaSlicer releases that GitHub publishes, read from its API every six hours. |
| `github_token` | secret | `` | A GitHub token for reading the releases, which raises the limit of 60 requests an hour. |
| `manifest_url` | string | `` | An https address or an absolute path of a manifest of builds you host. Empty offers none. |
| `local_binaries` | list | `[]` | Absolute paths of PrusaSlicer executables already on this machine. |
| `stl_profiles` | object | `{}` | Saved STL profiles. Each key names a profile and each value is the text of a PrusaSlicer config export. |

### Saved profiles for STL files

Select the printer, nozzle, filament and print settings in PrusaSlicer 2.x. Use
**File → Export → Export Config** to save them as one INI file. In **Settings →
Extensions → Server slicing**, enter its text as a value in the
`stl_profiles` object. For example, this command makes the JSON value for
one profile.

```sh
jq -Rs '{ "core-one-petg": . }' core-one-petg.ini
```

In **Storage**, choose **Slice** for an STL and select a profile. A REST
client sends the same name as `profile` in the job body or the upload
query. The default output name includes the chosen profile.

A job may change some settings of the profile for that job alone. It sends
them as `settings` in the body of `POST /jobs`. Each one it leaves out keeps
the profile's value.

| field | what it changes |
|---|---|
| `layer_height_mm` | the layer height, from 0.05 to 0.6 mm |
| `infill_percent` | the infill density, from 0 to 100 |
| `walls` | the perimeters of each layer, from 1 to 20 |
| `supports` | `off`, `build_plate` for supports that stand on the bed only, or `everywhere` |
| `brim_width_mm` | the brim, from 0 to 50 mm. 0 prints no brim |

`GET /slicers` answers what each profile prints with before a change, under
`prepare_settings`, with the material of its first extruder.

Profiles take a complete config for a filament printer. Clear any
post-processing scripts before exporting. Keep a PrusaSlicer 2.x binary
installed to run STL jobs with these profiles.

### Prepare a plate and print it

**Slicing** opens on the build plate of a printer. **Open in slicer** on an
STL in Storage opens it there too. The row of tools above the plate has, in
order:

| tool | does |
|---|---|
| **Add** | opens STL and 3MF files. Files dropped on the plate are added too |
| **Move** | a part follows the pointer. Shift-click adds a part to the selection |
| **Lay flat** | the flat face under the pointer lights up. A click turns the part so that face lies on the bed |
| **Arrange** | arranges every part, or only the selected ones around the others, or adds a copy of the selection in free space |
| **Undo** and the view | takes the last change back, and looks at the plate from an angle or from above |
| printer | the printers you may see, with state, model, material and bed size. The plate takes the chosen printer's bed |
| print settings | layer height, material, supports, infill, walls, brim and the storage for the files, kept for each printer |
| **Print** | slices the plate and starts it on the printer |
| ⋯ | the PrusaSlicer versions, the jobs and the API |

The keys R, Delete and Ctrl+Z (⌘Z on a Mac) turn the selection by 90°,
remove it, and take the last change back. A part that is outside the print
volume turns red, and the plate does not print until it fits.

A new STL goes to free space on the bed. Each object of a 3MF is a part of
its own, in the position stored in the file. A part below the bed is lifted
onto it, and a part that stands on another keeps its height. Arrange and
**Add a copy** read each part as the rectangle around it, so an L-shaped part
takes its whole rectangle.

The material is a saved profile, and the plate uses the first profile whose
bed has the printer's size. The other settings change that profile for this
plate, sent as `settings` with the job. Profiles take a rectangular bed. When
the profile's bed is smaller than the printer's, the plate is the smaller
one.

**Print** saves the plate as one STL with a unique name, slices it, and sends
the G-code to an idle printer, which starts it. Once the plate is sliced, the
button shows the print time from the G-code. A change to the plate or to the
settings slices it again. A printer that is printing, or that still holds a
finished print, shows **Add to queue** instead. That button is off for now:
the queue belongs to the Print queue module, and this module has no approved
call to it. The menu beside the button slices without printing, downloads
the G-code, or saves the placed STL. A saved STL keeps its position when it
is opened again.

Slicing uses the selected profile. 3MF project settings, colours, material
assignments, support and seam painting, and plate definitions are dropped.
The plate skips modifiers, negative volumes, support tools and other
non-model geometry. Apply cuts made with negative volumes before exporting.
Export one plate at a time if the project has several plates. If the page
cannot read the part roles, including those in PrusaSlicer 3 projects,
export the solid model as STL or geometry-only 3MF.

A plate holds files up to 20 MiB each and 300,000 triangles in all, copies
included. A 3MF archive can expand to 64 MiB.

A slicer uses a lot of memory, so the module asks the host for 2560 MiB
(`needs.memory_mib` in `module.yaml`). The operator overrides that figure per instance as `memory_mib` on the resource spec (ADR-20261027-5m4).
The figure is one PrusaSlicer 3.x run of a 2 M triangle project plus the
module. Measured on macOS, PrusaSlicer 3.0.0-alpha12 peaks at 2199 MiB on that
project (1858 MiB with `--threads 1`) and at about 740 MiB on a trivial one.
Version 2.9.6 peaks at 1278 to 1390 MiB.

The host tells the module its budget. The module keeps 64 MiB for itself and
divides the rest by `max_jobs`, and that is what one run may hold. A module
that you start yourself has no budget and no bound. On a Raspberry Pi with 4 GB, set
`max_jobs` to 1. The settings answer shows the result as `limits.memory_mib`,
which is read only. The `memory_mib` setting is gone. An install or an update drops it from the module's settings by itself. A server-slicing module declared in `config.yaml` keeps the key and fails to start with `RESOURCE_PLUGIN_SETTINGS` until you delete it there. A higher `max_jobs` needs a higher budget: at the default 2560 MiB, two jobs get 1248 MiB each, less than a large project peaks at.

## Where the slicers come from

An administrator installs a version on the Slicing page, or with
`POST /slicers/install`. The module downloads it and checks its SHA-256. It
unpacks it into its data directory beside the other versions, and runs it once
with `--help` to check that it is the version it was offered as. A download is
kept under its digest, so installing the same build again does not download
it again.

The offered versions come from three places, in this order:

1. a manifest you host, named in `manifest_url`. A build in it replaces the
   GitHub build of the same version on the same platform
2. the GitHub releases of `prusa3d/PrusaSlicer`, when `github_releases` is on
3. the executables named in `local_binaries`, which are installed already

### The GitHub releases

The module reads the release list from the GitHub Releases API when it
starts and every six hours. It sends the ETag of the copy it keeps on disk,
so an unchanged list costs nothing, and it keeps offering that copy while
GitHub is out of reach. Without a token GitHub answers 60 requests an hour
for each address. A server behind a shared address can set `github_token`.
An administrator can ask for a new reading with `POST /slicers/refresh`, at
most once a minute.

The module offers the releases from 2.7.0 up to the next major version, 4.0.0.
2.7.0 is the first release that writes binary G-code and the printer metadata
the module reads the print details from. A version 4 may change the command
line or the project format. Drafts and releases outside the range are not
shown. A release in the range without a build for this host is shown greyed
out, with the reason.

| host | the asset taken | releases with one |
|---|---|---|
| macOS, Apple and Intel | the `.dmg`, which is universal | every release |
| Windows, x86-64 | the `.zip` | every release |
| Linux, x86-64 | the GTK3 `.tar.bz2`, or else the GTK3 AppImage for newer distributions, run with `--appimage-extract-and-run` | up to 2.8.1 |
| Linux, arm64 | the GTK3 `.tar.bz2` | up to 2.7.4 |

PrusaSlicer ships Linux from 2.9 as a Flatpak, which this module does not
install. So a newer version on Linux comes from a manifest you host, or from
a binary already on the machine named in `local_binaries`. The Windows builds
are offered and have not been run by this module's tests.

GitHub publishes the SHA-256 of every asset uploaded since mid-2025, and the
download is checked against it. An older release has none. Its card says "No
checksum", and an install needs an administrator to confirm it, sent as
`"unverified": true`. Without it the install is refused with
`server-slicing/UNVERIFIED`.

A final release newer than the default version is announced on the Slicing
page. The module never installs it by itself.

### The manifest

A manifest is a JSON file. Each release names its version and channel. Each
build names the operating system and the processor it runs on and where to
download it. It also names the SHA-256 of the download, how it is packed, and
where the executable is inside it:

```json
{
  "schema": 1,
  "releases": [
    {
      "version": "2.9.6",
      "channel": "stable",
      "builds": [
        {
          "os": "linux",
          "arch": "arm64",
          "url": "https://builds.example.com/prusaslicer-2.9.6-cli-linux-arm64.tar.gz",
          "sha256": "<64 lowercase hex characters>",
          "size": 41234567,
          "archive": "tar.gz",
          "binary": "prusaslicer-2.9.6/bin/prusa-slicer"
        }
      ]
    }
  ]
}
```

| field | what it holds |
|---|---|
| `version` | `2.9.6` or `3.0.0-alpha12` |
| `channel` | `stable` for a final release, `prerelease` otherwise |
| `os`, `arch` | `linux`, `darwin` or `windows`, and `amd64` or `arm64` |
| `url` | an https address. A redirect is followed only to another https address |
| `sha256` | the digest of the download. A download that does not match is deleted |
| `size` | the size in bytes, optional. A download of another size is refused |
| `archive` | `tar.gz`, `tar.bz2`, `zip`, `dmg` on macOS only, or `file` for the executable itself |
| `binary` | the executable's path inside the archive. A `file` build names none |
| `args` | optional `--flags` put before every other argument, such as `--appimage-extract-and-run` for an AppImage |

A manifest with one wrong entry is refused whole.

### A command-line build for Linux on arm64

This module does not build PrusaSlicer. A build without the graphical
interface is the smallest one to host for a Raspberry Pi: PrusaSlicer's own
CMake option `-DSLIC3R_GUI=OFF` builds it without wxWidgets and OpenGL. To
offer it:

1. Build PrusaSlicer 2.9.6 from the public source with `-DSLIC3R_GUI=OFF` for
   linux/arm64, and install it with its `resources` folder beside the binary.
2. Pack the installed folder as a `.tar.gz` and publish it on an https
   address you control.
3. Write a manifest with one build: `os` linux, `arch` arm64, `archive`
   tar.gz, `binary` the path of `prusa-slicer` inside the archive, and the
   SHA-256 of the archive.
4. Set `manifest_url` to the manifest's address, or to its absolute path on
   the server, and restart the module. The version appears under the offered
   slicers on the Slicing page.

## The routes

Every path is relative to `/api/v1/modules/<namespace>`. The server checks who
is calling before the module sees the request. A read takes `view_resource`
on the module, which a token with the `read` scope reaches. Anything else
takes `operate_resource`, which a token with the `operate` scope reaches.

| route | scope | does |
|---|---|---|
| `GET /status` | read | whether the module can slice, the job counts and the bounds |
| `GET /slicers` | read | the installed and the offered slicers, the installs that run, the default |
| `POST /slicers/install` | administer | install one offered version |
| `POST /slicers/refresh` | administer | read the GitHub releases again, at most once a minute |
| `DELETE /slicers/{version}` | administer | remove one installed version |
| `PUT /slicers/default` | administer | choose the version a job gets |
| `GET /jobs` | read | the caller's jobs, newest first, or every job for an administrator |
| `POST /jobs` | operate | slice a project in a storage |
| `POST /jobs/upload` | operate | slice a project sent as the body, at most 1 MiB |
| `GET /jobs/{id}` | read | one job |
| `POST /jobs/{id}/cancel` | operate | stop one job |

The four slicer routes download and run a program on the server, so the
module also asks the server whether the caller may configure modules,
`configure_resource` on the module kind. An administrator's session has it,
and a token reaches it only with the `administer` scope.

A job is taken while the request is open. The module reads the project as
the caller, so a person who may not read it learns nothing about it. It also
asks the server whether the caller may write into the target storage. A worker
slices the job later, as the module. Before it reads the project and before it
writes the G-code, it asks the server again whether the caller may still read
and write there. A person who lost the permission in between gets a failed
job and no file.

```sh
curl -s -X POST "$SERVER/api/v1/modules/com.github.aux.prusa3d.server-slicing/jobs" \
  -H "authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"source":{"storage":"local","path":"models/bracket.3mf"},"format":"bgcode"}'
```

The answer is the job, queued. `GET /jobs/{id}` then shows its `progress`.
Once it is `done`, its `result` holds the path of the G-code and its SHA-256.
It also holds the estimated print time, the filament, the pictures, and the
objects a printer can cancel one by one. Each object carries the number the G-code labels it
with, `M486 S<id>`, and its outline on the bed.

### What a job writes

The G-code goes to the folder the request names, or beside the project. The
file name starts with eight characters of a hash of the storage and the path
asked for. A dash, the name asked for and the extension of what the slicer
wrote follow: `.bgcode` or `.gcode`. Asked for `sliced/bracket`, a job writes a file such as
`sliced/3f9a01c2-bracket.bgcode`, and `result.path` names it. The same request
always gives the same name. A file of that name stops the job with
`server-slicing/OUTPUT_EXISTS`, unless the request sets `overwrite`. The failed job
then names that file under `error.existing`, with its `storage` and its
`path`. So a client that wants the output that is there already reads its name
from the job, and never works the name out itself:

```json
{
  "state": "failed",
  "error": {
    "code": "server-slicing/OUTPUT_EXISTS",
    "message": "sliced/3f9a01c2-bracket.bgcode is in the storage local already. Ask to overwrite it, or name another file",
    "existing": { "storage": "local", "path": "sliced/3f9a01c2-bracket.bgcode" }
  }
}
```

The hash comes first because of the printer. A printer that stores files on
FAT also gives each file an 8.3 short name, made from the first six characters
of the long name. Outputs of one project begin alike, so they would share one
short name, and some printers then lose the second file of a transfer. The
hash makes the first six characters differ for every path. The name is cut so
that it is at most 167 bytes with its extension, the longest name the printer
firmware keeps since 6.4.0.

PrusaSlicer 2.9.6 writes binary G-code when it is asked with `"format":
"bgcode"`. PrusaSlicer 3.0.0-alpha12 writes text G-code whatever it is asked,
and the job then carries a warning.

### Why a job fails

| code | what happened |
|---|---|
| `server-slicing/SOURCE_NOT_FOUND` | the project is no longer in the storage |
| `server-slicing/SOURCE_DENIED` | the caller, or the module, may no longer read the storage |
| `server-slicing/SOURCE_TOO_LARGE` | the project is over `max_project_mib` |
| `server-slicing/NOT_A_PROJECT` | the file is not a 3MF project, or it has a post-processing script or an output folder. The message says which |
| `server-slicing/NOT_PRUSASLICER` | another program saved the project, such as OrcaSlicer. The message names it |
| `server-slicing/NO_SLICER` | no installed slicer can slice it. A project saved by PrusaSlicer 3 needs PrusaSlicer 3 |
| `server-slicing/SLICER_TOO_OLD` | the version the job named is older than the one that saved the project |
| `server-slicing/SELECTION_INVALID` | the project has no such bed or setup, or a chosen item does not stand whole on the bed |
| `server-slicing/NOTHING_TO_PRINT` | every object is off the bed, or none is printable |
| `server-slicing/NO_OUTPUT` | the slicer ended and wrote no file, or an empty one |
| `server-slicing/SLICER_FAILED` | the slicer stopped with an error |
| `server-slicing/TIMEOUT` | the slicer ran longer than `timeout_minutes` |
| `server-slicing/MEMORY` | the slicer held more than the memory one run may hold |
| `server-slicing/OUTPUT_EXISTS` | the G-code file is there already. `error.existing` names it |
| `server-slicing/OUTPUT_DENIED` | the caller, or the module, may no longer write into the storage |
| `server-slicing/OUTPUT_FAILED` | the storage did not take the file |
| `server-slicing/INTERRUPTED` | the module stopped while the job waited or ran |
| `server-slicing/INTERNAL` | the module failed. The server's log has the reason |

A failed job carries the last lines the slicer printed under `error.log`.

## How the slicer runs

PrusaSlicer exits with status 0 even when it writes nothing, for a bed with
nothing on it. So the module never trusts the status. It checks that the file
exists and is not empty, and reads its first bytes to tell binary from text.

- the slicer is a separate process in a process group of its own. A timeout,
  a cancel or a memory bound stops the whole group
- its arguments are a list, and no shell ever reads them
- its environment holds `PATH`, a locale of `C`, and a home in the job's own
  folder. No other variable of the server's environment reaches it
- `--datadir` points it at an empty folder, so it reads no profile and slices
  with the settings saved in the project
- on Linux and macOS a watch reads the memory of its process group twice a
  second and stops it over the memory one run may hold. Windows has no such watch
- what it prints is read line by line, each line cut at 2 KiB, and the last
  60 lines are kept
- the job's folder is deleted when the job ends, whatever the outcome

PrusaSlicer renders no picture on the command line. It copies the picture
saved in the project into the G-code, so the pictures of a result are those of
the project.

## Slicing one bed or some objects

The PrusaSlicer command line slices bed 0 alone, and has no option to leave an
object out. Slicing another bed, or only some objects to print again after a
failed print, needs the project rewritten first. The chosen bed becomes the
one the command line slices, and every object that is not chosen is marked as
not printable. The module reads and rewrites the project with the reader of the threemf module.
It does this for every job, so a PrusaSlicer 3 project slices the bed the job
names and not the one it was saved with.

A job names a `selection`: the printer `setup`, the `bed`, and the build
`items` to print. A build item is one placed copy of an object, counted from 0
in the order of the project's build list. No items means every printable item
on the bed.

```json
{"source": {"storage": "local", "path": "plates/brackets.3mf"}, "selection": {"bed": 1, "items": [4, 7]}}
```

A selection may name `positions` instead of `items`, to print copies. Each
position is a build item and the point on the bed where the centre of its box
stands, as `threemf.Arrange` answers them. The first position of an item
moves the item there, and each further one adds a copy of it. So twenty clips
need one clip in the project. The project's own settings are kept, and the
module refuses a copy that stands off the bed or on another copy with
`server-slicing/SELECTION_INVALID`. The PrusaSlicer command line does not arrange a
3MF project itself.

```json
{"source": {"storage": "local", "path": "plates/clip.3mf"},
 "selection": {"bed": 0, "positions": [{"item": 0, "x": 60, "y": 60}, {"item": 0, "x": 90, "y": 60}]}}
```

The G-code labels each object for the printer, `M486 S<id>`. A result names
the build item and the instance each label came from, matched by where the
object stands on the bed. So a page can cancel one object on the printer, or
print some of them again from the project.

## Installing it

The module asks for a data directory and for three permissions it uses as
itself: `read_storage`, `write_storage` and `configure_resource`.

```console
prusa-connect module add github.com/prusa3d/server-slicing@0.2.0 -from <release> -approve
```

`-approve` grants the data directory and the permissions the manifest lists. That is the viewer role, which reaches `read_storage`, and `write_storage` on the storage kind, so the module writes G-code as itself. The Extensions page asks for the same approval. The operator writes no grant of their own.

The module uses `configure_resource` only to ask whether a person may change
the installed slicers. It needs no grant for it.
