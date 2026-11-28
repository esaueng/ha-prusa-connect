// Package jobs is the queue of slicing jobs: what a person asked for, the
// workers that slice it, and the record of each job a page reads.
//
// A job is taken while the person's request is open, and that is where the
// person's own permissions are checked. It is sliced later, by a worker that
// acts as the module itself. So before the worker reads the project and before
// it writes the result, it asks the server whether the person may still read
// and write that storage. A person who lost the permission in between gets a
// failed job rather than a file.
package jobs

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/prusa3d/threemf/threemf"

	"github.com/prusa3d/server-slicing/internal/gcodemeta"
	"github.com/prusa3d/server-slicing/internal/project"
	"github.com/prusa3d/server-slicing/internal/runner"
	"github.com/prusa3d/server-slicing/internal/settings"
)

// State is where a job is.
type State string

// The states of a job. Done, Failed and Canceled are final.
const (
	Queued   State = "queued"
	Running  State = "running"
	Done     State = "done"
	Failed   State = "failed"
	Canceled State = "canceled"
)

// Final reports a state a job never leaves.
func (s State) Final() bool { return s == Done || s == Failed || s == Canceled }

// Code names why a job failed. A client branches on it.
type Code string

// The reasons a job fails.
const (
	CodeSourceNotFound  Code = "server-slicing/SOURCE_NOT_FOUND"
	CodeSourceDenied    Code = "server-slicing/SOURCE_DENIED"
	CodeSourceTooLarge  Code = "server-slicing/SOURCE_TOO_LARGE"
	CodeNotAProject     Code = "server-slicing/NOT_A_PROJECT"
	CodeNotPrusaSlicer  Code = "server-slicing/NOT_PRUSASLICER"
	CodeNoSlicer        Code = "server-slicing/NO_SLICER"
	CodeSlicerTooOld    Code = "server-slicing/SLICER_TOO_OLD"
	CodeSelection       Code = "server-slicing/SELECTION_INVALID"
	CodeNothingToPrint  Code = "server-slicing/NOTHING_TO_PRINT"
	CodeNoOutput        Code = "server-slicing/NO_OUTPUT"
	CodeSlicerFailed    Code = "server-slicing/SLICER_FAILED"
	CodeTimeout         Code = "server-slicing/TIMEOUT"
	CodeMemory          Code = "server-slicing/MEMORY"
	CodeOutputExists    Code = "server-slicing/OUTPUT_EXISTS"
	CodeOutputDenied    Code = "server-slicing/OUTPUT_DENIED"
	CodeOutputFailed    Code = "server-slicing/OUTPUT_FAILED"
	CodeInterrupted     Code = "server-slicing/INTERRUPTED"
	CodeInternalFailure Code = "server-slicing/INTERNAL"
)

// Problem is why a job failed.
type Problem struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	// Log is the end of what the slicer printed, when it ran.
	Log []string `json:"log,omitempty"`
	// Existing is the file that stopped the job, set with CodeOutputExists.
	// It is the file the job would have written, so a client that wants
	// that output reads it here rather than deriving the name.
	Existing *File `json:"existing,omitempty"`
}

// File is one file in a storage.
type File struct {
	Storage string `json:"storage"`
	Path    string `json:"path"`
}

// Source is the project a job slices: a file in a storage, or a file uploaded
// with the request.
type Source struct {
	Storage string `json:"storage,omitempty"`
	Path    string `json:"path,omitempty"`
	// Upload is the name of an uploaded project, and empty for a file in a
	// storage.
	Upload string `json:"upload,omitempty"`
}

// Name is the file name of the project, without its folder.
func (s Source) Name() string {
	if s.Upload != "" {
		return s.Upload
	}

	return path.Base(s.Path)
}

// Target is where the G-code goes. Path is the file without its extension,
// which the format the slicer wrote decides. OutputPath turns it into the
// name the module writes.
type Target struct {
	Storage string `json:"storage"`
	Path    string `json:"path"`
}

// Object is one object the G-code labels, and the build item of the project
// it is, when the module could tell.
type Object struct {
	// ID is the number the G-code labels it with, M486 S<id>, and the
	// number a printer cancels it by.
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Item is the project's build item the object came from, and Instance
	// the object and the copy of it that item is.
	Item     *int                 `json:"item,omitempty"`
	Instance *threemf.InstanceRef `json:"instance,omitempty"`
	// Polygon is the object's outline on the bed it was sliced on.
	Polygon []threemf.Point `json:"polygon,omitempty"`
}

