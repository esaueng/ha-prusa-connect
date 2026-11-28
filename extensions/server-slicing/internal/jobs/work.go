package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/prusa3d/threemf/threemf"

	"github.com/prusa3d/prusa-connect/sdk/storageapi"

	"github.com/prusa3d/server-slicing/internal/gcodemeta"
	"github.com/prusa3d/server-slicing/internal/install"
	"github.com/prusa3d/server-slicing/internal/project"
	"github.com/prusa3d/server-slicing/internal/runner"
)

// failure is a job that failed for a reason a person reads.
type failure struct {
	problem Problem
}

func (f *failure) Error() string { return string(f.problem.Code) + ": " + f.problem.Message }

func fail(code Code, format string, args ...any) error {
	return &failure{problem: Problem{Code: code, Message: fmt.Sprintf(format, args...)}}
}

// work runs one job from the queue to its end.
func (m *Manager) work(parent context.Context, id string) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	m.mu.Lock()

	job, found := m.jobs[id]
	if !found || job.State != Queued {
		m.mu.Unlock()

		return
	}

	now := m.deps.Now().UTC()
	job.State = Running
	job.StartedAt = &now
	job.Step = "reading the project"
	m.cancels[id] = cancel
	held := *job
	m.mu.Unlock()

	m.save()

	dir := filepath.Join(m.dir, workDir, id)

	defer func() {
		_ = os.RemoveAll(dir)         // the job's scratch files; the result is in the storage
		_ = os.Remove(m.uploadOf(id)) // an uploaded project is kept only until its job ends
	}()

	result, err := m.slice(ctx, held, dir)

	m.mu.Lock()
	delete(m.cancels, id)

	byPerson := m.canceled[id]
	delete(m.canceled, id)

	switch {
	case err == nil:
		job.Result = result
		m.finish(job, Done, nil)
	case byPerson:
		m.finish(job, Canceled, nil)
	case ctx.Err() != nil:
		m.finish(job, Failed, &Problem{Code: CodeInterrupted, Message: "the module stopped while the job was running"})
	default:
		m.finish(job, Failed, problemOf(err))
		m.deps.Log.InfoContext(parent, "a slicing job failed", "job", id, "code", job.Error.Code, "error", err.Error())
	}

	m.mu.Unlock()

	m.save()
}

// problemOf is what a person reads about err.
func problemOf(err error) *Problem {
	if known, ok := errors.AsType[*failure](err); ok {
		problem := known.problem

		return &problem
	}

	if ran, ok := errors.AsType[*runner.RunError](err); ok {
		code := CodeSlicerFailed

		switch {
		case errors.Is(err, runner.ErrNothingToPrint):
			code = CodeNothingToPrint
		case errors.Is(err, runner.ErrNoOutput):
			code = CodeNoOutput
		case errors.Is(err, runner.ErrTimeout):
			code = CodeTimeout
		case errors.Is(err, runner.ErrMemory):
			code = CodeMemory
		}

		return &Problem{Code: code, Message: ran.Reason.Error(), Log: ran.Log}
	}

	return &Problem{
		Code:    CodeInternalFailure,
		Message: "the module failed while slicing. The server's log has the reason",
	}
}

