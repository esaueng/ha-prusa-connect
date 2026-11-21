package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"

	"github.com/prusa3d/server-slicing/internal/jobs"
	"github.com/prusa3d/server-slicing/internal/routes"
	"github.com/prusa3d/server-slicing/internal/runner"
	"github.com/prusa3d/server-slicing/internal/slicers"
)

// refusal is the code of a refused answer.
type refusal struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// waitJob polls one of alice's jobs until it is final.
func (r rig) waitJob(t *testing.T, id string) jobs.Job {
	t.Helper()

	var job jobs.Job

	eventually(t, "the job "+id+" ends", func() bool {
		r.as(t, alice, http.MethodGet, "/jobs/"+id, nil, &job)

		return job.State.Final()
	})

	return job
}

// A person asks for a job over the routes, the worker slices it, and the
// G-code lands in the storage with what the slicer said about it.
func TestAJobIsSlicedThroughTheRoutes(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.host.Put("models/bracket.3mf", threeMF(t, "binary"))

	var job jobs.Job

	status := r.as(t, alice, http.MethodPost, "/jobs",
		[]byte(`{"source":{"storage":"shelf","path":"models/bracket.3mf"},"format":"bgcode"}`), &job)
	if status != http.StatusAccepted || job.ID == "" {
		t.Fatalf("the job was answered %d %+v", status, job)
	}

	want := jobs.OutputPath(jobs.Target{Storage: "shelf", Path: "models/bracket"}, runner.Binary)

	done := r.waitJob(t, job.ID)
	if done.State != jobs.Done || done.Result.Path != want || done.Result.Slicer != "2.9.6" {
		t.Fatalf("the job ended as %s %+v %+v", done.State, done.Error, done.Result)
	}

	if written, ok := r.host.File(want); !ok || !bytes.HasPrefix(written, []byte("GCDE")) {
		t.Fatal("the G-code is not in the storage")
	}
}

// A job whose file is there already names that file under error.existing,
// so a client that wants the output never derives the name itself.
func TestAJobWhoseFileExistsNamesThatFile(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.host.Put("models/bracket.3mf", threeMF(t, "binary"))

	existing := jobs.OutputPath(jobs.Target{Storage: "shelf", Path: "sliced/bracket"}, runner.Binary)
	r.host.Put(existing, []byte("old"))

	var job jobs.Job

	r.as(t, alice, http.MethodPost, "/jobs", []byte(
		`{"source":{"storage":"shelf","path":"models/bracket.3mf"},`+
			`"output":{"storage":"shelf","path":"sliced/bracket"},"format":"bgcode"}`), &job)
	r.waitJob(t, job.ID)

	// The answer is read as a client outside this module reads it, by the
	// names on the wire.
	var answer struct {
		State string `json:"state"`
		Error struct {
			Code     string `json:"code"`
			Existing struct {
				Storage string `json:"storage"`
				Path    string `json:"path"`
			} `json:"existing"`
		} `json:"error"`
	}

	r.as(t, alice, http.MethodGet, "/jobs/"+job.ID, nil, &answer)

	if answer.State != "failed" || answer.Error.Code != "server-slicing/OUTPUT_EXISTS" ||
		answer.Error.Existing.Storage != "shelf" || answer.Error.Existing.Path != existing {
		t.Fatalf("the job answered %+v, want the existing file shelf:%s", answer, existing)
	}

	if kept, _ := r.host.File(existing); string(kept) != "old" {
		t.Fatalf("the existing file now holds %q", kept)
	}
}

func TestAnUploadedProjectIsSlicedThroughTheRoutes(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	query := url.Values{"name": {"part.3mf"}, "output_storage": {"shelf"}, "output_path": {"sliced/part"}}

	var job jobs.Job

	if status := r.as(
		t,
		alice,
		http.MethodPost,
		"/jobs/upload?"+query.Encode(),
		threeMF(t, "text"),
		&job,
	); status != http.StatusAccepted {
		t.Fatalf("the upload was answered %d", status)
	}

	if done := r.waitJob(t, job.ID); done.State != jobs.Done || done.Result.Path != jobs.OutputPath(
		jobs.Target{Storage: "shelf", Path: "sliced/part"}, runner.Text) {
		t.Fatalf("the job ended as %+v %+v", done.Error, done.Result)
	}
}

