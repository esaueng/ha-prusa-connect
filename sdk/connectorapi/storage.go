package connectorapi

import (
	"context"
	"errors"
	"strings"

	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// The core keys for file operations. The host checks them against the
// capability set before it asks a session for a listing or a change.
const (
	// VerbSendInfo asks a device what it is and what storages it has.
	VerbSendInfo Capability = CorePrefix + "command.send_info"
	// VerbSendFileInfo asks a device for one path: what it is, and what is
	// inside it when it is a folder.
	VerbSendFileInfo Capability = CorePrefix + "command.send_file_info"
	// VerbCreateFolder makes a folder on a device's storage.
	VerbCreateFolder Capability = CorePrefix + "command.create_folder"
	// VerbDeleteFile removes one file from a device's storage.
	VerbDeleteFile Capability = CorePrefix + "command.delete_file"
	// VerbDeleteFolder removes a folder from a device's storage. A device
	// refuses a delete aimed at the wrong kind of entry.
	VerbDeleteFolder Capability = CorePrefix + "command.delete_folder"
)

// The errors of file operations. Callers inspect them with errors.Is.
var (
	// ErrNoFilesystem reports a session whose dialect cannot say what is stored
	// on the device.
	ErrNoFilesystem = errors.New("connectorapi: the driver cannot report what is stored on the device")
	// ErrNoFileManager reports a session whose dialect cannot change what is
	// stored on the device.
	ErrNoFileManager = errors.New("connectorapi: the driver cannot change what is stored on the device")
	// ErrFileChangeRefused reports a device that would not make the change.
	// The device's reason is the fault's machine reason.
	ErrFileChangeRefused = errors.New("connectorapi: the device refused that change to its storage")
	// ErrListingRefused reports a device that would not report what is at a
	// path. The device's reason is the fault's machine reason.
	ErrListingRefused = errors.New("connectorapi: the device refused to report that path")
)

// parentSegment is the one path component that leaves the storage it names.
const parentSegment = ".."

// Storage is one storage a device reports. Mountpoint identifies it. Name and
// Type are not unique.
type Storage struct {
	// Mountpoint is the device-side path with its leading separator, such as
	// /usb. It is the only required field.
	Mountpoint string `json:"mountpoint"`
	// Name is the first segment of a path on this storage, such as usb. It is
	// the mountpoint without its leading separator unless the device named it.
	Name string `json:"name"`
	// Type is the device class as the device spelled it, such as USB. It is
	// free text.
	Type string `json:"type,omitempty"`
	// FreeSpace is the bytes left. It is valid only when FreeSpaceKnown is
	// true.
	FreeSpace int64 `json:"free_space,omitempty"`
	// FreeSpaceKnown is whether the device reported a free-space figure.
	FreeSpaceKnown bool `json:"free_space_known,omitempty"`
	// ReadOnly is whether the device refuses writes to this storage.
	ReadOnly bool `json:"read_only,omitempty"`
}

// File is one entry in a device's file tree. Path starts with the storage
// name and is relative: "usb/models/part.bgcode" is a file on the storage
// whose Name is "usb". A driver never exposes the device's absolute path.
type File struct {
	// Path is the storage-prefixed relative path, with no leading separator.
	Path string `json:"path"`
	// Name is what the device addresses the entry by. On a FAT storage that is
	// the short name, which is not what a person sees.
	Name string `json:"name"`
	// DisplayName is the name written for a person, empty when the device
	// reports only one name.
	DisplayName string `json:"display_name,omitempty"`
	// Folder is whether the entry is a directory.
	Folder bool `json:"folder,omitempty"`
	// Size is the bytes the entry occupies, zero for a folder.
	Size int64 `json:"size,omitempty"`
	// Modified is seconds since the Unix epoch as the device reported them.
	// Zero means the device reported none.
	Modified int64 `json:"modified,omitempty"`
	// ReadOnly is whether the device refuses to change this entry.
	ReadOnly bool `json:"read_only,omitempty"`
	// Preview is the encoded picture from a print file, as the device sent it.
	// Fill it only on Listing.Directory, when the listing is of that one file,
	// never on Entries. The bytes are untrusted, and the host checks them.
	Preview []byte `json:"preview,omitempty"`
	// EstimatedSeconds is the print time the slicer estimated for a print
	// file, in whole seconds, as the device read it out of the file. Like
	// Preview it is filled only on Listing.Directory, when the listing is of
	// the one file. Zero is a device or a file that names none.
	EstimatedSeconds int64 `json:"estimated_seconds,omitempty"`
	// PrinterModel, LayerHeightMM and Material are what the slicer wrote
	// about the print: the printer it sliced for, such as XL2IS, the layer
	// height in millimetres, and the material, one for each tool joined by
	// a semicolon. Like Preview they are filled only on Listing.Directory.
	// Empty or zero is a device or a file that names none.
	PrinterModel  string  `json:"printer_model,omitempty"`
	LayerHeightMM float64 `json:"layer_height_mm,omitempty"`
	Material      string  `json:"material,omitempty"`
}

// Listing is one directory as the device reported it. Entries is empty for an
// empty folder and for a path that names a file. Directory.Folder tells the
// two apart.
type Listing struct {
	// Directory is the entry the listing is of.
	Directory File `json:"directory"`
	// Entries are what is directly inside it, in the order the device reported
	// them.
	Entries []File `json:"entries,omitempty"`
}

// FileChange is one entry of the device's file tree that the device said
// changed. File.Path is storage-prefixed and in the device's own spelling,
// which on a FAT storage may be the short name of each segment.
type FileChange struct {
	// File is the entry as it is now. Only Path is certain when Removed or
	// Rescan is set.
	File File `json:"file"`
	// Removed says the entry, and everything under it, is gone.
	Removed bool `json:"removed,omitempty"`
	// Rescan says File is a folder whose content changed in more ways than
	// one event names, so a listing of it held from before is out of date.
	Rescan bool `json:"rescan,omitempty"`
}

// Filesystem is an optional interface of a Session that reports what is
// stored on the device. A host reaches it with Files.
type Filesystem interface {
	// Storages returns the storages the device last reported, without asking
	// the device. It returns an empty slice, not an error, when the device has
	// reported none yet.
	Storages(ctx context.Context) ([]Storage, error)

	// List returns one directory. path starts with the storage name and is
	// relative. The empty path is the root, whose entries are the storages.
	// It may send a command to the device, and ctx bounds it. Call
	// RefusePathEscape first: a path that leaves its storage must be refused
	// with PathNotAllowed and must never reach the device.
	List(ctx context.Context, path string) (Listing, error)
}

// FileManager is an optional interface of a Session that changes what is
// stored on the device. A host reaches it with Manages.
//
// Every path has the form of File.Path. Call RefusePathEscape first: a path
// that leaves its storage must be refused with PathNotAllowed.
type FileManager interface {
	// CreateFolder makes one folder. A path that already exists is an error,
	// not a success.
	CreateFolder(ctx context.Context, path string) error

	// DeleteFile removes one file. A path that names a folder is refused.
	DeleteFile(ctx context.Context, path string) error

	// DeleteFolder removes one folder. The device decides whether it removes
	// a folder that is not empty or refuses.
	DeleteFolder(ctx context.Context, path string) error
}

// Manages returns the FileManager of a session. It reports false for a
// dialect that has none.
func Manages(s Session) (FileManager, bool) {
	manager, ok := s.(FileManager)

	return manager, ok
}

// RefusePathEscape returns a PathNotAllowed fault for a path that leaves the
// storage it names, and nil for any other path. A driver calls it before it
// sends a path to the device.
func RefusePathEscape(path string) error {
	if !PathEscapes(path) {
		return nil
	}

	return PathNotAllowed.
		New("the path leaves the storage it names").
		With(fault.StringParam("path", path))
}

// Files returns the Filesystem of a session. It reports false for a dialect
// that has none.
func Files(s Session) (Filesystem, bool) {
	files, ok := s.(Filesystem)

	return files, ok
}

// PathEscapes reports whether a device path has a ".." segment anywhere,
// including at the start or the end. Such a path is refused, never resolved.
func PathEscapes(path string) bool {
	for segment := range strings.SplitSeq(path, "/") {
		if segment == parentSegment {
			return true
		}
	}

	return false
}
