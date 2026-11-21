package manifest_test

import (
	"regexp"
	"slices"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/manifest"
)

var oneLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// TestEverySlotNameIsOneLabelAndNamedOnce holds the names a URL, an element
// attribute and a manifest key are made from.
func TestEverySlotNameIsOneLabelAndNamedOnce(t *testing.T) {
	t.Parallel()
	seen := map[manifest.SlotName]bool{}
	for _, slot := range manifest.Slots() {
		if !oneLabel.MatchString(string(slot.Name)) {
			t.Errorf("%q is not one label", slot.Name)
		}
		if seen[slot.Name] {
			t.Errorf("%q is in the table twice", slot.Name)
		}
		seen[slot.Name] = true
		if slot.Where == "" || slot.Offer == "" {
			t.Errorf("%q has no Where or no Offer", slot.Name)
		}
	}
}

// TestEveryScreenAndAreaIsUsed keeps the closed lists from carrying a value
// no slot names, which would be a screen the docs describe and nothing fills.
func TestEveryScreenAndAreaIsUsed(t *testing.T) {
	t.Parallel()
	for _, screen := range manifest.Screens() {
		if screen.Doc() == "" {
			t.Errorf("screen %q has no doc", screen)
		}
		if !slices.ContainsFunc(manifest.Slots(), func(s manifest.Slot) bool { return s.Screen == screen }) {
			t.Errorf("screen %q is used by no slot", screen)
		}
	}
	for _, area := range manifest.Areas() {
		if !slices.ContainsFunc(manifest.Slots(), func(s manifest.Slot) bool { return s.Area == area }) {
			t.Errorf("area %q is used by no slot", area)
		}
	}
}

// TestTheKeyAndTheRegisterNameFollowFromTheName pins the derivations every
// generated file relies on.
func TestTheKeyAndTheRegisterNameFollowFromTheName(t *testing.T) {
	t.Parallel()
	cases := map[manifest.SlotName]struct{ key, register, field string }{
		manifest.SlotPage:               {"pages", "page", "Pages"},
		manifest.SlotSettings:           {"settings", "settings", "Settings"},
		manifest.SlotDeviceCard:         {"device_card", "deviceCard", "DeviceCard"},
		manifest.SlotDeviceSection:      {"device_section", "deviceSection", "DeviceSection"},
		manifest.SlotCameraSection:      {"camera_section", "cameraSection", "CameraSection"},
		manifest.SlotCameraView:         {"camera_view", "cameraView", "CameraView"},
		manifest.SlotDeviceTab:          {"device_tab", "deviceTab", "DeviceTab"},
		manifest.SlotDeviceAction:       {"device_action", "deviceAction", "DeviceAction"},
		manifest.SlotDashboardCard:      {"cards", "card", "Cards"},
		manifest.SlotFileAction:         {"file_action", "fileAction", "FileAction"},
		manifest.SlotPrintSection:       {"print_section", "printSection", "PrintSection"},
		manifest.SlotFileView:           {"file_view", "fileView", "FileView"},
		manifest.SlotFileTab:            {"file_tab", "fileTab", "FileTab"},
		manifest.SlotDeviceFileAction:   {"device_file_action", "deviceFileAction", "DeviceFileAction"},
		manifest.SlotDeviceUploadAction: {"device_upload_action", "deviceUploadAction", "DeviceUploadAction"},
	}
	for name, want := range cases {
		if got := name.Key(); got != want.key {
			t.Errorf("%q key is %q, want %q", name, got, want.key)
		}
		if got := name.Register(); got != want.register {
			t.Errorf("%q register is %q, want %q", name, got, want.register)
		}
		if got := name.Field(); got != want.field {
			t.Errorf("%q field is %q, want %q", name, got, want.field)
		}
	}
}

// TestEveryIconIsOneLabelAndListedOnce holds the allow-list the manifest and
// the library both read.
func TestEveryIconIsOneLabelAndListedOnce(t *testing.T) {
	t.Parallel()
	icons := manifest.Icons()
	if len(icons) == 0 {
		t.Fatal("the icon list is empty")
	}
	sorted := slices.Clone(icons)
	slices.Sort(sorted)
	if !slices.Equal(icons, sorted) || len(slices.Compact(sorted)) != len(icons) {
		t.Error("the icon list is not sorted or names an icon twice")
	}
	for _, icon := range icons {
		if !oneLabel.MatchString(string(icon)) {
			t.Errorf("icon %q is not one label", icon)
		}
	}
}
