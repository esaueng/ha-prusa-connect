package settings

import (
	"math"
	"strconv"
	"strings"
)

// STLBed is the rectangular print volume the Prepare workspace can display.
// All coordinates and the height are in millimetres.
type STLBed struct {
	MinX   float64 `json:"min_x"`
	MinY   float64 `json:"min_y"`
	MaxX   float64 `json:"max_x"`
	MaxY   float64 `json:"max_y"`
	Height float64 `json:"height"`
}

// PrepareBeds exposes only print volumes from the checked configs.
func PrepareBeds(profiles map[string]string) map[string]STLBed {
	beds := make(map[string]STLBed, len(profiles))
	for name, config := range profiles {
		var shape, height string

		for line := range strings.SplitSeq(config, "\n") {
			key, value, _ := strings.Cut(line, " = ")
			switch key {
			case "bed_shape":
				shape = value
			case "max_print_height":
				height = value
			}
		}

		if bed, ok := rectangularBed(shape, height); ok {
			beds[name] = bed
		}
	}

	return beds
}

type corner struct{ x, y float64 }

func rectangularBed(shape, height string) (STLBed, bool) {
	points := strings.Split(shape, ",")
	const corners = 4
	if len(points) != corners {
		return STLBed{}, false
	}

	var polygon [corners]corner
	known := make(map[corner]bool, corners)
	bed := STLBed{MinX: math.Inf(1), MinY: math.Inf(1), MaxX: math.Inf(-1), MaxY: math.Inf(-1)}
	for i, point := range points {
		first, second, ok := strings.Cut(point, "x")
		x, xOK := bedNumber(first)
		y, yOK := bedNumber(second)
		at := corner{x, y}
		if !ok || !xOK || !yOK || known[at] {
			return STLBed{}, false
		}

		known[at] = true
		polygon[i] = at
		bed.MinX, bed.MaxX = min(bed.MinX, x), max(bed.MaxX, x)
		bed.MinY, bed.MaxY = min(bed.MinY, y), max(bed.MaxY, y)
	}

	for i, at := range polygon {
		next := polygon[(i+1)%corners]
		if at.x != next.x && at.y != next.y {
			return STLBed{}, false
		}
	}

	var ok bool
	bed.Height, ok = bedNumber(height)

	return bed, ok && bed.Height > 0 && bed.MinX < bed.MaxX && bed.MinY < bed.MaxY
}

func bedNumber(value string) (float64, bool) {
	const maxMM = 10000

	n, err := strconv.ParseFloat(strings.TrimSpace(value), 64)

	return n, err == nil && math.Abs(n) <= maxMM
}
