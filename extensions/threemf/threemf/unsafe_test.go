package threemf_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/prusa3d/threemf/threemf"
)

const (
	ps2ConfigEntry = "Metadata/Slic3r_PE.config"
	ps2ModelEntry  = "Metadata/Slic3r_PE_model.config"
	ps3Entry       = "Metadata/PrusaSlicer3_project.json"
)

// withEntry copies a 3MF file with the text of one entry changed by edit. An
// entry the file does not have is added with edit("").
func withEntry(t *testing.T, data []byte, name string, edit func(string) string) []byte {
	t.Helper()

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}

	var out bytes.Buffer

	w := zip.NewWriter(&out)
	found := false

	put := func(entry string, body []byte) {
		f, createErr := w.Create(entry)
		if createErr != nil {
			t.Fatalf("create %s: %v", entry, createErr)
		}

		if _, writeErr := f.Write(body); writeErr != nil {
			t.Fatalf("write %s: %v", entry, writeErr)
		}
	}

	for _, f := range r.File {
		body, openErr := f.Open()
		if openErr != nil {
			t.Fatalf("open %s: %v", f.Name, openErr)
		}

		text, readErr := io.ReadAll(body)
		_ = body.Close()

		if readErr != nil {
			t.Fatalf("read %s: %v", f.Name, readErr)
		}

		if f.Name == name {
			found = true
			text = []byte(edit(string(text)))
		}

		put(f.Name, text)
	}

	if !found {
		put(name, []byte(edit("")))
	}

	if err = w.Close(); err != nil {
		t.Fatalf("zip: %v", err)
	}

	return out.Bytes()
}

// replaceOnce replaces the first match of pattern, where $0 in with stands
// for the match, and fails the test when
// there is none, so a fixture that changes cannot make a case pass by itself.
func replaceOnce(t *testing.T, pattern, with string) func(string) string {
	t.Helper()

	re := regexp.MustCompile(pattern)

	return func(text string) string {
		at := re.FindStringIndex(text)
		if at == nil {
			t.Fatalf("no %q in the entry", pattern)
		}

		return text[:at[0]] + strings.ReplaceAll(with, "$0", text[at[0]:at[1]]) + text[at[1]:]
	}
}

func checkSettings(t *testing.T, data []byte, limits threemf.Limits) error {
	t.Helper()

	return threemf.CheckSettings(t.Context(), bytes.NewReader(data), int64(len(data)), limits)
}

// TestCheckSettingsAcceptsWhatPrusaSlicerSaves holds the check to the
// projects PrusaSlicer writes itself: an empty post_process and the default
// output_filename_format.
func TestCheckSettingsAcceptsWhatPrusaSlicerSaves(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("testdata/golden/*.3mf")
	if err != nil {
		t.Fatal(err)
	}

	files = append(files, fixture296, fixture281, fixture3Alpha)

	for _, file := range files {
		if err = checkSettings(t, readFile(t, file), threemf.Limits{}); err != nil {
			t.Errorf("%s: %v", file, err)
		}
	}
}

