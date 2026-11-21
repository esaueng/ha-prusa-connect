package xbuddy

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// The four file verbs travel as J frames under the names the shipping protocol
// constants give them (legacy/connector/xbuddy/protocol/messages.go), each
// carrying the one required path argument the shipping command table declares
// (legacy/connector/xbuddy/commands/config/commands.go).
func TestTheFileVerbsTravelAsJSONCommandsUnderTheirOwnNames(t *testing.T) {
	t.Parallel()

	wanted := map[connectorapi.Capability]string{
		VerbSendFileInfo: "SEND_FILE_INFO",
		VerbCreateFolder: "CREATE_FOLDER",
		VerbDeleteFile:   "DELETE_FILE",
		VerbDeleteFolder: "DELETE_FOLDER",
	}

	for key, command := range wanted {
		t.Run(key.String(), func(t *testing.T) {
			t.Parallel()

			form, declared := wireTable[key]
			if !declared {
				t.Fatalf("%s has no wire form", key)
			}

			if form.frame != frameJSON {
				t.Errorf("%s travels as %q, and the shipping connector sends it as JSON", key, string(form.frame))
			}

			if form.command != command {
				t.Errorf("%s travels as %q, and the firmware receives %q", key, form.command, command)
			}

			path, carries := form.args["path"]
			if !carries {
				t.Fatalf("%s carries no path argument, and the shipping command table requires one", key)
			}

			if !path.Required || path.Kind != connectorapi.KindString {
				t.Errorf("%s declares path as required=%v kind=%s, want required string",
					key, path.Required, path.Kind)
			}
		})
	}
}

// A frame built for one of the file verbs carries the path in the firmware's
// own absolute namespace, which is the same rewrite START_PRINT already gets.
func TestAFileVerbCarriesTheFirmwarePath(t *testing.T) {
	t.Parallel()

	session := &Session{storage: defaultStorage}

	pending, err := session.build(
		connectorapi.Command{Verb: VerbSendFileInfo, Args: map[string]string{"path": "models"}},
		wireTable[VerbSendFileInfo])
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	payload := string(pending.frame[headerLength:])
	if !strings.Contains(payload, `"/usb/models"`) {
		t.Errorf("the frame carries %s, and the firmware namespace spelling is /usb/models", payload)
	}
}

// The traversal rule the shipping validator holds for START_PRINT holds for
// every verb that names a path. A file verb that skipped it would be the same
// hole in a different frame.
func TestAFileVerbRefusesAPathThatLeavesItsStorage(t *testing.T) {
	t.Parallel()

	session := &Session{storage: defaultStorage}

	for _, verb := range []connectorapi.Capability{
		VerbSendFileInfo, VerbCreateFolder, VerbDeleteFile, VerbDeleteFolder,
	} {
		for _, path := range []string{"usb/../../etc/passwd", "/usb/../etc/passwd", "../etc/passwd", ".."} {
			if _, err := session.build(
				connectorapi.Command{Verb: verb, Args: map[string]string{"path": path}},
				wireTable[verb]); err == nil {
				t.Errorf("%s accepted the traversing path %q", verb, path)
			}
		}
	}
}

// The refusal names the path the caller sent and not the one the driver
// composed. A caller cannot correct a spelling they never wrote.
func TestARefusedPathReportsTheSpellingTheCallerSent(t *testing.T) {
	t.Parallel()

	session := &Session{storage: defaultStorage}

	_, err := session.build(
		connectorapi.Command{Verb: VerbSendFileInfo, Args: map[string]string{"path": "usb/../../etc/passwd"}},
		wireTable[VerbSendFileInfo])
	if err == nil {
		t.Fatal("the traversing path was accepted")
	}

	var refusal *fault.Fault
	if !errors.As(err, &refusal) {
		t.Fatalf("the refusal is %v, and a caller reads a code", err)
	}

	reported, carried := refusal.Params().Get("path")
	if !carried {
		t.Fatal("the refusal carries no path, so nothing tells the caller which argument was wrong")
	}

	if got := reported.String(); got != "usb/../../etc/passwd" {
		t.Errorf("the refusal reports path %q, and the caller sent %q", got, "usb/../../etc/passwd")
	}
}

