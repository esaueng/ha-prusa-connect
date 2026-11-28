package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"

	"github.com/prusa3d/server-slicing/internal/jobs"
	"github.com/prusa3d/server-slicing/internal/routes"
)

type queueRelay func(context.Context, routesapi.CallParams) (routesapi.Response, error)

func (f queueRelay) Call(ctx context.Context, params routesapi.CallParams) (routesapi.Response, error) {
	return f(ctx, params)
}

type queueWire func(context.Context, wirerpc.Method, any) (json.RawMessage, error)

func (f queueWire) Call(ctx context.Context, method wirerpc.Method, params any) (json.RawMessage, error) {
	return f(ctx, method, params)
}

func (r rig) withPrintQueue(calls routesapi.Modules) rig {
	configured := &module{state: r.module.current(), access: r.module.access, calls: calls}
	r.serves = configured.methods()

	return r
}

func finishedSlice(t *testing.T, r rig) jobs.Job {
	t.Helper()

	r.host.Put("models/bracket.3mf", threeMF(t, "binary"))

	var job jobs.Job
	status := r.as(t, alice, http.MethodPost, "/jobs",
		[]byte(`{"source":{"storage":"shelf","path":"models/bracket.3mf"}}`), &job)
	if status != http.StatusAccepted {
		t.Fatalf("submit answered %d", status)
	}

	done := r.waitJob(t, job.ID)
	if done.State != jobs.Done || done.Result == nil {
		t.Fatalf("the job did not write a print file: %+v", done)
	}

	return done
}

func TestEnqueueRelaysTheFinishedFileAsTheJobOwner(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	done := finishedSlice(t, r)
	// This is the Print queue handler's 201 response with fixed IDs and timestamps.
	queueView, readErr := os.ReadFile("testdata/print-queue-created.json")
	if readErr != nil {
		t.Fatal(readErr)
	}

	wire := queueWire(func(_ context.Context, method wirerpc.Method, params any) (json.RawMessage, error) {
		t.Helper()

		call, ok := params.(routesapi.CallParams)
		if !ok || method != routesapi.MethodCall || call.Principal != alice ||
			call.Module != routes.PrintQueueModule || call.Method != http.MethodPost || call.Path != "/printers/mk4/entries" {
			t.Fatalf("unexpected relay: %s %+v", method, params)
		}

		var file jobs.File
		if err := json.Unmarshal(
			call.Body,
			&file,
		); err != nil || file.Storage != done.Result.Storage ||
			file.Path != done.Result.Path {
			t.Fatalf("relay file: %+v, error: %v", file, err)
		}

		return json.Marshal(routesapi.Response{Status: http.StatusCreated, Body: queueView})
	})
	r = r.withPrintQueue(routesapi.NewClient(wire))

	var enqueued map[string]int
	status := r.as(t, alice, http.MethodPost, "/jobs/"+done.ID+"/enqueue", []byte(`{"device_id":"mk4"}`), &enqueued)
	if status != http.StatusCreated || len(enqueued) != 1 || enqueued["waiting"] != 2 {
		t.Fatalf("enqueue answered %d %+v", status, enqueued)
	}
}

func TestTheEnqueueResultDescribesOnlyTheWaitingCount(t *testing.T) {
	t.Parallel()

	var document struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPI, &document); err != nil {
		t.Fatal(err)
	}

	properties := document.Components.Schemas["EnqueueResult"].Properties
	if _, waiting := properties["waiting"]; len(properties) != 1 || !waiting {
		t.Fatalf("the enqueue response must describe just the waiting count, got %d properties", len(properties))
	}
}

func TestEnqueueRefusesInvalidInputAndJobsOwnedBySomeoneElse(t *testing.T) {
	t.Parallel()
	r := newRig(t).withPrintQueue(queueRelay(func(context.Context, routesapi.CallParams) (routesapi.Response, error) {
		t.Error("a refused request reached Print queue")

		return routesapi.Response{}, routesapi.ErrCallDenied
	}))
	done := finishedSlice(t, r)
	var failed jobs.Job
	r.as(t, alice, http.MethodPost, "/jobs",
		[]byte(`{"source":{"storage":"shelf","path":"models/bracket.3mf"}}`), &failed)
	failed = r.waitJob(t, failed.ID)
	if failed.State != jobs.Failed {
		t.Fatalf("the second slice wrote over the first: %+v", failed)
	}

	valid := `{"device_id":"mk4"}`
	for _, tc := range []struct {
		name, by, id, body string
		want               routes.Code
	}{
		{"no principal", "", done.ID, valid, routes.NoPrincipal},
		{"bad job ID", alice, "bad", valid, routes.Invalid},
		{"unknown job", alice, "0123456789abcdef", valid, routes.JobNotFound},
		{"another person's job", bob, done.ID, valid, routes.JobNotFound},
		{"administrator's view of someone else's job", root, done.ID, valid, routes.JobNotFound},
		{"failed job", alice, failed.ID, valid, routes.JobNotPrintable},
		{"malformed JSON", alice, done.ID, `{"device_id":`, routes.Invalid},
		{"trailing JSON", alice, done.ID, valid + `{}`, routes.Invalid},
		{"missing printer", alice, done.ID, `{}`, routes.Invalid},
		{"another file", alice, done.ID, `{"device_id":"mk4","path":"other.gcode"}`, routes.Invalid},
		{"separator in printer ID", alice, done.ID, `{"device_id":"mk4/entries"}`, routes.Invalid},
		{"escaped separator in printer ID", alice, done.ID, `{"device_id":"mk4%2fentries"}`, routes.Invalid},
		{"oversized body", alice, done.ID, strings.Repeat(" ", routes.MaxBodyBytes+1), routes.BodyTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertQueueRefusal(t, r, tc.by, http.MethodPost, "/jobs/"+tc.id+"/enqueue", []byte(tc.body), tc.want)
		})
	}
}