func TestCheckSettingsRefusesAScriptOrAFolder(t *testing.T) {
	t.Parallel()

	ps2, ps3 := readFile(t, fixture296), readFile(t, fixture3Alpha)

	for _, c := range []struct {
		name  string
		data  []byte
		entry string
		key   string
	}{
		{
			name: "PrusaSlicer 2 script",
			data: withEntry(t, ps2, ps2ConfigEntry,
				replaceOnce(t, `(?m)^; post_process = $`, "; post_process = /usr/bin/touch /tmp/owned")),
			entry: ps2ConfigEntry, key: "post_process",
		},
		{
			name: "PrusaSlicer 2 list of scripts",
			data: withEntry(t, ps2, ps2ConfigEntry,
				replaceOnce(t, `(?m)^; post_process = $`, `; post_process = "";"/bin/sh -c id"`)),
			entry: ps2ConfigEntry, key: "post_process",
		},
		{
			name: "PrusaSlicer 2 script without spaces",
			data: withEntry(t, ps2, ps2ConfigEntry,
				replaceOnce(t, `(?m)^; post_process = $`, ";post_process=/bin/sh")),
			entry: ps2ConfigEntry, key: "post_process",
		},
		{
			name: "PrusaSlicer 2 script in an entry named in another case",
			data: withEntry(t, ps2, "metadata/slic3r_pe.config", func(string) string {
				return "; post_process = /bin/sh\n"
			}),
			entry: "metadata/slic3r_pe.config", key: "post_process",
		},
		{
			name: "PrusaSlicer 2 script in an entry using a backslash separator",
			data: withEntry(t, ps2, `Metadata\Slic3r_PE.config`, func(string) string {
				return "; post_process = /bin/sh\n"
			}),
			entry: `Metadata\Slic3r_PE.config`, key: "post_process",
		},
		{
			name: "PrusaSlicer 2 script in an entry with a leading slash",
			data: withEntry(t, ps2, "/Metadata/Slic3r_PE.config", func(string) string {
				return "; post_process = /bin/sh\n"
			}),
			entry: "/Metadata/Slic3r_PE.config", key: "post_process",
		},
		{
			name: "PrusaSlicer 2 script in an entry with a dot segment",
			data: withEntry(t, ps2, "./Metadata/./Slic3r_PE.config", func(string) string {
				return "; post_process = /bin/sh\n"
			}),
			entry: "./Metadata/./Slic3r_PE.config", key: "post_process",
		},
		{
			name: "PrusaSlicer 3 script in an entry using a backslash separator",
			data: withEntry(t, ps3, `Metadata\PrusaSlicer3_project.json`, func(string) string {
				return `{"print_settings":{"post_process":["/bin/sh"]}}`
			}),
			entry: `Metadata\PrusaSlicer3_project.json`, key: "post_process",
		},
		{
			name: "PrusaSlicer 2 script of one object",
			data: withEntry(t, ps2, ps2ModelEntry, replaceOnce(t, `<object [^>]*>`,
				`$0<metadata type="object" key="post&#95;process" value="/bin/sh"/>`)),
			entry: ps2ModelEntry, key: "post_process",
		},
		{
			name: "PrusaSlicer 2 output name with a folder",
			data: withEntry(t, ps2, ps2ConfigEntry, replaceOnce(t,
				`(?m)^; output_filename_format = .*$`, "; output_filename_format = ../../x.gcode")),
			entry: ps2ConfigEntry, key: "output_filename_format",
		},
		{
			name: "PrusaSlicer 3 script",
			data: withEntry(t, ps3, ps3Entry,
				replaceOnce(t, `"post_process": \[\]`, `"post_process": ["/bin/sh -c id"]`)),
			entry: ps3Entry, key: "post_process",
		},
		{
			name: "PrusaSlicer 3 script written twice",
			data: withEntry(t, ps3, ps3Entry,
				replaceOnce(t, `"print_settings": \{`, `"print_settings": {"post_process": ["/bin/sh"],`)),
			entry: ps3Entry, key: "post_process",
		},
		{
			name: "PrusaSlicer 3 script under an escaped name",
			data: withEntry(t, ps3, ps3Entry,
				replaceOnce(t, `"post_process": \[\]`, `"post_process": "/bin/sh"`)),
			entry: ps3Entry, key: "post_process",
		},
		{
			name: "PrusaSlicer 3 script in settings written as text",
			data: withEntry(t, ps3, ps3Entry, replaceOnce(t,
				`"print_settings": \{`, `"print_settings": {"text": "; layer_height = 0.2\n; post_process = /bin/sh\n",`)),
			entry: ps3Entry, key: "post_process",
		},
		{
			name: "PrusaSlicer 3 output name with a folder",
			data: withEntry(t, ps3, ps3Entry, replaceOnce(t,
				`"output_filename_format": "[^"]*"`, `"output_filename_format": "..\\..\\x.gcode"`)),
			entry: ps3Entry, key: "output_filename_format",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			err := checkSettings(t, c.data, threemf.Limits{})

			var unsafe *threemf.UnsafeSettingError
			if !errors.Is(err, threemf.ErrUnsafeSettings) || !errors.As(err, &unsafe) {
				t.Fatalf("answered %v", err)
			}

			if unsafe.Entry != c.entry || !strings.EqualFold(unsafe.Key, c.key) {
				t.Fatalf("named %s in %s, want %s in %s", unsafe.Key, unsafe.Entry, c.key, c.entry)
			}
		})
	}
}