// The INFO payload is what a printer answers SEND_INFO with, transcribed from
// legacy/connector/xbuddy/printer/state parseStorages: entries with no
// mountpoint are dropped, and the name is the mountpoint without its separator.
func TestAnInfoPayloadBecomesTheStoragesTheSessionReports(t *testing.T) {
	t.Parallel()

	payload := json.RawMessage(`{
		"firmware": "6.4.0",
		"storages": [
			{"mountpoint": "/usb", "type": "USB", "free_space": 1073741824, "read_only": false},
			{"mountpoint": "/local", "type": "LOCAL", "read_only": true},
			{"type": "USB"}
		]
	}`)

	got, ok := storagesFrom(payload)
	if !ok {
		t.Fatal("a readable INFO was taken as unreadable")
	}

	if len(got) != 2 {
		t.Fatalf("%d storages were read, want 2: an entry with no mountpoint names nothing", len(got))
	}

	first := got[0]
	if first.Mountpoint != "/usb" || first.Name != "usb" || first.Type != "USB" {
		t.Errorf("the first storage reads %+v, want mountpoint /usb, name usb, type USB", first)
	}

	if !first.FreeSpaceKnown || first.FreeSpace != 1<<30 {
		t.Errorf("the first storage reports free space %d known=%v, want 1073741824 known",
			first.FreeSpace, first.FreeSpaceKnown)
	}

	second := got[1]
	if !second.ReadOnly {
		t.Error("the second storage is mounted read-only and does not say so")
	}

	if second.FreeSpaceKnown {
		t.Error("the second storage reported no free space and claims a figure")
	}
}

// The FILE_INFO payload is the node the firmware reports for one path, with its
// children. The paths it hands back are absolute in the firmware namespace and
// the listing's are storage-prefixed relative, because that is the one
// namespace the rest of the product addresses a file in.
func TestAFileInfoPayloadBecomesADirectoryListing(t *testing.T) {
	t.Parallel()

	payload := json.RawMessage(`{
		"event": "FILE_INFO",
		"data": {
			"path": "/usb/models",
			"type": "FOLDER",
			"name": "MODELS",
			"display_name": "models",
			"children": [
				{"name": "PART~1.BGC", "display_name": "part.bgcode", "type": "PRINT_FILE",
				 "size": 4096, "m_timestamp": 1750000000},
				{"name": "SUB", "display_name": "sub", "type": "FOLDER", "read_only": true}
			]
		}
	}`)

	listing, ok := listingFrom(payload)
	if !ok {
		t.Fatal("a FILE_INFO payload was not read as a listing")
	}

	if listing.Directory.Path != "usb/models" {
		t.Errorf("the directory reads %q, and the relative namespace spelling is usb/models",
			listing.Directory.Path)
	}

	if !listing.Directory.Folder {
		t.Error("a FOLDER node did not report itself as a folder")
	}

	if len(listing.Entries) != 2 {
		t.Fatalf("%d entries were read, want 2", len(listing.Entries))
	}

	file := listing.Entries[0]
	if file.Path != "usb/models/part.bgcode" {
		t.Errorf("the child path is %q, and the shipping connector addresses a child by its long name",
			file.Path)
	}

	if file.Name != "PART~1.BGC" {
		t.Errorf("the child reports short name %q, and a caller on a FAT storage needs it", file.Name)
	}

	if file.DisplayName != "part.bgcode" || file.Size != 4096 || file.Modified != 1750000000 {
		t.Errorf("the child reads %+v, want display part.bgcode, size 4096, modified 1750000000", file)
	}

	if file.Folder {
		t.Error("a PRINT_FILE was read as a folder")
	}

	folder := listing.Entries[1]
	if !folder.Folder || !folder.ReadOnly {
		t.Errorf("the child folder reads %+v, want folder and read-only", folder)
	}
}

// The firmware puts a print file's picture inline, as base64, when it is asked
// about that one file. A field that is not base64 is a file with no picture and
// still a listing.
func TestAFileInfoForOneFileCarriesItsPicture(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		preview string
		want    string
	}{
		{name: "base64", preview: "iVBORw0KGgo=", want: "\x89PNG\r\n\x1a\n"},
		{name: "absent", preview: "", want: ""},
		{name: "not base64", preview: "not base64!", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payload := json.RawMessage(`{"event":"FILE_INFO","data":{"path":"/usb/part.bgcode",` +
				`"type":"PRINT_FILE","size":10,"preview":"` + tc.preview + `"}}`)

			listing, ok := listingFrom(payload)
			if !ok {
				t.Fatal("a FILE_INFO for one file was not read as a listing")
			}

			if string(listing.Directory.Preview) != tc.want {
				t.Errorf("the picture reads %q, want %q", listing.Directory.Preview, tc.want)
			}
		})
	}
}

