package threemf

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// Selection is what a reprint prints: some instances of one bed.
type Selection struct {
	// Bed is the bed to print.
	Bed BedRef `json:"bed"`
	// Items are the build items to print, by Instance.Item. Each must stand
	// whole on Bed. Nil selects every printable instance whole on Bed.
	Items []int `json:"items,omitempty"`
	// Positions put copies of build items on Bed instead, as Arrange
	// answers them. The first position of an item moves the item itself, and
	// each further one adds a copy of it. The item may stand on any bed of
	// the project, or on none. A selection with positions names no Items.
	Positions []Position `json:"positions,omitempty"`
}

// Rewrite writes to w a copy of the 3MF file in r for printing the selection.
// project must be what Read returned for the same file.
//
// In the copy, the selected instances are printable and every other instance
// is not, and the chosen bed is the one a slicer's command line slices: bed 0
// of a PrusaSlicer 2.x project, and the first bed of the first setup of a
// PrusaSlicer 3.0 project. Slicing the copy with `prusa-slicer --export-gcode`
// gives the selected instances and nothing else.
//
// Only the entries that must change are written anew. Every other entry is
// copied as it is, compressed bytes and all. A selection the project cannot
// satisfy is ErrSelection, and a project that does not match the file is
// ErrMismatch.
//
// A project CheckSettings refuses is refused here too. The copy keeps
// post_process empty, so the slicer runs no program for it.
//
// Only a PrusaSlicer project is rewritten. A project of any other dialect is
// ErrUnsupported: this package does not make another program's file slice
// the same way in PrusaSlicer.
func Rewrite(
	ctx context.Context,
	w io.Writer,
	r io.ReaderAt,
	size int64,
	project *Project,
	sel Selection,
	limits Limits,
) error {
	limits = limits.withDefaults()

	if project != nil && !rewritable(project.Producer.Dialect) {
		return fmt.Errorf("%w: %q, read as %s", ErrUnsupported, project.Producer.Application, project.Producer.Dialect)
	}

	selected, err := resolveSelection(project, sel)
	if err != nil {
		return err
	}

	placed, err := resolvePositions(project, sel)
	if err != nil {
		return err
	}

	a, err := openArchive(r, size, limits)
	if err != nil {
		return err
	}

	if err = checkArchiveSettings(ctx, a); err != nil {
		return err
	}

	d := dialectNamed(project.Producer.Dialect)
	rw := &rewriting{
		a:        a,
		limits:   limits,
		project:  project,
		sel:      sel,
		selected: selected,
		placed:   placed,
		model:    readRelationships(ctx, a).model,
		changes:  map[string]func(io.Writer) error{},
	}

	if err = d.rewrite(ctx, rw); err != nil {
		return err
	}

	return rw.write(ctx, w)
}

// rewritable reports whether Rewrite writes projects of the dialect: the
// PrusaSlicer ones.
func rewritable(name Dialect) bool {
	return name == DialectPrusaSlicer2 || name == DialectPrusaSlicer3
}

func dialectNamed(name Dialect) dialect {
	for _, d := range dialects() {
		if d.name() == name {
			return d
		}
	}

	return &generic{}
}

// resolveSelection checks a selection against the project and returns the
// set of selected build items.
func resolveSelection(p *Project, sel Selection) (map[int]bool, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: no project", ErrSelection)
	}

	bed := p.Bed(sel.Bed)
	if bed == nil {
		return nil, fmt.Errorf("%w: no bed %d in setup %d", ErrSelection, sel.Bed.Bed, sel.Bed.Setup)
	}

	if len(sel.Positions) > 0 {
		return positionedItems(sel)
	}

	onBed := map[int]*Instance{}

	for _, ref := range bed.Instances {
		inst := p.Instance(ref)
		if inst != nil && (inst.Placement == PlacementInside || inst.Placement == PlacementUnknown) {
			onBed[inst.Item] = inst
		}
	}

	out := map[int]bool{}

	if sel.Items == nil {
		for item, inst := range onBed {
			if inst.Printable {
				out[item] = true
			}
		}
	}

	for _, item := range sel.Items {
		if onBed[item] == nil {
			return nil, fmt.Errorf("%w: item %d does not stand whole on the bed", ErrSelection, item)
		}

		out[item] = true
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("%w: nothing to print", ErrSelection)
	}

	return out, nil
}

// rewriting is the state of one Rewrite.
type rewriting struct {
	a        *archive
	limits   Limits
	project  *Project
	sel      Selection
	selected map[int]bool
	// placed is what the selection's positions do, and nil without any.
	placed *placing
	model  string
	// beds is the bed of every build item that stands on one.
	beds map[int]int
	// changes are the entries written anew, by name, each with the function
	// that writes its new content.
	changes map[string]func(io.Writer) error
}