// A person sees and cancels their own jobs. Another person reads them as not
// there, and an administrator sees every job.
func TestAPersonSeesTheirOwnJobsAndAnAdministratorEveryJob(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.host.Put("part.3mf", threeMF(t, "binary"))

	var job jobs.Job
	r.as(t, alice, http.MethodPost, "/jobs", []byte(`{"source":{"storage":"shelf","path":"part.3mf"}}`), &job)

	var refused refusal
	if status := r.as(t, bob, http.MethodGet, "/jobs/"+job.ID, nil, &refused); status != http.StatusNotFound ||
		refused.Code != routes.JobNotFound.Name {
		t.Fatalf("bob read alice's job: %d %+v", status, refused)
	}

	if status := r.as(
		t,
		bob,
		http.MethodPost,
		"/jobs/"+job.ID+"/cancel",
		nil,
		&refused,
	); status != http.StatusNotFound {
		t.Fatalf("bob canceled alice's job: %d", status)
	}

	var listed routes.JobList
	r.as(t, bob, http.MethodGet, "/jobs", nil, &listed)

	if listed.Total != 0 || listed.All {
		t.Fatalf("bob lists %+v", listed)
	}

	r.as(t, root, http.MethodGet, "/jobs", nil, &listed)

	if listed.Total != 1 || !listed.All || listed.Jobs[0].By != alice {
		t.Fatalf("the administrator lists %+v", listed)
	}

	if status := r.as(t, root, http.MethodGet, "/jobs/"+job.ID, nil, nil); status != http.StatusOK {
		t.Fatalf("the administrator read alice's job: %d", status)
	}
}

// Changing what is installed takes configure_resource. A person without it is
// refused before anything is downloaded.
func TestOnlyAnAdministratorChangesTheSlicers(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	for _, call := range []struct{ method, path, body string }{
		{http.MethodPost, "/slicers/install", `{"version":"2.9.6"}`},
		{http.MethodDelete, "/slicers/2.9.6", ""},
		{http.MethodPut, "/slicers/default", `{"version":"2.9.6"}`},
	} {
		var refused refusal
		if status := r.as(
			t,
			alice,
			call.method,
			call.path,
			[]byte(call.body),
			&refused,
		); status != http.StatusForbidden ||
			refused.Code != routes.NotAdministrator.Name {
			t.Errorf("%s %s as alice answered %d %+v", call.method, call.path, status, refused)
		}
	}

	var refused refusal
	if status := r.as(
		t,
		root,
		http.MethodPost,
		"/slicers/install",
		[]byte(`{"version":"9.9.9"}`),
		&refused,
	); status != http.StatusNotFound ||
		refused.Code != routes.NotOffered.Name {
		t.Fatalf("a version nothing offers answered %d %+v", status, refused)
	}

	var view slicers.View
	if status := r.as(
		t,
		root,
		http.MethodPut,
		"/slicers/default",
		[]byte(`{"version":"2.9.6"}`),
		&view,
	); status != http.StatusOK ||
		view.Pinned != "2.9.6" ||
		view.Default != "2.9.6" {
		t.Fatalf("the pin answered %d %+v", status, view)
	}

	if status := r.as(t, root, http.MethodDelete, "/slicers/2.9.6", nil, &refused); status != http.StatusNotFound {
		t.Fatalf("removing a local binary answered %d %+v", status, refused)
	}
}

