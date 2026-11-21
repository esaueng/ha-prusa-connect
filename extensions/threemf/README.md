# 3MF projects

This module reads 3MF project files. A 3MF file is a zip archive with the models, how they stand on the beds and the slicer's settings. PrusaSlicer, OrcaSlicer and BambuStudio save their projects in it.

With the module installed, the page of a `.3mf` file shows the project. It shows the printers, the beds with the slicer's picture of each, a plan of each bed, the objects with their copies, and the settings. A listing shows the project's picture, and the file's page lists its title, its printer and its objects.

Without the module, the server still shows the picture a project carries in a listing. It reads nothing else of the file.

The module ships three things:

| part | where | what it is |
|---|---|---|
| the reader | `threemf/` | a Go package other modules import as a normal Go dependency |
| the process | `main.go`, `internal/` | answers the server's question about a `.3mf` file, and serves the project routes |
| the bundle | `ui/` | the project view on the file's page, also handed to other modules as `project-view` |

## The reader as a Go package

Another module that needs to read or change a project imports the package:

```go
import "github.com/prusa3d/threemf/threemf"
```

and requires this module in its own `go.mod`. Until the module is tagged, a module in this repository uses a `replace` to `../threemf`, as the modules here do for the sdk.

| function | what it does |
|---|---|
| `Read` | reads a project into its printer setups, beds, objects and instances |
| `Thumbnails` | reads only the pictures |
| `ReadMesh` | reads the model parts of one object as triangles |
| `Rewrite` | writes a copy of a PrusaSlicer project for printing a selection of copies, or the copies `Arrange` placed |
| `CheckSettings` | refuses a project whose settings make PrusaSlicer run a program after slicing, or write the G-code into another folder |
| `Arrange` | places copies of objects onto as few beds of one setup as hold them |
| `ParseObjectsInfo`, `Project.MatchLabels` | tie the objects a sliced G-code file labels back to the instances of the project |

Each read is bounded by `Limits`, and the reader never trusts the archive. The rules are in [ADR-20261026-w6a](../../docs/decisions/20261026-w6a-a-3mf-project-is-read-by-a-core-reader-and-one-dialect-per.md) for reading and [ADR-20261027-2rq](../../docs/decisions/20261027-2rq-a-3mf-selection-places-copies-of-build-items-on-one-bed.md) for arranging. A module that links the package reads files with its own grants. It does not call this module, so it does not need to name it in its manifest.

## What the server asks

`module.yaml` names `.3mf` under `file_analysis`. When the server needs to describe a `.3mf` file, it calls `file.analyze` on this module with a read handle for that one file ([ADR-20261027-wvj](../../docs/decisions/20261027-wvj-a-module-extends-how-the-core-reads-a-file-and-draws-a-file.md)). The module answers:

| member | what it holds |
|---|---|
| `title` | the title in the project's metadata |
| `thumbnails` | the pictures of the whole project first, then those of the beds, at most four and 512 KiB together |
| `print` | the first setup's printer, nozzle, layer height, profiles, filaments and bed size. Nothing about time |
| `facts` | the program that saved it, the printer, and counts of setups, beds, objects, copies, extruders and issues |
| `objects` | each object by its index and name, at most 256 |

The counts also say how many copies are left out of the print and how many are not whole on a bed, when there are any.

When the module is not running, fails, or takes longer than the server waits, the server reads the picture itself and a person sees no error.

## The routes

Every path is relative to the address the server gives the module, `/api/v1/modules/com.github.aux.prusa3d.threemf`. `openapi.json` describes them, and `client/` is the typed client generated from it.

| route | what it answers |
|---|---|
| `GET /project?storage=&path=` | what one project holds. `raw_settings=true` adds every setting the slicer stored |
| `GET /project/thumbnail?storage=&path=&index=` | one picture by its index in `thumbnails`, with an `ETag` |
| `GET /project/mesh?storage=&path=&object=` | the model parts of one object as at most 20000 triangles |

The rules of the document are in [ADR-20261027-4tq](../../docs/decisions/20261027-4tq-a-3mf-project-in-a-storage-is-read-whole-and-answered-as.md).

The server takes `view_resource` on this module before it calls a route. The module then reads the file as the person who asked, so the storage takes `read_storage` for that person. The module asks for no permission of its own.

A project is read whole, so a read is costly. The module keeps the answer for one version of one file in memory, at most 8 projects and 64 MiB, and 16 meshes and 32 MiB. One read is shared by every caller asking for the same file. At most two files are read at once, and one read runs for up to two minutes. The server waits ten seconds for a route, so the first request for a very large project can time out while the read goes on. The next request then gets the kept answer.

A module answers at most 1 MiB. A project of thousands of copies can be larger, and it is refused with `threemf/PROJECT_TOO_LARGE` and `field: answer`.

| code | status | when |
|---|---|---|
| `threemf/INVALID_QUERY` | 400 | a parameter is missing or out of range |
| `threemf/NO_PRINCIPAL` | 403 | the server named no person |
| `threemf/DENIED` | 403 | the person may not read the file |
| `threemf/NO_FILE` | 404 | the storage holds no such file |
| `threemf/NO_OBJECT` | 404 | the project has no object at that index |
| `threemf/NO_THUMBNAIL` | 404 | the project has no picture at that index |
| `threemf/NOT_A_PROJECT` | 422 | the name does not end in `.3mf`, or the file is not a 3MF the reader can read |
| `threemf/PROJECT_TOO_LARGE` | 422 | the file is past the reader's limits, named in `field` |
| `threemf/READ_FAILED` | 503 | the storage did not answer, or the read ran out of time. Ask again |

## The project view for other modules

The bundle registers the same view twice: as the `file_view` of a `.3mf` file, and as the component `project-view` under `ui.exports`. A module that draws a project on its own page names this module under `require` or `suggest`, and imports the component:

```yaml
suggest:
  com.github.aux.prusa3d.threemf: "Draws the project of a .3mf file"
ui:
  imports:
    - module: com.github.aux.prusa3d.threemf
      name: project-view
```

```ts
const ProjectView = await host.components.load('com.github.aux.prusa3d.threemf', 'project-view');
```

The props are `storage` and `path` and three optional ones. `selectionActs` is a snippet handed the ticked copies, a summary and `clear`. `onselect` is a callback that gets the ticked copies, one entry per bed. `picture` hides the project's picture when false. `ui/src/project-file/types.ts` declares them as `ProjectFileProps`. The selection has the shape of `threemf.Selection`, so a module that prints or slices the ticked copies hands it to `Rewrite` as it is.

## The gate

```sh
make -C extensions check EXT=threemf
```

It builds and tests the Go half in its own folder, holds the typed client to the fragment, and tests the bundle in Chromium. It then packs a release and installs it into a demo server with `module add`. It uploads a project and reads it through the routes and through the server's stat route.

`THREEMF_CORPUS` names folders of `.3mf` files, joined by the list separator. `go test -run Corpus .` then reads each of them, and copies of each cut short or with bytes changed, through the routes and the answer to the server.
