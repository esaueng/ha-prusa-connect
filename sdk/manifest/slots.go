package manifest

import (
	"slices"
	"strings"
)

// SlotName names one place in the app a module's bundle may draw in. It is the
// string the library's ./slots exports and the value of the data-slot
// attribute.
type SlotName string

// The slots a bundle may fill. The sidebar is not one of them: a page that is
// not hidden gets a sidebar entry.
const (
	// SlotPage is a whole screen with a route of its own.
	SlotPage SlotName = "page"
	// SlotSettings is a tab on the module's own page under Settings, Extensions.
	SlotSettings SlotName = "settings"
	// SlotDeviceCard is one line on each printer's card in the listing.
	SlotDeviceCard SlotName = "device-card"
	// SlotDeviceSection is a section at the foot of a printer's Status tab.
	SlotDeviceSection SlotName = "device-section"
	// SlotDeviceTab is a tab of the module's own on a printer's page.
	SlotDeviceTab SlotName = "device-tab"
	// SlotDeviceAction is an item in the action menu of a printer's page.
	SlotDeviceAction SlotName = "device-action"
	// SlotDashboardCard is a card a person places on a dashboard.
	SlotDashboardCard SlotName = "dashboard-card"
	// SlotFileAction is an item in the action menu of a file in a storage.
	SlotFileAction SlotName = "file-action"
	// SlotPrintSection is a section in the print dialog of a file in a storage.
	SlotPrintSection SlotName = "print-section"
	// SlotDeviceFileAction is an item in the action menu of a file on a
	// printer, on the printer page's Files tab.
	SlotDeviceFileAction SlotName = "device-file-action"
	// SlotDeviceUploadAction is a choice in the question a file dropped on a
	// printer's page asks. The file is copied to the printer, and the action
	// runs on it as soon as the copy is on its way.
	SlotDeviceUploadAction SlotName = "device-upload-action"
	// SlotFileView is the part of a file's page that says what the file
	// contains. A module's view replaces the core's own.
	SlotFileView SlotName = "file-view"
	// SlotFileTab is a tab of the module's own on a file's page, after the
	// core's tabs.
	SlotFileTab SlotName = "file-tab"
	// SlotCameraSection is a section under each camera in a printer's Cameras
	// tab (ADR-20261106-xm4).
	SlotCameraSection SlotName = "camera-section"
	// SlotCameraView is the live view of a camera of a module's type, drawn
	// by that module when a person presses play (ADR-20261106-km2).
	SlotCameraView SlotName = "camera-view"
)

// Key is the list under ui.contributes that declares the slot's entries. It is
// the name with hyphens as underscores, except for pages and cards.
func (n SlotName) Key() string {
	if n == SlotPage {
		return "pages"
	}

	if n == SlotDashboardCard {
		return "cards"
	}

	return strings.ReplaceAll(string(n), "-", "_")
}

// Register is the function a bundle calls on host.register for this slot. A
// dashboard card is registered with card.
func (n SlotName) Register() string {
	if n == SlotDashboardCard {
		return "card"
	}

	parts := strings.Split(string(n), "-")
	for i := 1; i < len(parts); i++ {
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, "")
}

// Field is the Go field of a generated struct for the slot's entries.
func (n SlotName) Field() string {
	parts := strings.Split(n.Key(), "_")
	for i := range parts {
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, "")
}

// Screen is a screen of the app that has at least one slot.
type Screen string

// The screens that have slots.
const (
	// ScreenApp is the app frame: the sidebar and the module pages.
	ScreenApp Screen = "app"
	// ScreenSettings is the settings screen.
	ScreenSettings Screen = "settings"
	// ScreenPrinterDetail is one printer's page.
	ScreenPrinterDetail Screen = "printer-detail"
	// ScreenPrinters is the printer listing.
	ScreenPrinters Screen = "printers"
	// ScreenDashboard is a dashboard, the home page among them.
	ScreenDashboard Screen = "dashboard"
	// ScreenStorage is the storage listing and the page of one file in it.
	ScreenStorage Screen = "storage"
	// ScreenPrintDialog is the dialog that sends one file to the printers a
	// person ticks.
	ScreenPrintDialog Screen = "print-dialog"
)

