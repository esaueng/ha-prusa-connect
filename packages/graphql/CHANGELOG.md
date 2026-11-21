# Changelog

What an operator sees change when they upgrade this module, newest release
first. `version:` in `module.yaml` is the newest heading below.

Write the entry in the same change as the code. One bullet per behaviour a
person outside this repository can observe: a field, a type, a refusal code, a
route. Name the field and the code, never a function or a file.

## 1.0.0 - unreleased

### Added

- `POST /graphql` and `GET /graphql` answer a query or a mutation against the
  server's own vocabulary. Every field is one core operation, every type is one
  core schema, and every member keeps the name the OpenAPI document gave it.
- A refusal carries the core's own code under `extensions.code`, where it is
  documented under `extensions.type`, the values it names under
  `extensions.params`, and whether a retry can help under
  `extensions.retryable`.
- A device carries `tags`, the plain words an operator put on it, and
  `devices(tag)` keeps the printers carrying every named tag. `labels` reads
  the same tags as key and value labels until a later release removes it.
- `schema.graphql` is the schema a client reads, generated from the core's
  OpenAPI document.
- A print in `device_jobs` carries `device_job_id`, the job number the machine
  issued. Its `id` is the print's own UUID, so two prints that carry one job
  number have two ids.
- `GET /graphql` refuses a mutation or a subscription with 405 and the code
  `graphql/METHOD_NOT_ALLOWED`. Send a write as a `POST`.
- A query nested deeper than 15 fields, selecting more than 512 fields or
  carrying more than 64 aliases is refused with 400 and the code
  `graphql/QUERY_TOO_COMPLEX`. Fragments count once for every place they are
  spread.