// bedOf is the bed index of a build item in its setup, and -1 when it stands
// on none.
func (rw *rewriting) bedOf(item int) int {
	if rw.placed != nil {
		if _, moved := rw.placed.moves[item]; moved {
			return rw.sel.Bed.Bed
		}
	}

	if rw.beds == nil {
		rw.beds = map[int]int{}

		for _, object := range rw.project.Objects {
			for _, inst := range object.Instances {
				if inst.Bed != nil {
					rw.beds[inst.Item] = inst.Bed.Bed
				}
			}
		}
	}

	if bed, ok := rw.beds[item]; ok {
		return bed
	}

	return -1
}

// replace sets the new content of one entry.
func (rw *rewriting) replace(name string, data []byte) error {
	rw.changes[name] = func(w io.Writer) error {
		_, err := w.Write(data)

		return err //nolint:wrapcheck // the zip writer's own error, wrapped by write.
	}

	return nil
}

// rewriteItems rewrites the attributes of every build item of the root model
// through edit, and checks the build has as many items as the project.
func (rw *rewriting) rewriteItems(ctx context.Context, edit func(item int, attrs *tagAttrs, selected bool)) error {
	f := rw.a.entry(rw.model)
	if f == nil {
		return formatError("no model entry %q", rw.model)
	}

	rw.changes[rw.model] = func(w io.Writer) error {
		body, err := rw.a.openModel(f)
		if err != nil {
			return err
		}

		defer func() { _ = body.Close() }() // read to the end or failed; nothing to report.

		items, inBuild := 0, false
		templates := map[int]*tag{}

		err = scanTags(ctx, w, body, func(t *tag) {
			switch {
			case t.closing && t.local() == "build":
				inBuild = false
				t.prefix = rw.copiesMarkup(templates)
			case !t.closing && t.local() == "build":
				inBuild = !t.empty
			case !t.closing && inBuild && t.local() == "item":
				rw.placeItem(items, t, templates)
				edit(items, &t.attrs, rw.selected[items])
				items++
			}
		})
		if err != nil {
			return err
		}

		if items != rw.project.Items() {
			return fmt.Errorf("%w: the build has %d items and the project %d", ErrMismatch, items, rw.project.Items())
		}

		return nil
	}

	return nil
}

// swapBedIndex swaps the bed_idx attributes 0 and chosen in one XML entry, if
// the file has it.
func (rw *rewriting) swapBedIndex(ctx context.Context, name string, chosen int) error {
	f := rw.a.entry(name)
	if f == nil {
		return nil
	}

	data, err := readAll(ctx, f, rw.limits.MaxMetadataBytes, "MaxMetadataBytes")
	if err != nil {
		return err
	}

	from, to := "0", strconv.Itoa(chosen)

	var out bytes.Buffer

	err = scanTags(ctx, &out, bytes.NewReader(data), func(t *tag) {
		if t.closing {
			return
		}

		switch t.attrs.get("bed_idx") {
		case from:
			t.attrs.set("bed_idx", to)
		case to:
			t.attrs.set("bed_idx", from)
		}
	})
	if err != nil {
		return err
	}

	return rw.replace(name, out.Bytes())
}

// write writes the new archive: each entry in the order of the old one,
// copied raw unless it changed.
func (rw *rewriting) write(ctx context.Context, w io.Writer) error {
	zw := zip.NewWriter(w)

	for _, f := range rw.a.zip.File {
		if err := ctx.Err(); err != nil {
			return err //nolint:wrapcheck // the caller's own cancellation.
		}

		change, ok := rw.changes[f.Name]
		if !ok {
			if err := zw.Copy(f); err != nil {
				return fmt.Errorf("threemf: copy %s: %w", f.Name, err)
			}

			continue
		}

		header := &zip.FileHeader{Name: f.Name, Comment: f.Comment, Method: zip.Deflate, Modified: f.Modified}

		entry, err := zw.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("threemf: write %s: %w", f.Name, err)
		}

		if err = change(entry); err != nil {
			var limit *LimitError
			if errors.As(err, &limit) || errors.Is(err, ErrFormat) || errors.Is(err, ErrMismatch) {
				return err
			}

			return fmt.Errorf("threemf: write %s: %w", f.Name, err)
		}
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("threemf: finish the archive: %w", err)
	}

	return nil
}

func boolAttr(value bool) string {
	if value {
		return "1"
	}

	return "0"
}

// tag is one XML tag the scanner found: a start tag, an empty-element tag or
// an end tag.
type tag struct {
	name    string
	closing bool
	empty   bool
	attrs   tagAttrs
	// prefix is written before the tag, after the opening bracket already
	// out. A writer that adds tags in front of this one ends it with "<".
	prefix string
}

