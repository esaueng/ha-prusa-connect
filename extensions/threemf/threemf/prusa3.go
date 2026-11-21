package threemf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// ps3Project is the file PrusaSlicer 3.0 keeps its project in, beside the 3MF
// core. It holds the printer setups, each with its settings and the positions
// of its beds, the part types and the instances that are not printable.
const ps3Project = "Metadata/PrusaSlicer3_project.json"

// ps3Facets is the file PrusaSlicer 3.0 keeps the painting of each part in,
// by the id of the part's object: FacetsAnnotationSerialization in
// src/slic3r-shared/src/Slic3r/Biz/Format/3mf/PrusaFile.cpp of 3.0.0-alpha12.
const ps3Facets = "Metadata/Slic3r_facets_annotation.json"

// prusa3 reads PrusaSlicer 3.0 projects, in the layout of 3.0.0-alpha12.
type prusa3 struct {
	doc *ps3Doc
	// paints are the paintings of Slic3r_facets_annotation.json, by the id
	// of the part's object.
	paints map[int]paint
}

type ps3Doc struct {
	Objects []struct {
		ID             int                        `json:"id"`
		ObjectSettings map[string]json.RawMessage `json:"object_settings"`
		Ranges         []struct {
			ZRange        []float64                  `json:"zRange"`
			Configuration map[string]json.RawMessage `json:"configuration"`
		} `json:"ranges"`
		Volumes []struct {
			ID             int                        `json:"id"`
			Type           string                     `json:"type"`
			VolumeSettings map[string]json.RawMessage `json:"volume_settings"`
		} `json:"volumes"`
		Instances []struct {
			Ord       *int  `json:"ord"`
			Printable *bool `json:"printable"`
		} `json:"instances"`
	} `json:"objects"`
	Project struct {
		ID      string      `json:"id"`
		Version json.Number `json:"version"`
	} `json:"project"`
	Containers []ps3Container `json:"config_containers"`
}

type ps3Container struct {
	Beds []struct {
		X           float64         `json:"position_x"`
		Y           float64         `json:"position_y"`
		CustomGCode *ps3CustomGCode `json:"custom_gcode"`
	} `json:"beds"`
	Configuration map[string]json.RawMessage `json:"configuration"`
	Preset        struct {
		HW struct {
			Model      string `json:"model"`
			ConfigName string `json:"config_name"`
			Technology string `json:"technology"`
			Tools      map[string]struct {
				Features struct {
					Diameter *float64 `json:"nozzle_diameter"`
					HighFlow *bool    `json:"nozzle_high_flow"`
				} `json:"features"`
			} `json:"tools"`
		} `json:"hw_config"`
		Print     ps3Preset   `json:"print"`
		Printer   ps3Preset   `json:"printer"`
		Materials []ps3Preset `json:"materials"`
	} `json:"preset"`
}

type ps3Preset struct {
	Name       string   `json:"name"`
	Conditions []string `json:"conditions"`
}

func (*prusa3) name() Dialect { return DialectPrusaSlicer3 }

func (*prusa3) matches(p Producer, a *archive) bool {
	if p.Name == "PrusaSlicer" && versionAtLeast(p.Version, ps3Major, 0) {
		return true
	}

	return a.entry(ps3Project) != nil && a.entry(ps2Config) == nil
}

func (d *prusa3) prepare(rd *reading) error {
	f := rd.a.entry(ps3Project)
	if f == nil {
		return nil
	}

	data, err := readAll(rd.ctx, f, rd.limits.MaxMetadataBytes, "MaxMetadataBytes")
	if err != nil {
		return err
	}

	var doc ps3Doc
	if json.Unmarshal(data, &doc) != nil {
		rd.issue(IssueSettingsUnreadable, ps3Project)

		return nil //nolint:nilerr // a damaged project file is an issue, and the read goes on.
	}

	d.doc = &doc
	rd.projectID = doc.Project.ID
	rd.producer.FormatVersion = doc.Project.Version.String()
	rd.rules = d.rules()

	return d.readFacets(rd)
}

