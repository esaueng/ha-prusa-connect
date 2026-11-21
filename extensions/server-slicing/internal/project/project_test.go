package project_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/prusa3d/threemf/threemf"

	"github.com/prusa3d/server-slicing/internal/fixture"
	"github.com/prusa3d/server-slicing/internal/project"
)

// twoBeds is a project with two cubes on bed 0 and one on bed 1.
func twoBeds(t *testing.T) string {
	t.Helper()

	data, err := fixture.Project{
		At:       [][2]float64{{100, 100}, {fixture.Bed1X + 100, 100}, {130, 100}},
		Settings: fixture.Bed200,
	}.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "two-beds.3mf")
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// printable reads which build items a prepared project prints, on the bed the
// command line slices.
func printable(t *testing.T, path string) map[int]bool {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}

	read, err := threemf.Read(context.Background(), file, info.Size(), threemf.Limits{})
	if err != nil {
		t.Fatal(err)
	}

	out := map[int]bool{}

	for _, ref := range read.Setups[0].Beds[0].Instances {
		if instance := read.Instance(ref); instance.Printable {
			out[instance.Item] = true
		}
	}

	return out
}

func TestTheChosenBedIsTheOneTheSlicerSlices(t *testing.T) {
	t.Parallel()

	src := twoBeds(t)

	cases := map[string]struct {
		selection project.Selection
		want      []int
	}{
		"the whole project": {project.Selection{}, []int{0, 2}},
		"the second bed":    {project.Selection{Bed: 1}, []int{1}},
		"one item of bed 0": {project.Selection{Items: []int{2}}, []int{2}},
	}

	for name, held := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			prepared, err := project.ThreeMF{}.Prepare(
				t.Context(),
				src,
				filepath.Join(t.TempDir(), "p.3mf"),
				held.selection,
			)
			if err != nil {
				t.Fatal(err)
			}

			if prepared.Major() != 2 || prepared.Application() != "PrusaSlicer-2.9.6" {
				t.Fatalf("the project reads as saved by %q, major %d", prepared.Application(), prepared.Major())
			}

			got := printable(t, prepared.Path)
			if len(got) != len(held.want) {
				t.Fatalf("bed 0 prints %v, want %v", got, held.want)
			}

			for _, item := range held.want {
				if !got[item] {
					t.Fatalf("bed 0 prints %v, want %v", got, held.want)
				}
			}
		})
	}
}

// The objects of a file sliced from bed 1 are named by the build items they
// came from, although the G-code places them on bed 0.
func TestTheLabelsOfASlicedBedNameTheirItems(t *testing.T) {
	t.Parallel()

	prepared, err := project.ThreeMF{}.Prepare(t.Context(), twoBeds(t), filepath.Join(t.TempDir(), "p.3mf"),
		project.Selection{Bed: 1})
	if err != nil {
		t.Fatal(err)
	}

	matched := prepared.Labels([]threemf.LabelledObject{{
		ID: 0, Name: "Cube",
		Polygon: []threemf.Point{{X: 105, Y: 105}, {X: 95, Y: 105}, {X: 95, Y: 95}, {X: 105, Y: 95}},
	}})

	if match, ok := matched[0]; !ok || match.Item != 1 {
		t.Fatalf("the label matched %+v", matched)
	}
}

func TestASelectionTheProjectDoesNotHaveIsRefused(t *testing.T) {
	t.Parallel()

	for _, selection := range []project.Selection{{Bed: 5}, {Items: []int{1}}, {Setup: 1}} {
		_, err := project.ThreeMF{}.Prepare(t.Context(), twoBeds(t), filepath.Join(t.TempDir(), "p.3mf"), selection)
		if !errors.Is(err, project.ErrSelection) {
			t.Errorf("the selection %+v answered %v", selection, err)
		}
	}
}

func TestAFileThatIsNotAProjectIsRefused(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "p.3mf")
	if err := os.WriteFile(path, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := (project.ThreeMF{}).Prepare(
		t.Context(),
		path,
		path+".out",
		project.Selection{},
	); !errors.Is(
		err,
		project.ErrNotAProject,
	) {
		t.Fatalf("a file that is not a zip answered %v", err)
	}
}

