package xbuddy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// The events a printer volunteers about itself and its files. INFO answers
// SEND_INFO and is the only thing that says what storages a printer has. The
// firmware also sends one unasked when a USB drive is put in or pulled out,
// since has_usb is part of the fingerprint it watches (printer.cpp). A file
// created through a transfer, PrusaLink or a command is a FILE_INFO, and any
// other change a FILE_CHANGED (planner.cpp, ChangedPath).
const (
	eventInfo        = "INFO"
	eventFileInfo    = "FILE_INFO"
	eventFileChanged = "FILE_CHANGED"
)

// filesChangedDepth is how many file changes wait for the host. A change that
// finds it full is dropped, and the folder it named keeps rows a refresh mends.
const filesChangedDepth = 64

// folderType is the FILE_INFO node type the firmware gives a directory. Every
// other type it reports is a file of some kind, and the vocabulary of those is
// the firmware's rather than this driver's.
const folderType = "FOLDER"

var _ connectorapi.Filesystem = (*Session)(nil)

// Storages is what the printer last said its storage devices are.
//
// It reads the answer to the SEND_INFO this session sends when a connection
// attaches and puts nothing on the wire itself, so a printer that has not
// answered yet reports no storages rather than an error. A device that came
// back with a stick removed reports the list from its newest INFO.
func (s *Session) Storages(_ context.Context) ([]connectorapi.Storage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.storages), nil
}

// List returns one directory of the printer's own file tree.
//
// The empty path is the root, whose entries are the storages themselves. It is
// answered from a fresh INFO rather than a FILE_INFO, because the firmware has
// no node above a mountpoint to be asked about.
//
// Every other path is a SEND_FILE_INFO, and the FILE_INFO event that answers it
// carries the node and its children.
func (s *Session) List(ctx context.Context, path string) (connectorapi.Listing, error) {
	return s.listDirectory(ctx, path, 0)
}

// listDirectory is List with a floor under the recollection it falls back to. A
// caller reading a directory takes the newest one there is, and a readback
// judging a transfer takes only a listing the printer reported since that
// transfer began: see Session.recalled.
func (s *Session) listDirectory(
	ctx context.Context, path string, floor uint64,
) (connectorapi.Listing, error) {
	if isRoot(path) {
		// The storages are asked for again. The printer also reports a stick put
		// in or pulled out unasked, and asking catches a swap its own check
		// missed. A printer that does not answer is listed from what it last
		// said rather than not at all.
		if err := s.AskStorages(ctx); err != nil {
			s.logDebug(ctx, "the printer did not say what storages it has now, so the last answer is listed",
				slog.String("error", err.Error()))
		}

		return s.rootListing(), nil
	}

	result, err := s.Do(ctx, connectorapi.Command{
		Verb: connectorapi.VerbSendFileInfo,
		Args: map[string]string{"path": path},
	})
	if err != nil {
		return connectorapi.Listing{}, err
	}

	if !result.OK {
		// The human reason and the machine one, for the reason Session.manage
		// keeps both: this firmware does not always send the machine one. A
		// printer in ERROR answers with the human "Won't accept commands in
		// error state" and no machine reason at all. The machine reason is the
		// field to branch on; the human one is also the fault's message,
		// because the message is the detail a person reads under the title.
		said := humanReason(result.Raw)

		message := said
		if message == "" {
			message = "the printer refused to report that path"
		}

		return connectorapi.Listing{}, connectorapi.ListingRefused.
			New(message).
			With(
				fault.StringParam("path", path),
				fault.StringParam("machine_reason", result.MachineReason),
				fault.StringParam("reason", said),
			).
			Wrap(connectorapi.ErrListingRefused)
	}

	asked := relativePath(absolutePath(path, s.storageOf(path)))

	if listing, ok := listingFrom(result.Raw); ok {
		return rooted(listing, strings.TrimSuffix(asked, "/")), nil
	}

	// A firmware that answers the command with a bare terminal event and sends
	// the FILE_INFO beside it has still reported the directory, and the event
	// reached observe before the command settled.
	//
	// ponytail: the remembered listing is keyed by the firmware's 8.3 path, so a
	// folder asked for by a long name is not found here. Carry the asked path on
	// the command if such a firmware turns up.
	if listing, ok := s.recalled(asked, floor); ok {
		return listing, nil
	}

	return connectorapi.Listing{}, fmt.Errorf("%w: %s", ErrNoListing, path)
}