// Only an administrator is told where a slicer's executable is. Every other
// person gets the list without paths, and a refusal of every route that
// changes the slicers.
func TestOnlyAnAdministratorIsToldWhereTheSlicersAre(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	binary := stand(t)

	var view slicers.View
	if status := r.as(t, root, http.MethodGet, "/slicers", nil, &view); status != http.StatusOK ||
		len(view.Installed) != 1 || view.Installed[0].Path != binary {
		t.Fatalf("the administrator was answered %d %+v", status, view.Installed)
	}

	for _, who := range []string{alice, bob} {
		for _, call := range []struct{ method, path, body string }{
			{http.MethodGet, "/slicers", ""},
			{http.MethodPost, "/slicers/install", `{"version":"2.9.6"}`},
			{http.MethodPost, "/slicers/refresh", ""},
			{http.MethodDelete, "/slicers/2.9.6", ""},
			{http.MethodPut, "/slicers/default", `{"version":"2.9.6"}`},
		} {
			response := r.call(t, routesapi.Request{
				Method: call.method, Path: call.path, Principal: who, Body: []byte(call.body),
			})

			if bytes.Contains(response.Body, []byte(binary)) || bytes.Contains(response.Body, []byte(`"path"`)) {
				t.Errorf("%s %s as %s told where the slicer is: %s", call.method, call.path, who, response.Body)
			}

			if call.method != http.MethodGet && response.Status != http.StatusForbidden {
				t.Errorf("%s %s as %s answered %d", call.method, call.path, who, response.Status)
			}
		}
	}
}

func TestARequestTheJobsRefuseIsAnsweredWithItsCode(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.host.Put("part.3mf", threeMF(t, "binary"))

	cases := map[string]struct {
		who, body string
		want      routes.Code
	}{
		"no project": {
			alice,
			`{"source":{"storage":"shelf","path":"nope.3mf"}}`,
			routes.SourceNotFound,
		},
		"a reader who may not write": {bob, `{"source":{"storage":"shelf","path":"part.3mf"}}`, routes.OutputDenied},
		"a bed past the last one": {
			alice,
			`{"source":{"storage":"shelf","path":"part.3mf"},"selection":{"bed":9}}`,
			routes.Invalid,
		},
		"an unknown field": {
			alice,
			`{"source":{"storage":"shelf","path":"part.3mf"},"speed":"fast"}`,
			routes.Invalid,
		},
		"an upload in the body": {alice, `{"source":{"upload":"part.3mf"}}`, routes.Invalid},
	}

	for name, held := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var refused refusal

			status := r.as(t, held.who, http.MethodPost, "/jobs", []byte(held.body), &refused)
			if status != held.want.Status || refused.Code != held.want.Name {
				t.Fatalf("answered %d %+v, want %d %s", status, refused, held.want.Status, held.want.Name)
			}
		})
	}
}

// A module granted no data directory still answers its status and says why it
// cannot slice.
func TestAModuleWithNoDataDirectorySaysWhy(t *testing.T) {
	t.Parallel()

	r := configuredRig(t, json.RawMessage(`{}`), wirerpc.Grants{})

	var status routes.Status
	r.as(t, alice, http.MethodGet, "/status", nil, &status)

	if !status.Ready || !strings.Contains(status.Problem, "data directory") {
		t.Fatalf("the status is %+v", status)
	}

	var refused refusal
	if code := r.as(t, alice, http.MethodGet, "/jobs", nil, &refused); code != http.StatusServiceUnavailable ||
		refused.Code != routes.NoData.Name {
		t.Fatalf("the jobs answered %d %+v", code, refused)
	}
}

func TestASettingOutOfRangeRefusesTheStart(t *testing.T) {
	t.Parallel()

	held := &module{}
	serves := held.methods()

	params, err := json.Marshal(
		wirerpc.Settings{Config: json.RawMessage(`{"max_jobs":0}`), Grants: &wirerpc.Grants{Data: t.TempDir()}},
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = serves[wirerpc.MethodConfigure](
		t.Context(),
		wirerpc.Call{Method: wirerpc.MethodConfigure, Params: params},
	); err == nil {
		t.Fatal("a module with no worker started")
	}
}
