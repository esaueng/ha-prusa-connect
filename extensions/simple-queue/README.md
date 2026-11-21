# Print queue

This module keeps a queue of print files for each printer. A person puts
files on a printer's queue and puts them in the order they should print. A
file comes from four places:

- **Add files** on the printer's Queue tab. It lists the files on the printer
  and in the server's storages, and adds every file a person selects.
- **Add to queue** in the menu of a file on the printer's Files tab.
- The **Add to queue** section of the print dialog of a file in storage.
- **Add to queue** in the question a file dropped on a printer's page asks.
  The file is copied to the printer and added as soon as the copy starts. It
  prints from the printer like any file there, once the copy has landed.

A file in storage picked in **Add files** is copied to the printer at once
when the printer is connected. The copy goes through the printer's page, so a
name the printer already holds is asked about while the person is there. It waits in
the queue from the moment the copy starts. A file in storage added from the
print dialog, over the routes, or for a printer that is away is sent when
its turn comes. A file already on the printer is printed where it is. When the printer is free
and its bed is clear, the next file is sent to the printer and starts.

There is no planning across printers. Each printer has its own queue, and a
file waits in the queue it was put in.

The module ships two programs. This directory holds the Go program, which
keeps the queues, follows the prints and serves the routes listed below.
`ui/` holds the browser bundle. It draws a Queue tab on each printer's page
and one line on each printer's card. It adds Add to queue in three places.
One is a section of the print dialog, one the menu of each print file on a
printer's Files tab. The third is the question a file dropped on a
printer's page asks. Its Print queue page lists the files of every queue in
time, on one axis with a lane for each printer, or as a row of cards for
each printer. Each person's choice is kept in the module's store. It reads them all with one
`GET /queues` request, so twenty printers cost no more than one. A
printer's name opens its Queue tab. The page links to the core's API
reference, which draws `openapi.json`. Each operation there names the token
scope it needs in `x-scope` and the codes it refuses with in `x-codes`. Its
refusal responses
are written from `internal/routes`, and `openapi_test.go` holds all of it to
the routes.

## When a print starts

A printer that finished a print still has the print on its bed. The queue
never starts the next file on it by itself. A file starts in one of three
ways:

- A person presses the start button on the Queue tab: **Set ready and
  print**, or **Print** on a printer with no ready state. The person says the
  bed is clear by pressing it, and the file is sent as that person.
- A person adds a file to the queue of a printer that is ready and prints
  nothing from the queue. The file starts at once, as that person.
- The printer is ready, because a person said its bed is clear on the
  printer's screen, or with **Set ready** on the Queue tab while
  nothing waited. The queue sends the next file as itself. This needs a
  grant, described below.

Only a printer that takes the command marking its bed clear can be ready.
That is a Prusa printer with Buddy firmware. On any other printer the queue
never starts a file by itself: each file starts when a person presses
**Print**.

Before the queue sends a file as itself, it asks the server whether the
person who added the file may still print on the printer and read the file.
If not, the file waits with a note that says why.

Starting files by itself is reliable on a server with local accounts where
the install approved `check_absent_people`. There the server answers about
every person at any time. On a server with an OpenID Connect login or in open
mode the server answers from the person's roles on this server. It answers
only about a person who used the queue since the server or this module last
started. The files of anybody
else wait for a person to press **Print**, so there the queue does not
reliably start files by itself. A person removed only at the login provider
keeps their roles here, so take their role on the printer away too.

A printer marked ready belongs to the queue while the queue may start files by
itself. Marking the bed clear means the next print is the queue's. A person
who starts Print now on another file at that moment loses to it. Their file
lands on the printer, and the printer refuses its start, because it already
prints the queue's file. When the person's start reaches the printer first,
the queue's start is the one refused. Its file goes back to the head of the
queue with the note that the send failed.

A printer that already holds another file of the same name is no failure.
The queue asks the server to keep both: the file is sent under the first
free numbered name, such as `bracket[1].bgcode`, and the printer's file stays as it is. A printer that is busy taking another file is
no failure either: the file waits and the queue tries again. Any other send that failed is not tried again
by itself, and neither is a start the printer refused. The file goes back to the head of the queue with the reason, and a person presses the start button when
the problem is solved.

The queue follows each print to its end. It reads the server's events about
prints, transfers and a printer's state, so a printer marked ready takes its
next file at once. Once a minute it also compares each queue with what the
printer reports, so a print that ended while the module was stopped is still
recorded. The ended prints stay in the queue's history.

## Commands

| command | does |
|---|---|
| `make -C extensions check EXT=simple-queue` | test both halves, pack a release, install it into a demo and print a queue on a simulated printer |
| `make test` | build, vet and test the Go half, in this folder |
| `pnpm run check` in `ui/` | lint, type-check and test the browser half in Chromium |
| `pnpm run storybook` in `ui/` | draw every component in every state |