// Doc is the line the reference and the new-module menu print for the screen.
func (s Screen) Doc() string { return screenDocs[s] }

//nolint:gochecknoglobals // a closed table, read and never written.
var screenDocs = map[Screen]string{
	ScreenApp:           "The app: the sidebar and a page of its own",
	ScreenSettings:      "The settings screen",
	ScreenPrinterDetail: "A printer's page",
	ScreenPrinters:      "The printer listing",
	ScreenDashboard:     "A dashboard, the home page among them",
	ScreenStorage:       "The storage listing and a file's page",
	ScreenPrintDialog:   "The print dialog",
}

// Screens is every screen, in the order the menu shows them.
func Screens() []Screen {
	return []Screen{
		ScreenApp, ScreenDashboard, ScreenPrinterDetail, ScreenPrinters, ScreenSettings,
		ScreenStorage, ScreenPrintDialog,
	}
}

// Area is one place on a screen. Several slots may share one.
type Area string

// The areas slots sit in.
const (
	// AreaPage is a module page's whole body.
	AreaPage Area = "page"
	// AreaPanes is the tab strip of the settings screen.
	AreaPanes Area = "panes"
	// AreaStatusFoot is the foot of a printer's Status tab.
	AreaStatusFoot Area = "status-foot"
	// AreaCardLine is one line on each printer's card in the listing.
	AreaCardLine Area = "card-line"
	// AreaTabs is the tab strip of a printer's page.
	AreaTabs Area = "tabs"
	// AreaHeaderActions is the action menu at the top of a printer's page.
	AreaHeaderActions Area = "header-actions"
	// AreaGrid is the grid of a dashboard, where a person places cards.
	AreaGrid Area = "grid"
	// AreaFileActions is the action menu of a file: on each file's row in the
	// storage listing, and at the top of the file's own page.
	AreaFileActions Area = "file-actions"
	// AreaPrintOptions is the part of the print dialog below the printer
	// list and above the buttons.
	AreaPrintOptions Area = "print-options"
	// AreaFileBody is the part of a file's page under its header that says
	// what the file contains.
	AreaFileBody Area = "file-body"
	// AreaFileTabs is the tab strip of a file's page.
	AreaFileTabs Area = "file-tabs"
	// AreaCameraFoot is the foot of one camera in a printer's Cameras tab.
	AreaCameraFoot Area = "camera-foot"
	// AreaCameraPicture is the picture of one camera in a printer's Cameras
	// tab.
	AreaCameraPicture Area = "camera-picture"
	// AreaDeviceFileActions is the action menu of each file on a printer's
	// Files tab, in the list and in the tiles.
	AreaDeviceFileActions Area = "device-file-actions"
	// AreaDropChoices is the list of choices in the question a file dropped on
	// a printer's page asks.
	AreaDropChoices Area = "drop-choices"
)

// Areas is every area.
func Areas() []Area {
	return []Area{
		AreaPage, AreaPanes, AreaCardLine, AreaStatusFoot, AreaTabs, AreaHeaderActions, AreaGrid,
		AreaFileActions, AreaPrintOptions, AreaFileBody, AreaFileTabs, AreaCameraFoot, AreaCameraPicture,
		AreaDeviceFileActions, AreaDropChoices,
	}
}

// Form is the shape of a slot's manifest entry and of what the bundle
// registers for it.
type Form int

// The forms.
const (
	// FormPage is path, title, icon and hidden, and a component.
	FormPage Form = iota
	// FormSection is id and title, and a component.
	FormSection
	// FormAction is id, title, icon and destructive, and a run function.
	FormAction
	// FormCard is id, title, description, icon, a size, a smallest size and a
	// settings schema, and a component with an optional editor. It takes no
	// subject: a card is handed its own settings.
	FormCard
)

// Subject is the one id the host hands a slot's component or run function.
type Subject int

