package main

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prusa3d/simple-queue/internal/settings"
)

// The manifest is read here the way an operator reads it, so an author who
// changes a default in the code and not in the manifest finds out from
// `go test` rather than from a server that behaves differently from the page
// describing it.
//
// It is read line by line and with no YAML library, so this test adds no
// dependency to a released binary's module graph.

// MaxSummaryBytes is how long the sentence beside a setting may be. It is one
// plain sentence a person reads on a settings pane.
const MaxSummaryBytes = 120

// TestTheDefaultsInTheManifestAreTheDefaultsInTheCode. What the manifest
// publishes is what this module does when nobody writes a config block.
func TestTheDefaultsInTheManifestAreTheDefaultsInTheCode(t *testing.T) {
	t.Parallel()

	declared := manifestOptions(t)

	inCode := map[string]string{
		"max_waiting":  strconv.Itoa(settings.DefaultMaxWaiting),
		"keep_history": strconv.Itoa(settings.DefaultKeepHistory),
		"send_timeout": shortDuration(settings.DefaultSendTimeout),
	}

	if len(declared) != len(inCode) {
		t.Fatalf("module.yaml declares %d settings and the code holds %d defaults", len(declared), len(inCode))
	}

	for _, option := range declared {
		wanted, known := inCode[option.Key]
		if !known {
			t.Errorf("module.yaml declares %s and the code holds no default for it", option.Key)

			continue
		}

		if option.Default != wanted {
			t.Errorf("the default of %s is %q in module.yaml and %q in the code",
				option.Key, option.Default, wanted)
		}
	}
}

// TestEverySettingCarriesOneShortSentence: a settings pane draws the sentence
// beside the key, and a paragraph does not fit there.
func TestEverySettingCarriesOneShortSentence(t *testing.T) {
	t.Parallel()

	for _, option := range manifestOptions(t) {
		if option.Summary == "" {
			t.Errorf("%s carries no sentence", option.Key)

			continue
		}

		if len(option.Summary) > MaxSummaryBytes {
			t.Errorf("the sentence for %s is %d bytes and the limit is %d",
				option.Key, len(option.Summary), MaxSummaryBytes)
		}

		if !strings.HasSuffix(option.Summary, ".") {
			t.Errorf("the sentence for %s does not end in a full stop: %q", option.Key, option.Summary)
		}

		if option.Type == "" {
			t.Errorf("%s declares no type", option.Key)
		}
	}
}

// TestTheReadmeKeyTableIsTheManifest: the key table of README.md has the same
// keys, types, defaults and sentences as the options list of module.yaml. An
// operator reads the README, and the host refuses a key module.yaml does not
// declare.
func TestTheReadmeKeyTableIsTheManifest(t *testing.T) {
	t.Parallel()

	declared := manifestOptions(t)
	written := readmeKeys(t)

	if len(declared) != len(written) {
		t.Fatalf("module.yaml declares %d keys and README.md lists %d", len(declared), len(written))
	}

	for index, option := range declared {
		row := written[index]

		if row.key != option.Key {
			t.Fatalf("row %d of the README table is %q and module.yaml declares %q",
				index, row.key, option.Key)
		}

		if row.shape != option.Type {
			t.Errorf("the type of %s is %q in the README and %q in module.yaml",
				row.key, row.shape, option.Type)
		}

		if want := "`" + option.Default + "`"; row.fallback != want {
			t.Errorf("the default of %s is %q in the README and %q in module.yaml",
				row.key, row.fallback, want)
		}

		if row.summary != option.Summary {
			t.Errorf("the sentence for %s is\n  README:      %s\n  module.yaml: %s",
				row.key, row.summary, option.Summary)
		}
	}
}

// TestTheManifestNamesWhatTheCodeIs: the name, the version, the slots the
// browser half registers, and the methods the module calls.
func TestTheManifestNamesWhatTheCodeIs(t *testing.T) {
	t.Parallel()

	document := manifest(t)

	for key, wanted := range map[string]string{
		"name":                name,
		"version":             string(release),
		"openapi":             "openapi.json",
		"kind":                "sink",
		"title":               "Print queue",
		"check_absent_people": "true",
	} {
		if found := scalar(document, key); found != wanted {
			t.Errorf("the manifest says %s: %q and the code says %q", key, found, wanted)
		}
	}

	for _, slot := range []string{"      - path: queue\n", "    device_tab:\n", "    device_card:\n", "    print_section:\n", "    device_file_action:\n", "    device_upload_action:\n"} {
		if !strings.Contains(document, slot) {
			t.Errorf("the manifest declares no %q, which ui/src/index.ts registers", strings.TrimSpace(slot))
		}
	}

	if !strings.Contains(document, "  entry: ui/index.js\n") {
		t.Error("the manifest declares no bundle entry, so the module would draw nothing")
	}

	// The queue watches printers, starts prints and reads the files it sends
	// as itself, and asks about each of them, so each is in the list
	// access.check holds it to.
	for _, permission := range []string{"observe", "operate", "read_storage"} {
		if !strings.Contains(document, "  - "+permission+"\n") {
			t.Errorf("the manifest does not ask for %s", permission)
		}
	}

	for _, capability := range []string{
		"device.list", "device.get", "device.jobs", "device.files", "device.print", "device.command",
		"storage.stat", "access.check", "bus.subscribe",
	} {
		if !strings.Contains(document, "  core-"+capability+": \"*\"\n") {
			t.Errorf("the manifest does not require %s, which the module calls", capability)
		}
	}

	// The queues are kept in the module's own database, which the operator
	// grants only to a manifest that asks for it.
	if !strings.Contains(document, "\nneeds:\n  database: true\n") {
		t.Error("the manifest does not ask for the database the module keeps its queues in")
	}
}