func TestASelectionIsHeldToWhatAProjectHolds(t *testing.T) {
	t.Parallel()

	for _, bad := range []project.Selection{
		{Bed: -1}, {Bed: 9}, {Setup: 64}, {Items: []int{1, 1}}, {Items: []int{-2}},
		{Items: []int{0}, Positions: []threemf.Position{{Item: 0, X: 50, Y: 50}}},
		{Positions: []threemf.Position{{Item: -1, X: 50, Y: 50}}},
	} {
		if err := bad.Validate(); !errors.Is(err, project.ErrSelection) {
			t.Errorf("%+v was accepted", bad)
		}
	}

	if err := (project.Selection{Bed: 8, Items: []int{0, 3}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

// A project another slicer saved is refused before the slicer runs, and the
// refusal names that slicer. A project that names no program is refused too.
func TestAProjectPrusaSlicerDidNotSaveIsRefused(t *testing.T) {
	t.Parallel()

	for _, held := range []struct {
		project fixture.Project
		savedBy string
	}{
		{fixture.Project{At: [][2]float64{{100, 100}}, Application: "OrcaSlicer-2.3.0"}, "OrcaSlicer 2.3.0"},
		{fixture.Project{At: [][2]float64{{100, 100}}, Plain: true}, "a program that does not name itself"},
	} {
		data, err := held.project.Bytes()
		if err != nil {
			t.Fatal(err)
		}

		path := filepath.Join(t.TempDir(), "foreign.3mf")
		if err = os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}

		_, err = (project.ThreeMF{}).Prepare(t.Context(), path, path+".out", project.Selection{})

		var foreign *project.NotPrusaSlicerError
		if !errors.As(err, &foreign) || foreign.SavedBy != held.savedBy {
			t.Fatalf("a project of %q answered %v", held.savedBy, err)
		}

		if _, statErr := os.Stat(path + ".out"); !os.IsNotExist(statErr) {
			t.Fatalf("a refused project was rewritten: %v", statErr)
		}
	}
}

// Copies arranged onto the second bed are what the slicer slices, and the
// labels of the sliced file name the object each copy is of.
func TestPositionsPutCopiesOnTheChosenBed(t *testing.T) {
	t.Parallel()

	src := twoBeds(t)

	file, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}

	read, err := threemf.Read(t.Context(), file, info.Size(), threemf.Limits{})
	if err != nil {
		t.Fatal(err)
	}

	plates, err := threemf.Arrange(read, []threemf.Piece{{Item: 0, Copies: 4}}, threemf.ArrangeOptions{})
	if err != nil || len(plates) != 1 {
		t.Fatalf("Arrange: %v, %d plates", err, len(plates))
	}

	prepared, err := project.ThreeMF{}.Prepare(t.Context(), src, filepath.Join(t.TempDir(), "p.3mf"),
		project.Selection{Bed: 1, Positions: plates[0].Positions})
	if err != nil {
		t.Fatal(err)
	}

	if got := printable(t, prepared.Path); len(got) != 4 {
		t.Fatalf("bed 0 prints %v, want 4 copies", got)
	}

	if prepared.Project == nil || prepared.Project.Items() != read.Items()+3 || prepared.Bed != (threemf.BedRef{}) {
		t.Fatalf("the prepared project holds %d items on %+v", prepared.Project.Items(), prepared.Bed)
	}

	first := plates[0].Positions[0]
	matched := prepared.Labels([]threemf.LabelledObject{{
		ID: 0, Name: "Cube",
		Polygon: []threemf.Point{
			{X: first.X + 5, Y: first.Y + 5}, {X: first.X - 5, Y: first.Y + 5},
			{X: first.X - 5, Y: first.Y - 5}, {X: first.X + 5, Y: first.Y - 5},
		},
	}})

	if match, ok := matched[0]; !ok || match.Instance.Object != 0 {
		t.Fatalf("the label matched %+v", matched)
	}
}

// scripted writes a PrusaSlicer 2 project of three cubes whose settings end
// with the lines given.
func scripted(t *testing.T, lines ...string) string {
	t.Helper()

	data, err := fixture.Project{
		At:       [][2]float64{{100, 100}, {fixture.Bed1X + 100, 100}, {130, 100}},
		Settings: append(append([]string{}, fixture.Bed200...), lines...),
	}.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "scripted.3mf")
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// ps3Scripted is the PrusaSlicer 3 project of the threemf tests with a
// post-processing script in its first setup.
func ps3Scripted(t *testing.T) string {
	t.Helper()

	const name = "Metadata/PrusaSlicer3_project.json"

	data, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "threemf", "threemf", "testdata", "prusaslicer-3.0.0-alpha12-two-setups.3mf"))
	if err != nil {
		t.Fatal(err)
	}

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	w := zip.NewWriter(&out)

	for _, f := range r.File {
		body, openErr := f.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}

		text, readErr := io.ReadAll(body)
		_ = body.Close()

		if readErr != nil {
			t.Fatal(readErr)
		}

		if f.Name == name {
			edited := bytes.Replace(text, []byte(`"post_process": []`), []byte(`"post_process": ["/bin/sh -c id"]`), 1)
			if bytes.Equal(edited, text) {
				t.Fatal("the project has no empty post_process to replace")
			}

			text = edited
		}

		entry, createErr := w.Create(f.Name)
		if createErr != nil {
			t.Fatal(createErr)
		}

		if _, err = entry.Write(text); err != nil {
			t.Fatal(err)
		}
	}

	if err = w.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "ps3-scripted.3mf")
	if err = os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// A project with a post-processing script is refused before anything is