// The subjects.
const (
	// SubjectNone hands nothing.
	SubjectNone Subject = iota
	// SubjectDevice hands the device id.
	SubjectDevice
	// SubjectFile hands a file reference: the name of a storage of this
	// server, a colon, and the file's path inside that storage. A storage
	// name never contains a colon, so the first one splits the two.
	SubjectFile
	// SubjectCamera hands the camera's name, the one its HTTP routes take.
	SubjectCamera
	// SubjectPrinterFile hands a printer file reference: the device id, a
	// colon, and the file's path on the printer, such as usb/part.bgcode. A
	// device id never holds a colon, so the first one splits the two, the way
	// it splits a file reference.
	SubjectPrinterFile
)

// Prop is the name the id is handed under, and empty for SubjectNone.
func (s Subject) Prop() string {
	switch s {
	case SubjectDevice:
		return "deviceId"
	case SubjectFile:
		return "fileRef"
	case SubjectCamera:
		return "camera"
	case SubjectPrinterFile:
		return "printerFileRef"
	case SubjectNone:
		return ""
	default:
		return ""
	}
}

// Doc is what the reference says the host hands over, and empty for
// SubjectNone.
func (s Subject) Doc() string {
	switch s {
	case SubjectDevice:
		return "the device id"
	case SubjectFile:
		return "the file reference `<storage>:<path>`"
	case SubjectCamera:
		return "the camera's name"
	case SubjectPrinterFile:
		return "the printer file reference `<device id>:<path>`"
	case SubjectNone:
		return ""
	default:
		return ""
	}
}

// Slot is one row of the slot table.
type Slot struct {
	// Name is the slot's string.
	Name SlotName
	// Screen is the screen the slot sits on.
	Screen Screen
	// Area is where on the screen.
	Area Area
	// Form is the shape of the entry and of the registration.
	Form Form
	// Subject is the id the host hands over.
	Subject Subject
	// Where is the sentence the reference and the guide print.
	Where string
	// Offer is the hint make new-module shows beside the choice.
	Offer string
	// Primary is whether an entry of an action slot may ask to be a button
	// on its subject's own page instead of an item in its menu.
	Primary bool
	// CrossOriginIsolated is whether an entry may ask for a cross-origin
	// isolated page.
	CrossOriginIsolated bool
}