// rules are the extruder rules of the first setup, with the most
// extruders any setup has. The objects are shared by every setup, and a
// setup with fewer extruders prints an extruder it does not have with its
// first.
func (d *prusa3) rules() extruderRules {
	out := extruderRules{roles: prusaRoles, setAll: true, printSettings: d.printSettings}

	for _, c := range d.doc.Containers {
		out.count = max(out.count, len(c.Preset.Materials))
	}

	return out
}

// readFacets reads the painting of every part from
// Slic3r_facets_annotation.json. Each painted triangle is written as the
// same hexadecimal text as in the model of PrusaSlicer 2.x.
func (d *prusa3) readFacets(rd *reading) error {
	f := rd.a.entry(ps3Facets)
	if f == nil {
		return nil
	}

	data, err := readAll(rd.ctx, f, rd.limits.MaxMetadataBytes, "MaxMetadataBytes")
	if err != nil {
		return err
	}

	var entries []struct {
		ID       int        `json:"id"`
		Colors   []ps3Facet `json:"mmSegmentationFacets"`
		Supports []ps3Facet `json:"supportedFacets"`
		Seams    []ps3Facet `json:"seamFacets"`
	}

	if json.Unmarshal(data, &entries) != nil {
		rd.issue(IssueSettingsUnreadable, ps3Facets)

		return nil //nolint:nilerr // a damaged painting file is an issue, and the read goes on.
	}

	d.paints = map[int]paint{}

	for _, e := range entries {
		pt := d.paints[e.ID]

		for _, f := range e.Colors {
			decodePaint(f.Dividing, false, &pt.colors)
		}

		pt.supports = pt.supports || facetsPainted(e.Supports)
		pt.enforcers = pt.enforcers || facetsHave(e.Supports, paintEnforcer)
		pt.seams = pt.seams || facetsPainted(e.Seams)
		d.paints[e.ID] = pt
	}

	return nil
}

// ps3Facet is one painted triangle of Slic3r_facets_annotation.json.
type ps3Facet struct {
	Dividing string `json:"dividing"`
}

// facetsHave reports whether any triangle has the state.
func facetsHave(facets []ps3Facet, state int) bool {
	for _, f := range facets {
		var states stateSet
		if decodePaint(f.Dividing, false, &states) && states.has(state) {
			return true
		}
	}

	return false
}

// facetsPainted reports whether any triangle has a state other than 0.
func facetsPainted(facets []ps3Facet) bool {
	for _, f := range facets {
		var states stateSet
		if decodePaint(f.Dividing, false, &states) && states.painted() {
			return true
		}
	}

	return false
}

func (d *prusa3) objects(rd *reading) ([]objectSpec, error) {
	types, notPrintable := d.flags()

	specs, err := groupByObject(rd, func(_ *resource, c component) PartType {
		if t, ok := types[c.ref.id]; ok {
			return t
		}

		return PartModel
	})
	if err != nil {
		return nil, err
	}

	objects, volumes := d.settingsOf()

	for o := range specs {
		specs[o].printable = make([]bool, len(specs[o].items))
		for i, item := range specs[o].items {
			specs[o].printable[i] = !notPrintable[item.ordinal]
		}

		object := objects[specs[o].resourceID]
		specs[o].settings = jsonGetter(object.settings)
		specs[o].ranges = object.ranges
		specs[o].support = supportExtruders(prusa3Support, overlay(specs[o].settings, d.printSettings))

		for p := range specs[o].parts {
			part := &specs[o].parts[p]
			part.settings = jsonGetter(volumes[part.id])
			part.paint = d.paints[part.id]
		}
	}

	return specs, nil
}

// ps3ObjectInfo is what the project file says of one object.
type ps3ObjectInfo struct {
	settings map[string]json.RawMessage
	ranges   []layerRange
}

