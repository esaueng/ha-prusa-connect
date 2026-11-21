package main

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"

	"github.com/prusa3d/simple-queue/internal/routes"
)

// The fragment and the routes are held together here, so an author who adds a
// route and forgets the document finds out from `go test` rather than from a
// generated client that has no method for it.

// TestTheFragmentIsOneTheHostAccepts is what a module author runs before the
// host does: the embedded document passes the rule the host holds it to, and
// it rebases under a mount without losing a path.
func TestTheFragmentIsOneTheHostAccepts(t *testing.T) {
	t.Parallel()

	info, err := routesapi.Describe(
		wirerpc.Info{Name: name, Version: release, Contract: wirerpc.Contract}, openAPI)
	if err != nil {
		t.Fatalf("the host would refuse this module's fragment: %v", err)
	}

	fragment, err := routesapi.ParseFragment(info.OpenAPI)
	if err != nil {
		t.Fatalf("parse what plugin.info carries: %v", err)
	}

	if !slices.Contains(fragment.Paths(), "/queues/summary") {
		t.Fatalf("the fragment describes %v, and the module serves /queues/summary", fragment.Paths())
	}

	rebased, err := fragment.Rebase("/api/v1/modules/com.github.aux.prusa3d.simple-queue")
	if err != nil {
		t.Fatalf("rebase: %v", err)
	}

	if !strings.Contains(string(rebased), `"/api/v1/modules/com.github.aux.prusa3d.simple-queue/queues/summary"`) {
		t.Error("the rebased document does not carry the summary route under the mount")
	}
}

// operation is one route, as both sides name it: a method and a path relative
// to the mount.
type operation struct {
	method string
	path   string
}

func (o operation) String() string { return o.method + " " + o.path }

