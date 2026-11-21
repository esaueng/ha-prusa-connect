package simulator

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"slices"
	"strings"
)

// The FILE_INFO node types the firmware reports. Everything that is not a
// folder is a file of some kind, and PRINT_FILE is what a printable one is.
const (
	nodeFolder    = "FOLDER"
	nodePrintFile = "PRINT_FILE"
)

// defaultFileSize is what the entries in the default tree weigh. It is a fixed
// figure so two runs of one test see the same printer.
const defaultFileSize = 1 << 20

// defaultModified is the timestamp the default tree carries: 2026-01-01T00:00:00Z.
const defaultModified = 1767225600

// File is one entry in a simulated printer's file tree.
//
// Path is the firmware's own absolute spelling, storage-prefixed with its
// leading separator, because that is what a printer receives and answers with.
// The connector turns it into the relative namespace the rest of the product
// uses.
type File struct {
	// Path is the absolute firmware path, such as /usb/models/part.bgcode.
	Path string
	// Folder is whether the entry holds other entries.
	Folder bool
	// Size is the bytes the entry occupies. Ignored for a folder.
	Size int64
	// Modified is seconds since the Unix epoch. Zero is a board with no clock,
	// which is what a real one without an RTC reports.
	Modified int64
	// ReadOnly refuses a change to this entry.
	ReadOnly bool
}

// fileNode returns the entry at an absolute firmware path.
//
// A storage mountpoint is a folder whether or not the configured tree names it,
// because a printer with a stick in it always has a root to list.
func (p *Printer) fileNode(path string) (File, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	path = p.resolveLocked(path)

	for _, entry := range p.files {
		if entry.Path == path {
			return entry, true
		}
	}

	for _, device := range p.storages {
		if device.Mountpoint == path {
			return File{Path: path, Folder: true, ReadOnly: device.ReadOnly}, true
		}
	}

	return File{}, false
}

// fileChildren returns the entries directly inside a path, sorted by path so
// two runs of one listing produce the same order.
func (p *Printer) fileChildren(path string) []File {
	path = normalisePath(path)

	p.mu.Lock()
	defer p.mu.Unlock()

	prefix := path + "/"

	var out []File

	for _, entry := range p.files {
		rest, inside := strings.CutPrefix(entry.Path, prefix)
		if !inside || rest == "" || strings.Contains(rest, "/") {
			continue
		}

		out = append(out, entry)
	}

	slices.SortFunc(out, func(a, b File) int { return strings.Compare(a.Path, b.Path) })

	return out
}

// createFolder adds a folder. It reports false for a path that is already
// there, which is what CREATE_FOLDER on an existing name answers.
func (p *Printer) createFolder(path string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	path = p.resolveLocked(path)

	for _, entry := range p.files {
		if entry.Path == path {
			return false
		}
	}

	p.files = append(p.files, File{Path: path, Folder: true, Modified: defaultModified})

	return true
}

// removeEntry deletes one entry. folder says which command asked: DELETE_FOLDER
// takes a folder and everything under it, and DELETE_FILE takes a file and
// refuses a folder.
func (p *Printer) removeEntry(path string, folder bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	path = p.resolveLocked(path)

	index := slices.IndexFunc(p.files, func(entry File) bool { return entry.Path == path })
	if index < 0 || p.files[index].Folder != folder || p.files[index].ReadOnly {
		return false
	}

	// A file the board will not let go of. See Printer.plant: the survivor of
	// an 8.3 collision holds a lock nothing in a session releases. It is not
	// the read_only flag, which the firmware raises only for a staging
	// directory.
	if _, locked := p.locked[path]; locked {
		return false
	}

	prefix := path + "/"
	p.files = slices.DeleteFunc(p.files, func(entry File) bool {
		return entry.Path == path || strings.HasPrefix(entry.Path, prefix)
	})

	return true
}

// holdsLocked reports whether the storage holds an entry at a path, by either
// of its names and without regard to case, as FAT finds one. The caller holds
// the lock.
func (p *Printer) holdsLocked(path string) bool {
	path = p.resolveLocked(path)

	return slices.ContainsFunc(p.files, func(entry File) bool { return entry.Path == path })
}

// printable is the long-name path of the file a path names, and reports whether
// it is one this printer can start a job from. A folder is not one, and neither
// is a name the storage does not hold. The path may use either of the file's
// names, as a firmware path may.
//
// A printer reads the file off its own storage, so a path it does not hold is a
// print it cannot begin. The simulator answers that with FILE_NOT_FOUND, as it
// answers SEND_FILE_INFO on the same path. The reason is the simulator's own
// word. See the reason block in commands.go.
func (p *Printer) printable(path string) (string, bool) {
	node, found := p.fileNode(path)

	return node.Path, found && !node.Folder
}