// slice does the work of one job and answers its result.
func (m *Manager) slice(ctx context.Context, job Job, dir string) (*Result, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("make the job's directory: %w", err)
	}

	input, err := m.fetch(ctx, job, dir)
	if err != nil {
		return nil, err
	}

	m.step(job.ID, 0, "preparing the project", "")

	prepared, config, err := m.prepare(ctx, job, input, dir)
	if err != nil {
		return nil, err
	}

	chosen, err := m.chooseSlicer(job.Slicer, job.Profile, prepared.Major())
	if err != nil {
		if job.Profile != "" {
			return nil, fail(CodeNoSlicer, "%s", stlSlicerRequired)
		}

		return nil, chooseFailure(err, prepared)
	}

	release := m.deps.Slicers.Acquire(chosen.Name())
	defer release()

	m.step(job.ID, 0, "starting PrusaSlicer", chosen.Name())

	ran, err := m.deps.Run(ctx, runner.Spec{
		Binary:        chosen.Binary,
		Args:          chosen.Args,
		Input:         prepared.Path,
		Dir:           filepath.Join(dir, "slicer"),
		Config:        config,
		KeepPlacement: job.KeepPlacement,
		Format:        job.Format,
		Threads:       m.limits.Threads,
		Timeout:       m.limits.Timeout,
		MemoryBytes:   m.limits.MemoryBytes,
	}, func(percent int, step string) { m.step(job.ID, percent, step, "") })
	if err != nil {
		return nil, err
	}

	meta, err := gcodemeta.Read(ran.Output)
	if err != nil {
		m.deps.Log.WarnContext(
			ctx,
			"the G-code the slicer wrote could not be read for its details",
			"job",
			job.ID,
			"error",
			err,
		)
	}

	m.step(job.ID, runner.Finished, "writing the G-code", "")

	written, err := m.deliver(ctx, job, ran)
	if err != nil {
		return nil, err
	}

	result := &Result{
		Storage: job.Output.Storage, Path: written.Path, Size: written.Size, SHA256: written.SHA256,
		Format: ran.Format, Slicer: chosen.Name(),
		DurationMS: ran.Duration.Milliseconds(), PeakMemoryBytes: ran.PeakBytes,
		EstimatedSeconds: meta.EstimatedSeconds, FilamentMM: meta.FilamentMM, FilamentG: meta.FilamentG,
		FilamentType: meta.FilamentType, PrinterModel: meta.PrinterModel, NozzleMM: meta.NozzleMM,
		LayerHeightMM: meta.LayerHeightMM, Objects: objectsOf(meta.Objects, prepared),
		Thumbnails: meta.Thumbnails,
	}

	if result.Thumbnails == nil {
		result.Thumbnails = []gcodemeta.Thumbnail{}
	}

	if prepared.Warning != "" {
		result.Warnings = append(result.Warnings, prepared.Warning)
	}

	if job.Format != "" && job.Format != ran.Format {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%s was asked for and PrusaSlicer %s wrote %s", job.Format, chosen.Name(), ran.Format))
	}

	return result, nil
}

func (m *Manager) prepare(ctx context.Context, job Job, input, dir string) (project.Prepared, string, error) {
	if job.Profile != "" {
		settings, found := m.deps.STLProfiles[job.Profile]
		if !found {
			return project.Prepared{}, "", fail(CodeNotAProject, "the selected STL profile is unavailable")
		}

		if job.Settings != nil {
			settings = job.Settings.Apply(settings)
		}

		config := filepath.Join(dir, "profile.ini")
		if err := os.WriteFile(config, []byte(settings), 0o600); err != nil {
			return project.Prepared{}, "", fmt.Errorf("write the STL profile: %w", err)
		}

		return project.Prepared{Path: input}, config, nil
	}

	prepared, err := m.deps.Preparer.Prepare(ctx, input, filepath.Join(dir, "prepared.3mf"), job.Selection)

	var (
		foreign *project.NotPrusaSlicerError
		risky   *project.UnsafeSettingsError
	)

	switch {
	case errors.As(err, &foreign):
		err = fail(CodeNotPrusaSlicer, "%s", foreign.Error())
	case errors.As(err, &risky):
		err = fail(CodeNotAProject, "%s", risky.Error())
	case errors.Is(err, project.ErrNotAProject):
		err = fail(CodeNotAProject, "the file is not a 3MF project this module reads")
	case errors.Is(err, project.ErrSelection) && job.Selection.Whole():
		err = fail(CodeNothingToPrint, "no printable object stands whole on the first bed: %s", err.Error())
	case errors.Is(err, project.ErrSelection):
		err = fail(CodeSelection, "%s", err.Error())
	}

	return prepared, "", err
}

func chooseFailure(err error, prepared project.Prepared) error {
	switch {
	case errors.Is(err, install.ErrTooOld):
		return fail(CodeSlicerTooOld, "the project was saved by %s, and %s", prepared.Application(), err.Error())
	default:
		return fail(CodeNoSlicer, "%s", err.Error())
	}
}

// fetch puts the project into the job's directory. A project in a storage is
// read as the module, once the server said the person may still read it.
func (m *Manager) fetch(ctx context.Context, job Job, dir string) (string, error) {
	input := filepath.Join(dir, "input.3mf")
	if job.Profile != "" {
		input = filepath.Join(dir, "input.stl")
	}
	if job.Source.Upload != "" {
		upload := m.uploadOf(job.ID)
		if job.Profile == "" {
			return upload, nil
		}
		if err := os.Rename(upload, input); err != nil {
			return "", fmt.Errorf("prepare the uploaded STL: %w", err)
		}

		return input, nil
	}

	if may, err := m.may(ctx, job.By, "read_storage"); err != nil || !may {
		return "", fail(CodeSourceDenied, "the person who asked for the job may no longer read the storage %s",
			job.Source.Storage)
	}

	file, err := os.OpenFile(input, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("make the project file: %w", err)
	}

	err = m.copyFrom(ctx, job.Source, file)
	if closeErr := file.Close(); err == nil && closeErr != nil {
		err = fmt.Errorf("write the project file: %w", closeErr)
	}

	if err != nil {
		return "", err
	}

	return input, nil
}