// clone is a copy of the tag whose attributes change apart from the
// original's.
func (t *tag) clone() *tag {
	out := *t
	out.attrs.list = slices.Clone(t.attrs.list)
	out.prefix = ""

	return &out
}

// local is the tag's name when it has no namespace prefix, and "" otherwise.
// The 3MF core elements are written without one.
func (t *tag) local() string {
	if strings.Contains(t.name, ":") {
		return ""
	}

	return t.name
}

// tagAttr is one attribute as written, with its value still escaped.
type tagAttr struct {
	name  string
	value string
	quote byte
}

// tagAttrs are a tag's attributes in their written order.
type tagAttrs struct {
	list    []tagAttr
	changed bool
}

func (a *tagAttrs) get(name string) string {
	for _, attr := range a.list {
		if attr.name == name {
			return unescapeAttr(attr.value)
		}
	}

	return ""
}

func (a *tagAttrs) set(name, value string) {
	escaped := escapeAttr(value)

	for i := range a.list {
		if a.list[i].name == name {
			if a.list[i].value != escaped {
				a.list[i].value, a.changed = escaped, true
			}

			return
		}
	}

	a.list = append(a.list, tagAttr{name: name, value: escaped, quote: '"'})
	a.changed = true
}

// moveTransform moves the transform attribute by (dx, dy). An item without one
// stands at the identity.
func (a *tagAttrs) moveTransform(dx, dy float64) {
	tr, ok := parseTransform(a.get("transform"))
	if !ok {
		return
	}

	a.set("transform", tr.Translated(dx, dy).String())
}

func escapeAttr(value string) string {
	var b strings.Builder
	if err := xmlEscape(&b, value); err != nil {
		return value
	}

	return b.String()
}

func xmlEscape(w io.Writer, value string) error {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	_, err := r.WriteString(w, value)

	return err //nolint:wrapcheck // a strings.Builder does not fail.
}

func unescapeAttr(value string) string {
	if !strings.Contains(value, "&") {
		return value
	}

	r := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'")

	return r.Replace(value)
}

// maxTagBytes bounds one tag. A build item is a few hundred bytes.
const maxTagBytes = 1 << 20

// scanTags copies XML from src to dst. Text, comments, CDATA sections and
// declarations go through byte for byte. Every tag is passed to fn, and a tag
// whose attributes fn changed is written anew with them; any other tag goes
// through as written.
func scanTags(ctx context.Context, dst io.Writer, src io.Reader, fn func(*tag)) error {
	in := bufio.NewReaderSize(src, 64<<10)  //nolint:mnd // a read buffer.
	out := bufio.NewWriterSize(dst, 64<<10) //nolint:mnd // a write buffer.
	tags := 0

	for {
		text, err := in.ReadSlice('<')
		if _, werr := out.Write(text); werr != nil {
			return werr //nolint:wrapcheck // the writer's error, wrapped by the caller.
		}

		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return out.Flush() //nolint:wrapcheck // as above.
		case err != nil:
			return err //nolint:wrapcheck // the reader's error: a limit, or a broken entry.
		}

		if tags++; tags%4096 == 0 {
			if err = ctx.Err(); err != nil {
				return err //nolint:wrapcheck // the caller's own cancellation.
			}
		}

		if err = scanMarkup(in, out, fn); err != nil {
			return err
		}
	}
}

// scanMarkup handles what follows one '<', which has been written already.
func scanMarkup(in *bufio.Reader, out *bufio.Writer, fn func(*tag)) error {
	next, err := in.Peek(1)
	if err != nil {
		return formatError("XML ends inside a tag")
	}

	switch next[0] {
	case '!':
		head, _ := in.Peek(8) //nolint:mnd // the longest opener after '<' is ![CDATA[.

		switch {
		case bytes.HasPrefix(head, []byte("!--")):
			return copyThrough(in, out, "-->")
		case bytes.HasPrefix(head, []byte("![CDATA[")):
			return copyThrough(in, out, "]]>")
		}

		return copyThrough(in, out, ">")
	case '?':
		return copyThrough(in, out, "?>")
	}

	raw, err := readTag(in)
	if err != nil {
		return err
	}

	t, ok := parseTag(raw)
	if !ok {
		return formatError("a tag that is not XML")
	}

	fn(t)

	if t.prefix != "" {
		if _, err = out.WriteString(t.prefix); err != nil {
			return err //nolint:wrapcheck // the writer's error, wrapped by the caller.
		}
	}

	if !t.attrs.changed {
		_, err = out.Write(raw)

		return err //nolint:wrapcheck // the writer's error, wrapped by the caller.
	}

	_, err = out.WriteString(t.render())

	return err //nolint:wrapcheck // as above.
}