| path | purpose |
|---|---|
| `main.go` | the handshake with the server, the settings, the wiring |
| `internal/queue/` | the queue of one printer, its rules and the store that keeps it in the database |
| `migrations/` | the schema of the database, for SQLite and for Postgres |
| `internal/dbtest/` | a fresh database for one test, on each engine |
| `internal/runner/` | sending the next file, and following each print to its end |
| `internal/routes/` | the HTTP routes and their refusal codes |
| `internal/settings/` | the settings, their defaults and their ranges |
| `internal/hostfake/` | the host the tests run against |
| `client/` | the TypeScript client generated from `openapi.json` |
| `ui/` | the browser bundle |

## Configuration

| key | type | default | summary |
|---|---|---|---|
| `max_waiting` | int | `30` | How many files may wait in one printer's queue, from 1 to 60. |
| `keep_history` | int | `10` | How many ended prints each queue keeps, from 0 to 30. |
| `send_timeout` | string | `2h15m` | How long a file may be on its way to the printer before the queue gives it back, from 5m to 24h. |

The queues are kept in a database of the module's own, one row for each
file. The install grants it: a SQLite file in the module's data directory, or
a Postgres database the operator names with `-database`. The schema is in
`migrations/`, and a new version of the module brings its own changes to it,
which are applied when it starts. `make test-engines`-style runs set
`AUTHZ_TEST_POSTGRES_DSN`, and the store's tests then run on Postgres as well
as SQLite.

A file on its way to a printer counts
as sent until the printer opens it. A large file over a slow link takes a
while, so `send_timeout` is long.

## The routes

Every path is relative to `/api/v1/modules/<namespace>/`. A GET takes a token
with the read scope, and the rest a token with the operate scope. Only a
person's token may add a file: a service account is refused with
`NOT_A_PERSON`.

| route | does |
|---|---|
| `GET /queues/summary` | every printer the caller may see, with its queue in short |
| `GET /queues` | every queue the caller may see, whole, in one answer |
| `GET /printers/{device}` | one printer's queue and the printer's state |
| `GET /printers/{device}/history` | one printer's ended prints, newest first |
| `POST /printers/{device}/entries` | queue a file in storage, `{"storage":"local","path":"a.bgcode"}` |
| `POST /printers/{device}/entries` | queue a file on the printer, `{"path":"usb/a.bgcode"}` |
| `DELETE /printers/{device}/entries/{entry}` | take a waiting file off the queue, or an ended print out of the history |
| `POST /printers/{device}/entries/{entry}/move` | move a waiting file, `{"position":0}` |
| `POST /printers/{device}/start` | print the next file now |
| `POST /printers/{device}/ready` | mark the printer as ready, `{"ready":true}`, or not |
| `DELETE /printers/{device}/history` | forget the ended prints |

A person sees the queue of a printer they may see, and changes it when they
may print on the printer.

A script sends a token from Settings, API credentials as a bearer token. To
queue a file in storage on a printer:

```console
curl -X POST "http://<server>:8080/api/v1/modules/<namespace>/printers/$PRINTER/entries" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"storage":"local","path":"parts/bracket.bgcode"}'
```

The core's API reference, `/docs?module=<namespace>`, lists every route with
its fields and lets a person try them. Print queue links to it from its page.

## Installing it

```console
prusa-connect module add github.com/prusa3d/simple-queue@0.1.0 -from <release> -approve
```

The module asks for three permissions it uses as itself:

- `observe`, to watch the printers it has a queue for.
- `read_storage`, to read the files it sends.
- `operate`, to start the next file on a ready printer.
 It also asks to
check people who are away, to ask about the owner of a file when the queue
starts it.

`-approve` grants the viewer role on the organization, which reaches
`observe` and `read_storage`, but not `operate`. So after the install the
queue starts a file only when a person presses the start button or adds a
file to a ready printer. A person who may share the
printer can let the queue start the next file by itself. The switch for it
is on the printer's Queue tab: **Let the queue start the next file by
itself**. It covers every start no person presses Print next for. One comes after the
printer is marked ready, on its own screen or with Set ready on the Queue
tab. Another comes when a dropped file lands. That
writes the grant `operator` on that printer for the module's instance, and
turning the switch off withdraws it.

The same grant from the command line, for a printer with the id `$PRINTER` and the
module installed under the name `simple-queue`:

```console
curl -X POST "$CORE/grants/device/$PRINTER" -H "Authorization: Bearer $TOKEN" \
  -H 'content-type: application/json' \
  -d '{"relation":"operator","subject":"sink:simple-queue"}'
```

## Its gate

`make -C extensions check EXT=simple-queue` lints the Go half and runs
`scripts/check.sh`. That script builds and tests both halves, and checks that
the typed client in `client/` is what `openapi.json` generates. It builds the
Storybook, packs a release and installs it into a demo server with one
simulated printer.

Then it puts two files on the printer's queue, writes the grant and marks the
bed clear. It checks that the first file starts by itself. After the first
one finished, it checks that the second waits for a person, and that Print
next starts it.
