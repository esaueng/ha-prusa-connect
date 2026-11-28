package settings_test

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/prusa3d/server-slicing/internal/settings"
)

func pointer[T any](value T) *T { return &value }

func TestPrintSettingsRefuseWhatTheSlicerWouldRefuse(t *testing.T) {
	t.Parallel()

	for name, bad := range map[string]settings.PrintSettings{
		"a layer too thin":   {LayerHeightMM: pointer(0.01)},
		"a layer too thick":  {LayerHeightMM: pointer(0.8)},
		"a layer of NaN":     {LayerHeightMM: pointer(math.NaN())},
		"negative infill":    {InfillPercent: pointer(-1)},
		"infill over 100":    {InfillPercent: pointer(101)},
		"no walls":           {Walls: pointer(0)},
		"too many walls":     {Walls: pointer(21)},
		"a negative brim":    {BrimWidthMM: pointer(-1.0)},
		"a brim too wide":    {BrimWidthMM: pointer(51.0)},
		"an unknown support": {Supports: "trees"},
	} {
		if err := bad.Check(); !errors.Is(err, settings.ErrInvalid) {
			t.Errorf("%s: Check answered %v, want ErrInvalid", name, err)
		}
	}

	good := settings.PrintSettings{
		LayerHeightMM: pointer(0.2), InfillPercent: pointer(0), Walls: pointer(20),
		Supports: settings.SupportsBuildPlate, BrimWidthMM: pointer(0.0),
	}
	if err := good.Check(); err != nil {
		t.Fatalf("Check refused settings within the bounds: %v", err)
	}
	if err := (settings.PrintSettings{}).Check(); err != nil {
		t.Fatalf("Check refused settings that change nothing: %v", err)
	}
}

func TestPrintSettingsReplaceTheProfilesKeysAndAddTheMissingOnes(t *testing.T) {
	t.Parallel()

	const profile = "layer_height = 0.15\nfill_density = 20%\nperimeters = 2\n" +
		"support_material = 0\nbrim_width = 0\nbrim_type = no_brim\nfilament_type = PETG\n"

	got := settings.PrintSettings{
		LayerHeightMM: pointer(0.28), InfillPercent: pointer(15), Walls: pointer(3),
		Supports: settings.SupportsEverywhere, BrimWidthMM: pointer(5.0),
	}.Apply(profile)

	want := "layer_height = 0.28\nfill_density = 15%\nperimeters = 3\n" +
		"support_material = 1\nbrim_width = 5\nbrim_type = outer_only\nfilament_type = PETG\n" +
		"support_material_auto = 1\nsupport_material_buildplate_only = 0\n"
	if got != want {
		t.Fatalf("the profile with the settings is\n%s\nwant\n%s", got, want)
	}

	if unchanged := (settings.PrintSettings{}).Apply(profile); unchanged != profile {
		t.Fatalf("settings that change nothing rewrote the profile into\n%s", unchanged)
	}

	noBrim := settings.PrintSettings{BrimWidthMM: pointer(0.0), Supports: settings.SupportsOff}.Apply(profile)
	if noBrim != "layer_height = 0.15\nfill_density = 20%\nperimeters = 2\n"+
		"support_material = 0\nbrim_width = 0\nbrim_type = no_brim\nfilament_type = PETG\n"+
		"support_material_buildplate_only = 0\n" {
		t.Fatalf("no brim and no supports wrote\n%s", noBrim)
	}
}

func TestPrepareSettingsReadsWhatEachProfilePrintsWith(t *testing.T) {
	t.Parallel()

	got := settings.PrepareSettings(map[string]string{
		"petg": "filament_type = PETG;PLA\nlayer_height = 0.2\nfill_density = 15%\nperimeters = 2\n" +
			"support_material = 1\nsupport_material_buildplate_only = 1\nbrim_width = 3\nbrim_type = no_brim\n",
		"bare": "filament_type = \"PLA\"\nlayer_height = thick\n",
	})
	want := map[string]settings.ProfileSettings{
		"petg": {FilamentType: "PETG", PrintSettings: settings.PrintSettings{
			LayerHeightMM: pointer(0.2), InfillPercent: pointer(15), Walls: pointer(2),
			Supports: settings.SupportsBuildPlate, BrimWidthMM: pointer(0.0),
		}},
		"bare": {FilamentType: "PLA"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the profile settings are %+v, want %+v", got, want)
	}
}