// settingsOf reads the settings and layer ranges of each object and the
// settings of each part, by their ids in the model.
func (d *prusa3) settingsOf() (map[int]ps3ObjectInfo, map[int]map[string]json.RawMessage) {
	objects, volumes := map[int]ps3ObjectInfo{}, map[int]map[string]json.RawMessage{}

	if d.doc == nil {
		return objects, volumes
	}

	for _, o := range d.doc.Objects {
		object := ps3ObjectInfo{settings: o.ObjectSettings}

		for _, r := range o.Ranges {
			if len(r.ZRange) != pointValues || !finite(r.ZRange[0]) || !finite(r.ZRange[1]) {
				continue
			}

			values := map[string]string{}
			for key, raw := range r.Configuration {
				if v, ok := jsonScalar(raw); ok {
					values[key] = v
				}
			}

			object.ranges = append(object.ranges, layerRange{
				minZ: r.ZRange[0], maxZ: r.ZRange[1], values: values, get: jsonGetter(r.Configuration),
			})
		}

		slices.SortStableFunc(object.ranges, func(a, b layerRange) int { return cmpFloat(a.minZ, b.minZ) })
		objects[o.ID] = object

		for _, v := range o.Volumes {
			volumes[v.ID] = v.VolumeSettings
		}
	}

	return objects, volumes
}

// printSettings reads a print setting of the first setup as plain text.
func (d *prusa3) printSettings(key string) (string, bool) {
	if d.doc == nil || len(d.doc.Containers) == 0 {
		return "", false
	}

	var group map[string]json.RawMessage
	if json.Unmarshal(d.doc.Containers[0].Configuration["print_settings"], &group) != nil {
		return "", false
	}

	return jsonScalar(group[key])
}

// flags reads the part types, by the id of the volume object, and the build
// items that are not printable.
func (d *prusa3) flags() (map[int]PartType, map[int]bool) {
	types := map[int]PartType{}
	notPrintable := map[int]bool{}

	if d.doc == nil {
		return types, notPrintable
	}

	for _, o := range d.doc.Objects {
		for _, v := range o.Volumes {
			if v.Type != "" {
				types[v.ID] = PartType(v.Type)
			}
		}

		for _, inst := range o.Instances {
			if inst.Ord != nil && inst.Printable != nil && !*inst.Printable {
				notPrintable[*inst.Ord] = true
			}
		}
	}

	return types, notPrintable
}

// place puts each instance on the first bed, setup by setup and bed by bed,
// that it is whole on or sticks out of, as BedTracking::
// find_bed_instance_for_bounds of PrusaSlicer 3.0.0-alpha12 does. An instance
// whole on a bed is sliced with it. One that sticks out of the first bed it
// touches is not, and is not looked for on any later bed: it keeps that bed,
// partly outside. An instance wholly below the bed's plane is on none.
func (d *prusa3) place(rd *reading, p *Project, specs []objectSpec) {
	if d.doc == nil || len(d.doc.Containers) == 0 {
		(&generic{}).place(rd, p, specs)

		return
	}

	areas := d.setups(p)
	j := &judge{work: shapeWork * rd.limits.MaxVertices}

	for o := range p.Objects {
		for i := range p.Objects[o].Instances {
			inst := &p.Objects[o].Instances[i]

			outline, box, defined := j.ps3Footprint(&specs[o].shapes[i])
			if !defined {
				continue
			}

			for _, a := range areas {
				st := ps3State(&a.bed, outline, box)
				if st != stInside && st != stColliding {
					continue
				}

				ref := a.ref
				inst.Bed, inst.Placement = &ref, PlacementInside

				if st == stColliding {
					inst.Placement = PlacementPartlyOutside
				}

				bed := &p.Setups[ref.Setup].Beds[ref.Bed]
				bed.Instances = append(bed.Instances, InstanceRef{Object: o, Instance: i})

				break
			}
		}
	}
}

// ps3Area is one bed of one setup, as the slicer tests it.
type ps3Area struct {
	ref BedRef
	bed ps3Bed
}

