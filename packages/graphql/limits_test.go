package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/routesapi"
)

// introspection is the query GraphiQL and graphql-js send to learn a schema,
// with the type reference nested nine levels, the deepest any of them writes.
// The members graphql-js added after the 2018 specification are left out,
// because graphql-go v0.8.1 refuses them in validation.
const introspection = `query IntrospectionQuery {
  __schema {
    queryType { name }
    mutationType { name }
    subscriptionType { name }
    types { ...FullType }
    directives { name description locations args { ...InputValue } }
  }
}
fragment FullType on __Type {
  kind name description
  fields(includeDeprecated: true) {
    name description
    args { ...InputValue }
    type { ...TypeRef }
    isDeprecated deprecationReason
  }
  inputFields { ...InputValue }
  interfaces { ...TypeRef }
  enumValues(includeDeprecated: true) { name description isDeprecated deprecationReason }
  possibleTypes { ...TypeRef }
}
fragment InputValue on __InputValue {
  name description type { ...TypeRef } defaultValue
}
fragment TypeRef on __Type {
  kind name
  ofType { kind name ofType { kind name ofType { kind name ofType { kind name ofType { kind name
  ofType { kind name ofType { kind name ofType { kind name ofType { kind name } } } } } } } } }
}`

// refusalCode reads the code off a problem document.
func refusalCode(t *testing.T, body []byte) string {
	t.Helper()

	var refused struct {
		Code string `json:"code"`
	}

	if err := json.Unmarshal(body, &refused); err != nil {
		t.Fatalf("read the refusal %q: %v", body, err)
	}

	return refused.Code
}

func TestAWriteInTheURLIsRefused(t *testing.T) {
	t.Parallel()

	for _, one := range []struct {
		name      string
		query     string
		operation string
	}{
		{
			name:  "an anonymous mutation",
			query: `mutation { send_device_command(device_id: "p3f2a1c9b", verb: "core.command.pause_print") { ok } }`,
		},
		{
			name:  "a named mutation, the only operation",
			query: `mutation Pause { send_device_command(device_id: "p3f2a1c9b", verb: "core.command.pause_print") { ok } }`,
		},
		{
			name: "a mutation chosen by name beside a query",
			query: `query Read { devices { total } }
				mutation Pause { send_device_command(device_id: "p3f2a1c9b", verb: "core.command.pause_print") { ok } }`,
			operation: "Pause",
		},
		{
			name: "a mutation beside a query, no name chosen",
			query: `query Read { devices { total } }
				mutation Pause { send_device_command(device_id: "p3f2a1c9b", verb: "core.command.pause_print") { ok } }`,
		},
		{name: "a subscription", query: `subscription { devices { total } }`},
	} {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()

			core := fleet()
			serves := start(t, core)

			query := "query=" + url(one.query)
			if one.operation != "" {
				query += "&operationName=" + url(one.operation)
			}

			for _, method := range []string{http.MethodGet, http.MethodHead} {
				response := serve(t, serves, routesapi.Request{
					Method: method, Path: "/", Principal: caller, Query: query,
				})

				if response.Status != http.StatusMethodNotAllowed {
					t.Fatalf("%s answered %d: %s", method, response.Status, response.Body)
				}

				if allow := response.Headers["Allow"]; len(allow) != 1 || allow[0] != http.MethodPost {
					t.Errorf("%s Allow reads %v", method, allow)
				}

				if method == http.MethodGet {
					if code := refusalCode(t, response.Body); code != "graphql/METHOD_NOT_ALLOWED" {
						t.Errorf("the refusal carries the code %q", code)
					}
				}

				if saw := core.asked(); saw.sawVerb != "" {
					t.Errorf("%s sent %q to the core", method, saw.sawVerb)
				}
			}
		})
	}
}

func TestAQueryChosenByNameBesideAMutationRunsInTheURL(t *testing.T) {
	t.Parallel()

	serves := start(t, fleet())

	document := `query Read { devices { total } }
		mutation Pause { send_device_command(device_id: "p3f2a1c9b", verb: "core.command.pause_print") { ok } }`

	response := serve(t, serves, routesapi.Request{
		Method: http.MethodGet, Path: "/", Principal: caller,
		Query: "query=" + url(document) + "&operationName=Read",
	})

	if response.Status != http.StatusOK {
		t.Fatalf("the query answered %d: %s", response.Status, response.Body)
	}

	if !strings.Contains(string(response.Body), `"total":2`) {
		t.Errorf("the answer read as %s", response.Body)
	}
}

func TestAMutationInTheBodyStillRuns(t *testing.T) {
	t.Parallel()

	core := fleet()
	serves := start(t, core)

	response, read := ask(t, serves,
		`mutation { send_device_command(device_id: "p3f2a1c9b", verb: "core.command.pause_print") { ok } }`)

	if response.Status != http.StatusOK || len(read.Errors) != 0 {
		t.Fatalf("the mutation answered %d: %s", response.Status, response.Body)
	}

	if saw := core.asked(); saw.sawVerb != "core.command.pause_print" {
		t.Errorf("the core was sent %q", saw.sawVerb)
	}
}