// copyFrom walks a file in a storage in the largest ranges the host serves.
func (m *Manager) copyFrom(ctx context.Context, source Source, into *os.File) error {
	var offset int64

	for {
		chunk, err := m.deps.Files.Read(ctx, storageapi.ReadParams{
			Storage: source.Storage, Path: source.Path, Offset: offset, Length: storageapi.MaxReadBytes,
		})

		switch {
		case errors.Is(err, storageapi.ErrNoFile):
			return fail(
				CodeSourceNotFound,
				"the project %s is no longer in the storage %s",
				source.Path,
				source.Storage,
			)
		case errors.Is(err, storageapi.ErrDenied):
			return fail(
				CodeSourceDenied,
				"the module may not read the storage %s. An administrator grants it read_storage",
				source.Storage,
			)
		case err != nil:
			return fmt.Errorf("read the project: %w", err)
		}

		if chunk.Size > m.limits.MaxInputBytes {
			return fail(
				CodeSourceTooLarge,
				"the project is %d bytes and the limit is %d",
				chunk.Size,
				m.limits.MaxInputBytes,
			)
		}

		if _, err = into.Write(chunk.Data); err != nil {
			return fmt.Errorf("write the project file: %w", err)
		}

		offset += int64(len(chunk.Data))

		if chunk.EOF || len(chunk.Data) == 0 {
			return nil
		}
	}
}

// deliver writes the G-code into the storage, as the module, once the server
// said the person may still write there.
func (m *Manager) deliver(ctx context.Context, job Job, ran runner.Result) (storageapi.Written, error) {
	target := OutputPath(job.Output, ran.Format)

	if may, err := m.may(ctx, job.By, "write_storage"); err != nil || !may {
		return storageapi.Written{}, fail(CodeOutputDenied,
			"the person who asked for the job may no longer write into the storage %s", job.Output.Storage)
	}

	if !job.Overwrite {
		_, err := m.deps.Files.Stat(ctx, storageapi.StatParams{Storage: job.Output.Storage, Path: target})

		switch {
		case err == nil:
			return storageapi.Written{}, &failure{problem: Problem{
				Code: CodeOutputExists,
				Message: fmt.Sprintf("%s is in the storage %s already. Ask to overwrite it, or name another file",
					target, job.Output.Storage),
				Existing: &File{Storage: job.Output.Storage, Path: target},
			}}
		case !errors.Is(err, storageapi.ErrNoFile):
			return storageapi.Written{}, fail(CodeOutputFailed, "the module could not check whether %s exists: %s",
				target, err.Error())
		}
	}

	file, err := os.Open(ran.Output)
	if err != nil {
		return storageapi.Written{}, fmt.Errorf("open the G-code: %w", err)
	}

	defer func() { _ = file.Close() }() // read-only

	written, err := m.deps.Files.Write(ctx, storageapi.WriteParams{
		Storage: job.Output.Storage, Path: target, Size: ran.Size,
	}, file)

	switch {
	case errors.Is(err, storageapi.ErrDenied):
		return storageapi.Written{}, fail(
			CodeOutputDenied,
			"the module may not write into the storage %s. An administrator grants it write_storage",
			job.Output.Storage,
		)
	case err != nil:
		return storageapi.Written{}, fail(CodeOutputFailed, "the G-code could not be written into the storage %s: %s",
			job.Output.Storage, err.Error())
	}

	if written.Path == "" {
		written.Path = target
	}

	return written, nil
}

// step records how far a running job is. A percent of -1 keeps the last one.
func (m *Manager) step(id string, percent int, step, slicer string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, found := m.jobs[id]
	if !found || job.State != Running {
		return
	}

	if percent >= 0 {
		job.Progress = percent
	}

	if step != "" {
		job.Step = step
	}

	if slicer != "" {
		job.Slicer = slicer
	}
}

// objectsOf is the objects the G-code labels, each with the build item it
// came from when the project accounts for it.
func objectsOf(labels []threemf.LabelledObject, prepared project.Prepared) []Object {
	matched := prepared.Labels(labels)
	out := make([]Object, 0, len(labels))

	for _, label := range labels {
		held := Object{ID: label.ID, Name: label.Name, Polygon: label.Polygon}

		if match, ok := matched[label.ID]; ok {
			item, instance := match.Item, match.Instance
			held.Item, held.Instance = &item, &instance
		}

		out = append(out, held)
	}

	return out
}
