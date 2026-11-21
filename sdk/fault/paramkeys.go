package fault

import "slices"

// paramKeys is the closed set of param keys the core's codes may declare. It is
// a static table rather than a registry, so it costs .rodata and nothing else
// (ADR-20260825-bhe rule 4).
//
// Three rules govern what may be added to it. A key names one fact, and a fact
// that already has a key does not get a second one: the instance a refusal is
// about is `resource`, carrying the kind, a colon and the name. A key carrying
// a bound names its unit or its subject, which is why there is `limit_bytes`
// and `limit_streams` and no bare `limit`. A size is a count of bytes. See
// ADR-20260826-c77 rules 6, 7 and 12.
//
// A new key is added here in the same change as the code that declares it, and
// the gate in cmd/generate-error-codes refuses a code that declares a key this
// table does not carry. The table binds the codes this repository registers. A
// code a plugin registers in its own domain is not measured by that gate,
// because a param key carries no namespace to close.
//
// The order is alphabetical so a reader can find a key, and the gate compares
// the set and not the order.
var paramKeys = []string{
	"action",
	"allowed_characters",
	"argument",
	"attempts",
	"coarsest_step",
	"command_id",
	"conflicts",
	"credential",
	"cycle",
	"declared",
	"device_id",
	"event",
	"expected_size",
	"expires_at",
	"extension",
	"field",
	"file",
	"firmware",
	"found",
	"gcode",
	"header",
	"held_by",
	"id",
	"instances",
	"job_id",
	"kind",
	"limit_bytes",
	"limit_characters",
	"limit_dashboards",
	"limit_streams",
	"limit_tags",
	"location",
	"machine_reason",
	"max",
	"method",
	"min",
	"mount",
	"name",
	"object",
	"op",
	"path",
	"preset_models",
	"previous_firmware",
	"printer_model",
	"provider",
	"read_only",
	"reason",
	"relation",
	"requires",
	"resource",
	"retry_after_seconds",
	"serves",
	"size",
	"source",
	"state",
	"storage_id",
	"subject",
	"suggested_verb",
	"supported",
	"tag",
	"timeout_ms",
	"transfer_id",
	"type",
	"verb",
	"verb_count",
	"verbs",
	"version",
	"word",
}

// ParamKeys returns the closed set of param keys, sorted. A client reads it to
// know every key a `params` object can carry, and a code that declares a key
// outside it fails the error-code gate.
func ParamKeys() []string { return slices.Clone(paramKeys) }

// ParamKeyDeclared reports whether name is in the published set.
func ParamKeyDeclared(name string) bool { return slices.Contains(paramKeys, name) }