// rooted puts a listing under the path it was asked for.
//
// The firmware names the node it answers about by its 8.3 path, such as
// usb/SUMMER~1 for a folder asked for as usb/Summer prints, after get_SFN_path
// in render.cpp's FileInfo arm. Every other path this driver hands out uses the
// long names, so the entries are joined to the path that was asked for and the
// listing stays in the namespace the caller browses in.
func rooted(listing connectorapi.Listing, path string) connectorapi.Listing {
	if path == "" || listing.Directory.Path == path {
		return listing
	}

	listing.Directory.Path = path

	for at, entry := range listing.Entries {
		listing.Entries[at].Path = join(path, baseName(entry.Path))
	}

	return listing
}

// rootListing is the storages as directory entries. The root has no node of its
// own on the firmware side, so its own entry carries only what is true of it:
// it is a folder and it has no name.
func (s *Session) rootListing() connectorapi.Listing {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries := make([]connectorapi.File, 0, len(s.storages))
	for _, device := range s.storages {
		entries = append(entries, connectorapi.File{
			Path:        device.Name,
			Name:        device.Name,
			DisplayName: device.Name,
			Folder:      true,
			ReadOnly:    device.ReadOnly,
		})
	}

	return connectorapi.Listing{Directory: connectorapi.File{Folder: true}, Entries: entries}
}

// observe reads what an event says about the printer rather than about the
// command it answers. Both of these arrive with a command id on the firmware
// this port was written against and without one on a printer that volunteers
// them, so an absent id is no reason to skip it.
//
// It runs only after the id has been found usable. What it writes outlives the
// frame and answers the next caller, so a frame the driver refuses must not
// reach it: see Session.dispatchEvent.
func (s *Session) observe(message inboundMessage) {
	switch message.event.Event {
	case eventInfo:
		if storages, ok := storagesFrom(message.event.Data); ok {
			s.setStorages(storages)
		}

		s.reportNetwork(message.event.Data)
		s.reportTools(message.event.Data)
		s.reportFirmware(message.event.Data)
	case eventFileInfo:
		if listing, ok := listingFrom(message.raw); ok {
			s.remember(listing)

			// A folder's FILE_INFO answers a read, which lands on its own.
			// A file's is that file as it is now, asked for or volunteered.
			if !listing.Directory.Folder {
				file := listing.Directory
				file.Preview = nil
				s.fileChanged(connectorapi.FileChange{File: file})
			}
		}
	case eventFileChanged:
		s.observeFileChanged(message.event.Data)
	default:
		s.observeTransfer(message.event)
	}
}

// FilesChanged carries each change of a file the printer reported.
func (s *Session) FilesChanged() <-chan connectorapi.FileChange { return s.filesChanged }

func (s *Session) fileChanged(change connectorapi.FileChange) {
	select {
	case s.filesChanged <- change:
	default:
		s.logDebug(context.Background(), "a file change was dropped, since the host has not taken the ones before it",
			slog.String("path", change.File.Path))
	}
}

// fileChangedPayload is the data of a FILE_CHANGED, after render.cpp. The
// firmware marks the shape as not yet settled, so a field it does not send is
// read as absent and a frame with no path changes nothing.
type fileChangedPayload struct {
	OldPath   string `json:"old_path"`
	NewPath   string `json:"new_path"`
	Rescan    bool   `json:"rescan"`
	FreeSpace *int64 `json:"free_space"`
	File      struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Size     int64  `json:"size"`
		Modified int64  `json:"m_timestamp"`
	} `json:"file"`
}

