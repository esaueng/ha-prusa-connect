// Package runner runs the PrusaSlicer command line once, for one job.
//
// The slicer is a separate process in a process group of its own. Its
// arguments are a list and never pass through a shell, its environment is
// built from a short list, its home is the job's own directory, and its time
// and memory are bounded. Its output is read line by line into a bounded tail,
// which is where a job's error comes from.
//
// The exit status of PrusaSlicer does not say whether it wrote a file: a
// project with nothing on the bed exits with 0 and writes nothing. So the
// result is the file on disk, checked to exist and not to be empty, and its
// first bytes say whether it is binary or text G-code.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prusa3d/server-slicing/internal/sandbox"
)

// Format is what kind of G-code a file holds.
type Format string

// The formats of a result.
const (
	// Binary is Prusa's binary G-code, .bgcode.
	Binary Format = "bgcode"
	// Text is plain G-code, .gcode.
	Text Format = "gcode"
)

// Extension is the file name extension of a format, with its dot.
func (f Format) Extension() string { return "." + string(f) }

// Spec is one run.
type Spec struct {
	// Binary is the slicer's executable and Args the arguments its build puts
	// before every other one.
	Binary string
	Args   []string
	// Input is the model, and Dir the job's own directory. The run writes
	// only under Dir. Config names the checked settings for an STL input.
	Input  string
	Dir    string
	Config string
	// Format asks for binary or text G-code. Empty keeps what the project
	// says.
	Format Format
	// Threads bounds the slicer's threads, and zero lets it choose.
	Threads int
	// Timeout ends the run. MemoryBytes ends it when the slicer's process
	// group holds more memory than that, and zero sets no bound.
	Timeout     time.Duration
	MemoryBytes int64
}

// Result is a run that wrote a file.
type Result struct {
	Output   string
	Format   Format
	Size     int64
	Duration time.Duration
	// PeakBytes is the most memory the slicer held, as far as the watch saw
	// it, and zero where the host cannot read it.
	PeakBytes int64
	// Log is the end of what the slicer printed.
	Log []string
}

// The ways a run fails. Each one carries the end of what the slicer printed.
var (
	// ErrNothingToPrint reports a project with no object on the bed that is
	// printed, or every object outside it.
	ErrNothingToPrint = errors.New("the slicer found nothing to print on the bed")
	// ErrNoOutput reports a run that ended without writing a file.
	ErrNoOutput = errors.New("the slicer wrote no file")
	// ErrFailed reports a slicer that stopped with an error status.
	ErrFailed = errors.New("the slicer stopped with an error")
	// ErrTimeout reports a run that took longer than its bound.
	ErrTimeout = errors.New("the slicer took longer than the time it is given")
	// ErrMemory reports a slicer that held more memory than its bound.
	ErrMemory = errors.New("the slicer held more memory than it is given")
	// ErrStart reports a slicer that could not be started.
	ErrStart = errors.New("the slicer could not be started")
)

// RunError is a run that failed, with the end of what the slicer printed.
type RunError struct {
	Reason error
	Log    []string
}

func (f *RunError) Error() string {
	if len(f.Log) == 0 {
		return f.Reason.Error()
	}

	return f.Reason.Error() + ": " + f.Log[len(f.Log)-1]
}

func (f *RunError) Unwrap() error { return f.Reason }

// Finished is the progress of a run that is done.
const Finished = 100

// binaryMagic is how a binary G-code file starts.
var binaryMagic = []byte("GCDE")

// Progress reports how far the slicer is, from 0 to 100, and the step it
// named. A slicer that prints no percentage reports -1 and the last line.
type Progress func(percent int, step string)

// The bounds on what a run keeps of the slicer's output.
const (
	keptLines   = 60
	maxLineSize = 2 << 10
)

// watchEvery is how often the memory of the slicer's process group is read.
const watchEvery = 500 * time.Millisecond

// outputBase is the name the slicer is told to write, before the extension.
const outputBase = "result"

