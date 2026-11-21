package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/prusa3d/prusa-connect/internal/codegen"
	"github.com/prusa3d/prusa-connect/sdk/manifest"
)

const (
	frontMatter = "---\ntitle: Module slots\n" +
		"description: Every place a module's bundle may register into, on every screen, and what each place " +
		"hands it.\n" +
		"audience: extend\n" +
		"rewrite:\n  describes: current\n  implementedBy:\n    - sdk/manifest\n---\n\n"

	preamble = "A module declares a `ui.contributes` block in its manifest. The block may only register " +
		"into the slots on this page. A slot the manifest does not name is closed to it.\n\n" +
		"Every slot sits on one screen, in one area of that screen, and takes one of four forms: a " +
		"page, a section, an action or a card. The form decides what the manifest entry looks like and " +
		"what the bundle registers for it.\n"
)

func renderReference(slots []manifest.Slot) []byte {
	var page strings.Builder

	page.WriteString(frontMatter)
	page.WriteString(generatedNotice)
	page.WriteString("\n\n")
	page.WriteString(preamble)

	// One note for every printer slot, before the screens that list them.
	if slices.ContainsFunc(slots, handedADevice) {
		page.WriteString("\n" + devicesNote)
	}

	for _, screen := range manifest.Screens() {
		writeScreen(&page, screen, slotsOn(slots, screen))
	}

	writeAdding(&page)

	return []byte(collapseBlankLines(page.String()))
}

// collapseBlankLines reduces every run of blank lines to one.
func collapseBlankLines(text string) string {
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}

	return text
}

// slotsOn returns the slots of one screen, in table order.
func slotsOn(slots []manifest.Slot, screen manifest.Screen) []manifest.Slot {
	var on []manifest.Slot

	for _, slot := range slots {
		if slot.Screen == screen {
			on = append(on, slot)
		}
	}

	return on
}

// writeScreen renders one screen's table and one YAML example per slot. A
// screen with no slot is left out.
func writeScreen(page *strings.Builder, screen manifest.Screen, slots []manifest.Slot) {
	if len(slots) == 0 {
		return
	}

	fmt.Fprintf(page, "\n## %s\n\n", screen.Doc())
	page.WriteString("| slot | area | what the bundle registers | what it is handed | where |\n")
	page.WriteString("| --- | --- | --- | --- | --- |\n")

	for _, slot := range slots {
		fmt.Fprintf(page, "| `%s` | `%s` | %s | %s | %s |\n",
			slot.Name, slot.Area, registersText(slot.Form), handedText(slot), slot.Where)
	}

	page.WriteString("\n")

	for _, slot := range slots {
		fmt.Fprintf(page, "`%s`\n\n```yaml\n%s\n```\n\n", slot.Name, yamlExample(slot))
	}
}

// formRegisters is what the bundle registers for a slot of each form.
//
//nolint:gochecknoglobals // a closed table, read and never written.
var formRegisters = map[manifest.Form]string{
	manifest.FormPage:    "a component",
	manifest.FormSection: "a component",
	manifest.FormAction:  "a `run` function",
	manifest.FormCard:    "a component, and an editor if a schema cannot express its settings",
}

func registersText(form manifest.Form) string { return formRegisters[form] }

// handedText is what the host passes to the slot's component or run
// function: the id of its subject, a card's props, or "nothing".
func handedText(slot manifest.Slot) string {
	if slot.Form == manifest.FormCard {
		return "its checked settings as `config`, a person's heading as `title`, " +
			"its size as `size`, and `editing`"
	}

	if prop := slot.Subject.Prop(); prop != "" {
		return slot.Subject.Doc() + ", as `" + prop + "`"
	}

	return "nothing"
}

// exampleName derives the id and title of a slot's YAML example from the
// slot's name, so every example is distinct.
func exampleName(name manifest.SlotName) (id, title string) {
	return "my-" + string(name), "My " + strings.ReplaceAll(string(name), "-", " ")
}