// observeFileChanged reads one FILE_CHANGED. old_path is an entry deleted,
// new_path one created or changed, and new_path with rescan the folder that
// holds several changes the firmware merged into one event.
func (s *Session) observeFileChanged(data json.RawMessage) {
	var payload fileChangedPayload
	if json.Unmarshal(data, &payload) != nil {
		return
	}

	if payload.FreeSpace != nil {
		s.setFreeSpace(*payload.FreeSpace)
	}

	switch {
	case payload.OldPath != "":
		s.fileChanged(connectorapi.FileChange{
			File: connectorapi.File{
				Path:        strings.Trim(payload.OldPath, "/"),
				DisplayName: payload.File.Name,
				Folder:      payload.File.Type == folderType,
			},
			Removed: true,
		})
	case payload.NewPath != "" && payload.Rescan:
		s.fileChanged(connectorapi.FileChange{
			File:   connectorapi.File{Path: strings.Trim(payload.NewPath, "/"), Folder: true},
			Rescan: true,
		})
	case payload.NewPath != "":
		path := strings.Trim(payload.NewPath, "/")
		s.fileChanged(connectorapi.FileChange{File: connectorapi.File{
			Path:        path,
			Name:        baseName(path),
			DisplayName: payload.File.Name,
			Folder:      payload.File.Type == folderType,
			Size:        payload.File.Size,
			Modified:    payload.File.Modified,
		}})
	}
}

// setFreeSpace takes the USB free space a FILE_CHANGED carries. render.cpp
// sends it only while a USB drive is in, so it is the drive at /usb.
func (s *Session) setFreeSpace(free int64) {
	s.mu.Lock()
	storages := slices.Clone(s.storages)
	s.mu.Unlock()

	for at, storage := range storages {
		if storage.Mountpoint == "/usb" {
			storages[at].FreeSpace = free
			storages[at].FreeSpaceKnown = true
			s.setStorages(storages)

			return
		}
	}
}

// setStorages takes the list an INFO reported and wakes StoragesChanged when it
// differs from the one held, or when it is the first this connection answered.
// An INFO repeating what is held changes nothing a reader acts on.
func (s *Session) setStorages(storages []connectorapi.Storage) {
	s.mu.Lock()
	moved := !s.storagesReported || !slices.Equal(s.storages, storages)
	s.storages = storages
	s.storagesReported = true
	s.mu.Unlock()

	if moved {
		select {
		case s.storagesChanged <- struct{}{}:
		default:
		}
	}
}

// StoragesReported reports whether the printer has answered an INFO on the
// connection it holds now. Until it has, an empty Storages is a printer that has
// not said yet rather than one with no storage, and the two take different
// answers: waiting, and putting a drive in.
func (s *Session) StoragesReported() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.storagesReported
}

// StoragesChanged wakes a reader whenever an INFO changes the storages, asked
// for or volunteered. It carries no value: the reader takes the answer from
// Storages and StoragesReported.
func (s *Session) StoragesChanged() <-chan struct{} { return s.storagesChanged }

// remember keeps the newest listing only. It is a fallback for a firmware whose
// FILE_INFO carries no command id, and the frame that answered the command in
// flight is the one immediately before it, so one slot is the whole of what a
// fallback can honestly claim.
// It is stamped with a count of the listings this session has remembered, which
// is what dates one: a recollection older than the transfer that asks about it
// was taken before the file was written and is no evidence about it.
func (s *Session) remember(listing connectorapi.Listing) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.listing = listing
	s.listings++
}

// listingFloor is the stamp a listing has to carry to be newer than this
// moment. A transfer takes one when it begins and its readback recalls nothing
// older.
func (s *Session) listingFloor() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.listings + 1
}

// recalled is the newest remembered listing of one directory, and false for a
// path this session has not been told about or a recollection older than floor.
// The zero floor takes any.
func (s *Session) recalled(path string, floor uint64) (connectorapi.Listing, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listing.Directory.Path != path || s.listings < floor {
		return connectorapi.Listing{}, false
	}

	return s.listing, true
}

