package threemf

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// The settings a project may not carry into a slicer that runs on a server.
const (
	// settingPostProcess names programs PrusaSlicer runs on the G-code it
	// wrote. A project that sets it runs a command of its author's choice.
	settingPostProcess = "post_process"
	// settingOutputName is the name PrusaSlicer gives the G-code when the
	// command line names only a folder.
	settingOutputName = "output_filename_format"
)

// maxSettingsDepth bounds how deep CheckSettings follows nested JSON.
const maxSettingsDepth = 256

// ErrUnsafeSettings is a project whose settings make PrusaSlicer run a
// program, or name a G-code file in another folder. An UnsafeSettingError
// says which setting.
var ErrUnsafeSettings = errors.New("threemf: the project's settings are not safe to slice")

// UnsafeSettingError is one setting that CheckSettings refuses.
type UnsafeSettingError struct {
	// Entry is the file in the archive that holds the setting.
	Entry string
	// Key is the name of the setting, such as post_process.
	Key string
}

func (e *UnsafeSettingError) Error() string {
	switch strings.ToLower(e.Key) {
	case settingPostProcess:
		return fmt.Sprintf("threemf: %s sets post_process, a post-processing script, "+
			"and a project with one is not sliced on a server", e.Entry)
	default:
		return fmt.Sprintf("threemf: %s sets %s to a path with a folder in it, "+
			"and a project with one is not sliced on a server", e.Entry, e.Key)
	}
}

// Is makes errors.Is(err, ErrUnsafeSettings) true for every UnsafeSettingError.
func (e *UnsafeSettingError) Is(target error) bool { return target == ErrUnsafeSettings }

// settingsEntries are the files PrusaSlicer 2 and 3 read settings from. The
// slicer finds them without regard to case, so CheckSettings does too.
func settingsEntries() []string {
	return []string{ps2Config, ps2ModelConfig, ps3Project}
}

// CheckSettings reads the settings files of the 3MF file in r and refuses a
// project PrusaSlicer would run a program for: one with a post_process that
// is not empty, or an output_filename_format with a folder in it. It reads
// only those files, each bounded by limits.MaxMetadataBytes, so it answers
// for a project too large for Read as well.
//
// It returns an UnsafeSettingError, ErrFormat for a settings file it cannot
// read, or ErrLimit for one larger than the limits.
func CheckSettings(ctx context.Context, r io.ReaderAt, size int64, limits Limits) error {
	limits = limits.withDefaults()

	a, err := openArchive(r, size, limits)
	if err != nil {
		return err
	}

	return checkArchiveSettings(ctx, a)
}

// normalizedEntryName cleans a ZIP entry name the way PrusaSlicer does before
// matching settings files: the name ends at its first NUL, as miniz reads it
// as a C string, backslashes become slashes, and leading slashes and
// redundant dot segments are stripped.
func normalizedEntryName(raw string) string {
	if cut, _, found := strings.Cut(raw, "\x00"); found {
		raw = cut
	}

	slashed := strings.ReplaceAll(raw, "\\", "/")
	trimmed := strings.TrimLeft(slashed, "/")
	cleaned := path.Clean(trimmed)

	return strings.TrimPrefix(cleaned, "./")
}

// settingsKind reports which settings file a ZIP entry name resolves to, after
// matching PrusaSlicer's path separator normalization and case-folding, or ""
// when the entry is not a settings file. The slicer compares the whole path,
// so a file of the same name in another folder is not one it reads.
func settingsKind(raw string) string {
	normalized := normalizedEntryName(raw)

	for _, want := range settingsEntries() {
		if strings.EqualFold(normalized, want) {
			return want
		}
	}

	return ""
}

// checkArchiveSettings checks every entry whose name is a settings file's,
// in any case or path spelling: a zip may hold backslashes, leading slashes,
// a NUL-terminated name or duplicate entries differing only in case, and the
// slicer may read any of them.
func checkArchiveSettings(ctx context.Context, a *archive) error {
	for _, f := range a.zip.File {
		name := settingsKind(f.Name)
		if name == "" {
			continue
		}

		data, err := readAll(ctx, f, a.limits.MaxMetadataBytes, "MaxMetadataBytes")
		if err != nil {
			return err
		}

		key, err := unsafeIn(name, data)
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrFormat, f.Name, err)
		}

		if key != "" {
			return &UnsafeSettingError{Entry: f.Name, Key: key}
		}
	}

	return nil
}

// unsafeIn answers the first unsafe setting in one settings file, by the
// file's kind.
func unsafeIn(name string, data []byte) (string, error) {
	switch name {
	case ps2Config:
		return unsafeInText(string(data)), nil
	case ps2ModelConfig:
		return unsafeInXML(data)
	default:
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()

		key, err := unsafeInJSON(dec, "", 0)
		if err != nil || key != "" {
			return key, err
		}

		if _, err = dec.Token(); !errors.Is(err, io.EOF) {
			return "", errors.New("data after the JSON document")
		}

		return key, nil
	}
}

// unsafeInText reads "; key = value" lines, as in Slic3r_PE.config, and
// answers the first unsafe setting. A line without the ';' counts too.
func unsafeInText(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		rest := strings.TrimLeft(strings.TrimSpace(line), ";")

		key, value, ok := strings.Cut(rest, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		if unsafeValue(key, strings.TrimSpace(value)) {
			return key
		}
	}

	return ""
}

