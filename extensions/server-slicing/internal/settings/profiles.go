package settings

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxSTLProfiles  = 16
	maxProfileBytes = 64 << 10
)

var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

// checkedProfiles accepts a flat PrusaSlicer config export for each name.
// Normalizing the parsed entries keeps the checked text and the text the
// slicer reads in agreement.
func checkedProfiles(held map[string]string) (map[string]string, error) {
	if len(held) > maxSTLProfiles {
		return nil, fmt.Errorf("%w: stl_profiles takes up to %d profiles", ErrInvalid, maxSTLProfiles)
	}

	profiles := make(map[string]string, len(held))
	for name, raw := range held {
		if !profileName.MatchString(name) {
			return nil, fmt.Errorf(
				"%w: STL profile names take 1 to 48 lowercase letters, digits or hyphens and begin with a letter or digit",
				ErrInvalid,
			)
		}
		config, err := checkedProfile(raw)
		if err != nil {
			return nil, fmt.Errorf("STL profile %s: %w", name, err)
		}
		profiles[name] = config
	}

	return profiles, nil
}

var settingName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func checkedProfile(raw string) (string, error) {
	if len(raw) > maxProfileBytes || !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("%w: use a UTF-8 PrusaSlicer config export up to %d bytes", ErrInvalid, maxProfileBytes)
	}

	var normalized strings.Builder
	seen := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimPrefix(raw, "\ufeff"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		before, after, found := strings.Cut(line, "=")
		key, value := strings.TrimSpace(before), strings.TrimSpace(after)
		if !found || !settingName.MatchString(key) || strings.ContainsRune(value, '\r') {
			return "", fmt.Errorf("%w: use File > Export > Export Config in PrusaSlicer", ErrInvalid)
		}
		if _, duplicate := seen[key]; duplicate {
			return "", fmt.Errorf("%w: the config repeats %s", ErrInvalid, key)
		}
		seen[key] = value

		if key == "post_process" || key == "output_filename_format" {
			continue
		}

		fmt.Fprintf(&normalized, "%s = %s\n", key, value)
	}

	if scripts := seen["post_process"]; scripts != "" && scripts != `""` {
		return "", fmt.Errorf("%w: clear the post-processing scripts before exporting", ErrInvalid)
	}
	if seen["printer_technology"] != "FFF" {
		return "", fmt.Errorf("%w: export an FFF printer config", ErrInvalid)
	}
	for _, key := range []string{"bed_shape", "max_print_height", "nozzle_diameter", "gcode_flavor", "filament_type", "layer_height"} {
		if seen[key] == "" {
			return "", fmt.Errorf("%w: the exported config needs %s", ErrInvalid, key)
		}
	}

	return normalized.String(), nil
}