//nolint:gochecknoglobals // a closed table, read and never written.
var slots = []Slot{
	{
		Name: SlotPage, Screen: ScreenApp, Area: AreaPage, Form: FormPage, Subject: SubjectNone,
		Where: "A whole screen with a route of its own, and an entry in the sidebar unless it is hidden.",
		Offer: "a page of its own, with an entry in the sidebar",
	},
	{
		Name: SlotSettings, Screen: ScreenSettings, Area: AreaPanes, Form: FormSection, Subject: SubjectNone,
		Where: "A tab on the module's own page under Settings, Extensions.",
		Offer: "a tab on the module's page under Settings, Extensions",
	},
	{
		Name: SlotDeviceCard, Screen: ScreenPrinters, Area: AreaCardLine, Form: FormSection, Subject: SubjectDevice,
		Where: "One line on each printer's card in the listing, under its status. Text past one line is cut.",
		Offer: "one line on each printer's card in the listing",
	},
	{
		Name:    SlotDeviceSection,
		Screen:  ScreenPrinterDetail,
		Area:    AreaStatusFoot,
		Form:    FormSection,
		Subject: SubjectDevice,
		Where:   "A section at the foot of a printer's Status tab, after the core's own.",
		Offer:   "a section at the foot of each printer's Status tab",
	},
	{
		Name:                SlotDeviceTab,
		Screen:              ScreenPrinterDetail,
		Area:                AreaTabs,
		Form:                FormSection,
		Subject:             SubjectDevice,
		Where:               "A tab of the module's own on a printer's page, after the core's tabs.",
		Offer:               "a tab of its own on each printer's page",
		CrossOriginIsolated: true,
	},
	{
		Name:    SlotDashboardCard,
		Screen:  ScreenDashboard,
		Area:    AreaGrid,
		Form:    FormCard,
		Subject: SubjectNone,
		Where:   "A card a person places on a dashboard, sized on its grid and drawn with the settings the person chose.",
		Offer:   "a card a person can place on a dashboard",
	},
	{
		Name:    SlotDeviceAction,
		Screen:  ScreenPrinterDetail,
		Area:    AreaHeaderActions,
		Form:    FormAction,
		Subject: SubjectDevice,
		Where:   "An item in the action menu at the top of a printer's page. The module runs it with the printer's id.",
		Offer:   "an item in the action menu of each printer's page",
	},
	{
		Name:    SlotFileAction,
		Screen:  ScreenStorage,
		Area:    AreaFileActions,
		Form:    FormAction,
		Subject: SubjectFile,
		Where:   "An item in a file's menu, in the listing and on its page. A primary one is a button on the page.",
		Offer:   "an item in the action menu of each file in storage",
		Primary: true,
	},
	{
		Name:    SlotPrintSection,
		Screen:  ScreenPrintDialog,
		Area:    AreaPrintOptions,
		Form:    FormSection,
		Subject: SubjectFile,
		Where:   "A section of the print dialog, below the printers, for a file on a storage of this server.",
		Offer:   "a section in the print dialog of a file in storage",
	},
	{
		Name:    SlotFileView,
		Screen:  ScreenStorage,
		Area:    AreaFileBody,
		Form:    FormSection,
		Subject: SubjectFile,
		Where:   "What the file holds, drawn in place of the core's part, in the Contents tab of the file's page.",
		Offer:   "a view of what a file holds, on the file's page",
	},
	{
		Name:    SlotFileTab,
		Screen:  ScreenStorage,
		Area:    AreaFileTabs,
		Form:    FormSection,
		Subject: SubjectFile,
		Where:   "A tab of the module's own on a file's page, after the core's tabs.",
		Offer:   "a tab of its own on a file's page",
	},
	{
		Name:    SlotCameraSection,
		Screen:  ScreenPrinterDetail,
		Area:    AreaCameraFoot,
		Form:    FormSection,
		Subject: SubjectCamera,
		Where:   "A section under each camera in a printer's Cameras tab. A module draws nothing for a camera it does not know.",
		Offer:   "a section under each camera of a printer",
	},
	{
		Name:    SlotCameraView,
		Screen:  ScreenPrinterDetail,
		Area:    AreaCameraPicture,
		Form:    FormSection,
		Subject: SubjectCamera,
		Where:   "The live view of a camera of a type the module offers, drawn over its picture while a person plays it.",
		Offer:   "the live view of a camera of a type it offers",
	},
	{
		Name:    SlotDeviceFileAction,
		Screen:  ScreenPrinterDetail,
		Area:    AreaDeviceFileActions,
		Form:    FormAction,
		Subject: SubjectPrinterFile,
		Where:   "An item in the action menu of each file on a printer's Files tab. A folder has none.",
		Offer:   "an item in the action menu of each file on a printer",
	},
	{
		Name:    SlotDeviceUploadAction,
		Screen:  ScreenPrinterDetail,
		Area:    AreaDropChoices,
		Form:    FormAction,
		Subject: SubjectPrinterFile,
		Where:   "A choice in the question a file dropped on a printer's page asks. The action runs once the copy is on its way.",
		Offer:   "a choice for a file dropped on a printer, run while it is copied there",
	},
}

// Slots is the slot table, in the order the generated files list it.
func Slots() []Slot { return slices.Clone(slots) }

// SlotNamed is the row for one name.
func SlotNamed(name SlotName) (Slot, bool) {
	i := slices.IndexFunc(slots, func(s Slot) bool { return s.Name == name })
	if i < 0 {
		return Slot{}, false
	}
	return slots[i], true
}

// Icon is the name of an icon a manifest may give a page or an action. The
// list is short and fixed, so a module cannot make the app load an icon it
// does not ship.
type Icon string

//nolint:gochecknoglobals // a closed table, read and never written.
var icons = []Icon{
	"blocks", "box", "calendar-clock", "chart-line", "clipboard-list", "flask-conical", "layers",
	"list-checks", "materials", "package", "palette", "printer", "puzzle", "qr-code", "spool",
	"swatch-book", "tag", "thermometer", "wrench",
}

// Icons is every icon a manifest may name, sorted.
func Icons() []Icon { return slices.Clone(icons) }
