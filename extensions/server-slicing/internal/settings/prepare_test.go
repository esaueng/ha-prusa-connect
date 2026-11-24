package settings_test

import (
	"reflect"
	"testing"

	"github.com/prusa3d/server-slicing/internal/settings"
)

func TestPrepareOnlyListsRectangularPrintVolumes(t *testing.T) {
	t.Parallel()

	got := settings.PrepareBeds(map[string]string{
		"rectangle": "bed_shape = -20x30,180x30,180x240,-20x240\nmax_print_height = 250.5\n",
		"angled":    "bed_shape = 0x0,50x1,49x49,0x53\nmax_print_height = 50\n",
		"crossed":   "bed_shape = 0x0,10x10,0x10,10x0\nmax_print_height = 50\n",
		"no-height": "bed_shape = 0x0,50x0,50x50,0x50\nmax_print_height = NaN\n",
		"repeated":  "bed_shape = 0x0,50x0,50x50,50x50\nmax_print_height = 50\n",
	})
	want := map[string]settings.STLBed{
		"rectangle": {MinX: -20, MinY: 30, MaxX: 180, MaxY: 240, Height: 250.5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the print volumes are %v, want %v", got, want)
	}
}
