package jobs_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/prusa3d/threemf/threemf"

	"github.com/prusa3d/prusa-connect/sdk/accessapi"
	"github.com/prusa3d/prusa-connect/sdk/storageapi"

	"github.com/prusa3d/server-slicing/internal/catalog"
	"github.com/prusa3d/server-slicing/internal/fixture"
	"github.com/prusa3d/server-slicing/internal/install"
	"github.com/prusa3d/server-slicing/internal/jobs"
	"github.com/prusa3d/server-slicing/internal/project"
	"github.com/prusa3d/server-slicing/internal/runner"
)

const (
	alice = "user:alice"
	bob   = "user:bob"
)

// shelf is one storage held in memory, and the people who may read and write
// it. The module itself is the empty subject.
type shelf struct {
	mu    sync.Mutex
	files map[string][]byte
	// may is each subject's actions on the storages.
	may    map[string][]string
	writes int
}

func newShelf() *shelf {
	return &shelf{
		files: map[string][]byte{},
		may: map[string][]string{
			alice: {"read_storage", "write_storage"},
			bob:   {"read_storage"},
			"":    {"read_storage", "write_storage"},
		},
	}
}

func (s *shelf) allowed(subject, action string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Contains(s.may[subject], action)
}

func (s *shelf) set(subject string, actions ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.may[subject] = actions
}

func (s *shelf) put(name string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.files["shelf/"+name] = data
}

func (s *shelf) get(name string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, ok := s.files["shelf/"+name]

	return data, ok
}

func (s *shelf) Stat(_ context.Context, p storageapi.StatParams) (storageapi.FileInfo, error) {
	if !s.allowed(p.Principal, "read_storage") {
		return storageapi.FileInfo{}, storageapi.ErrDenied
	}

	data, ok := s.get(p.Path)
	if !ok || p.Storage != "shelf" {
		return storageapi.FileInfo{}, storageapi.ErrNoFile
	}

	return storageapi.FileInfo{Path: p.Path, Size: int64(len(data))}, nil
}

func (s *shelf) Read(_ context.Context, p storageapi.ReadParams) (storageapi.Chunk, error) {
	if !s.allowed(p.Principal, "read_storage") {
		return storageapi.Chunk{}, storageapi.ErrDenied
	}

	data, ok := s.get(p.Path)
	if !ok {
		return storageapi.Chunk{}, storageapi.ErrNoFile
	}

	end := min(int64(len(data)), p.Offset+int64(p.Length))

	return storageapi.Chunk{Data: data[p.Offset:end], Size: int64(len(data)), EOF: end == int64(len(data))}, nil
}

func (s *shelf) Write(_ context.Context, p storageapi.WriteParams, body io.Reader) (storageapi.Written, error) {
	if !s.allowed(p.Principal, "write_storage") {
		return storageapi.Written{}, storageapi.ErrDenied
	}

	data, err := io.ReadAll(body)
	if err != nil {
		return storageapi.Written{}, err
	}

	s.put(p.Path, data)

	s.mu.Lock()
	s.writes++
	s.mu.Unlock()

	sum := sha256.Sum256(data)

	return storageapi.Written{Path: p.Path, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}, nil
}

// Check answers for the storages from the same table. Only the storage kind
// is asked about.
func (s *shelf) Check(_ context.Context, p accessapi.CheckParams) (accessapi.Decision, error) {
	if p.ObjectType != "resource_kind" || p.ObjectID != "storage" {
		return accessapi.Decision{}, accessapi.ErrInvalidQuestion
	}

	return accessapi.Decision{Allowed: s.allowed(p.Subject, p.Action)}, nil
}

// slicers is a fixed list of installed slicers, all the stand-in.
type slicers struct {
	list []install.Slicer
	pin  string
}

func (s slicers) Installed() []install.Slicer { return s.list }
func (s slicers) Pinned() string              { return s.pin }
func (s slicers) Acquire(string) func()       { return func() {} }

func stand(t *testing.T, versions ...string) slicers {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the stand-in slicer is a shell script")
	}

	binary, err := filepath.Abs("../../testdata/fake-slicer.sh")
	if err != nil {
		t.Fatal(err)
	}

	out := slicers{}

	for _, version := range versions {
		parsed, parseErr := catalog.ParseVersion(version)
		if parseErr != nil {
			t.Fatal(parseErr)
		}

		out.list = append(out.list, install.Slicer{Version: parsed, Channel: catalog.Stable, Binary: binary})
	}

	return out
}

