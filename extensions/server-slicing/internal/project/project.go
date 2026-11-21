// Package project prepares a 3MF project for one slicer run: one bed of one
// printer setup, and some or all of the build items on it.
//
// The PrusaSlicer command line slices one bed and has no option to leave an
// object out. So a selection is made by rewriting the project before the
// slicer runs: the chosen bed becomes the one the command line slices, and
// every build item that is not chosen is marked as not printable. The 3MF
// reading and rewriting is the threemf package's. This package decides when to call
// it and what a failure means for a job.
package project

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/prusa3d/threemf/threemf"
)

// Selection is what a job slices: one bed of one printer setup, and the build
// items on it.
//
// A build item is one placed copy of an object, an <item> of the project's
// <build>, counted from zero in the order the project lists them. Items empty
// means every printable item on the bed.
type Selection struct {
	// Setup is the printer setup. A PrusaSlicer 2 project has one, setup 0.
	Setup int   `json:"setup,omitempty"`
	Bed   int   `json:"bed"`
	Items []int `json:"items,omitempty"`
	// Positions put copies of build items on the bed instead of Items, as
	// threemf.Arrange answers them: the first position of an item moves it,
	// and each further one adds a copy. A selection names Items or
	// Positions, not both.
	Positions []threemf.Position `json:"positions,omitempty"`
}

// Whole reports the selection of a job that names nothing: the first bed of
// the first setup, every printable item on it.
func (s Selection) Whole() bool {
	return s.Setup == 0 && s.Bed == 0 && len(s.Items) == 0 && len(s.Positions) == 0
}

// The bounds on a selection. A PrusaSlicer project holds at most nine beds.
const (
	MaxSetups = 64
	MaxBeds   = 9
	MaxItems  = 4096
)

// ErrSelection reports a selection the project does not have, such as a bed
// it has no item on.
var ErrSelection = errors.New("the selection is not one this project has")

// ErrNotAProject reports a file that is not a 3MF project.
var ErrNotAProject = errors.New("the file is not a 3MF project")

// NotPrusaSlicerError reports a project that PrusaSlicer did not save. Only
// PrusaSlicer 2 and 3 projects are sliced: the module reads and rewrites the
// project for PrusaSlicer, and another slicer keeps its settings and its
// plates in files PrusaSlicer does not read.
type NotPrusaSlicerError struct {
	// SavedBy names the program that saved the project, such as
	// "OrcaSlicer 2.3.0".
	SavedBy string
}

func (e *NotPrusaSlicerError) Error() string {
	return "this project was saved by " + e.SavedBy + ", and only PrusaSlicer projects can be sliced"
}

// UnsafeSettingsError reports a project whose settings make PrusaSlicer run
// a program, or name a G-code file in another folder. The slicer runs on the
// server, so such a project is never given to it.
type UnsafeSettingsError struct {
	// Key is the setting, post_process or output_filename_format.
	Key string
}

func (e *UnsafeSettingsError) Error() string {
	if e.Key == "output_filename_format" {
		return "the project's output file name has a folder in it, and the server writes the G-code to a " +
			"name of its own. Remove the folder from Output filename format in PrusaSlicer's print " +
			"settings and save the project again"
	}

	return "the project has a post-processing script, and the server never runs a script a project " +
		"names. Clear Post-processing scripts in PrusaSlicer's print settings and save the project again"
}

// savedBy names the program that wrote a project, for a person to read.
func savedBy(producer threemf.Producer) string {
	switch {
	case producer.Name != "" && producer.Version != "":
		return producer.Name + " " + producer.Version
	case producer.Name != "":
		return producer.Name
	case producer.Application != "":
		return producer.Application
	default:
		return "a program that does not name itself"
	}
}