func TestEnqueueMapsPrintQueueRefusals(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	done := finishedSlice(t, r)
	for _, tc := range []struct {
		name string
		err  error
		code string
		want routes.Code
	}{
		{"module absent", routesapi.ErrCallUnavailable, "", routes.PrintQueueUnavailable},
		{"call not approved", routesapi.ErrCallNotDeclared, "", routes.PrintQueueNotApproved},
		{"caller cannot operate the module", routesapi.ErrCallDenied, "", routes.PrintQueueDenied},
		{"host failed", errors.New("private host detail"), "", routes.PrintQueueFailed},
		{"printer denied", nil, "NOT_OPERATOR", routes.PrintQueueDenied},
		{"not a person", nil, "NOT_A_PERSON", routes.PrintQueueDenied},
		{"storage denied", nil, "FILE_DENIED", routes.PrintQueueDenied},
		{"printer not found", nil, "PRINTER_NOT_FOUND", routes.PrintQueuePrinterNotFound},
		{"printer unreadable", nil, "PRINTER_UNKNOWN", routes.PrintQueuePrinterUnknown},
		{"permission unreadable", nil, "ACCESS_UNKNOWN", routes.PrintQueuePrinterUnknown},
		{"queue full", nil, "QUEUE_FULL", routes.PrintQueueFull},
		{"queue starting", nil, "NOT_READY", routes.PrintQueueUnavailable},
		{"file gone", nil, "FILE_NOT_FOUND", routes.PrintQueueFileNotFound},
		{"file not printable", nil, "NOT_PRINTABLE", routes.JobNotPrintable},
		{"unrecognized reply", nil, "STORE_FAILED", routes.PrintQueueFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			forwarded := r.withPrintQueue(
				queueRelay(func(context.Context, routesapi.CallParams) (routesapi.Response, error) {
					return routesapi.Response{Status: tc.want.Status,
						Body: []byte(`{"code":"simple-queue/` + tc.code + `","detail":"private queue detail"}`)}, tc.err
				}),
			)
			assertQueueRefusal(
				t,
				forwarded,
				alice,
				http.MethodPost,
				"/jobs/"+done.ID+"/enqueue",
				[]byte(`{"device_id":"mk4"}`),
				tc.want,
			)
		})
	}
}

func TestCheckingThePrintQueueSendsNoFileAndRequiresAnExplicitValidationRefusal(t *testing.T) {
	t.Parallel()
	r := newRig(t)

	assertQueueRefusal(t, r, alice, http.MethodGet, "/print-queue/mk4", nil, routes.PrintQueueUnavailable)

	for _, tc := range []struct {
		name   string
		status int
		code   string
		want   int
	}{
		{"empty object refused", http.StatusBadRequest, "simple-queue/INVALID", http.StatusNoContent},
		{"printer denied", http.StatusForbidden, "simple-queue/NOT_OPERATOR", http.StatusForbidden},
		{"unexpected answer", http.StatusCreated, "", http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			forwarded := r.withPrintQueue(
				queueRelay(func(_ context.Context, call routesapi.CallParams) (routesapi.Response, error) {
					if call.Principal != alice || call.Method != http.MethodPost ||
						call.Path != "/printers/mk4/entries" ||
						string(call.Body) != "{}" {
						t.Fatalf("unexpected relay: %+v", call)
					}

					return routesapi.Response{Status: tc.status, Body: []byte(`{"code":"` + tc.code + `"}`)}, nil
				}),
			)
			response := forwarded.call(
				t,
				routesapi.Request{Method: http.MethodGet, Path: "/print-queue/mk4", Principal: alice},
			)
			if response.Status != tc.want {
				t.Fatalf("availability answered %d %s, want %d", response.Status, response.Body, tc.want)
			}
		})
	}
}

func assertQueueRefusal(t *testing.T, r rig, by, method, path string, body []byte, want routes.Code) {
	t.Helper()

	var got refusal
	status := r.as(t, by, method, path, body, &got)
	if status != want.Status || got.Code != want.Name || strings.Contains(got.Detail, "private") {
		t.Fatalf("%s %s answered %d %+v, want %d %s", method, path, status, got, want.Status, want.Name)
	}
}
