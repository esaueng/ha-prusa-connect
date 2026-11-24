package jobs

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/accessapi"
	"github.com/prusa3d/prusa-connect/sdk/storageapi"

	"github.com/prusa3d/server-slicing/internal/install"
	"github.com/prusa3d/server-slicing/internal/project"
	"github.com/prusa3d/server-slicing/internal/runner"
)

// Files is the part of the host's storage door the jobs use.
type Files interface {
	Stat(ctx context.Context, params storageapi.StatParams) (storageapi.FileInfo, error)
	Read(ctx context.Context, params storageapi.ReadParams) (storageapi.Chunk, error)
	Write(ctx context.Context, params storageapi.WriteParams, body io.Reader) (storageapi.Written, error)
}

// Access is the host's answer to whether a person may do something.
type Access interface {
	Check(ctx context.Context, params accessapi.CheckParams) (accessapi.Decision, error)
}

// Slicers is what is installed.
type Slicers interface {
	// Installed lists every slicer that can run, downloaded and local.
	Installed() []install.Slicer
	// Pinned is the version an administrator chose as the default, or empty.
	Pinned() string
	// Acquire marks a version as in use until the returned function runs.
	Acquire(version string) func()
}

// RunFunc runs the slicer once. runner.Run is the one the module uses.
type RunFunc func(ctx context.Context, spec runner.Spec, progress runner.Progress) (runner.Result, error)

// Deps is what the jobs reach outside themselves.
type Deps struct {
	Files    Files
	Access   Access
	Slicers  Slicers
	Preparer project.Preparer
	// STLProfiles are the administrator's checked config exports, by name.
	STLProfiles map[string]string
	Run         RunFunc
	Log         *slog.Logger
	Now         func() time.Time
}

// Limits bound the work.
type Limits struct {
	// Workers is how many jobs slice at once.
	Workers int
	// MaxQueued is how many jobs may wait. One more is refused.
	MaxQueued int
	// Keep is how many finished jobs are remembered.
	Keep int
	// Timeout, MemoryBytes and Threads bound one slicer run.
	Timeout     time.Duration
	MemoryBytes int64
	Threads     int
	// MaxInputBytes bounds one project.
	MaxInputBytes int64
}

// Request is what a person asks for.
type Request struct {
	// By is who asks, as the host named them.
	By     string
	Source Source
	// Upload is the project itself, for a job whose source is an upload.
	Upload        []byte
	Output        Target
	Slicer        string
	Profile       string
	Selection     project.Selection
	Format        runner.Format
	Overwrite     bool
	KeepPlacement bool
}

// RefusalError is a request the jobs did not take.
type RefusalError struct {
	Code    Code
	Message string
}

func (r *RefusalError) Error() string { return r.Message }

// The refusals of a request that are not reasons a job fails.
const (
	CodeInvalid   Code = "server-slicing/INVALID"
	CodeQueueFull Code = "server-slicing/QUEUE_FULL"
	CodeNotFound  Code = "server-slicing/JOB_NOT_FOUND"
	CodeFinished  Code = "server-slicing/JOB_FINISHED"
)