// threeMF is a project of two cubes on bed 0, saved by PrusaSlicer 2.9.6,
// whose stored entry tells the stand-in slicer what to do.
func threeMF(t *testing.T, mode string) []byte {
	t.Helper()

	data, err := fixture.Project{
		At: [][2]float64{{100, 100}, {130, 100}}, Settings: fixture.Bed200, Mode: mode,
	}.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	return data
}

type world struct {
	shelf   *shelf
	manager *jobs.Manager
	dir     string
	deps    jobs.Deps
	limits  jobs.Limits
}

func TestAnSTLJobUsesItsSavedProfile(t *testing.T) {
	t.Parallel()

	const mesh = "solid test\nendsolid test\n"
	const profile = "filament_type = PETG\n"
	installed := stand(t, "3.0.0", "2.9.6")
	installed.pin = "3.0.0"
	w := setUp(t, jobs.Deps{
		Slicers:     installed,
		STLProfiles: map[string]string{"core-one-petg": profile},
		Run: func(_ context.Context, spec runner.Spec, _ runner.Progress) (runner.Result, error) {
			input, err := os.ReadFile(spec.Input)
			if err != nil || filepath.Ext(spec.Input) != ".stl" || string(input) != mesh {
				return runner.Result{}, fmt.Errorf("the runner received the wrong STL: %q, %w", spec.Input, err)
			}
			config, err := os.ReadFile(spec.Config)
			if err != nil || string(config) != profile {
				return runner.Result{}, fmt.Errorf("the runner received the wrong profile: %w", err)
			}

			out := filepath.Join(filepath.Dir(spec.Config), "result.gcode")
			data := []byte("; generated by PrusaSlicer\nG28\n")
			err = os.WriteFile(out, data, 0o600)

			return runner.Result{Output: out, Size: int64(len(data)), Format: runner.Text}, err
		},
	})
	w.shelf.put("models/box.STL", []byte(mesh))
	stored := jobs.Request{By: alice, Source: jobs.Source{Storage: "shelf", Path: "models/box.STL"}}
	if _, err := w.manager.Submit(t.Context(), stored); err == nil {
		t.Fatal("an STL without a saved profile was accepted")
	}
	stored.Profile = "core-one-petg"
	stored.Selection.Bed = 1
	if _, err := w.manager.Submit(t.Context(), stored); err == nil {
		t.Fatal("an STL with a bed selection was accepted")
	}
	stored.Selection.Bed = 0

	w.manager.Start(t.Context())
	for _, req := range []jobs.Request{
		stored,
		{By: alice, Source: jobs.Source{Upload: "upload.stl"}, Upload: []byte(mesh),
			Output: jobs.Target{Storage: "shelf"}, Profile: "core-one-petg"},
	} {
		queued, err := w.manager.Submit(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		done := w.wait(t, queued.ID)
		if done.State != jobs.Done || done.Profile != req.Profile || done.Slicer != "2.9.6" ||
			!strings.HasSuffix(done.Result.Path, "-core-one-petg.gcode") {
			t.Fatalf("the STL job ended as %+v", done)
		}
	}
}

func TestAnSTLJobRequiresPrusaSlicer2(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		installed []string
		requested string
	}{
		{name: "only 3.x installed", installed: []string{"3.0.0-alpha11"}},
		{name: "explicitly requested 3.x", installed: []string{"3.0.0", "2.9.6"}, requested: "3.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := setUp(t, jobs.Deps{
				Slicers:     stand(t, tc.installed...),
				STLProfiles: map[string]string{"petg": "filament_type = PETG\n"},
			})
			w.shelf.put("cube.stl", []byte("solid test\nendsolid test\n"))

			_, err := w.manager.Submit(t.Context(), jobs.Request{
				By: alice, Source: jobs.Source{Storage: "shelf", Path: "cube.stl"},
				Profile: "petg", Slicer: tc.requested,
			})
			var refusal *jobs.RefusalError
			if !errors.As(err, &refusal) || refusal.Code != jobs.CodeNoSlicer ||
				!strings.Contains(err.Error(), "PrusaSlicer 2.x") {
				t.Fatalf("wanted a PrusaSlicer 2.x refusal, got %v", err)
			}
		})
	}
}