// setups adds a setup for every config container, with its beds, and returns
// the beds in the order the slicer looks through them.
func (d *prusa3) setups(p *Project) []ps3Area {
	var areas []ps3Area

	for s, c := range d.doc.Containers {
		setup := ps3Setup(s, c)
		sequential := jsonBool(groupValues(setup.Settings, "print_settings")["complete_objects"])

		for b, bed := range c.Beds {
			offset := Point{X: bed.X, Y: bed.Y}
			setup.Beds = append(setup.Beds, Bed{
				Index: b, Offset: offset, Size: size(setup.BedShape),
				Sequential: sequential, LayerChanges: ps3Changes(bed.CustomGCode),
			})

			if len(setup.BedShape) >= minPolygon {
				areas = append(areas, ps3Area{
					ref: BedRef{Setup: s, Bed: b},
					bed: newPS3Bed(setup.BedShape, offset, setup.MaxPrintHeight),
				})
			}
		}

		p.Setups = append(p.Setups, setup)
	}

	return areas
}

// ps3CustomGCode is the custom_gcode of one bed: the changes PrusaSlicer 3.0
// inserts at given heights, in the numbers of CustomGCode::Type.
type ps3CustomGCode struct {
	GCodes []struct {
		GCode struct {
			Type     int     `json:"type"`
			PrintZ   float64 `json:"print_z"`
			Extruder int     `json:"extruder"`
			Color    string  `json:"color"`
			Extra    string  `json:"extra"`
		} `json:"gcode"`
	} `json:"gcodes"`
}

// ps3Changes reads the layer changes of one bed.
func ps3Changes(custom *ps3CustomGCode) []LayerChange {
	if custom == nil {
		return nil
	}

	var out []LayerChange

	for _, g := range custom.GCodes {
		kind, ok := layerKind(g.GCode.Type)
		if !ok || !finite(g.GCode.PrintZ) {
			continue
		}

		out = append(out, tidyChange(LayerChange{
			Height: g.GCode.PrintZ, Kind: kind, Extruder: max(g.GCode.Extruder, 0),
			Color: g.GCode.Color, Extra: g.GCode.Extra,
		}))
	}

	sortChanges(out)

	return out
}

func jsonBool(raw string) bool {
	var b bool

	return json.Unmarshal([]byte(raw), &b) == nil && b
}

// ps3Setup reads one setup from a config container.
func ps3Setup(index int, c ps3Container) Setup {
	groups := settingsGroups(c.Configuration)
	printer := groupValues(groups, "printer_settings")
	printSettings := groupValues(groups, "print_settings")

	out := Setup{
		Index:          index,
		PrinterModel:   firstNonEmpty(jsonString(printer["printer_model"]), c.Preset.HW.Model),
		PrinterProfile: firstNonEmpty(c.Preset.Printer.Name, c.Preset.HW.ConfigName),
		PrintProfile:   c.Preset.Print.Name,
		BedShape:       jsonPoints(printer["bed_shape"]),
		MaxPrintHeight: jsonNumber(printer["max_print_height"]),
		Technology: firstNonEmpty(
			jsonString(printer["printer_technology"]),
			strings.ToUpper(c.Preset.HW.Technology),
		),
		GCodeFlavor:  jsonString(printer["gcode_flavor"]),
		LabelObjects: jsonString(printSettings["gcode_label_objects"]),
		Settings:     groups,
	}

	if raw, ok := printer["binary_gcode"]; ok {
		var binary bool
		if json.Unmarshal([]byte(raw), &binary) == nil {
			out.BinaryGCode = &binary
		}
	}

	for _, m := range c.Preset.Materials {
		out.MaterialProfiles = append(out.MaterialProfiles, m.Name)
	}

	if !strings.EqualFold(out.Technology, "SLA") {
		out.Filaments = filaments(
			len(c.Preset.Materials),
			jsonStrings(groupValues(groups, "project_settings")["extruder_colour"]),
			jsonStrings(groupValues(groups, "filament_settings")["filament_colour"]),
			jsonStrings(groupValues(groups, "filament_settings")["filament_type"]),
		)
	}

	tools := make([]string, 0, len(c.Preset.HW.Tools))
	for key := range c.Preset.HW.Tools {
		tools = append(tools, key)
	}

	sort.Slice(tools, func(i, j int) bool { return toolOrder(tools[i]) < toolOrder(tools[j]) })

	for _, key := range tools {
		f := c.Preset.HW.Tools[key].Features
		if f.Diameter != nil {
			out.Nozzles = append(out.Nozzles, Nozzle{DiameterMM: *f.Diameter, HighFlow: f.HighFlow})
		}
	}

	for _, preset := range []ps3Preset{c.Preset.Printer, c.Preset.Print} {
		for _, cond := range preset.Conditions {
			if !slices.Contains(out.Conditions, cond) {
				out.Conditions = append(out.Conditions, cond)
			}
		}
	}

	return out
}