func refuse(code Code, format string, args ...any) error {
	return &RefusalError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// The names of the files and folders under the data directory.
const (
	jobsFile   = "jobs.json"
	workDir    = "work"
	uploadsDir = "uploads"
)

// storageKind is the resource kind a storage's grants are held on.
const storageKind = "storage"

// Manager is the queue and its workers.
type Manager struct {
	deps   Deps
	limits Limits
	dir    string

	mu      sync.Mutex
	jobs    map[string]*Job
	order   []string
	cancels map[string]context.CancelFunc
	// canceled is the running jobs a person canceled, so the worker tells a
	// cancel from a stop of the whole module.
	canceled map[string]bool

	queue   chan string
	workers sync.WaitGroup
	stop    context.CancelFunc
	saving  sync.Mutex
}

// New reads the jobs the data directory remembers. A job that was queued or
// running when the module stopped is failed, because its work did not survive
// the stop, and what it left on disk is removed.
func New(dir string, deps Deps, limits Limits) (*Manager, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}

	if deps.Log == nil {
		deps.Log = slog.New(slog.DiscardHandler)
	}

	limits.Workers = max(limits.Workers, 1)
	limits.MaxQueued = max(limits.MaxQueued, 1)
	limits.Keep = max(limits.Keep, 1)

	m := &Manager{
		deps: deps, limits: limits, dir: dir,
		jobs: map[string]*Job{}, cancels: map[string]context.CancelFunc{}, canceled: map[string]bool{},
		queue: make(chan string, limits.MaxQueued),
	}

	for _, name := range []string{workDir, uploadsDir} {
		_ = os.RemoveAll(filepath.Join(dir, name)) // what a stopped job left; nothing reads it again

		if err := os.MkdirAll(filepath.Join(dir, name), 0o750); err != nil {
			return nil, fmt.Errorf("make the %s directory: %w", name, err)
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, jobsFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read the jobs: %w", err)
	}

	if len(data) > 0 {
		var held []*Job
		if err = json.Unmarshal(data, &held); err != nil {
			return nil, fmt.Errorf("read the jobs: %w", err)
		}

		for _, job := range held {
			if !job.State.Final() {
				m.finish(
					job,
					Failed,
					&Problem{Code: CodeInterrupted, Message: "the module stopped while the job was waiting or running"},
				)
			}

			m.jobs[job.ID] = job
			m.order = append(m.order, job.ID)
		}
	}

	return m, nil
}

// Start runs the workers until ctx ends or Stop is called.
func (m *Manager) Start(ctx context.Context) {
	ctx, m.stop = context.WithCancel(ctx)

	for range m.limits.Workers {
		m.workers.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case id := <-m.queue:
					m.work(ctx, id)
				}
			}
		})
	}
}

// Stop ends the workers. A job that is running is stopped and failed.
func (m *Manager) Stop() {
	if m.stop != nil {
		m.stop()
	}

	m.workers.Wait()
}

// Submit checks a request as the person who sent it and queues the job.
func (m *Manager) Submit(ctx context.Context, request Request) (Job, error) {
	job, err := m.check(ctx, request)
	if err != nil {
		return Job{}, err
	}

	if len(request.Upload) > 0 {
		if err = os.WriteFile(m.uploadOf(job.ID), request.Upload, 0o600); err != nil {
			return Job{}, fmt.Errorf("keep the uploaded project: %w", err)
		}
	}

	m.mu.Lock()
	m.jobs[job.ID] = job
	m.order = append(m.order, job.ID)

	select {
	case m.queue <- job.ID:
	default:
		delete(m.jobs, job.ID)
		m.order = m.order[:len(m.order)-1]
		m.mu.Unlock()

		_ = os.Remove(m.uploadOf(job.ID)) // the job was not taken

		return Job{}, refuse(CodeQueueFull, "%d jobs are waiting already. Try again when one has finished",
			m.limits.MaxQueued)
	}

	copied := *job
	m.mu.Unlock()

	m.save()

	return copied, nil
}

// check holds a request to what the module takes and to what the person may
// do, while the person's request is open.
func (m *Manager) check(ctx context.Context, request Request) (*Job, error) {
	if err := request.Selection.Validate(); err != nil {
		return nil, refuse(CodeInvalid, "%s", err.Error())
	}

	if request.Format != "" && request.Format != runner.Binary && request.Format != runner.Text {
		return nil, refuse(CodeInvalid, "the format is %q, and the formats are bgcode and gcode", request.Format)
	}

	source, output, err := m.places(ctx, request)
	if err != nil {
		return nil, err
	}

	// The source was read as the person above. The target is written later,
	// as the module, so the person's right to write is asked for here, while
	// their request and its scopes are still open.
	if may, mayErr := m.may(ctx, request.By, "write_storage"); mayErr != nil || !may {
		return nil, refuse(CodeOutputDenied, "you may not write files into the storage %s", output.Storage)
	}

	if slicerErr := m.checkSlicer(request.Slicer, request.Profile); slicerErr != nil {
		return nil, slicerErr
	}

	now := m.deps.Now().UTC()

	return &Job{
		ID: newID(), State: Queued, Progress: 0, By: request.By,
		Source: source, Output: output, Slicer: request.Slicer, Profile: request.Profile, Selection: request.Selection,
		Format: request.Format, Overwrite: request.Overwrite, KeepPlacement: request.KeepPlacement, CreatedAt: now,
	}, nil
}

const (
	stlSlicerMajor    = 2
	stlSlicerRequired = "STL jobs with saved INI profiles need an installed PrusaSlicer 2.x"
)

