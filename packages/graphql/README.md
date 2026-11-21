# graphql

The GraphQL surface of this server, as a module of its own. It serves the
core's own vocabulary: every field is one core operation, every type is one
core schema, and every member keeps the name the OpenAPI document gave it.

```
POST /graphql   a query or a mutation
GET  /graphql   the same, with the query in the URL
GET  /graphql/openapi.json   the two routes above, served by the core
```

`schema.graphql` is the schema a client reads. It is generated and never
edited.

## What is in here

| file | what it is |
|---|---|
| `main.go` | the process the host launches: the handshake, the method set, the schema built once at start-up |
| `graphql.go` | the two routes, the request shapes and the refusals |
| `resolve.go` | one resolver per root field, each reading the fleet through `sdk/deviceapi` |
| `faults.go` | a wire refusal as a GraphQL error carrying the core's code and where it is documented |
| `internal/gen` | the generator, and the two tables that say what this module serves |
| `internal/schema` | the schema as data, the SDL it renders as, and the runtime types it builds into |
| `schema.graphql`, `model.json` | what the generator wrote. One model produces both |
| `CHANGELOG.md` | what an operator sees change when they upgrade this module |

## Installation

```sh
sh scripts/pack-example-modules.sh        # from the repository root
prusa-connect module add github.com/prusa3d/graphql@1.0.0 \
  -from ./bin/demo-modules/graphql
```

That writes the declaration and reaches the module under its namespace.

Paste what the two commands printed on your machine under this paragraph.

## Configuration

This module declares no `options:`, so there is nothing to write under the
instance's `config:` block, and nothing in `main.go` reads an environment
variable.

What an operator does set is the mount. It is a second step, because
`module add` does not name one. Write `mount: /graphql` into the declaration's
`service` block, which `PUT /api/v1/resources/sink/<name>` does while the
process runs.

The mount is the operator's to name and never the module's. The host refuses a
second claim on one prefix and any claim under `/api/`.

## Permissions

The manifest declares no `permissions:`. This module acts only for the person
the host authorized and does nothing as itself: no poll, no sync loop, no
background work. A permission list is the ceiling for what a module does with
nobody behind it, and this module does none of that.

The host takes `view_resource` on the instance before the `GET` and
`operate_resource` before the `POST`. `openapi.json` states that on each
operation. The authorization graph then decides every fleet read for the person
who asked.

## Errors

When a query fails, the module answers with a GraphQL error. The English text
is in `message`. The `extensions` object of the error has these members:

| member | what it is |
|---|---|
| `code` | the error code, the same one the REST API returns for this failure |
| `type` | the address of the page that documents the code |
| `params` | the values that belong to the code, such as the device id |
| `retryable` | whether the same request can succeed if you send it again |

A program should check `code`. The REST API and this module use the same codes,
so one set of translations serves both.

`faults.go` converts an error of the server into this shape. Add a new case in
that file.

A request that never runs is refused with an HTTP status and a problem
document instead:

| code | status | what it means |
|---|---|---|
| `graphql/REQUEST_INVALID` | 400 | the body or the URL is not a GraphQL request |
| `graphql/QUERY_TOO_LONG` | 400 | the query text is over 64 KiB |
| `graphql/QUERY_TOO_COMPLEX` | 400 | the query is over one of the bounds below |
| `graphql/METHOD_NOT_ALLOWED` | 405 | a `GET` carries a mutation or a subscription. Send it as a `POST` |

A query may nest 15 fields deep, select 512 fields and carry 64 aliases. A
fragment counts once for every place it is spread, because that is what runs.

## Regenerating

```sh
go generate ./...
```

It reads `internal/transport/http/httpapi/openapi.json` in the checkout above
and writes `schema.graphql` and `model.json`.

`go test ./...` refuses a committed copy that differs from what the generator
would write now. A core that moved and a module that was not regenerated fail
the same way.

The generator refuses the document rather than writing a schema when the
tables in `internal/gen/bindings.go` no longer describe it. Three cases fail it:

- an operation the core grew and nothing decided about
- a property added to a schema this module serves
- a name the tables use that the core dropped

Each refusal says what to do. Each is watched failing in
`internal/gen/generate_test.go`.

## What it does not serve

Most of the core's operations. The module reads the fleet through the plugin
wire, and that wire reaches the fleet alone. An operation with no wire method
behind it is listed in `unboundGroups` with the reason, rather than published
as a field that would answer a refusal forever.

There is no subscription root. The wire carries one request and one whole
answer, so a stream cannot cross it, and no GraphQL client this server is
written against opens one. Live data is `GET /api/v1/events` on the core.

There is no explorer page. The module ships no browser half, so a person
exploring the schema reads `schema.graphql`, or runs an introspection query
with whatever client they already have.

## Troubleshooting

| what you see | what it means |
|---|---|
| `go test ./...` refuses the committed schema | the core's document moved. Run `go generate ./...` and commit both files |
| the generator refuses the document | an operation or a property is neither served nor listed in `unboundGroups`. The refusal names it |
| every field answers a refusal | the host serves none of the wire methods `require:` names |
| `/graphql` answers 404 | the declaration has no `mount`. Write `mount: /graphql` into its `service` block |
| the instance is listed and switched off | `module add` ran without `-approve`. Read what it printed, then switch the instance on |
