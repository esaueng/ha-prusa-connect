package settings

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Supports is where the slicer may add support material.
type Supports string

// The support choices a job may name.
const (
	SupportsOff        Supports = "off"
	SupportsBuildPlate Supports = "build_plate"
	SupportsEverywhere Supports = "everywhere"
)

// The bounds of PrintSettings. A layer above the nozzle's width still fails
// in the slicer, because the module does not know the nozzle of every profile.
const (
	MinLayerHeightMM = 0.05
	MaxLayerHeightMM = 0.6
	MaxWalls         = 20
	MaxBrimWidthMM   = 50
	maxPercent       = 100
)

// PrintSettings are the settings a person changes over a saved STL profile
// for one job. A field that is absent keeps the profile's value.
type PrintSettings struct {
	LayerHeightMM *float64 `json:"layer_height_mm,omitempty"`
	InfillPercent *int     `json:"infill_percent,omitempty"`
	Walls         *int     `json:"walls,omitempty"`
	Supports      Supports `json:"supports,omitempty"`
	BrimWidthMM   *float64 `json:"brim_width_mm,omitempty"`
}

// Check refuses a value the slicer would refuse or read in another way.
func (p PrintSettings) Check() error {
	switch {
	case p.LayerHeightMM != nil && !within(*p.LayerHeightMM, MinLayerHeightMM, MaxLayerHeightMM):
		return fmt.Errorf("%w: the layer height is from %g to %g mm", ErrInvalid, MinLayerHeightMM, MaxLayerHeightMM)
	case p.InfillPercent != nil && (*p.InfillPercent < 0 || *p.InfillPercent > maxPercent):
		return fmt.Errorf("%w: the infill is from 0 to 100 %%", ErrInvalid)
	case p.Walls != nil && (*p.Walls < 1 || *p.Walls > MaxWalls):
		return fmt.Errorf("%w: the walls are from 1 to %d", ErrInvalid, MaxWalls)
	case p.BrimWidthMM != nil && !within(*p.BrimWidthMM, 0, MaxBrimWidthMM):
		return fmt.Errorf("%w: the brim is from 0 to %d mm wide", ErrInvalid, MaxBrimWidthMM)
	}

	switch p.Supports {
	case "", SupportsOff, SupportsBuildPlate, SupportsEverywhere:
		return nil
	default:
		return fmt.Errorf("%w: supports is off, build_plate or everywhere", ErrInvalid)
	}
}

func within(value, low, high float64) bool {
	return !math.IsNaN(value) && value >= low && value <= high
}

// Apply writes the settings over a checked profile, key by key. A key the
// profile lacks is added at the end, where the slicer reads it last.
func (p PrintSettings) Apply(config string) string {
	values := map[string]string{}
	if p.LayerHeightMM != nil {
		values["layer_height"] = formatNumber(*p.LayerHeightMM)
	}
	if p.InfillPercent != nil {
		values["fill_density"] = strconv.Itoa(*p.InfillPercent) + "%"
	}
	if p.Walls != nil {
		values["perimeters"] = strconv.Itoa(*p.Walls)
	}
	switch p.Supports {
	case SupportsOff:
		values["support_material"] = "0"
		values["support_material_buildplate_only"] = "0"
	case SupportsBuildPlate:
		values["support_material"] = "1"
		values["support_material_auto"] = "1"
		values["support_material_buildplate_only"] = "1"
	case SupportsEverywhere:
		values["support_material"] = "1"
		values["support_material_auto"] = "1"
		values["support_material_buildplate_only"] = "0"
	}
	if p.BrimWidthMM != nil {
		values["brim_width"] = formatNumber(*p.BrimWidthMM)
		// PrusaSlicer 2.4 and newer ignore the width while brim_type is no_brim.
		values["brim_type"] = "outer_only"
		if *p.BrimWidthMM == 0 {
			values["brim_type"] = "no_brim"
		}
	}

	if len(values) == 0 {
		return config
	}

	var out strings.Builder
	for line := range strings.SplitSeq(strings.TrimSuffix(config, "\n"), "\n") {
		key, _, _ := strings.Cut(line, " = ")
		if value, found := values[key]; found {
			fmt.Fprintf(&out, "%s = %s\n", key, value)
			delete(values, key)

			continue
		}
		if line != "" {
			out.WriteString(line + "\n")
		}
	}
	for _, key := range slices.Sorted(maps.Keys(values)) {
		fmt.Fprintf(&out, "%s = %s\n", key, values[key])
	}

	return out.String()
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// ProfileSettings is what a saved profile prints with before a person changes
// anything, so a page shows the real starting values.
type ProfileSettings struct {
	// FilamentType is the material of the profile's first extruder, such as PETG.
	FilamentType string `json:"filament_type,omitempty"`
	PrintSettings
}

// PrepareSettings reads the starting settings of each checked profile. A
// value the profile does not state, or states in a form not read here, is
// left out.
func PrepareSettings(profiles map[string]string) map[string]ProfileSettings {
	out := make(map[string]ProfileSettings, len(profiles))
	for name, config := range profiles {
		seen := map[string]string{}
		for line := range strings.SplitSeq(config, "\n") {
			key, value, found := strings.Cut(line, " = ")
			if found {
				seen[key] = strings.TrimSpace(value)
			}
		}

		var profile ProfileSettings
		profile.FilamentType = strings.Trim(strings.Split(seen["filament_type"], ";")[0], `"`)
		profile.LayerHeightMM = number(seen["layer_height"])
		profile.BrimWidthMM = number(seen["brim_width"])
		if seen["brim_type"] == "no_brim" {
			zero := 0.0
			profile.BrimWidthMM = &zero
		}
		profile.Walls = whole(seen["perimeters"])
		profile.InfillPercent = whole(strings.TrimSuffix(seen["fill_density"], "%"))

		switch {
		case seen["support_material"] == "0":
			profile.Supports = SupportsOff
		case seen["support_material"] == "1" && seen["support_material_buildplate_only"] == "1":
			profile.Supports = SupportsBuildPlate
		case seen["support_material"] == "1":
			profile.Supports = SupportsEverywhere
		}

		out[name] = profile
	}

	return out
}

func number(text string) *float64 {
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}

	return &value
}

func whole(text string) *int {
	value, err := strconv.Atoi(text)
	if err != nil {
		return nil
	}

	return &value
}