// storagePayload is one entry of the INFO event's storages array, with the
// field names shipping firmware uses.
//
// FreeSpace is a pointer because a printer that reported no figure and one with
// nothing left both read as zero, and only one of them refuses an upload.
type storagePayload struct {
	Mountpoint string `json:"mountpoint"`
	Type       string `json:"type"`
	FreeSpace  *int64 `json:"free_space"`
	ReadOnly   bool   `json:"read_only"`
}

// infoPayload is the INFO event's data object, as far as this driver reads it.
// Firmware is the version the machine runs now: see Session.reportFirmware.
type infoPayload struct {
	Storages []storagePayload `json:"storages"`
	Firmware string           `json:"firmware"`
}

// storagesFrom reads the storage devices out of an INFO payload.
//
// An entry with no mountpoint is dropped, which is what
// legacy/connector/xbuddy/printer/state parseStorages does: the mountpoint is
// the only field that identifies a device, and a nameless one is not
// addressable.
//
// It reports false for a payload it cannot read, which says nothing about the
// storages and must not wipe the list a readable INFO reported.
func storagesFrom(data json.RawMessage) ([]connectorapi.Storage, bool) {
	var payload infoPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, false
	}

	out := make([]connectorapi.Storage, 0, len(payload.Storages))

	for _, entry := range payload.Storages {
		if entry.Mountpoint == "" {
			continue
		}

		device := connectorapi.Storage{
			Mountpoint: entry.Mountpoint,
			Name:       relativePath(entry.Mountpoint),
			Type:       entry.Type,
			ReadOnly:   entry.ReadOnly,
		}

		if entry.FreeSpace != nil {
			device.FreeSpace = *entry.FreeSpace
			device.FreeSpaceKnown = true
		}

		out = append(out, device)
	}

	return out, true
}

// nodePayload is one FILE_INFO node, with the field names shipping firmware
// uses.
type nodePayload struct {
	Path        string        `json:"path"`
	Name        string        `json:"name"`
	DisplayName string        `json:"display_name"`
	Type        string        `json:"type"`
	Size        int64         `json:"size"`
	Modified    int64         `json:"m_timestamp"`
	ReadOnly    bool          `json:"read_only"`
	Children    []nodePayload `json:"children"`
	// Preview is a base64 PNG the firmware reads out of a print file. It sends
	// one only when it is asked about that one file, and after a download of
	// it finishes.
	Preview string `json:"preview"`
	// The firmware sends the slicer's own headers of a print file beside the
	// picture, under the names the slicer wrote them. The print time is text
	// such as "2h 5m 3s" under the slicer's name, and whole seconds under the
	// name a file prepared by Connect carries.
	//
	// Each is kept raw and read by hand, because a file can write any value
	// under these names, and a value of another type must not cost the whole
	// answer.
	EstimatedText    json.RawMessage `json:"estimated printing time (normal mode)"`
	EstimatedSeconds json.RawMessage `json:"estimated_print_time"`
	PrinterModel     json.RawMessage `json:"printer_model"`
	LayerHeight      json.RawMessage `json:"layer_height"`
	FilamentType     json.RawMessage `json:"filament_type"`
}

// fileInfoFrame is a FILE_INFO event as it arrives.
type fileInfoFrame struct {
	Event string      `json:"event"`
	Data  nodePayload `json:"data"`
}