// unsafeValue reports whether one value of a setting is unsafe. A list of
// scripts in the text form is `"a";"b"`, and an empty list or a list of empty
// names is safe.
func unsafeValue(key, value string) bool {
	switch strings.ToLower(key) {
	case settingPostProcess:
		return strings.Trim(value, "\"; \t\r\n") != ""
	case settingOutputName:
		return unsafeOutputName(value)
	}

	return false
}

// unsafeOutputName reports a file name with a folder in it. The command line
// names the output file, so PrusaSlicer does not use this setting. A name
// with a folder is refused all the same.
func unsafeOutputName(value string) bool {
	return strings.ContainsAny(value, "/\\\x00")
}

// unsafeInXML reads the metadata of Slic3r_PE_model.config, which sets a
// setting with key and value attributes on any element.
func unsafeInXML(data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return "", nil
		}

		if err != nil {
			return "", fmt.Errorf("XML: %w", err)
		}

		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		var key, value string

		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "key":
				key = attr.Value
			case "value":
				value = attr.Value
			}
		}

		if unsafeValue(key, value) {
			return key, nil
		}
	}
}

// unsafeInJSON reads one JSON value whose member name is key and answers the
// first unsafe setting in it. It reads the tokens one by one, so a member
// written twice is checked both times. A string that holds settings as text
// is read as text too.
func unsafeInJSON(dec *json.Decoder, key string, depth int) (string, error) {
	if depth > maxSettingsDepth {
		return "", fmt.Errorf("JSON nested deeper than %d", maxSettingsDepth)
	}

	tok, err := dec.Token()
	if err != nil {
		return "", fmt.Errorf("JSON: %w", err)
	}

	script := strings.EqualFold(key, settingPostProcess)

	switch v := tok.(type) {
	case json.Delim:
		if v == '{' && script {
			return key, nil
		}

		return unsafeInJSONContainer(dec, v, key, depth)
	case string:
		if unsafeValue(key, v) {
			return key, nil
		}

		if strings.Contains(v, settingPostProcess) || strings.Contains(v, settingOutputName) {
			return unsafeInText(v), nil
		}
	case nil:
	default:
		// A number or a boolean is no script PrusaSlicer reads, and no name.
		if script {
			return key, nil
		}
	}

	return "", nil
}

// unsafeInJSONContainer reads the members of an object, or the elements of
// an array, after its opening token. An element of an array keeps the
// array's member name.
func unsafeInJSONContainer(dec *json.Decoder, open json.Delim, key string, depth int) (string, error) {
	for dec.More() {
		name := key

		if open == '{' {
			tok, err := dec.Token()
			if err != nil {
				return "", fmt.Errorf("JSON: %w", err)
			}

			member, ok := tok.(string)
			if !ok {
				return "", errors.New("JSON: a key was expected")
			}

			name = member
		}

		found, err := unsafeInJSON(dec, name, depth+1)
		if err != nil || found != "" {
			return found, err
		}
	}

	if _, err := dec.Token(); err != nil {
		return "", fmt.Errorf("JSON: %w", err)
	}

	return "", nil
}

// clearPS2Scripts writes every post_process line of Slic3r_PE.config as an
// empty one. Rewrite refuses a project with a script already, so this
// changes only a line whose value is empty quotes.
func (rw *rewriting) clearPS2Scripts(ctx context.Context) error {
	f := rw.a.entry(ps2Config)
	if f == nil {
		return nil
	}

	data, err := readAll(ctx, f, rw.limits.MaxMetadataBytes, "MaxMetadataBytes")
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	changed := false

	for i, line := range lines {
		rest := strings.TrimLeft(strings.TrimSpace(line), ";")

		key, value, ok := strings.Cut(rest, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), settingPostProcess) || strings.TrimSpace(value) == "" {
			continue
		}

		lines[i], changed = "; "+settingPostProcess+" = ", true
		if strings.HasSuffix(line, "\r") {
			lines[i] += "\r"
		}
	}

	if !changed {
		return nil
	}

	return rw.replace(f.Name, []byte(strings.Join(lines, "\n")))
}

// ps3ClearScripts empties post_process in the print settings of a setup.
// Rewrite refuses a project with a script already, so this changes only a
// list of empty names.
func ps3ClearScripts(container *ordered) error {
	raw := container.get("configuration")
	if raw == nil {
		return nil
	}

	configuration, err := parseOrdered(raw)
	if err != nil {
		return err
	}

	raw = configuration.get("print_settings")
	if raw == nil {
		return nil
	}

	settings, err := parseOrdered(raw)
	if err != nil {
		return err
	}

	scripts := settings.get(settingPostProcess)
	if scripts == nil || string(bytes.TrimSpace(scripts)) == "[]" {
		return nil
	}

	if err = settings.set(settingPostProcess, []string{}); err != nil {
		return err
	}

	out, err := settings.marshal()
	if err != nil {
		return err
	}

	if err = configuration.set("print_settings", json.RawMessage(out)); err != nil {
		return err
	}

	if out, err = configuration.marshal(); err != nil {
		return err
	}

	return container.set("configuration", json.RawMessage(out))
}