func toolOrder(key string) int {
	n, err := strconv.Atoi(key)
	if err != nil {
		return math.MaxInt
	}

	return n
}

// settingsGroups turns each group of the configuration into a table of raw
// JSON values, in the order of the group names.
func settingsGroups(configuration map[string]json.RawMessage) []SettingsGroup {
	names := make([]string, 0, len(configuration))
	for name := range configuration {
		names = append(names, name)
	}

	slices.Sort(names)

	var out []SettingsGroup

	for _, name := range names {
		var values map[string]json.RawMessage
		if json.Unmarshal(configuration[name], &values) != nil {
			continue
		}

		group := SettingsGroup{Name: name, Values: make(map[string]string, len(values))}

		for key, raw := range values {
			var compact bytes.Buffer
			if json.Compact(&compact, raw) == nil {
				group.Values[key] = compact.String()
			}
		}

		out = append(out, group)
	}

	return out
}

func groupValues(groups []SettingsGroup, name string) map[string]string {
	for _, g := range groups {
		if g.Name == name {
			return g.Values
		}
	}

	return nil
}

func jsonString(raw string) string {
	var s string
	if json.Unmarshal([]byte(raw), &s) != nil {
		return ""
	}

	return s
}

// jsonStrings reads a list of strings, or one string as a list of one.
func jsonStrings(raw string) []string {
	var list []string
	if json.Unmarshal([]byte(raw), &list) == nil {
		return list
	}

	if s := jsonString(raw); s != "" {
		return []string{s}
	}

	return nil
}

func jsonNumber(raw string) float64 {
	var n float64
	if json.Unmarshal([]byte(raw), &n) != nil || !finite(n) {
		return 0
	}

	return n
}

func jsonPoints(raw string) []Point {
	var pairs [][]float64
	if json.Unmarshal([]byte(raw), &pairs) != nil {
		return nil
	}

	out := make([]Point, 0, len(pairs))

	for _, pair := range pairs {
		if len(pair) != pointValues || !finite(pair[0]) || !finite(pair[1]) {
			return nil
		}

		out = append(out, Point{X: pair[0], Y: pair[1]})
	}

	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}

func (*prusa3) thumbnail(string) (BedRef, bool) { return BedRef{}, false }

// rewrite marks every instance that was not selected as not printable, in the
// project file, and keeps only the chosen setup with only the chosen bed. The
// command line of PrusaSlicer 3.0 slices the first bed of the first setup, so
// the chosen bed becomes that bed. The model is not touched.
func (d *prusa3) rewrite(ctx context.Context, rw *rewriting) error {
	f := rw.a.entry(ps3Project)
	if f == nil {
		return formatError("no %s", ps3Project)
	}

	data, err := readAll(ctx, f, rw.limits.MaxMetadataBytes, "MaxMetadataBytes")
	if err != nil {
		return err
	}

	doc, err := parseOrdered(data)
	if err != nil {
		return err
	}

	if err = ps3KeepBed(doc, rw.sel.Bed); err != nil {
		return err
	}

	if err = ps3SetPrintable(doc, rw); err != nil {
		return err
	}

	// Positions move build items and add copies, and the build is in the
	// model. A copy is printable: the project names only the instances that
	// are not.
	if rw.placed != nil {
		if err = rw.rewriteItems(ctx, func(int, *tagAttrs, bool) {}); err != nil {
			return err
		}
	}

	out, err := doc.marshal()
	if err != nil {
		return err
	}

	return rw.replace(ps3Project, out)
}