// copyThrough copies bytes up to and including end.
func copyThrough(in *bufio.Reader, out *bufio.Writer, end string) error {
	matched := 0

	for {
		c, err := in.ReadByte()
		if err != nil {
			return formatError("XML ends inside markup")
		}

		if err = out.WriteByte(c); err != nil {
			return err //nolint:wrapcheck // the writer's error, wrapped by the caller.
		}

		switch c {
		case end[matched]:
			matched++
		case end[0]:
			matched = 1
		default:
			matched = 0
		}

		if matched == len(end) {
			return nil
		}
	}
}

// readTag reads a tag up to and including its '>', not counting a '>' inside
// a quoted value.
func readTag(in *bufio.Reader) ([]byte, error) {
	var (
		raw   []byte
		quote byte
	)

	for {
		c, err := in.ReadByte()
		if err != nil {
			return nil, formatError("XML ends inside a tag")
		}

		raw = append(raw, c)
		if len(raw) > maxTagBytes {
			return nil, formatError("a tag longer than %d bytes", maxTagBytes)
		}

		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return raw, nil
		}
	}
}

// parseTag reads a tag's name and attributes from its text after the '<'.
func parseTag(raw []byte) (*tag, bool) {
	body := strings.TrimSuffix(string(raw), ">")
	t := &tag{}

	if rest, ok := strings.CutPrefix(body, "/"); ok {
		t.closing = true
		t.name = strings.TrimSpace(rest)

		return t, t.name != ""
	}

	if rest, ok := strings.CutSuffix(body, "/"); ok {
		t.empty = true
		body = rest
	}

	end := strings.IndexAny(body, " \t\r\n")
	if end < 0 {
		end = len(body)
	}

	t.name = body[:end]
	if t.name == "" {
		return nil, false
	}

	rest := body[end:]

	for {
		rest = strings.TrimLeft(rest, " \t\r\n")
		if rest == "" {
			return t, true
		}

		name, after, ok := strings.Cut(rest, "=")
		if !ok {
			return nil, false
		}

		after = strings.TrimLeft(after, " \t\r\n")
		if after == "" || (after[0] != '"' && after[0] != '\'') {
			return nil, false
		}

		quote := after[0]

		closeAt := strings.IndexByte(after[1:], quote)
		if closeAt < 0 {
			return nil, false
		}

		t.attrs.list = append(t.attrs.list, tagAttr{
			name: strings.TrimSpace(name), value: after[1 : 1+closeAt], quote: quote,
		})
		rest = after[2+closeAt:]
	}
}

// render writes a tag with its attributes.
func (t *tag) render() string {
	var b strings.Builder

	b.WriteString(t.name)

	for _, a := range t.attrs.list {
		b.WriteByte(' ')
		b.WriteString(a.name)
		b.WriteByte('=')
		b.WriteByte(a.quote)
		b.WriteString(a.value)
		b.WriteByte(a.quote)
	}

	if t.empty {
		b.WriteString("/>")
	} else {
		b.WriteByte('>')
	}

	return b.String()
}

// ordered is a JSON object that keeps the order of its members, so a rewrite
// changes only the members it sets.
type ordered struct {
	members []member
}

type member struct {
	key   string
	value json.RawMessage
}

func parseOrdered(data []byte) (*ordered, error) {
	dec := json.NewDecoder(bytes.NewReader(data))

	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, formatError("a JSON object was expected")
	}

	out := &ordered{}

	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, formatError("JSON: %v", err)
		}

		key, ok := tok.(string)
		if !ok {
			return nil, formatError("JSON: a key was expected")
		}

		var value json.RawMessage
		if err = dec.Decode(&value); err != nil {
			return nil, formatError("JSON: %v", err)
		}

		out.members = append(out.members, member{key: key, value: value})
	}

	return out, nil
}

func (o *ordered) get(key string) json.RawMessage {
	for _, m := range o.members {
		if m.key == key {
			return m.value
		}
	}

	return nil
}

func (o *ordered) set(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("threemf: encode %s: %w", key, err)
	}

	for i := range o.members {
		if o.members[i].key == key {
			o.members[i].value = raw

			return nil
		}
	}

	o.members = append(o.members, member{key: key, value: raw})

	return nil
}

func (o *ordered) remove(key string) {
	for i := range o.members {
		if o.members[i].key == key {
			o.members = append(o.members[:i], o.members[i+1:]...)

			return
		}
	}
}

func (o *ordered) marshal() ([]byte, error) {
	var b bytes.Buffer

	b.WriteByte('{')

	for i, m := range o.members {
		if i > 0 {
			b.WriteByte(',')
		}

		key, err := json.Marshal(m.key)
		if err != nil {
			return nil, fmt.Errorf("threemf: encode a key: %w", err)
		}

		b.Write(key)
		b.WriteByte(':')
		b.Write(m.value)
	}

	b.WriteByte('}')

	return b.Bytes(), nil
}