// fileInfoData is the FILE_INFO payload for one path, with the field names
// shipping firmware uses.
//
// The node's own path is its 8.3 spelling, whichever spelling it was asked by.
// A child carries its short name as name, its long name as display_name, and
// no path. That is what Prusa-Firmware-Buddy src/connect/render.cpp renders:
// the EventType::FileInfo arm after get_SFN_path, and DirRenderer for the
// children.
func (p *Printer) fileInfoData(path string) (map[string]any, bool) {
	node, found := p.fileNode(path)
	if !found {
		return nil, false
	}

	data := nodeData(node)
	data["path"] = shortPath(node.Path)

	if !node.Folder {
		// The firmware reads the picture out of a print file only when it is
		// asked about that one file, so a folder's children never carry it.
		if isPrintFile(node.Path) {
			data["preview"] = previewPicture()
			// The slicer's headers come with the picture. The tree's files
			// have no contents to read them out of, so each names the same
			// print time, in the slicer's own spelling.
			data["estimated printing time (normal mode)"] = "1h 15m 30s"
		}

		return data, true
	}

	children := p.fileChildren(node.Path)
	entries := make([]any, 0, len(children))

	for _, child := range children {
		entries = append(entries, nodeData(child))
	}

	data["children"] = entries

	return data, true
}

// isPrintFile reports whether the firmware would read a picture out of the file
// at path: a .gcode or a .bgcode.
func isPrintFile(path string) bool {
	lower := strings.ToLower(path)

	return strings.HasSuffix(lower, ".gcode") || strings.HasSuffix(lower, ".bgcode")
}

// previewPicture is the base64 PNG every print file in a simulated tree reports.
// The tree's files have no contents to read a picture out of, so one drawn
// picture stands for all of them: a square block on a plate.
func previewPicture() string {
	const side, inset = 64, 18

	plate := color.NRGBA{R: 0x3a, G: 0x3f, B: 0x47, A: 0xff} //nolint:mnd // a colour is its numbers.
	block := color.NRGBA{R: 0xfa, G: 0x68, B: 0x31, A: 0xff} //nolint:mnd // a colour is its numbers.

	picture := image.NewNRGBA(image.Rect(0, 0, side, side))

	for y := range side {
		for x := range side {
			shade := plate
			if x >= inset && x < side-inset && y >= inset && y < side-inset {
				shade = block
			}

			picture.SetNRGBA(x, y, shade)
		}
	}

	var encoded bytes.Buffer

	// Encoding into memory fails only on an image png cannot represent, and an
	// NRGBA of this size is not one.
	_ = png.Encode(&encoded, picture)

	return base64.StdEncoding.EncodeToString(encoded.Bytes())
}

// nodeData is one node as the firmware spells it: the 8.3 short name as name,
// and the long name as display_name.
func nodeData(node File) map[string]any {
	name := baseName(node.Path)

	data := map[string]any{
		"name":         shortSpelling(name),
		"display_name": name,
		"type":         nodePrintFile,
		"read_only":    node.ReadOnly,
		"m_timestamp":  node.Modified,
	}

	if node.Folder {
		data["type"] = nodeFolder

		return data
	}

	data["size"] = node.Size

	return data
}

// defaultTree is what a printer with no configured files holds: one folder with
// a file in it and one file beside it, on the first storage.
//
// It is not empty, because a listing checked against an empty storage passes
// whatever the code under it does.
func defaultTree(storages []Storage) []File {
	if len(storages) == 0 {
		return nil
	}

	root := storages[0].Mountpoint

	return []File{
		{Path: root + "/examples", Folder: true, Modified: defaultModified},
		{Path: root + "/examples/benchy.bgcode", Size: defaultFileSize, Modified: defaultModified},
		{Path: root + "/tangle.bgcode", Size: defaultFileSize, Modified: defaultModified},
	}
}

// The 8.3 short name a FAT storage gives a long name, as this simulator models
// it: six characters of the name, a numeric tail, and three of the extension.
//
// It models what a CORE One L at 6.8.1+16182 does and is not a transcription
// of the firmware's allocator. Two files sent as bug009-probe.gcode and
// bug009-control.gcode both shorten to BUG009~1.GCO, and the board reports
// both transfers finished while it lists only the first.
//
// The tail is ~1 for every name. FatFs numbers its way past a slot that is
// taken, and the board this models does not, so the numbering is not
// modelled.
//
// shortNameBody is the eight characters a name that already fits 8.3 may have
// before its dot. Such a name is its own short name and gets no tail.
const (
	shortNameBody      = 8
	shortNameStem      = 6
	shortNameExtension = 3
	shortNameTail      = "~1"
)

// shortName is the 8.3 spelling of one long name: uppercase, with everything
// FAT does not allow in a short name dropped.
func shortName(name string) string {
	stem, extension := name, ""
	if index := strings.LastIndex(name, "."); index > 0 {
		stem, extension = name[:index], name[index+1:]
	}

	stem = shortNameCharacters(stem, shortNameStem)
	extension = shortNameCharacters(extension, shortNameExtension)

	short := stem + shortNameTail
	if extension != "" {
		short += "." + extension
	}

	return short
}