// Validate holds a selection to what any project can hold. Whether this
// project has it is known only once the project is read.
func (s Selection) Validate() error {
	switch {
	case s.Setup < 0 || s.Setup >= MaxSetups:
		return fmt.Errorf("%w: the setup is %d, and a project has setups 0 to %d", ErrSelection, s.Setup, MaxSetups-1)
	case s.Bed < 0 || s.Bed >= MaxBeds:
		return fmt.Errorf("%w: the bed is %d, and a project has beds 0 to %d", ErrSelection, s.Bed, MaxBeds-1)
	case len(s.Items) > MaxItems:
		return fmt.Errorf("%w: %d items, and the limit is %d", ErrSelection, len(s.Items), MaxItems)
	case len(s.Positions) > threemf.MaxPositions:
		return fmt.Errorf("%w: %d positions, and the limit is %d", ErrSelection, len(s.Positions), threemf.MaxPositions)
	case len(s.Positions) > 0 && len(s.Items) > 0:
		return fmt.Errorf("%w: a selection names items or positions, not both", ErrSelection)
	}

	for _, position := range s.Positions {
		if position.Item < 0 || math.IsNaN(position.X) || math.IsNaN(position.Y) ||
			math.IsInf(position.X, 0) || math.IsInf(position.Y, 0) {
			return fmt.Errorf("%w: a position of item %d is not a place on a bed", ErrSelection, position.Item)
		}
	}

	seen := map[int]bool{}

	for _, item := range s.Items {
		if item < 0 || seen[item] {
			return fmt.Errorf("%w: the item %d is negative or named twice", ErrSelection, item)
		}

		seen[item] = true
	}

	return nil
}

// Prepared is a project ready to slice.
type Prepared struct {
	// Path is the project the slicer is given.
	Path string
	// Project is the project as it was read, and nil when it could not be
	// read and the file is sliced as it is.
	Project *threemf.Project
	// Bed is the bed of Project that is sliced.
	Bed threemf.BedRef
	// Warning says why the file is sliced as it is, when it is.
	Warning string
}

// Major is the major version of the PrusaSlicer that saved the project, and
// zero when another program saved it or the project could not be read.
func (p Prepared) Major() int {
	if p.Project == nil || p.Project.Producer.Name != "PrusaSlicer" {
		return 0
	}

	major, _, _ := strings.Cut(p.Project.Producer.Version, ".")

	number, err := strconv.Atoi(major)
	if err != nil {
		return 0
	}

	return number
}

// Application is what saved the project, such as PrusaSlicer-2.9.6.
func (p Prepared) Application() string {
	if p.Project == nil {
		return ""
	}

	return p.Project.Producer.Application
}

// Labels names the build item behind each object a sliced file labels, by
// the object's id. An object the project cannot account for is left out.
func (p Prepared) Labels(labels []threemf.LabelledObject) map[int]threemf.LabelMatch {
	out := map[int]threemf.LabelMatch{}

	if p.Project == nil {
		return out
	}

	for _, match := range p.Project.MatchLabels(p.Bed, labels) {
		out[match.Label] = match
	}

	return out
}

// Preparer prepares a project for one selection. It writes what it needs to
// dst and leaves src as it is.
type Preparer interface {
	Prepare(ctx context.Context, src, dst string, selection Selection) (Prepared, error)
}

// ThreeMF prepares a project with the threemf package.
type ThreeMF struct {
	// Limits bound one read and one rewrite. A zero field takes the
	// library's default.
	Limits threemf.Limits
}