// Result is the G-code a job wrote and what it says about the print.
type Result struct {
	Storage string        `json:"storage"`
	Path    string        `json:"path"`
	Size    int64         `json:"size"`
	SHA256  string        `json:"sha256,omitempty"`
	Format  runner.Format `json:"format"`
	// Slicer is the version that sliced it.
	Slicer          string `json:"slicer"`
	DurationMS      int64  `json:"duration_ms"`
	PeakMemoryBytes int64  `json:"peak_memory_bytes,omitempty"`

	EstimatedSeconds int64                 `json:"estimated_seconds,omitempty"`
	FilamentMM       float64               `json:"filament_mm,omitempty"`
	FilamentG        float64               `json:"filament_g,omitempty"`
	FilamentType     string                `json:"filament_type,omitempty"`
	PrinterModel     string                `json:"printer_model,omitempty"`
	NozzleMM         float64               `json:"nozzle_mm,omitempty"`
	LayerHeightMM    float64               `json:"layer_height_mm,omitempty"`
	Objects          []Object              `json:"objects"`
	Thumbnails       []gcodemeta.Thumbnail `json:"thumbnails"`
	// Warnings are things that went differently from what was asked, such as
	// text G-code from a slicer asked for binary.
	Warnings []string `json:"warnings,omitempty"`
}

// Job is one slicing job.
type Job struct {
	ID    string `json:"id"`
	State State  `json:"state"`
	// Progress is from 0 to 100, and -1 while the slicer says nothing about
	// how far it is.
	Progress int    `json:"progress"`
	Step     string `json:"step,omitempty"`
	// By is who asked for the job.
	By            string            `json:"by"`
	Source        Source            `json:"source"`
	Output        Target            `json:"output"`
	Slicer        string            `json:"slicer,omitempty"`
	Profile       string            `json:"profile,omitempty"`
	Selection     project.Selection `json:"selection"`
	Format        runner.Format     `json:"format,omitempty"`
	Overwrite     bool              `json:"overwrite"`
	KeepPlacement bool              `json:"keep_placement,omitempty"`
	// Settings change the saved STL profile for this job alone.
	Settings *settings.PrintSettings `json:"settings,omitempty"`

	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	Error  *Problem `json:"error,omitempty"`
	Result *Result  `json:"result,omitempty"`
}

// A printer that stores files on FAT gives each long name an 8.3 short
// name, made from the first six characters it may keep. Two names that begin
// alike shorten to one entry, and a printer seen in the field then lost the
// second file of a transfer it reported finished. So a file name the module
// writes begins with a hash of the target, which differs from the start for
// every target, and the stem asked for follows it.
const (
	// namePrefixBytes is the length of the hash at the start of the name.
	namePrefixBytes = 8
	// printerNameBytes is the longest file name, with its extension, that
	// the Buddy firmware keeps since 6.4.0.
	printerNameBytes = 167
)

// OutputPath is the file a job writes: the folder of the stem, then a hash of
// the target, a dash, the stem's own name and the extension of the format.
// The same target always gives the same name, so overwrite still finds it.
// The stem's name is cut so the whole name stays within printerNameBytes.
func OutputPath(target Target, format runner.Format) string {
	folder, name := path.Split(target.Path)
	sum := sha256.Sum256([]byte(target.Storage + "\x00" + target.Path))
	prefix := hex.EncodeToString(sum[:])[:namePrefixBytes]
	extension := format.Extension()

	name = strings.TrimRight(clipName(name, printerNameBytes-len(prefix)-1-len(extension)), ". ")
	if name == "" {
		return folder + prefix + extension
	}

	return folder + prefix + "-" + name + extension
}

// clipName cuts a name to at most limit bytes without splitting a character.
func clipName(name string, limit int) string {
	if len(name) <= limit {
		return name
	}

	for limit > 0 && !utf8.RuneStart(name[limit]) {
		limit--
	}

	return name[:limit]
}

// Stem is a path without a G-code extension, the form a Target keeps.
func Stem(name string) string {
	lower := strings.ToLower(name)

	for _, extension := range []string{".bgcode", ".gcode", ".3mf"} {
		if strings.HasSuffix(lower, extension) {
			return name[:len(name)-len(extension)]
		}
	}

	return name
}