// shortSpelling is the 8.3 name FAT gives one long name. A name that already
// fits 8.3 is its own short name in upper case, and every other name gets the
// numbered form shortName models.
func shortSpelling(name string) string {
	if fitsShortName(name) {
		return strings.ToUpper(name)
	}

	return shortName(name)
}

// fitsShortName reports whether a name is already a valid 8.3 name: at most
// eight characters, one dot, at most three more, and only the characters a short
// name may hold. Case does not matter, because FAT keeps such a name's case in a
// flag and not in a long name.
func fitsShortName(name string) bool {
	stem, extension, dotted := strings.Cut(name, ".")
	if stem == "" || len(stem) > shortNameBody || strings.Contains(extension, ".") {
		return false
	}

	if dotted && (extension == "" || len(extension) > shortNameExtension) {
		return false
	}

	for _, letter := range stem + extension {
		if letter > 0x7e || letter <= ' ' || strings.ContainsRune(`"*+,/:;<=>?[\]|.`, letter) {
			return false
		}
	}

	return true
}

// shortPath is an absolute path with every segment below the mountpoint in its
// 8.3 spelling, which is how the firmware names the node FILE_INFO is about.
func shortPath(path string) string {
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for at := 1; at < len(segments); at++ {
		segments[at] = shortSpelling(segments[at])
	}

	return "/" + strings.Join(segments, "/")
}

// resolveLocked is the long-name path of the entry a path names, whatever
// spelling each segment uses. FAT finds an entry by its long name or its short
// one, ignoring case, so a printer asked about /usb/TANGLE~1.BGC answers about
// /usb/tangle.bgcode. A segment that names nothing is kept as it was given, so a
// path to an entry that does not exist stays one. The caller holds the lock.
func (p *Printer) resolveLocked(path string) string {
	path = normalisePath(path)

	mountpoint, rest, below := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !below {
		return path
	}

	var resolved strings.Builder

	resolved.WriteString("/" + mountpoint)

	for segment := range strings.SplitSeq(rest, "/") {
		resolved.WriteString("/" + p.segmentLocked(resolved.String(), segment))
	}

	return resolved.String()
}

// segmentLocked is the long name of the entry in one directory that a segment
// names by either of its spellings. The caller holds the lock.
func (p *Printer) segmentLocked(directory, segment string) string {
	for _, entry := range p.files {
		if parentPath(entry.Path) != directory {
			continue
		}

		name := baseName(entry.Path)
		if strings.EqualFold(name, segment) || strings.EqualFold(shortSpelling(name), segment) {
			return name
		}
	}

	return segment
}

// shortNameCharacters is the first count characters of one component, upper
// case, with the characters a short name cannot hold left out.
func shortNameCharacters(text string, count int) string {
	var out strings.Builder

	for _, letter := range strings.ToUpper(text) {
		if out.Len() == count {
			break
		}

		if !strings.ContainsRune(`+,;=[]. `, letter) {
			out.WriteRune(letter)
		}
	}

	return out.String()
}

// shortNameHolder is the sibling of path that already holds its 8.3 short name,
// and false when no sibling does. The caller holds the lock.
func (p *Printer) shortNameHolder(path string) (string, bool) {
	directory, name := parentPath(path), baseName(path)
	if directory == "" {
		return "", false
	}

	// A name that fits 8.3 is its own short name and takes no numbered slot.
	if fitsShortName(name) {
		return "", false
	}

	short := shortName(name)

	for _, entry := range p.files {
		if parentPath(entry.Path) != directory || entry.Path == path {
			continue
		}

		if shortSpelling(baseName(entry.Path)) == short {
			return entry.Path, true
		}
	}

	return "", false
}

// lockEntry leaves one path undeletable for the life of the printer, as the
// board does to the survivor of an 8.3 collision. DELETE answers "File is
// busy", because FatFs identifies a held object by its directory entry slot
// and never by name, and the survivor sits in the slot the abandoned transfer
// left locked. Only remounting the stick clears it. The caller holds the lock.
func (p *Printer) lockEntry(path string) {
	if p.locked == nil {
		p.locked = make(map[string]struct{})
	}

	p.locked[path] = struct{}{}
}

// normalisePath drops a trailing separator so that /usb/models/ and /usb/models
// name one entry.
func normalisePath(path string) string {
	path = strings.TrimSpace(path)
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}

	return path
}

func baseName(path string) string {
	if index := strings.LastIndex(path, "/"); index >= 0 {
		return path[index+1:]
	}

	return path
}

// parentPath is the directory one entry sits in, empty for a path with no
// directory above it.
func parentPath(path string) string {
	if index := strings.LastIndex(path, "/"); index > 0 {
		return path[:index]
	}

	return ""
}