func (m *Manager) checkSlicer(requested, profile string) error {
	if _, err := m.chooseSlicer(requested, profile, 0); err != nil {
		switch {
		case profile != "":
			return refuse(CodeNoSlicer, "%s", stlSlicerRequired)
		case requested != "":
			return refuse(CodeNoSlicer, "PrusaSlicer %s is not installed", requested)
		default:
			return refuse(
				CodeNoSlicer,
				"no PrusaSlicer is installed. An administrator installs one on the Slicing page",
			)
		}
	}

	return nil
}

func (m *Manager) chooseSlicer(requested, profile string, needMajor int) (install.Slicer, error) {
	installed := m.deps.Slicers.Installed()
	if profile != "" {
		// PrusaSlicer 3 reads JSON where PrusaSlicer 2 reads the saved INI.
		installed = slices.DeleteFunc(slices.Clone(installed), func(held install.Slicer) bool {
			return held.Version.Major != stlSlicerMajor
		})
	}

	return install.Choose(installed, requested, m.deps.Slicers.Pinned(), needMajor)
}

// places reads the source and the target of a request. A source in a storage
// is read as the person, so a person who may not read it learns nothing about
// it.
func (m *Manager) places(ctx context.Context, request Request) (Source, Target, error) {
	source := request.Source
	output := request.Output

	if len(request.Upload) > 0 {
		if source.Upload == "" || strings.ContainsAny(source.Upload, "/\\") {
			return Source{}, Target{}, refuse(CodeInvalid, "an uploaded project needs a file name with no folder")
		}

		if int64(len(request.Upload)) > m.limits.MaxInputBytes {
			return Source{}, Target{}, refuse(
				CodeSourceTooLarge,
				"the project is over %d bytes",
				m.limits.MaxInputBytes,
			)
		}

		if output.Storage == "" {
			return Source{}, Target{}, refuse(
				CodeInvalid,
				"an uploaded project needs the storage to write the G-code into",
			)
		}

		source = Source{Upload: source.Upload}
	} else {
		if source.Storage == "" || source.Path == "" {
			return Source{}, Target{}, refuse(
				CodeInvalid,
				"the request names no project: give a storage and a path, or upload one",
			)
		}

		info, err := m.deps.Files.Stat(ctx, storageapi.StatParams{
			Principal: request.By, Storage: source.Storage, Path: source.Path,
		})

		switch {
		case errors.Is(err, storageapi.ErrDenied):
			return Source{}, Target{}, refuse(
				CodeSourceDenied,
				"you may not read files in the storage %s",
				source.Storage,
			)
		case err != nil:
			return Source{}, Target{}, refuse(CodeSourceNotFound, "there is no project %s in the storage %s",
				source.Path, source.Storage)
		case info.IsDir:
			return Source{}, Target{}, refuse(CodeSourceNotFound, "%s is a folder", source.Path)
		case info.Size > m.limits.MaxInputBytes:
			return Source{}, Target{}, refuse(CodeSourceTooLarge, "the project is %d bytes and the limit is %d",
				info.Size, m.limits.MaxInputBytes)
		}

		source = Source{Storage: source.Storage, Path: source.Path}
	}

	if err := m.checkSourceFormat(source, request); err != nil {
		return Source{}, Target{}, err
	}

	if output.Storage == "" {
		output.Storage = source.Storage
	}

	if output.Path == "" {
		output.Path = Stem(cmp.Or(source.Path, source.Name()))
		if request.Profile != "" {
			output.Path = strings.TrimSuffix(output.Path, path.Ext(output.Path)) + "-" + request.Profile
		}
	}

	output.Path = Stem(strings.TrimPrefix(output.Path, "/"))

	if !validPath(output.Path) || len(output.Storage) > storageapi.MaxStorageBytes {
		return Source{}, Target{}, refuse(
			CodeInvalid,
			"the output path %q is not a path inside a storage",
			request.Output.Path,
		)
	}

	return source, output, nil
}

func (m *Manager) checkSourceFormat(source Source, request Request) error {
	switch strings.ToLower(path.Ext(source.Name())) {
	case ".3mf":
		if request.Profile != "" || request.KeepPlacement {
			return refuse(CodeInvalid, "a 3MF project carries its own settings and placement")
		}
	case ".stl":
		if _, found := m.deps.STLProfiles[request.Profile]; !found {
			return refuse(CodeInvalid, "choose a saved STL profile")
		}
		if !request.Selection.Whole() {
			return refuse(CodeInvalid, "an STL file has no setup, bed or item selection")
		}
	default:
		return refuse(CodeNotAProject, "%s is not a .3mf or .stl file", source.Name())
	}

	return nil
}