// The firmware sends the slicer's headers of a print file beside its picture
// (Prusa-Firmware-Buddy src/connect/render.cpp, the meta records). The print
// time arrives as the slicer wrote it, "2h 5m 3s", and as whole seconds under
// the name a file Connect prepared carries. A value this driver cannot read is
// no time at all, and never a wrong one.
func TestAFileInfoForOneFileCarriesItsPrintTime(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		header string
		want   int64
	}{
		{name: "as the slicer writes it", header: `"estimated printing time (normal mode)":"2h 5m 3s"`, want: 7503},
		{name: "days and minutes", header: `"estimated printing time (normal mode)":"1d 30m"`, want: 88200},
		{name: "seconds alone", header: `"estimated printing time (normal mode)":"42s"`, want: 42},
		{name: "whole seconds", header: `"estimated_print_time":930`, want: 930},
		{name: "seconds win over the text", header: `"estimated_print_time":930,` +
			`"estimated printing time (normal mode)":"2h"`, want: 930},
		{name: "absent", header: `"layer_height":0.2`, want: 0},
		{name: "not a duration", header: `"estimated printing time (normal mode)":"soon"`, want: 0},
		{name: "a unit nobody writes", header: `"estimated printing time (normal mode)":"3w 1h"`, want: 0},
		{name: "a unit with no number", header: `"estimated printing time (normal mode)":"1h m"`, want: 0},
		{name: "a negative part", header: `"estimated printing time (normal mode)":"-5m"`, want: 0},
		{name: "past a year", header: `"estimated printing time (normal mode)":"900000d"`, want: 0},
		{name: "seconds past a year fall back to nothing", header: `"estimated_print_time":99999999999`, want: 0},
		// A file writes these headers, so each can hold any type. None of them
		// may cost the listing and the picture that came in the same answer.
		{name: "seconds as text", header: `"estimated_print_time":"930"`, want: 930},
		{name: "seconds with a part", header: `"estimated_print_time":930.5`, want: 930},
		{name: "seconds that are a duration", header: `"estimated_print_time":"1h 2m"`, want: 0},
		{name: "seconds of another type", header: `"estimated_print_time":{"s":930}`, want: 0},
		{name: "the text as a number", header: `"estimated printing time (normal mode)":7503`, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payload := json.RawMessage(`{"event":"FILE_INFO","data":{"path":"/usb/part.bgcode",` +
				`"type":"PRINT_FILE","size":10,"filament used [g]":12.5,` + tc.header + `}}`)

			listing, ok := listingFrom(payload)
			if !ok {
				t.Fatal("a FILE_INFO for one file was not read as a listing")
			}

			if listing.Directory.EstimatedSeconds != tc.want {
				t.Errorf("the print time reads %d s, want %d", listing.Directory.EstimatedSeconds, tc.want)
			}
		})
	}
}