// yamlExample is one manifest entry for the slot, in the shape of its form.
func yamlExample(slot manifest.Slot) string {
	icon := manifest.Icons()[0]
	key := slot.Name.Key()
	id, title := exampleName(slot.Name)

	switch slot.Form {
	case manifest.FormPage:
		return fmt.Sprintf("%s:\n  - path: %s\n    title: %s\n    icon: %s\n    hidden: false", key, id, title, icon)
	case manifest.FormSection:
		example := fmt.Sprintf("%s:\n  - id: %s\n    title: %s", key, id, title)
		if slot.CrossOriginIsolated {
			example += "\n    cross_origin_isolated: false"
		}

		return example
	case manifest.FormAction:
		example := fmt.Sprintf("%s:\n  - id: %s\n    title: %s\n    icon: %s\n    destructive: false",
			key, id, title, icon)
		if slot.Primary {
			example += "\n    primary: false"
		}

		return example
	case manifest.FormCard:
		return fmt.Sprintf("%s:\n  - id: %s\n    title: %s\n    description: What the card shows, in one sentence.\n"+
			"    icon: %s\n    size: { w: 4, h: 3 }\n    least: { w: 2, h: 2 }\n    settings:\n"+
			"      - key: device\n        kind: printer\n        label: Printer",
			key, id, title, icon)
	default:
		return key + ": []"
	}
}

// devicesNote explains the devices selector of a printer slot. The examples
// leave it out, because an entry without it shows on every printer.
const devicesNote = "An entry of a printer slot may also carry `devices: { resident: true }` to show only on the " +
	"printer this server runs on. See [Limiting an entry to the printer the server runs on]" +
	"(../writing-a-module.md#limiting-an-entry-to-the-printer-the-server-runs-on).\n\n"

func handedADevice(slot manifest.Slot) bool { return slot.Subject == manifest.SubjectDevice }

// writeAdding ends the page with how to add a slot, a screen or a form to the
// table in sdk/manifest.
func writeAdding(page *strings.Builder) {
	page.WriteString("\n## Adding a slot\n\n")
	page.WriteString("- **New slot on an existing screen**: add a row to the table in " +
		"`sdk/manifest/slots.go` and run `make generate-slots`. Then place the outlet the failing " +
		"test names, write the slot's own scaffold template, and add its branch to the scaffold's " +
		"locales template. The scaffold's README and changelog templates need no edit: they read " +
		"the slot table.\n")
	page.WriteString("- **New screen**: add a `Screen` constant and its rows. Add a `Subject` if the " +
		"screen hands a new kind of id. Add the screen's render function to the outlet test's " +
		"`Record<Screen, ...>`.\n")
	page.WriteString("- **New form**: add the Go entry type, the TS registration type, the registry " +
		"and outlet branch, and the template. The generator refuses a form it does not know.\n")
}

// guideRegion is the marker pair around the generated table in
// writing-a-module.md.
//
//nolint:gochecknoglobals // a constant value the package treats as read-only.
var guideRegion = codegen.Region{
	Start: "<!-- generated by make generate-slots for ",
	End:   "<!-- end generated -->",
}

// spliceGuide replaces the generated region of the committed guide with the
// slot table. The rest of the page is copied unchanged.
func spliceGuide(path string, slots []manifest.Slot) ([]byte, error) {
	committed, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	region := guideRegion.Wrap("the slot table",
		"change sdk/manifest/slots.go and run make generate-slots", guideTable(slots))

	spliced, err := guideRegion.Splice(committed, region)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return spliced, nil
}

func guideTable(slots []manifest.Slot) string {
	var b strings.Builder

	b.WriteString("| slot | manifest key | where it lands |\n")
	b.WriteString("| --- | --- | --- |\n")

	for _, slot := range slots {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", slot.Name, slot.Name.Key(), slot.Where)
	}

	return b.String()
}