// listingFrom reads one directory out of a FILE_INFO event.
//
// It takes the whole frame rather than the data object because that is what a
// command's Result carries back, and because the event key is what says the
// frame is a listing at all: reading a terminal event as an empty directory
// would report an empty folder for every answer this driver has no reading of.
func listingFrom(raw json.RawMessage) (connectorapi.Listing, bool) {
	var frame fileInfoFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		return connectorapi.Listing{}, false
	}

	if frame.Event != eventFileInfo || frame.Data.Path == "" {
		return connectorapi.Listing{}, false
	}

	directory := fileOf(frame.Data, relativePath(frame.Data.Path))
	directory.Preview = previewOf(frame.Data.Preview)
	directory.EstimatedSeconds = estimateOf(frame.Data)
	directory.PrinterModel = headerText(frame.Data.PrinterModel)
	directory.LayerHeightMM = layerHeightOf(frame.Data.LayerHeight)
	directory.Material = headerText(frame.Data.FilamentType)

	entries := make([]connectorapi.File, 0, len(frame.Data.Children))
	for _, child := range frame.Data.Children {
		segment := segmentOf(child)
		if segment == "" {
			continue
		}

		entries = append(entries, fileOf(child, join(directory.Path, segment)))
	}

	return connectorapi.Listing{Directory: directory, Entries: entries}, true
}

func fileOf(node nodePayload, path string) connectorapi.File {
	return connectorapi.File{
		Path:        path,
		Name:        node.Name,
		DisplayName: node.DisplayName,
		Folder:      node.Type == folderType,
		Size:        node.Size,
		Modified:    node.Modified,
		ReadOnly:    node.ReadOnly,
	}
}

// The most one part of a slicer's duration counts, and the longest print this
// driver hands on. A file that names more is not a print, and adding its parts
// would overflow.
const (
	maxDurationPart  = 1_000_000
	maxPrintSeconds  = 366 * 24 * 60 * 60
	secondsPerMinute = 60
	secondsPerHour   = 60 * secondsPerMinute
	secondsPerDay    = 24 * secondsPerHour
)

// estimateOf reads the print time a FILE_INFO carries, in whole seconds, and
// zero when it names none this driver can read.
func estimateOf(node nodePayload) int64 {
	// A number, or the same number written as text. A part of a second is
	// dropped.
	var seconds float64
	if json.Unmarshal(node.EstimatedSeconds, &seconds) != nil {
		seconds, _ = strconv.ParseFloat(headerText(node.EstimatedSeconds), 64)
	}

	if seconds >= 1 && seconds <= maxPrintSeconds {
		return int64(seconds)
	}

	return slicerDuration(headerText(node.EstimatedText))
}

// The longest header text kept, and the thickest layer believed. A file
// writes these, so anything longer or out of bounds is no value.
const (
	maxHeaderText = 64
	maxLayerMM    = 5
)

// headerText reads a header the slicer wrote as text, and empty for any
// other type or for text too long to be a name.
func headerText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) != nil || len(text) > maxHeaderText {
		return ""
	}

	return strings.TrimSpace(text)
}

// layerHeightOf reads the layer height in millimetres, and zero for any other
// type or a height no printer prints.
func layerHeightOf(raw json.RawMessage) float64 {
	var height float64
	if json.Unmarshal(raw, &height) != nil || height <= 0 || height > maxLayerMM {
		return 0
	}

	return height
}

// slicerDuration reads a duration as PrusaSlicer writes it, such as
// "1d 2h 5m 3s" or "45m 10s": numbers, each followed by its unit. Anything
// else, a part out of bounds and a total past a year are zero, because the
// text is a file's and a wrong time is worse than none.
func slicerDuration(text string) int64 {
	var total int64

	for part := range strings.FieldsSeq(text) {
		// A unit with no number before it leaves "", which ParseInt refuses.
		count, err := strconv.ParseInt(part[:len(part)-1], 10, 64)
		if err != nil || count < 0 || count > maxDurationPart {
			return 0
		}

		switch part[len(part)-1] {
		case 'd':
			total += count * secondsPerDay
		case 'h':
			total += count * secondsPerHour
		case 'm':
			total += count * secondsPerMinute
		case 's':
			total += count
		default:
			return 0
		}
	}

	if total > maxPrintSeconds {
		return 0
	}

	return total
}

// previewOf decodes the picture a FILE_INFO carries. A field that is not base64
// is a file with no picture this driver can hand on, not a failed listing.
func previewOf(encoded string) []byte {
	if encoded == "" {
		return nil
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil
	}

	return decoded
}