// An empty script in any form PrusaSlicer writes is no script.
func TestCheckSettingsAcceptsAnEmptyScript(t *testing.T) {
	t.Parallel()

	ps2, ps3 := readFile(t, fixture296), readFile(t, fixture3Alpha)

	for name, data := range map[string][]byte{
		"PrusaSlicer 2 empty quotes": withEntry(t, ps2, ps2ConfigEntry,
			replaceOnce(t, `(?m)^; post_process = $`, `; post_process = ""`)),
		"PrusaSlicer 3 list of an empty name": withEntry(t, ps3, ps3Entry,
			replaceOnce(t, `"post_process": \[\]`, `"post_process": [""]`)),
		"PrusaSlicer 3 null": withEntry(t, ps3, ps3Entry,
			replaceOnce(t, `"post_process": \[\]`, `"post_process": null`)),
	} {
		if err := checkSettings(t, data, threemf.Limits{}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The check reads only the settings files, so a project too large for Read
// is still checked. A settings file too large to check is ErrLimit.
func TestCheckSettingsReadsAProjectTooLargeForRead(t *testing.T) {
	t.Parallel()

	data := withEntry(t, readFile(t, fixture296), ps2ConfigEntry,
		replaceOnce(t, `(?m)^; post_process = $`, "; post_process = /bin/sh"))
	small := threemf.Limits{MaxItems: 1}

	if _, err := threemf.Read(t.Context(), bytes.NewReader(data), int64(len(data)), small); !errors.Is(
		err, threemf.ErrLimit) {
		t.Fatalf("Read answered %v, want ErrLimit", err)
	}

	if err := checkSettings(t, data, small); !errors.Is(err, threemf.ErrUnsafeSettings) {
		t.Fatalf("a project too large for Read answered %v", err)
	}

	if err := checkSettings(t, data, threemf.Limits{MaxMetadataBytes: 64}); !errors.Is(err, threemf.ErrLimit) {
		t.Fatalf("settings too large to check answered %v", err)
	}

	if err := checkSettings(t, []byte("not a zip"), threemf.Limits{}); !errors.Is(err, threemf.ErrFormat) {
		t.Fatalf("a file that is not a zip answered %v", err)
	}
}

func TestRewriteRefusesAScript(t *testing.T) {
	t.Parallel()

	for name, data := range map[string][]byte{
		"PrusaSlicer 2": withEntry(t, readFile(t, fixture296), ps2ConfigEntry,
			replaceOnce(t, `(?m)^; post_process = $`, "; post_process = /bin/sh")),
		"PrusaSlicer 3": withEntry(t, readFile(t, fixture3Alpha), ps3Entry,
			replaceOnce(t, `"post_process": \[\]`, `"post_process": ["/bin/sh"]`)),
	} {
		p := read(t, data)

		var out bytes.Buffer

		err := threemf.Rewrite(t.Context(), &out, bytes.NewReader(data), int64(len(data)), p,
			threemf.Selection{}, threemf.Limits{})
		if !errors.Is(err, threemf.ErrUnsafeSettings) {
			t.Errorf("%s: Rewrite answered %v", name, err)
		}
	}
}

// The copy Rewrite writes keeps post_process empty in the form the slicer
// reads most plainly.
func TestRewriteEmptiesTheScripts(t *testing.T) {
	t.Parallel()

	ps2 := withEntry(t, readFile(t, fixture296), ps2ConfigEntry,
		replaceOnce(t, `(?m)^; post_process = $`, `; post_process = ""`))
	out := rewrite(t, ps2, read(t, ps2), threemf.Selection{})

	if config := entryText(t, out, ps2ConfigEntry); !regexp.MustCompile(`(?m)^; post_process = $`).Match(config) {
		t.Errorf("PrusaSlicer 2 settings kept a script: %s", regexp.MustCompile(`(?m)^.*post_process.*$`).Find(config))
	}

	ps3 := withEntry(t, readFile(t, fixture3Alpha), ps3Entry,
		replaceOnce(t, `"post_process": \[\]`, `"post_process": [""]`))
	out = rewrite(t, ps3, read(t, ps3), threemf.Selection{})

	if project := entryText(t, out, ps3Entry); !bytes.Contains(project, []byte(`"post_process":[]`)) ||
		bytes.Contains(project, []byte(`"post_process": [""]`)) {
		t.Errorf("PrusaSlicer 3 project kept a script")
	}
}
