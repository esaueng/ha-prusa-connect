package settings_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prusa3d/server-slicing/internal/settings"
)

func TestAnEmptyBlockTakesTheDefaults(t *testing.T) {
	t.Parallel()

	values, err := settings.Settings{}.Checked()
	if err != nil {
		t.Fatal(err)
	}

	if values.MaxJobs != 1 || values.Timeout != 30*time.Minute ||
		!values.GitHubReleases || values.MaxInputBytes != 512<<20 {
		t.Fatalf("the defaults are %+v", values)
	}
}

func TestASettingOutsideItsBoundsIsRefused(t *testing.T) {
	t.Parallel()

	zero, huge := 0, 100_000

	cases := map[string]settings.Settings{
		"no worker":            {MaxJobs: &zero},
		"a day and more":       {TimeoutMinutes: &huge},
		"a manifest over http": {ManifestURL: "http://builds.example.com/m.json"},
		"a relative manifest":  {ManifestURL: "m.json"},
		"a relative binary":    {LocalBinaries: []string{"prusa-slicer"}},
	}

	for name, held := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := held.Checked(); !errors.Is(err, settings.ErrInvalid) {
				t.Fatalf("answered %v", err)
			}
		})
	}
}

func TestSavedSTLProfiles(t *testing.T) {
	t.Parallel()

	config := "printer_technology = FFF\r\nbed_shape = 0x0,200x0,200x200,0x200\r\n" +
		"max_print_height = 200\r\nnozzle_diameter = 0.4\r\ngcode_flavor = marlin2\r\n" +
		"filament_type = PETG\r\nlayer_height = 0.2\r\n"
	checked, err := (settings.Settings{STLProfiles: map[string]string{
		"core-one-petg": config + "output_filename_format = ../../outside.gcode\npost_process = \n",
	}}).Checked()
	if err != nil {
		t.Fatal(err)
	}
	held := checked.STLProfiles["core-one-petg"]
	if !strings.Contains(held, "filament_type = PETG") || strings.Contains(held, "output_filename_format") ||
		strings.Contains(held, "post_process") {
		t.Fatalf("the prepared profile holds %q", held)
	}

	for _, value := range []string{
		config + "post_process = /usr/bin/false\n",
		config + "post_process = \"\";/usr/bin/false\n",
		config + "filament_type = PLA\n",
		"[printer:Core One]\n" + config,
		strings.ReplaceAll(config, "FFF", "SLA"),
		"printer_technology = FFF\n",
	} {
		if _, err = (settings.Settings{STLProfiles: map[string]string{"test": value}}).Checked(); !errors.Is(
			err,
			settings.ErrInvalid,
		) {
			t.Errorf("an invalid config was accepted: %v", err)
		}
	}
}

func TestRunMemoryBytes(t *testing.T) {
	t.Parallel()

	const mib = 1 << 20

	for _, tc := range []struct {
		name    string
		budget  int64
		ok      bool
		maxJobs int
		want    int64
	}{
		{"one job", 2560 * mib, true, 1, 2496 * mib},
		{"two jobs share it", 2560 * mib, true, 2, 1248 * mib},
		{"no budget", 0, false, 1, 0},
		{"a budget below the reserve", 32 * mib, true, 1, mib},
		{"no jobs counts as one", 2560 * mib, true, 0, 2496 * mib},
	} {
		if got := settings.RunMemoryBytes(tc.budget, tc.ok, tc.maxJobs); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}