// served reads the routes the module registers out of internal/routes, which
// is where they are written. The mux cannot be asked what it holds, so the
// list is read from the file that builds it.
var servedPattern = regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) ([^"]+)"`)

// servedRoutes is every route the module registers, read out of the source.
func servedRoutes(t *testing.T) []operation {
	t.Helper()

	source, err := os.ReadFile("internal/routes/routes.go")
	if err != nil {
		t.Fatalf("read the routes: %v", err)
	}

	var out []operation

	for _, found := range servedPattern.FindAllStringSubmatch(string(source), -1) {
		out = append(out, operation{method: found[1], path: found[2]})
	}

	if len(out) == 0 {
		t.Fatal("no route was read out of internal/routes/routes.go, so this test proved nothing")
	}

	sort.Slice(out, func(a, b int) bool { return out[a].String() < out[b].String() })

	return out
}

// documentedRoutes is every operation openapi.json describes.
func documentedRoutes(t *testing.T) []operation {
	t.Helper()

	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}

	if err := json.Unmarshal(openAPI, &document); err != nil {
		t.Fatalf("read openapi.json: %v", err)
	}

	var out []operation

	for path, item := range document.Paths {
		for member := range item {
			// A path item may also carry parameters or a summary, which are
			// not operations.
			if member == "parameters" || member == "summary" || member == "description" {
				continue
			}

			out = append(out, operation{method: strings.ToUpper(member), path: path})
		}
	}

	sort.Slice(out, func(a, b int) bool { return out[a].String() < out[b].String() })

	return out
}

// TestTheDocumentAndTheRoutesAgree, operation by operation. A route with no
// line in the document has no method on the generated client, and a line with
// no route answers this module's own 404.
func TestTheDocumentAndTheRoutesAgree(t *testing.T) {
	t.Parallel()

	documented := documentedRoutes(t)

	for _, route := range servedRoutes(t) {
		if !slices.Contains(documented, route) {
			t.Errorf("the module serves %s and openapi.json does not describe it", route)
		}
	}

	served := servedRoutes(t)

	for _, route := range documented {
		if !slices.Contains(served, route) {
			t.Errorf("openapi.json describes %s and the module serves no such route", route)
		}
	}
}

// TestEveryDocumentedRouteAnswers drives each operation the document
// describes, so a path spelled one way in the document and another way in the
// mux is found here.
func TestEveryDocumentedRouteAnswers(t *testing.T) {
	t.Parallel()

	module := newRig(t)

	for _, route := range documentedRoutes(t) {
		path := strings.NewReplacer("{device}", printer, "{entry}", "0011223344556677").Replace(route.path)

		response := module.call(t, routesapi.Request{Method: route.method, Path: path, Principal: alice})

		if response.Status == http.StatusNotFound && strings.Contains(string(response.Body), "module_saw") {
			t.Errorf("%s reached the module's own 404: %s", route, response.Body)
		}
	}
}

// TestEveryCodeThisModuleAnswersIsInTheDocument holds the code table to the
// enum a client branches on. A code added to codes.go without a line in
// openapi.json is a code no client knows.
func TestEveryCodeThisModuleAnswersIsInTheDocument(t *testing.T) {
	t.Parallel()

	var document struct {
		Components struct {
			Schemas struct {
				Refusal struct {
					Properties struct {
						Code struct {
							Enum []string `json:"enum"`
						} `json:"code"`
					} `json:"properties"`
				} `json:"Refusal"`
			} `json:"schemas"`
		} `json:"components"`
	}

	if err := json.Unmarshal(openAPI, &document); err != nil {
		t.Fatalf("read openapi.json: %v", err)
	}

	listed := document.Components.Schemas.Refusal.Properties.Code.Enum
	if len(listed) == 0 {
		t.Fatal("openapi.json lists no refusal code, so this test proved nothing")
	}

	var answered []string
	for _, code := range routes.Codes() {
		answered = append(answered, code.Name)

		if !slices.Contains(listed, code.Name) {
			t.Errorf("the module answers %s and openapi.json does not list it", code.Name)
		}

		if code.Title == "" {
			t.Errorf("%s carries no English title", code.Name)
		}

		if !strings.HasPrefix(code.Name, name+"/") {
			t.Errorf("%s does not start with the module's own name", code.Name)
		}
	}

	for _, code := range listed {
		if !slices.Contains(answered, code) {
			t.Errorf("openapi.json lists %s and the module never answers it", code)
		}
	}
}

// TestEveryOperationNamesItsScopeAndItsCodes holds x-scope and x-codes, which
// the API tab of the page draws, to the routes. A GET needs a token that may
// read and anything else one that may operate, which is the split the host
// draws. Every
// code an operation names is one the module answers with, and every code the
// module answers with is named by some operation.
func TestEveryOperationNamesItsScopeAndItsCodes(t *testing.T) {
	t.Parallel()

	var document struct {
		Paths map[string]map[string]struct {
			Scope string   `json:"x-scope"`
			Codes []string `json:"x-codes"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(openAPI, &document); err != nil {
		t.Fatal(err)
	}

	known := map[string]bool{}
	for _, code := range routes.Codes() {
		known[code.Name] = true
	}

	named := map[string]bool{}

	for path, operations := range document.Paths {
		for method, op := range operations {
			where := strings.ToUpper(method) + " " + path

			switch {
			case method == "get" && op.Scope != "read":
				t.Errorf("%s names scope %q, and a GET takes read", where, op.Scope)
			case method != "get" && op.Scope != "operate":
				t.Errorf("%s names scope %q, and it takes operate", where, op.Scope)
			}

			if len(op.Codes) == 0 {
				t.Errorf("%s names no x-codes", where)
			}

			for _, code := range op.Codes {
				if !known[code] {
					t.Errorf("%s names %s, which the module never answers", where, code)
				}

				named[code] = true
			}
		}
	}

	for code := range known {
		if !named[code] {
			t.Errorf("%s is answered by no operation's x-codes", code)
		}
	}
}

// TestEveryRefusalIsTheModulesOwn holds the refusal responses to the code
// table the routes answer from. Each code an operation names in x-codes is an
// example under the status it is answered with, with its title and whether a
// retry may work, and an operation shows no refusal it never gives. A code
// whose status or title changes in internal/routes fails here until the
// fragment says the same.
func TestEveryRefusalIsTheModulesOwn(t *testing.T) {
	t.Parallel()

	type example struct {
		Value struct {
			Status    int    `json:"status"`
			Code      string `json:"code"`
			Title     string `json:"title"`
			Retryable bool   `json:"retryable"`
		} `json:"value"`
	}

	var document struct {
		Paths map[string]map[string]struct {
			Codes     []string `json:"x-codes"`
			Responses map[string]struct {
				Content map[string]struct {
					Examples map[string]example `json:"examples"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(openAPI, &document); err != nil {
		t.Fatal(err)
	}

	table := map[string]routes.Code{}
	for _, code := range routes.Codes() {
		table[code.Name] = code
	}

	for path, operations := range document.Paths {
		for method, op := range operations {
			where := strings.ToUpper(method) + " " + path
			shown := map[string]bool{}

			for status, response := range op.Responses {
				if strings.HasPrefix(status, "2") {
					continue
				}

				for name, got := range response.Content["application/problem+json"].Examples {
					shown[name] = true
					want, known := table[name]

					switch {
					case !known || !slices.Contains(op.Codes, name):
						t.Errorf("%s shows %s, which it does not answer with", where, name)
					case status != strconv.Itoa(want.Status) || got.Value.Status != want.Status:
						t.Errorf("%s shows %s under %s, and it is answered with %d", where, name, status, want.Status)
					case got.Value.Code != name || got.Value.Title != want.Title || got.Value.Retryable != want.Retryable:
						t.Errorf("%s shows %s as %+v, and the routes answer %+v", where, name, got.Value, want)
					}
				}
			}

			for _, name := range op.Codes {
				if !shown[name] {
					t.Errorf("%s names %s in x-codes and shows no response for it", where, name)
				}
			}
		}
	}
}