// segmentOf is the path component a child is addressed by.
//
// The long name wins, which is what the shipping connector does
// (legacy/printer/files/manager.go parseFileNode) and therefore what the
// printers in the field are already addressed with. Both spellings reach the
// caller on the File itself, so a client that needs the short one has it.
func segmentOf(node nodePayload) string {
	if node.DisplayName != "" {
		return node.DisplayName
	}

	return node.Name
}

func join(directory, segment string) string {
	if directory == "" {
		return segment
	}

	return directory + "/" + segment
}

// wirePaths returns the command's arguments with any path in the firmware's own
// namespace, which is absolute and storage-prefixed.
//
// The traversal check is here rather than in the descriptor because Value
// declares a kind, a set of values and two numeric bounds, and has no way to
// say "not this substring". Losing it would put a path the caller composed
// straight onto a filesystem.
//
// It checks the path that reaches the wire rather than the one that arrived.
// The shipping validator refuses "/../" on the relative path and then prefixes
// the storage, so "../etc/passwd" passes its check and leaves as
// "/usb/../etc/passwd".
func (s *Session) wirePaths(cmd connectorapi.Command) (map[string]string, error) {
	path, carries := cmd.Args["path"]
	if !carries {
		return cmd.Args, nil
	}

	absolute, err := s.firmwarePath(path)
	if err != nil {
		return nil, err
	}

	values := maps.Clone(cmd.Args)
	values["path"] = absolute

	return values, nil
}

// firmwarePath is the one place a caller's path becomes a firmware path, and
// therefore the one place the traversal rule is stated. Every verb that names a
// path goes through it, so a verb ported later cannot be ported without it.
func (s *Session) firmwarePath(path string) (string, error) {
	// The refusal names the spelling the caller sent, which is the only one
	// they can correct.
	if refusal := connectorapi.RefusePathEscape(path); refusal != nil {
		return "", refusal
	}

	// Checked again on the path that reaches the wire. The shipping validator
	// checks the relative path and then prefixes the storage, so a rule that
	// stopped at the first check would be the one that let "../etc/passwd"
	// leave as "/usb/../etc/passwd".
	absolute := absolutePath(path, s.storageOf(path))
	if refusal := connectorapi.RefusePathEscape(absolute); refusal != nil {
		return "", refusal
	}

	return absolute, nil
}

// absolutePath puts a relative path into the firmware namespace.
//
// A path that is already absolute passes through, one whose first segment names
// the storage gains a leading slash, and one with no storage segment resolves
// against the storage so a caller that names only a file still reaches one. The
// caller picks the storage: see storageOf.
func absolutePath(path, storage string) string {
	path = strings.TrimSpace(path)

	switch {
	case path == "" || path == "/":
		return storage
	case strings.HasPrefix(path, "/"):
		return path
	}

	if first, _, _ := strings.Cut(path, "/"); first == strings.TrimPrefix(storage, "/") {
		return "/" + path
	}

	return strings.TrimSuffix(storage, "/") + "/" + path
}

// storageOf is the storage a caller's path starts in: the one its first
// segment names when the printer reported a storage by that name, such as the
// HT90's "local", and the default one otherwise.
func (s *Session) storageOf(path string) string {
	first, _, _ := strings.Cut(relativePath(path), "/")

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, device := range s.storages {
		if device.Name != "" && device.Name == first {
			return "/" + first
		}
	}

	return s.storage
}

// relativePath is the firmware's absolute spelling in the storage-prefixed
// relative namespace the rest of the product addresses a file in: the firmware
// path without its leading separator.
func relativePath(path string) string {
	return strings.TrimPrefix(strings.TrimSpace(path), "/")
}

// isRoot reports whether a path names the tree above every storage.
func isRoot(path string) bool {
	trimmed := strings.TrimSpace(path)

	return trimmed == "" || trimmed == "/"
}
