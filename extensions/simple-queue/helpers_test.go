package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"

	"github.com/prusa3d/simple-queue/internal/hostfake"
)

// The module, driven the way the host drives it: the method set main.go
// builds, configured once, and then called.

// The people of the tests. alice may print on the printer, bob may only see
// it, and carol may not see it at all.
const (
	alice = "user:alice"
	bob   = "user:bob"
	carol = "user:carol"

	printer = "printer-1"
)

// rig is one configured module and the host it calls.
type rig struct {
	module *module
	serves wirerpc.Methods
	host   *hostfake.Host
}

// newRig configures a module over a host with one idle printer, which holds
// one file of its own, and one file in storage.
func newRig(t *testing.T) rig {
	t.Helper()

	fake := hostfake.New()
	fake.AddPrinter(printer, hostfake.Printer{
		Name: "MK4", Model: "MK4S", State: "IDLE", Connected: true, CanBeReady: true,
		Storages: []deviceapi.Storage{{Name: "usb"}},
		Files: []deviceapi.File{
			{Path: "usb/plate.bgcode", Name: "plate.bgcode", Size: 1024},
			{Path: "usb/models", Name: "models", Folder: true},
		},
	})
	fake.AddFile("local", "parts/bracket.bgcode")
	fake.AddFile("local", "parts/model.stl")

	for _, who := range []string{hostfake.Self, alice, bob} {
		fake.Grant(who, "observe", "device", printer)
	}

	fake.Grant(alice, "operate", "device", printer)
	fake.Grant(alice, "read_storage", "resource_kind", "storage")

	held := newModule(host{
		fleet: fake.Fleet(), access: fake, files: fake, feed: fake,
	}, func(string, ...any) {})
	serves := held.methods()

	t.Cleanup(held.stop)

	config, err := json.Marshal(map[string]any{"max_waiting": 3, "keep_history": 5, "send_timeout": "1h"})
	if err != nil {
		t.Fatal(err)
	}

	// The host grants the module a database of its own, as an install does.
	grant := wirerpc.DatabaseGrant{Engine: wirerpc.DatabaseSQLite, DSN: filepath.Join(t.TempDir(), "database.sqlite")}

	params, err := json.Marshal(wirerpc.Settings{Config: config, Grants: &wirerpc.Grants{Database: &grant}})
	if err != nil {
		t.Fatalf("render the settings: %v", err)
	}

	if _, err = serves[wirerpc.MethodConfigure](t.Context(),
		wirerpc.Call{Method: wirerpc.MethodConfigure, Params: params}); err != nil {
		t.Fatalf("the module refused the settings the host sends: %v", err)
	}

	return rig{module: held, serves: serves, host: fake}
}

// call makes one routes.serve call and reads the answer back.
func (r rig) call(t *testing.T, request routesapi.Request) routesapi.Response {
	t.Helper()

	params, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("render the request: %v", err)
	}

	raw, err := r.serves[routesapi.MethodServe](context.Background(),
		wirerpc.Call{Method: routesapi.MethodServe, Params: params})
	if err != nil {
		t.Fatalf("%s %s: %v", request.Method, request.Path, err)
	}

	response, ok := raw.(routesapi.Response)
	if !ok {
		t.Fatalf("the module answered %T and not a routesapi.Response", raw)
	}

	if err = response.Validate(); err != nil {
		t.Fatalf("the host would refuse this answer: %v", err)
	}

	if response.Status == 0 {
		response.Status = 200
	}

	return response
}

// as makes one call as a person and decodes the answer into into.
func (r rig) as(t *testing.T, who, method, path, body string, into any) int {
	t.Helper()

	response := r.call(t, routesapi.Request{Method: method, Path: path, Principal: who, Body: []byte(body)})

	if into != nil && len(response.Body) > 0 {
		if err := json.Unmarshal(response.Body, into); err != nil {
			t.Fatalf("%s %s answered %d %s: %v", method, path, response.Status, response.Body, err)
		}
	}

	return response.Status
}