// ps3KeepBed keeps the chosen setup as the only one, with the chosen bed as
// its only bed.
func ps3KeepBed(doc *ordered, ref BedRef) error {
	var containers []json.RawMessage
	if err := json.Unmarshal(doc.get("config_containers"), &containers); err != nil || ref.Setup >= len(containers) {
		return formatError("%s has no setup %d", ps3Project, ref.Setup)
	}

	container, err := parseOrdered(containers[ref.Setup])
	if err != nil {
		return err
	}

	var beds []json.RawMessage
	if err = json.Unmarshal(container.get("beds"), &beds); err != nil || ref.Bed >= len(beds) {
		return formatError("%s has no bed %d in setup %d", ps3Project, ref.Bed, ref.Setup)
	}

	if err = container.set("beds", []json.RawMessage{beds[ref.Bed]}); err != nil {
		return err
	}

	if err = ps3ClearScripts(container); err != nil {
		return err
	}

	kept, err := container.marshal()
	if err != nil {
		return err
	}

	return doc.set("config_containers", []json.RawMessage{kept})
}

// ps3SetPrintable writes, for every object, the instances that are not
// printable, in the form the alpha12 writer uses: {"ord": n, "printable":
// false}, where n is the position of the build item.
func ps3SetPrintable(doc *ordered, rw *rewriting) error {
	var objects []json.RawMessage
	if raw := doc.get("objects"); raw != nil {
		if err := json.Unmarshal(raw, &objects); err != nil {
			return formatError("%s objects: %v", ps3Project, err)
		}
	}

	off := map[int][]int{}

	for _, object := range rw.project.Objects {
		for _, inst := range object.Instances {
			if !rw.selected[inst.Item] {
				off[object.ResourceID] = append(off[object.ResourceID], inst.Item)
			}
		}
	}

	listed := map[int]bool{}

	for i, raw := range objects {
		changed, id, err := ps3SetObject(raw, off)
		if err != nil {
			return err
		}

		objects[i], listed[id] = changed, true
	}

	// An object the project file does not list yet gets an entry of its own,
	// so none of its instances is printed by mistake.
	for _, object := range rw.project.Objects {
		ords := off[object.ResourceID]
		if listed[object.ResourceID] || len(ords) == 0 {
			continue
		}

		raw, err := json.Marshal(ps3Object{ID: object.ResourceID, Instances: offList(ords)})
		if err != nil {
			return fmt.Errorf("threemf: encode object %d: %w", object.ResourceID, err)
		}

		objects = append(objects, raw)
	}

	return doc.set("objects", objects)
}

// ps3Object is the smallest entry of the objects list: an object and the
// instances of it that are not printable.
type ps3Object struct {
	ID        int      `json:"id"`
	Instances []ps3Off `json:"instances"`
}

// ps3SetObject replaces the instances list of one object of the project file
// with the build items in off, and returns the object and its id.
func ps3SetObject(raw json.RawMessage, off map[int][]int) (json.RawMessage, int, error) {
	object, err := parseOrdered(raw)
	if err != nil {
		return nil, 0, err
	}

	id := -1
	if json.Unmarshal(object.get("id"), &id) != nil {
		return raw, -1, nil //nolint:nilerr // an object without an id is copied as it is.
	}

	if list := offList(off[id]); len(list) == 0 {
		object.remove("instances")
	} else if err = object.set("instances", list); err != nil {
		return nil, 0, err
	}

	out, err := object.marshal()

	return out, id, err
}

// ps3Off is one instance the alpha12 writer marks as not printable.
type ps3Off struct {
	Ord       int  `json:"ord"`
	Printable bool `json:"printable"`
}

func offList(ords []int) []ps3Off {
	out := make([]ps3Off, len(ords))
	for i, ord := range ords {
		out[i] = ps3Off{Ord: ord}
	}

	return out
}