// Beside the time, the headers name the printer the file was sliced for, the
// layer height and the material. A value of a type this driver does not expect
// is no value, and the rest of the answer still reads.
func TestAFileInfoForOneFileCarriesWhatTheSlicerWrote(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		headers string
		want    connectorapi.File
	}{
		{
			name:    "as the firmware sends them",
			headers: `"printer_model":"XL2IS","layer_height":0.2,"filament_type":"PLA"`,
			want:    connectorapi.File{PrinterModel: "XL2IS", LayerHeightMM: 0.2, Material: "PLA"},
		},
		{
			name:    "a material for each tool",
			headers: `"filament_type":"PLA;PETG"`,
			want:    connectorapi.File{Material: "PLA;PETG"},
		},
		{
			name:    "types nobody writes",
			headers: `"printer_model":7,"layer_height":"thin","filament_type":["PLA"]`,
		},
		{
			name:    "a layer height out of bounds",
			headers: `"layer_height":-0.2`,
		},
		{
			name:    "a name too long to be a model",
			headers: `"printer_model":"` + strings.Repeat("X", 65) + `"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payload := json.RawMessage(`{"event":"FILE_INFO","data":{"path":"/usb/part.bgcode",` +
				`"type":"PRINT_FILE","size":10,` + tc.headers + `}}`)

			listing, ok := listingFrom(payload)
			if !ok {
				t.Fatal("a FILE_INFO for one file was not read as a listing")
			}

			got := listing.Directory
			if got.PrinterModel != tc.want.PrinterModel || got.LayerHeightMM != tc.want.LayerHeightMM ||
				got.Material != tc.want.Material {
				t.Errorf("read model %q, layer %v, material %q; want %q, %v, %q",
					got.PrinterModel, got.LayerHeightMM, got.Material,
					tc.want.PrinterModel, tc.want.LayerHeightMM, tc.want.Material)
			}
		})
	}
}

// A payload that is not a FILE_INFO node is not a listing. Reading one as an
// empty directory would report a folder with nothing in it for every answer the
// firmware gives that this driver has no reading of.
func TestAPayloadThatIsNotAFileInfoIsNotAListing(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{
		`{"event":"FINISHED","state":"IDLE"}`,
		`{"event":"FILE_INFO"}`,
		`not json`,
		``,
	} {
		if _, ok := listingFrom(json.RawMessage(payload)); ok {
			t.Errorf("%q was read as a directory listing", payload)
		}
	}
}

// A listing remembered before a transfer began says nothing about the file the
// transfer wrote, so the readback refuses it.
//
// The recollection is the fallback for a firmware whose FILE_INFO carries no
// command id, and it is matched by path alone. A readback that took the
// directory as it stood before the file was sent would report BUG-014's own
// case, a file that never landed, against a listing that could not have held it
// either way.
func TestAReadbackRefusesAListingRememberedBeforeItsTransfer(t *testing.T) {
	t.Parallel()

	session := &Session{}
	usb := connectorapi.Listing{Directory: connectorapi.File{Path: "usb", Folder: true}}

	session.remember(usb)

	floor := session.listingFloor()

	if _, recalled := session.recalled("usb", floor); recalled {
		t.Error("the readback judged the transfer against a listing taken before it began")
	}

	// A listing the printer reported since is the one the readback asked for.
	session.remember(usb)

	if _, recalled := session.recalled("usb", floor); !recalled {
		t.Error("the readback refused the listing its own command produced")
	}

	// And an ordinary List, which is answering a caller and not judging a
	// transfer, takes the newest recollection whenever it was taken.
	if _, recalled := session.recalled("usb", 0); !recalled {
		t.Error("a listing this session remembered is not recalled for a plain caller")
	}
}

// An INFO this session cannot read says nothing about the storages, so it must
// not wipe the list a readable one reported.
func TestAnUnreadableInfoKeepsTheStoragesAlreadyReported(t *testing.T) {
	t.Parallel()

	if _, ok := storagesFrom(json.RawMessage(`{"storages":"usb"}`)); ok {
		t.Fatal("an INFO whose storages are not a list was read as a list")
	}

	if got, ok := storagesFrom(json.RawMessage(`{"storages":[]}`)); !ok || len(got) != 0 {
		t.Fatalf("an INFO listing no storage read as %+v ok=%v, want none ok", got, ok)
	}
}

// The firmware's "File is being transferred" names no way out and reads as the
// file arriving, and on a printer with a screen the way out is closing the print
// preview. Every other refusal keeps the firmware's words as they came.
func TestARefusalForAHeldFileSaysWhatToClose(t *testing.T) {
	t.Parallel()

	held := explained(heldReason)
	if strings.Contains(held, "transferred") || !strings.Contains(held, "kept its file") ||
		!strings.Contains(held, "print preview") {
		t.Errorf("the refusal for a held file reads %q", held)
	}

	for _, reason := range []string{"File is busy", "File not found", ""} {
		if got := explained(reason); got != reason {
			t.Errorf("the refusal %q reads %q, want it unchanged", reason, got)
		}
	}
}

// A FILE_CHANGED names an entry deleted by old_path, one created or changed by
// new_path, and a folder of merged changes by new_path with rescan, after
// render.cpp. Its free space is the USB drive's.
func TestAFileChangedBecomesTheChangeItNames(t *testing.T) {
	t.Parallel()

	session := queuedSession(t)
	session.setStorages([]connectorapi.Storage{{Mountpoint: "/usb", Name: "usb", FreeSpace: 10, FreeSpaceKnown: true}})
	<-session.StoragesChanged()

	frames := []string{
		`{"old_path":"/usb/OLD~1.GCO","free_space":99,"file":{"name":"old.gcode","type":"PRINT_FILE"}}`,
		`{"new_path":"/usb/NEWDIR~1","file":{"name":"New dir","type":"FOLDER","m_timestamp":5}}`,
		`{"new_path":"/usb/BATCH/","rescan":true,"file":{"type":"FOLDER"}}`,
		`{"free_space":1}`,
	}
	for _, frame := range frames {
		session.observeFileChanged(json.RawMessage(frame))
	}

	want := []connectorapi.FileChange{
		{File: connectorapi.File{Path: "usb/OLD~1.GCO", DisplayName: "old.gcode"}, Removed: true},
		{
			File: connectorapi.File{
				Path:        "usb/NEWDIR~1",
				Name:        "NEWDIR~1",
				DisplayName: "New dir",
				Folder:      true,
				Modified:    5,
			},
		},
		{File: connectorapi.File{Path: "usb/BATCH", Folder: true}, Rescan: true},
	}
	for _, expected := range want {
		select {
		case got := <-session.FilesChanged():
			if !reflect.DeepEqual(got, expected) {
				t.Errorf("a FILE_CHANGED read as %+v, want %+v", got, expected)
			}
		default:
			t.Fatalf("no change was read for %+v", expected)
		}
	}

	select {
	case extra := <-session.FilesChanged():
		t.Errorf("a FILE_CHANGED naming no path read as %+v", extra)
	default:
	}

	storages, _ := session.Storages(t.Context())
	if storages[0].FreeSpace != 1 {
		t.Errorf("the USB drive reports %d bytes free, want the last FILE_CHANGED's 1", storages[0].FreeSpace)
	}
}