// written for the slicer, whatever the selection, and whether or not the 3MF
// reader can read the rest of it.
func TestAProjectWithAScriptIsRefused(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name      string
		path      func(*testing.T) string
		selection project.Selection
		limits    threemf.Limits
		key       string
	}{
		{"PrusaSlicer 2, whole", func(t *testing.T) string {
			t.Helper()

			return scripted(t, "post_process = /usr/bin/touch /tmp/owned")
		}, project.Selection{}, threemf.Limits{}, "post_process"},
		{"PrusaSlicer 2, one item", func(t *testing.T) string {
			t.Helper()

			return scripted(t, "post_process = /usr/bin/touch /tmp/owned")
		}, project.Selection{Items: []int{0}}, threemf.Limits{}, "post_process"},
		{"PrusaSlicer 2, too large for the reader", func(t *testing.T) string {
			t.Helper()

			return scripted(t, "post_process = /usr/bin/touch /tmp/owned")
		}, project.Selection{}, threemf.Limits{MaxItems: 1}, "post_process"},
		{"PrusaSlicer 2, an output name with a folder, too large for the reader", func(t *testing.T) string {
			t.Helper()

			return scripted(t, "output_filename_format = ../../owned.gcode")
		}, project.Selection{}, threemf.Limits{MaxItems: 1}, "output_filename_format"},
		{"PrusaSlicer 3, whole", ps3Scripted, project.Selection{}, threemf.Limits{}, "post_process"},
		{"PrusaSlicer 3, too large for the reader", ps3Scripted, project.Selection{},
			threemf.Limits{MaxItems: 1}, "post_process"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			src := c.path(t)
			dst := filepath.Join(t.TempDir(), "prepared.3mf")

			prepared, err := project.ThreeMF{Limits: c.limits}.Prepare(t.Context(), src, dst, c.selection)

			var refused *project.UnsafeSettingsError
			if !errors.As(err, &refused) || refused.Key != c.key {
				t.Fatalf("answered %+v, %v", prepared, err)
			}

			if prepared.Path != "" {
				t.Fatalf("a refused project is given to the slicer as %s", prepared.Path)
			}

			if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
				t.Fatalf("a refused project was rewritten: %v", statErr)
			}
		})
	}
}

// The settings PrusaSlicer saves in every project, an empty script and the
// default output name, are sliced as before. A project too large for the
// reader is still sliced as saved, once its settings are checked.
func TestTheSettingsPrusaSlicerSavesAreSliced(t *testing.T) {
	t.Parallel()

	src := scripted(t, "output_filename_format = [input_filename_base].gcode", "post_process = ")

	prepared, err := project.ThreeMF{}.Prepare(t.Context(), src, filepath.Join(t.TempDir(), "p.3mf"),
		project.Selection{})
	if err != nil || prepared.Project == nil {
		t.Fatalf("answered %+v, %v", prepared, err)
	}

	prepared, err = project.ThreeMF{Limits: threemf.Limits{MaxItems: 1}}.Prepare(
		t.Context(), src, filepath.Join(t.TempDir(), "p.3mf"), project.Selection{})
	if err != nil || prepared.Path != src || prepared.Warning == "" {
		t.Fatalf("a project too large for the reader answered %+v, %v", prepared, err)
	}
}

// Settings too large to check are refused, so a project too large for the
// reader never reaches the slicer unchecked.
func TestSettingsTooLargeToCheckAreRefused(t *testing.T) {
	t.Parallel()

	src := scripted(t, "post_process = ")

	_, err := project.ThreeMF{Limits: threemf.Limits{MaxItems: 1, MaxMetadataBytes: 16}}.Prepare(
		t.Context(), src, filepath.Join(t.TempDir(), "p.3mf"), project.Selection{})
	if !errors.Is(err, project.ErrNotAProject) {
		t.Fatalf("answered %v", err)
	}
}