func setUp(t *testing.T, deps jobs.Deps) *world {
	t.Helper()

	held := newShelf()
	deps.Files, deps.Access = held, held

	if deps.Slicers == nil {
		deps.Slicers = stand(t, "2.9.6")
	}

	if deps.Preparer == nil {
		deps.Preparer = project.ThreeMF{}
	}

	if deps.Run == nil {
		deps.Run = runner.Run
	}

	limits := jobs.Limits{Workers: 1, MaxQueued: 4, Keep: 50, Timeout: 20 * time.Second, MaxInputBytes: 1 << 20}
	dir := t.TempDir()

	manager, err := jobs.New(dir, deps, limits)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(manager.Stop)

	return &world{shelf: held, manager: manager, dir: dir, deps: deps, limits: limits}
}

// wait returns the job once it reaches a final state.
func (w *world) wait(t *testing.T, id string) jobs.Job {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for time.Now().Before(deadline) {
		job, ok := w.manager.Get(id)
		if !ok {
			t.Fatalf("the job %s is gone", id)
		}

		if job.State.Final() {
			return job
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("the job %s did not end", id)

	return jobs.Job{}
}

func TestAJobSlicesAProjectIntoTheStorage(t *testing.T) {
	t.Parallel()

	w := setUp(t, jobs.Deps{})
	w.shelf.put("models/part.3mf", threeMF(t, "binary"))
	w.manager.Start(t.Context())

	job, err := w.manager.Submit(t.Context(), jobs.Request{
		By: alice, Source: jobs.Source{Storage: "shelf", Path: "models/part.3mf"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if job.State != jobs.Queued || job.Output.Path != "models/part" {
		t.Fatalf("the job was taken as %+v", job)
	}

	done := w.wait(t, job.ID)
	if done.State != jobs.Done || done.Result == nil {
		t.Fatalf("the job ended as %+v %+v", done, done.Error)
	}

	want := jobs.OutputPath(jobs.Target{Storage: "shelf", Path: "models/part"}, runner.Binary)
	if done.Result.Path != want || done.Result.Format != runner.Binary ||
		done.Result.Slicer != "2.9.6" {
		t.Fatalf("the result is %+v", done.Result)
	}

	written, ok := w.shelf.get(want)
	if !ok || !bytes.HasPrefix(written, []byte("GCDE")) {
		t.Fatal("the G-code is not in the storage")
	}

	// The module remembers the job across a restart. Stop waits for the
	// worker, which writes the record after it records the end in memory.
	w.manager.Stop()

	reopened, err := jobs.New(w.dir, w.deps, w.limits)
	if err != nil {
		t.Fatal(err)
	}

	if again, found := reopened.Get(job.ID); !found || again.State != jobs.Done {
		t.Fatalf("after a restart the job reads as %+v", again)
	}
}

func TestAFileThatExistsIsKeptUnlessTheJobOverwritesIt(t *testing.T) {
	t.Parallel()

	w := setUp(t, jobs.Deps{})
	w.shelf.put("part.3mf", threeMF(t, "text"))
	existing := jobs.OutputPath(jobs.Target{Storage: "shelf", Path: "part"}, runner.Text)
	w.shelf.put(existing, []byte("old"))
	w.manager.Start(t.Context())

	for _, overwrite := range []bool{false, true} {
		job, err := w.manager.Submit(t.Context(), jobs.Request{
			By: alice, Source: jobs.Source{Storage: "shelf", Path: "part.3mf"}, Overwrite: overwrite,
		})
		if err != nil {
			t.Fatal(err)
		}

		done := w.wait(t, job.ID)
		kept, _ := w.shelf.get(existing)

		if !overwrite && (done.Error == nil || done.Error.Code != jobs.CodeOutputExists || string(kept) != "old") {
			t.Fatalf("without overwrite the job ended as %+v and the file holds %q", done.Error, kept)
		}

		if want := (jobs.File{Storage: "shelf", Path: existing}); !overwrite &&
			(done.Error.Existing == nil || *done.Error.Existing != want) {
			t.Fatalf("without overwrite the job names %+v as the existing file, want %+v", done.Error.Existing, want)
		}

		if overwrite && (done.State != jobs.Done || string(kept) == "old") {
			t.Fatalf("with overwrite the job ended as %+v", done.Error)
		}
	}
}

// A person checked when the job was taken, who lost the right to write before
// it finished, gets a failed job and no file.
func TestAPersonWhoLostTheirWriteGetsNoFile(t *testing.T) {
	t.Parallel()

	w := setUp(t, jobs.Deps{})
	w.shelf.put("part.3mf", threeMF(t, "binary"))

	job, err := w.manager.Submit(
		t.Context(),
		jobs.Request{By: alice, Source: jobs.Source{Storage: "shelf", Path: "part.3mf"}},
	)
	if err != nil {
		t.Fatal(err)
	}

	w.shelf.set(alice, "read_storage")
	w.manager.Start(t.Context())

	done := w.wait(t, job.ID)
	if done.Error == nil || done.Error.Code != jobs.CodeOutputDenied {
		t.Fatalf("the job ended as %+v", done.Error)
	}

	if _, ok := w.shelf.get(jobs.OutputPath(jobs.Target{Storage: "shelf", Path: "part"}, runner.Binary)); ok {
		t.Fatal("a file was written for a person who may not write")
	}
}

func TestARequestTheModuleCannotTakeIsRefused(t *testing.T) {
	t.Parallel()

	w := setUp(t, jobs.Deps{})
	w.shelf.put("part.3mf", threeMF(t, "binary"))
	w.shelf.put("part.stl", []byte("solid"))
	w.shelf.put("part.obj", []byte("model"))

	cases := map[string]struct {
		request jobs.Request
		want    jobs.Code
	}{
		"a file that is not there": {
			jobs.Request{By: alice, Source: jobs.Source{Storage: "shelf", Path: "nope.3mf"}},
			jobs.CodeSourceNotFound,
		},
		"an unsupported model format": {
			jobs.Request{By: alice, Source: jobs.Source{Storage: "shelf", Path: "part.obj"}},
			jobs.CodeNotAProject,
		},
		"an STL with no configured profile": {
			jobs.Request{By: alice, Source: jobs.Source{Storage: "shelf", Path: "part.stl"}},
			jobs.CodeInvalid,
		},
		"a person who may not write": {
			jobs.Request{By: bob, Source: jobs.Source{Storage: "shelf", Path: "part.3mf"}},
			jobs.CodeOutputDenied,
		},
		"a person who may not read": {
			jobs.Request{By: "user:carol", Source: jobs.Source{Storage: "shelf", Path: "part.3mf"}},
			jobs.CodeSourceDenied,
		},
		"a bed past the last one": {
			jobs.Request{
				By:        alice,
				Source:    jobs.Source{Storage: "shelf", Path: "part.3mf"},
				Selection: project.Selection{Bed: 9},
			},
			jobs.CodeInvalid,
		},
		"a version that is not installed": {
			jobs.Request{By: alice, Source: jobs.Source{Storage: "shelf", Path: "part.3mf"}, Slicer: "2.8.0"},
			jobs.CodeNoSlicer,
		},
		"an output that climbs": {
			jobs.Request{
				By:     alice,
				Source: jobs.Source{Storage: "shelf", Path: "part.3mf"},
				Output: jobs.Target{Path: "../out"},
			},
			jobs.CodeInvalid,
		},
		"a format that is not one": {
			jobs.Request{By: alice, Source: jobs.Source{Storage: "shelf", Path: "part.3mf"}, Format: "stl"},
			jobs.CodeInvalid,
		},
		"an upload with no storage": {
			jobs.Request{By: alice, Source: jobs.Source{Upload: "part.3mf"}, Upload: []byte("x")},
			jobs.CodeInvalid,
		},
	}

	for name, held := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := w.manager.Submit(t.Context(), held.request)

			var refusal *jobs.RefusalError
			if !errors.As(err, &refusal) || refusal.Code != held.want {
				t.Fatalf("answered %v, want %s", err, held.want)
			}
		})
	}

	if listed, total := w.manager.List("", true, "", 0); total != 0 || len(listed) != 0 {
		t.Fatalf("a refused request left %d jobs", total)
	}
}

func TestNoSlicerInstalledIsRefused(t *testing.T) {
	t.Parallel()

	w := setUp(t, jobs.Deps{Slicers: slicers{}})
	w.shelf.put("part.3mf", threeMF(t, "binary"))

	_, err := w.manager.Submit(
		t.Context(),
		jobs.Request{By: alice, Source: jobs.Source{Storage: "shelf", Path: "part.3mf"}},
	)

	var refusal *jobs.RefusalError
	if !errors.As(err, &refusal) || refusal.Code != jobs.CodeNoSlicer {
		t.Fatalf("answered %v", err)
	}
}

// savedBy is a preparer that slices the file as it is and says which
// PrusaSlicer saved it.
type savedBy string

func (s savedBy) Prepare(_ context.Context, src, _ string, _ project.Selection) (project.Prepared, error) {
	name, version, _ := strings.Cut(string(s), "-")

	return project.Prepared{Path: src, Project: &threemf.Project{
		Producer: threemf.Producer{Application: string(s), Name: name, Version: version},
	}}, nil
}

// A project saved by PrusaSlicer 3 is not sliced by PrusaSlicer 2.
func TestAProjectOfANewerSlicerFails(t *testing.T) {
	t.Parallel()

	w := setUp(t, jobs.Deps{Preparer: savedBy("PrusaSlicer-3.0.0-alpha12")})
	w.shelf.put("part.3mf", threeMF(t, "binary"))
	w.manager.Start(t.Context())

	job, err := w.manager.Submit(
		t.Context(),
		jobs.Request{By: alice, Source: jobs.Source{Storage: "shelf", Path: "part.3mf"}},
	)
	if err != nil {
		t.Fatal(err)
	}

	done := w.wait(t, job.ID)
	if done.Error == nil || done.Error.Code != jobs.CodeNoSlicer || !strings.Contains(done.Error.Message, "3") {
		t.Fatalf("the job ended as %+v", done.Error)
	}
}

// A project OrcaSlicer saved fails before any slicer runs, and says which
// program saved it.
func TestAProjectOfAnotherSlicerFails(t *testing.T) {
	t.Parallel()

	data, err := fixture.Project{
		At: [][2]float64{{100, 100}}, Application: "OrcaSlicer-2.3.0", Mode: "binary",
	}.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	w := setUp(t, jobs.Deps{})
	w.shelf.put("orca.3mf", data)
	w.manager.Start(t.Context())

	job, err := w.manager.Submit(
		t.Context(),
		jobs.Request{By: alice, Source: jobs.Source{Storage: "shelf", Path: "orca.3mf"}},
	)
	if err != nil {
		t.Fatal(err)
	}

	done := w.wait(t, job.ID)
	if done.Error == nil || done.Error.Code != jobs.CodeNotPrusaSlicer ||
		done.Error.Message != "this project was saved by OrcaSlicer 2.3.0, and only PrusaSlicer projects can be sliced" {
		t.Fatalf("the job ended as %+v", done.Error)
	}
}

// A project with a post-processing script fails before any slicer runs, and
// says why.
func TestAProjectWithAScriptNeverReachesTheSlicer(t *testing.T) {
	t.Parallel()

	data, err := fixture.Project{
		At:       [][2]float64{{100, 100}},
		Settings: append(append([]string{}, fixture.Bed200...), "post_process = /usr/bin/touch /tmp/owned"),
		Mode:     "binary",
	}.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	var (
		mu   sync.Mutex
		runs int
	)

	w := setUp(t, jobs.Deps{
		Run: func(ctx context.Context, spec runner.Spec, progress runner.Progress) (runner.Result, error) {
			mu.Lock()
			runs++
			mu.Unlock()

			return runner.Run(ctx, spec, progress)
		},
	})
	w.shelf.put("scripted.3mf", data)
	w.manager.Start(t.Context())

	job, err := w.manager.Submit(
		t.Context(),
		jobs.Request{By: alice, Source: jobs.Source{Storage: "shelf", Path: "scripted.3mf"}},
	)
	if err != nil {
		t.Fatal(err)
	}

	done := w.wait(t, job.ID)
	if done.Error == nil || done.Error.Code != jobs.CodeNotAProject ||
		!strings.Contains(done.Error.Message, "post-processing script") {
		t.Fatalf("the job ended as %+v", done.Error)
	}

	mu.Lock()
	defer mu.Unlock()

	if runs != 0 || w.shelf.writes != 0 {
		t.Fatalf("the slicer ran %d times and the storage took %d files", runs, w.shelf.writes)
	}
}

func TestASlicerThatWritesNothingFailsTheJobWithItsLog(t *testing.T) {
	t.Parallel()

	w := setUp(t, jobs.Deps{})
	w.shelf.put("part.3mf", threeMF(t, "nothing"))
	w.manager.Start(t.Context())

	job, err := w.manager.Submit(
		t.Context(),
		jobs.Request{By: alice, Source: jobs.Source{Storage: "shelf", Path: "part.3mf"}},
	)
	if err != nil {
		t.Fatal(err)
	}

	done := w.wait(t, job.ID)
	if done.Error == nil || done.Error.Code != jobs.CodeNothingToPrint || len(done.Error.Log) == 0 {
		t.Fatalf("the job ended as %+v", done.Error)
	}

	if w.shelf.writes != 0 {
		t.Fatal("a job that sliced nothing wrote a file")
	}
}

// An uploaded project is sliced like one in a storage, into the storage the
// request names.
func TestAnUploadedProjectIsSliced(t *testing.T) {
	t.Parallel()

	w := setUp(t, jobs.Deps{})
	w.manager.Start(t.Context())

	job, err := w.manager.Submit(t.Context(), jobs.Request{
		By: alice, Source: jobs.Source{Upload: "bracket.3mf"}, Upload: threeMF(t, "text"),
		Output: jobs.Target{Storage: "shelf", Path: "sliced/bracket"},
	})
	if err != nil {
		t.Fatal(err)
	}

	done := w.wait(t, job.ID)
	if done.State != jobs.Done || done.Result.Path != jobs.OutputPath(
		jobs.Target{Storage: "shelf", Path: "sliced/bracket"}, runner.Text) {
		t.Fatalf("the job ended as %+v %+v", done.Error, done.Result)
	}
}

// blockingRun stands in for a slicer that runs until it is stopped.
func blockingRun(started chan<- struct{}) jobs.RunFunc {
	return func(ctx context.Context, _ runner.Spec, _ runner.Progress) (runner.Result, error) {
		started <- struct{}{}
		<-ctx.Done()

		return runner.Result{}, ctx.Err()
	}
}

func TestAJobIsCanceledWhetherItWaitsOrRuns(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 4)
	w := setUp(t, jobs.Deps{Run: blockingRun(started)})
	w.shelf.put("part.3mf", threeMF(t, "binary"))
	w.manager.Start(t.Context())

	request := jobs.Request{By: alice, Source: jobs.Source{Storage: "shelf", Path: "part.3mf"}}

	running, err := w.manager.Submit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}

	<-started

	waiting, err := w.manager.Submit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}

	// Another person cannot cancel it, and it reads as not there.
	if _, err = w.manager.Cancel(running.ID, bob, false); err == nil {
		t.Fatal("another person canceled the job")
	}

	if canceled, cancelErr := w.manager.Cancel(
		waiting.ID,
		alice,
		false,
	); cancelErr != nil ||
		canceled.State != jobs.Canceled {
		t.Fatalf("the waiting job answered %+v, %v", canceled, cancelErr)
	}

	if _, err = w.manager.Cancel(running.ID, alice, false); err != nil {
		t.Fatal(err)
	}

	if done := w.wait(t, running.ID); done.State != jobs.Canceled {
		t.Fatalf("the running job ended as %s %+v", done.State, done.Error)
	}

	if _, err = w.manager.Cancel(running.ID, alice, false); err == nil {
		t.Fatal("a finished job was canceled again")
	}
}