// TestTheIntrospectionQueryFitsTheBounds runs alone. graphql-go v0.8.1 fills
// an enum's value table on first use without a lock, and two introspection
// answers serialising __TypeKind at once race on it.
func TestTheIntrospectionQueryFitsTheBounds(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		t.Run(method, func(t *testing.T) {
			serves := start(t, fleet())

			ask := routesapi.Request{Method: method, Path: "/", Principal: caller}
			if method == http.MethodGet {
				ask.Query = "query=" + url(introspection)
			} else {
				body, err := json.Marshal(request{Query: introspection})
				if err != nil {
					t.Fatalf("render the request: %v", err)
				}

				ask.Body = body
			}

			response := serve(t, serves, ask)

			if response.Status != http.StatusOK {
				t.Fatalf("introspection answered %d: %s", response.Status, response.Body)
			}

			var read answer
			if err := json.Unmarshal(response.Body, &read); err != nil {
				t.Fatalf("read the answer: %v", err)
			}

			if len(read.Errors) != 0 {
				t.Fatalf("introspection was refused: %+v", read.Errors)
			}
		})
	}
}

func TestAQueryOverTheBoundsIsRefusedBeforeItRuns(t *testing.T) {
	t.Parallel()

	nested := "__schema { queryType { " +
		strings.Repeat("fields { type { ", 7) + "name" + strings.Repeat(" } }", 7) + " } }"

	var aliased strings.Builder

	aliased.WriteString("{")

	for i := range maxQueryAliases + 1 {
		fmt.Fprintf(&aliased, " d%d: devices { total }", i)
	}

	aliased.WriteString(" }")

	// Each fragment spreads the next one under two aliases. The text holds 22
	// aliases, and written out it holds thousands.
	var doubling strings.Builder

	doubling.WriteString("{ __schema { queryType { ...T0 } } }\n")

	for level := range 11 {
		fmt.Fprintf(&doubling, "fragment T%d on __Type { a: ofType { ...T%d } b: ofType { ...T%d } }\n",
			level, level+1, level+1)
	}

	doubling.WriteString("fragment T11 on __Type { name }\n")

	var wide strings.Builder

	wide.WriteString("{ __schema { types { ")

	for range maxQueryFields {
		wide.WriteString("name ")
	}

	wide.WriteString("} } }")

	// A fragment graph with no fields until the leaves still expands across
	// every branch; the step bound stops it before traversal grows unbounded.
	var branchBomb strings.Builder

	branchBomb.WriteString("{ ...B0 }\n")

	for level := range 16 {
		fmt.Fprintf(&branchBomb, "fragment B%d on Query { ...B%d ...B%d }\n", level, level+1, level+1)
	}

	branchBomb.WriteString("fragment B16 on Query { __typename }\n")

	for _, one := range []struct {
		name  string
		query string
		code  string
	}{
		{name: "deeper than the bound", query: "{ " + nested + " }", code: "graphql/QUERY_TOO_COMPLEX"},
		{name: "more aliases than the bound", query: aliased.String(), code: "graphql/QUERY_TOO_COMPLEX"},
		{name: "fragments that expand past the bound", query: doubling.String(), code: "graphql/QUERY_TOO_COMPLEX"},
		{name: "more fields than the bound", query: wide.String(), code: "graphql/QUERY_TOO_COMPLEX"},
		{name: "branching fragments past the step bound", query: branchBomb.String(), code: "graphql/QUERY_TOO_COMPLEX"},
		{
			name:  "cyclic fragment",
			query: "{ ...A } fragment A on Query { ...B } fragment B on Query { ...A }",
			code:  "graphql/REQUEST_INVALID",
		},
		{
			name:  "unknown fragment",
			query: "{ ...Missing }",
			code:  "graphql/REQUEST_INVALID",
		},
	} {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()

			core := fleet()
			serves := start(t, core)

			body, err := json.Marshal(request{Query: one.query})
			if err != nil {
				t.Fatalf("render the request: %v", err)
			}

			response := serve(t, serves, routesapi.Request{
				Method: http.MethodPost, Path: "/", Principal: caller, Body: body,
			})

			if response.Status != http.StatusBadRequest {
				t.Fatalf("it answered %d: %s", response.Status, response.Body)
			}

			if code := refusalCode(t, response.Body); code != one.code {
				t.Errorf("the refusal carries the code %q, want %q", code, one.code)
			}

			if saw := core.asked(); saw.sawPrincipal != "" {
				t.Errorf("the core was asked as %q", saw.sawPrincipal)
			}
		})
	}
}