// validPath is a relative path with no empty segment and no "..".
func validPath(name string) bool {
	if name == "" || len(name) > storageapi.MaxPathBytes-len(".bgcode") || strings.ContainsAny(name, "\\\x00") {
		return false
	}

	return path.Clean(name) == name && !strings.HasPrefix(name, "../") && name != ".." && name != "."
}

// may asks the host whether subject may take action on the storages.
func (m *Manager) may(ctx context.Context, subject, action string) (bool, error) {
	decision, err := m.deps.Access.Check(ctx, accessapi.CheckParams{
		Subject: subject, Action: action, ObjectType: "resource_kind", ObjectID: storageKind,
	})
	if err != nil {
		return false, err
	}

	return decision.Allowed, nil
}

// Get answers one job.
func (m *Manager) Get(id string) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, found := m.jobs[id]
	if !found {
		return Job{}, false
	}

	return *job, true
}

// List answers the jobs of one person, or of everybody when all is set,
// newest first. state filters by state when it is set. total counts what the
// filter matched, before limit.
func (m *Manager) List(by string, all bool, state State, limit int) ([]Job, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := []Job{}
	total := 0

	for _, v := range slices.Backward(m.order) {
		job := m.jobs[v]

		if (!all && job.By != by) || (state != "" && job.State != state) {
			continue
		}

		total++

		if limit <= 0 || len(out) < limit {
			out = append(out, *job)
		}
	}

	return out, total
}

// Counts answers how many jobs are in each state.
func (m *Manager) Counts() map[State]int {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := map[State]int{Queued: 0, Running: 0, Done: 0, Failed: 0, Canceled: 0}
	for _, job := range m.jobs {
		out[job.State]++
	}

	return out
}

// Cancel stops one job. A person cancels their own jobs, and one who may see
// every job cancels any.
func (m *Manager) Cancel(id, by string, all bool) (Job, error) {
	m.mu.Lock()

	job, found := m.jobs[id]
	if !found || (!all && job.By != by) {
		m.mu.Unlock()

		return Job{}, refuse(CodeNotFound, "there is no job %s", id)
	}

	if job.State.Final() {
		copied := *job
		m.mu.Unlock()

		return copied, refuse(CodeFinished, "the job has finished already")
	}

	if cancel, running := m.cancels[id]; running {
		m.canceled[id] = true
		job.Step = "stopping the slicer"

		cancel()
	}

	// A queued job is final at once. A running one becomes final when its
	// slicer has stopped, which the worker records.
	if job.State == Queued {
		m.finish(job, Canceled, nil)
	}

	copied := *job
	m.mu.Unlock()

	m.save()

	return copied, nil
}

// finish moves a job into a final state. The caller holds the lock.
func (m *Manager) finish(job *Job, state State, problem *Problem) {
	now := m.deps.Now().UTC()
	job.State = state
	job.FinishedAt = &now
	job.Error = problem
	job.Step = ""

	if state == Done {
		job.Progress = 100
	}
}

// save writes every job the module remembers. The oldest finished jobs over
// the bound are forgotten first.
func (m *Manager) save() {
	m.mu.Lock()

	finished := 0
	for _, id := range m.order {
		if m.jobs[id].State.Final() {
			finished++
		}
	}

	m.order = slices.DeleteFunc(m.order, func(id string) bool {
		if finished <= m.limits.Keep || !m.jobs[id].State.Final() {
			return false
		}

		finished--
		delete(m.jobs, id)

		return true
	})

	held := make([]*Job, 0, len(m.order))
	for _, id := range m.order {
		copied := *m.jobs[id]
		held = append(held, &copied)
	}

	m.mu.Unlock()

	data, err := json.Marshal(held)
	if err != nil {
		m.deps.Log.Error("the jobs could not be written", "error", err)

		return
	}

	m.saving.Lock()
	defer m.saving.Unlock()

	if err = install.WriteFileAtomic(filepath.Join(m.dir, jobsFile), data); err != nil {
		m.deps.Log.Error("the jobs could not be written", "error", err)
	}
}

func (m *Manager) uploadOf(id string) string { return filepath.Join(m.dir, uploadsDir, id+".3mf") }

// idBytes is how many random bytes name a job.
const idBytes = 8

func newID() string {
	held := make([]byte, idBytes)
	_, _ = rand.Read(held) // crypto/rand does not fail on a supported platform

	return hex.EncodeToString(held)
}