// A job that was running when the module stopped is failed when it starts
// again, rather than left running for ever.
func TestAJobRunningAtAStopIsFailed(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 1)
	w := setUp(t, jobs.Deps{Run: blockingRun(started)})
	w.shelf.put("part.3mf", threeMF(t, "binary"))
	w.manager.Start(t.Context())

	job, err := w.manager.Submit(
		t.Context(),
		jobs.Request{By: alice, Source: jobs.Source{Storage: "shelf", Path: "part.3mf"}},
	)
	if err != nil {
		t.Fatal(err)
	}

	<-started
	w.manager.Stop()

	stopped, _ := w.manager.Get(job.ID)
	if stopped.State != jobs.Failed || stopped.Error.Code != jobs.CodeInterrupted {
		t.Fatalf("the stopped job reads as %s %+v", stopped.State, stopped.Error)
	}
}

func TestAFullQueueRefusesTheNextJob(t *testing.T) {
	t.Parallel()

	w := setUp(t, jobs.Deps{})
	w.shelf.put("part.3mf", threeMF(t, "binary"))

	request := jobs.Request{By: alice, Source: jobs.Source{Storage: "shelf", Path: "part.3mf"}}

	for range w.limits.MaxQueued {
		if _, err := w.manager.Submit(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}

	_, err := w.manager.Submit(t.Context(), request)

	var refusal *jobs.RefusalError
	if !errors.As(err, &refusal) || refusal.Code != jobs.CodeQueueFull {
		t.Fatalf("the job over the queue answered %v", err)
	}
}

// A person sees their own jobs, and the count is of those alone.
func TestAPersonListsTheirOwnJobs(t *testing.T) {
	t.Parallel()

	w := setUp(t, jobs.Deps{})
	w.shelf.put("part.3mf", threeMF(t, "binary"))
	w.shelf.set(bob, "read_storage", "write_storage")

	for _, by := range []string{alice, alice, bob} {
		if _, err := w.manager.Submit(
			t.Context(),
			jobs.Request{By: by, Source: jobs.Source{Storage: "shelf", Path: "part.3mf"}},
		); err != nil {
			t.Fatal(err)
		}
	}

	if listed, total := w.manager.List(bob, false, "", 10); total != 1 || len(listed) != 1 || listed[0].By != bob {
		t.Fatalf("bob lists %d of %d", len(listed), total)
	}

	if listed, total := w.manager.List(alice, false, "", 1); total != 2 || len(listed) != 1 {
		t.Fatalf("alice lists %d of %d", len(listed), total)
	}

	if _, total := w.manager.List("", true, jobs.Queued, 0); total != 3 {
		t.Fatalf("everybody's queued jobs count %d", total)
	}
}

// A bed the project has nothing on fails the job, before the slicer runs.
func TestABedTheProjectDoesNotHaveFailsTheJob(t *testing.T) {
	t.Parallel()

	w := setUp(t, jobs.Deps{})
	w.shelf.put("part.3mf", threeMF(t, "binary"))
	w.manager.Start(t.Context())

	job, err := w.manager.Submit(t.Context(), jobs.Request{
		By: alice, Source: jobs.Source{Storage: "shelf", Path: "part.3mf"}, Selection: project.Selection{Bed: 3},
	})
	if err != nil {
		t.Fatal(err)
	}

	if done := w.wait(t, job.ID); done.Error == nil || done.Error.Code != jobs.CodeSelection {
		t.Fatalf("the job ended as %s %+v", done.State, done.Error)
	}
}

// A printer with a FAT storage keeps six characters of a long name in its 8.3
// short name. Outputs of one project, whose stems begin alike, must differ in
// those six, or the printer stores the second one over nothing.
func TestOutputNamesDifferInTheirFirstSixCharacters(t *testing.T) {
	t.Parallel()

	seen := map[string]string{}

	for bed := range 200 {
		for _, storage := range []string{"local", "usb"} {
			target := jobs.Target{
				Storage: storage,
				Path:    fmt.Sprintf("planner/project/sliced/prusaslicer-2.9.6-multibed-%03d", bed),
			}
			name := path.Base(jobs.OutputPath(target, runner.Binary))

			if !strings.HasSuffix(name, fmt.Sprintf("-prusaslicer-2.9.6-multibed-%03d.bgcode", bed)) {
				t.Fatalf("%+v is named %q, which drops the stem", target, name)
			}

			if other, taken := seen[name[:6]]; taken {
				t.Fatalf("%q and %q begin with the same six characters", other, name)
			}

			seen[name[:6]] = name
		}
	}

	got := jobs.OutputPath(jobs.Target{Storage: "local", Path: "a/b"}, runner.Binary)
	if !strings.HasPrefix(got, "a/") || strings.Count(got, "/") != 1 {
		t.Fatalf("the folder of the stem was not kept: %q", got)
	}
}

// A long stem is cut so the printer keeps the whole name, and the cut never
// splits a character.
func TestALongOutputNameFitsThePrinter(t *testing.T) {
	t.Parallel()

	for _, stem := range []string{strings.Repeat("a", 400), strings.Repeat("č", 200), strings.Repeat("x", 148) + "....."} {
		name := path.Base(jobs.OutputPath(jobs.Target{Storage: "local", Path: "dir/" + stem}, runner.Binary))

		if len(name) > 167 || !utf8.ValidString(name) || !strings.HasSuffix(name, ".bgcode") ||
			strings.Contains(name, "..") {
			t.Fatalf("a %d-byte stem gave %q, %d bytes", len(stem), name, len(name))
		}
	}
}