// Prepare reads the project and writes the copy the slicer is given. The copy
// is written for a whole selection too, because a PrusaSlicer 3 project may
// have saved another bed as the chosen one, and the command line slices that.
//
// A project too large for the 3MF reader is sliced as it is when the
// selection is whole, and refused otherwise. The settings of every project
// are checked first, that one too, so a project with a post-processing
// script never reaches the slicer.
func (t ThreeMF) Prepare(ctx context.Context, src, dst string, selection Selection) (Prepared, error) {
	file, err := os.Open(src)
	if err != nil {
		return Prepared{}, fmt.Errorf("open the project: %w", err)
	}

	defer func() { _ = file.Close() }() // read-only

	info, err := file.Stat()
	if err != nil {
		return Prepared{}, fmt.Errorf("open the project: %w", err)
	}

	if err = checkSettings(ctx, file, info.Size(), t.Limits); err != nil {
		return Prepared{}, err
	}

	read, err := threemf.Read(ctx, file, info.Size(), t.Limits)

	switch {
	case errors.Is(err, threemf.ErrLimit) && selection.Whole():
		return Prepared{Path: src, Warning: "the project is larger than the 3MF reader reads, so it is sliced as " +
			"it was saved and its objects are not matched to its build items"}, nil
	case errors.Is(err, threemf.ErrLimit):
		return Prepared{}, fmt.Errorf("%w: the project is larger than the 3MF reader reads: %w", ErrSelection, err)
	case err != nil:
		return Prepared{}, fmt.Errorf("%w: %w", ErrNotAProject, err)
	}

	// Every dialect but these two is another program's, the ones the library
	// adds later too.
	if dialect := read.Producer.Dialect; dialect != threemf.DialectPrusaSlicer2 &&
		dialect != threemf.DialectPrusaSlicer3 {
		return Prepared{}, &NotPrusaSlicerError{SavedBy: savedBy(read.Producer)}
	}

	bed := threemf.BedRef{Setup: selection.Setup, Bed: selection.Bed}

	var items []int
	if len(selection.Items) > 0 {
		items = selection.Items
	}

	if err = rewrite(
		ctx,
		dst,
		file,
		info.Size(),
		read,
		threemf.Selection{Bed: bed, Items: items, Positions: selection.Positions},
		t.Limits,
	); err != nil {
		return Prepared{}, err
	}

	if len(selection.Positions) == 0 {
		return Prepared{Path: dst, Project: read, Bed: bed}, nil
	}

	// The copies are build items of the rewritten project alone, so the
	// labels of the sliced file are matched against that project. The chosen
	// bed is its first bed: PrusaSlicer 2 moved it onto bed 0, and a
	// PrusaSlicer 3 project keeps only that bed.
	placed, err := readPrepared(ctx, dst, t.Limits)
	if err != nil {
		return Prepared{}, err
	}

	return Prepared{Path: dst, Project: placed, Bed: threemf.BedRef{}}, nil
}

// checkSettings refuses a project whose settings PrusaSlicer would run a
// program for. A settings file too large to check is refused as well,
// because nothing else reads it before the slicer does.
func checkSettings(ctx context.Context, src io.ReaderAt, size int64, limits threemf.Limits) error {
	err := threemf.CheckSettings(ctx, src, size, limits)

	var risky *threemf.UnsafeSettingError

	switch {
	case err == nil:
		return nil
	case errors.As(err, &risky):
		return &UnsafeSettingsError{Key: risky.Key}
	case errors.Is(err, threemf.ErrLimit):
		return fmt.Errorf("%w: its settings are larger than the module checks: %w", ErrNotAProject, err)
	case errors.Is(err, threemf.ErrFormat):
		return fmt.Errorf("%w: %w", ErrNotAProject, err)
	default:
		return fmt.Errorf("check the project's settings: %w", err)
	}
}

// readPrepared reads the project Prepare wrote.
func readPrepared(ctx context.Context, path string, limits threemf.Limits) (*threemf.Project, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read the prepared project: %w", err)
	}

	defer func() { _ = file.Close() }() // read-only

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("read the prepared project: %w", err)
	}

	read, err := threemf.Read(ctx, file, info.Size(), limits)
	if err != nil {
		return nil, fmt.Errorf("read the prepared project: %w", err)
	}

	return read, nil
}

func rewrite(
	ctx context.Context, dst string, src io.ReaderAt, size int64,
	read *threemf.Project, selection threemf.Selection, limits threemf.Limits,
) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("make the prepared project: %w", err)
	}

	err = threemf.Rewrite(ctx, out, src, size, read, selection, limits)

	// A full disk can report itself on Close alone.
	if closeErr := out.Close(); err == nil && closeErr != nil {
		err = fmt.Errorf("write the prepared project: %w", closeErr)
	}

	var risky *threemf.UnsafeSettingError

	switch {
	case errors.As(err, &risky):
		// Prepare checks the settings before it rewrites, so this answers a
		// file that changed between the two reads.
		return &UnsafeSettingsError{Key: risky.Key}
	case errors.Is(err, threemf.ErrUnsupported):
		// Prepare refuses another program's project before it rewrites, so
		// this answers a dialect the library refuses and Prepare lets through.
		return &NotPrusaSlicerError{SavedBy: savedBy(read.Producer)}
	case errors.Is(err, threemf.ErrSelection):
		return fmt.Errorf("%w: %w", ErrSelection, err)
	case errors.Is(err, threemf.ErrFormat):
		return fmt.Errorf("%w: %w", ErrNotAProject, err)
	case err != nil:
		return fmt.Errorf("prepare the project: %w", err)
	}

	return nil
}