// shortDuration writes a duration the way an operator writes it, 2h15m and
// not 2h15m0s.
func shortDuration(d time.Duration) string {
	out := d.String()
	out = strings.TrimSuffix(out, "0s")

	return strings.TrimSuffix(out, "0m")
}

// declaredOption is one entry of the options list in module.yaml.
type declaredOption struct {
	Key     string
	Type    string
	Default string
	Summary string
}

// manifest reads module.yaml.
func manifest(t *testing.T) string {
	t.Helper()

	document, err := os.ReadFile("module.yaml")
	if err != nil {
		t.Fatalf("read module.yaml: %v", err)
	}

	return strings.ReplaceAll(string(document), "\r\n", "\n")
}

// scalar reads one top-level key of the manifest.
func scalar(document, key string) string {
	for line := range strings.SplitSeq(document, "\n") {
		rest, found := strings.CutPrefix(line, key+":")
		if found {
			return unquote(rest)
		}
	}

	return ""
}

// manifestOptions returns the options of module.yaml, in the order of the
// file.
func manifestOptions(t *testing.T) []declaredOption {
	t.Helper()

	_, after, found := strings.Cut(manifest(t), "\noptions:\n")
	if !found {
		t.Fatal("module.yaml declares no options block")
	}

	var (
		out  []declaredOption
		held *declaredOption
	)

	for line := range strings.SplitSeq(after, "\n") {
		trimmed := strings.TrimSpace(line)

		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
			continue
		case !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "-"):
			// The first line at the left margin ends the options block.
			return finish(t, out, held)
		case strings.HasPrefix(trimmed, "- "):
			out = finished(out, held)
			held = &declaredOption{}
			trimmed = strings.TrimPrefix(trimmed, "- ")
		}

		if held == nil {
			continue
		}

		key, value, split := strings.Cut(trimmed, ": ")
		if !split {
			continue
		}

		switch key {
		case "key":
			held.Key = unquote(value)
		case "type":
			held.Type = unquote(value)
		case "default":
			held.Default = unquote(value)
		case "summary":
			held.Summary = unquote(value)
		}
	}

	return finish(t, out, held)
}

// finish appends the last entry and fails the test when the list is empty.
func finish(t *testing.T, out []declaredOption, held *declaredOption) []declaredOption {
	t.Helper()

	out = finished(out, held)
	if len(out) == 0 {
		t.Fatal("module.yaml declares no settings")
	}

	return out
}

// finished appends held to out. held is nil before the first entry.
func finished(out []declaredOption, held *declaredOption) []declaredOption {
	if held == nil {
		return out
	}

	return append(out, *held)
}

// unquote removes the single or double quotes around a YAML value.
func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1]
	}

	return value
}

// readmeRow is one row of the settings table in README.md.
type readmeRow struct {
	key      string
	shape    string
	fallback string
	summary  string
}

// readmeKeys returns the rows of the table under the Configuration heading of
// README.md, in the order of the file.
func readmeKeys(t *testing.T) []readmeRow {
	t.Helper()

	page, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}

	_, after, found := strings.Cut(strings.ReplaceAll(string(page), "\r\n", "\n"), "\n## Configuration\n")
	if !found {
		t.Fatal("README.md has no Configuration section")
	}

	section, _, _ := strings.Cut(after, "\n## ")

	var rows []readmeRow

	for line := range strings.SplitSeq(section, "\n") {
		cells := tableCells(line)
		if len(cells) != 4 || !strings.HasPrefix(cells[0], "`") {
			continue
		}

		rows = append(rows, readmeRow{
			key:      strings.Trim(cells[0], "`"),
			shape:    cells[1],
			fallback: cells[2],
			summary:  cells[3],
		})
	}

	if len(rows) == 0 {
		t.Fatal("the Configuration section of README.md has no table rows")
	}

	return rows
}

// tableCells splits one markdown table row into its cells. It returns nil for
// a line that is not a table row.
func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
		return nil
	}

	parts := strings.Split(strings.Trim(line, "|"), "|")

	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, strings.TrimSpace(part))
	}

	return out
}