// Run slices one project and answers the file it wrote.
func Run(ctx context.Context, spec Spec, progress Progress) (Result, error) {
	home := filepath.Join(spec.Dir, "home")
	outputs := filepath.Join(spec.Dir, "output")

	for _, dir := range []string{home, outputs, filepath.Join(home, "datadir")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return Result{}, &RunError{Reason: fmt.Errorf("%w: make %s: %w", ErrStart, filepath.Base(dir), err)}
		}
	}

	if spec.Timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}

	format := spec.Format
	if format == "" {
		format = Text
	}

	target := filepath.Join(outputs, outputBase+format.Extension())

	//nolint:gosec // an installed slicer, with its arguments as a list
	command := exec.CommandContext(ctx, spec.Binary, Arguments(spec, home, target)...)
	command.Dir = spec.Dir
	command.Env = sandbox.Env(home)
	command.Stdin = nil
	sandbox.Prepare(command)

	tail := &lines{}
	stdout, stderr := tail.writer(progress), tail.writer(nil)
	command.Stdout, command.Stderr = stdout, stderr

	started := time.Now()

	if err := command.Start(); err != nil {
		return Result{}, &RunError{Reason: fmt.Errorf("%w: %w", ErrStart, err)}
	}

	watched := watch(command, spec.MemoryBytes)
	waitErr := command.Wait()
	peak, overMemory := watched()
	peak = max(peak, maxRSS(command.ProcessState))

	stdout.flush()
	stderr.flush()

	log := tail.kept()

	switch {
	case overMemory:
		return Result{}, &RunError{Reason: fmt.Errorf("%w: %d bytes", ErrMemory, spec.MemoryBytes), Log: log}
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return Result{}, &RunError{Reason: fmt.Errorf("%w: %s", ErrTimeout, spec.Timeout), Log: log}
	case ctx.Err() != nil:
		return Result{}, ctx.Err()
	case waitErr != nil:
		return Result{}, &RunError{Reason: fmt.Errorf("%w: %w", ErrFailed, waitErr), Log: log}
	}

	written, err := findOutput(outputs, target)
	if err != nil {
		if nothingToPrint(log) {
			return Result{}, &RunError{Reason: ErrNothingToPrint, Log: log}
		}

		return Result{}, &RunError{Reason: err, Log: log}
	}

	found, err := Detect(written)
	if err != nil {
		return Result{}, &RunError{Reason: err, Log: log}
	}

	info, err := os.Stat(written)
	if err != nil {
		return Result{}, &RunError{Reason: fmt.Errorf("%w: %w", ErrNoOutput, err), Log: log}
	}

	return Result{
		Output: written, Format: found, Size: info.Size(),
		Duration: time.Since(started), PeakBytes: peak, Log: log,
	}, nil
}

// Arguments is the command line of one run, after the build's own arguments.
// --datadir points the slicer at an empty directory of the job's, so it reads
// no profile the server's user keeps, and the project's own settings are the
// ones it slices with.
func Arguments(spec Spec, home, target string) []string {
	out := append([]string{}, spec.Args...)
	out = append(out, "--export-gcode", "--datadir", filepath.Join(home, "datadir"))
	if spec.Config != "" {
		out = append(out, "--load", spec.Config)
	}

	switch spec.Format {
	case Binary:
		out = append(out, "--binary-gcode")
	case Text:
		out = append(out, "--binary-gcode=0")
	}

	if spec.Threads > 0 {
		out = append(out, "--threads", strconv.Itoa(spec.Threads))
	}

	return append(out, "--output", target, spec.Input)
}

// findOutput is the file the slicer wrote. It is the target, or the one file
// in the output directory when the slicer chose another extension.
func findOutput(dir, target string) (string, error) {
	if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() {
		if info.Size() == 0 {
			return "", fmt.Errorf("%w: the file it wrote is empty", ErrNoOutput)
		}

		return target, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoOutput, err)
	}

	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), outputBase) {
			continue
		}

		info, infoErr := entry.Info()
		if infoErr == nil && info.Size() > 0 {
			return filepath.Join(dir, entry.Name()), nil
		}
	}

	return "", ErrNoOutput
}

// Detect reads the first bytes of a file and says which format it holds. A
// binary G-code file starts with GCDE, and a text one with a comment or a
// command.
func Detect(name string) (Format, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoOutput, err)
	}

	defer func() { _ = file.Close() }() // read-only

	head := make([]byte, len(binaryMagic))

	read, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("%w: %w", ErrNoOutput, err)
	}

	if bytes.Equal(head[:read], binaryMagic) {
		return Binary, nil
	}

	return Text, nil
}

// nothingToPrint reads the messages PrusaSlicer prints for a bed with nothing
// to slice, after which it exits with 0 and writes nothing.
func nothingToPrint(log []string) bool {
	for _, line := range log {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "nothing to print") || strings.Contains(lower, "outside of the print volume") ||
			strings.Contains(lower, "no object") {
			return true
		}
	}

	return false
}

// percentLine is one progress line of PrusaSlicer 2: "45 => Making infill".
var percentLine = regexp.MustCompile(`^(\d{1,3})(?:%)? => (.+)$`)

// lines keeps the last lines of both streams of a run, in the order they
// arrived.
type lines struct {
	mu   sync.Mutex
	held []string
}

func (l *lines) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.held = append(l.held, line)
	if len(l.held) > keptLines {
		l.held = l.held[len(l.held)-keptLines:]
	}
}

func (l *lines) kept() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]string{}, l.held...)
}

// writer is one stream. progress, when set, is told each progress line.
func (l *lines) writer(progress Progress) *lineWriter {
	return &lineWriter{into: l, progress: progress}
}

// lineWriter splits a stream into lines, cuts each at maxLineSize, and keeps
// them.
type lineWriter struct {
	into     *lines
	progress Progress
	partial  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			w.emit()

			continue
		}

		if len(w.partial) < maxLineSize {
			w.partial = append(w.partial, b)
		}
	}

	return len(p), nil
}

func (w *lineWriter) flush() {
	if len(w.partial) > 0 {
		w.emit()
	}
}

func (w *lineWriter) emit() {
	line := strings.TrimSpace(strings.ToValidUTF8(string(w.partial), "?"))
	w.partial = w.partial[:0]

	if line == "" {
		return
	}

	w.into.add(line)

	if w.progress == nil {
		return
	}

	if found := percentLine.FindStringSubmatch(line); found != nil {
		percent, _ := strconv.Atoi(found[1])
		w.progress(min(percent, Finished), found[2])

		return
	}

	w.progress(-1, line)
}
