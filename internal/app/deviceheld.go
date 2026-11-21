package app

// A file sent to a name the printer's storage already holds.
//
// A printer refuses a transfer to a name its storage holds and keeps the file
// there (Prusa-Firmware-Buddy src/transfers/transfer.cpp, Transfer::begin). So
// the server asks the printer about the destination before it sends, and
// decides there what the transfer would otherwise fail on.

import (
	"context"
	"errors"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// errHeldSame says the printer already holds the very file a print would
// send, so nothing is sent and the print starts on that copy.
var errHeldSame = errors.New("the printer already holds this file")

// machineFileExists is the word a Buddy printer refuses a transfer to a name it
// holds with.
const machineFileExists = "FILE_EXISTS"

// destination decides what happens to the file the printer holds where an
// upload lands, and answers the path to delete before the send and the path
// the file lands at. It only asks: nothing is deleted here.
//
// Nothing there, or a printer that does not answer, decides nothing, and the
// transfer answers for itself. A file there is the one this upload would send
// only when reuse is set and this process sent that very version there and the
// printer's copy has not changed since; that is errHeldSame. An upload that
// says keep_both lands at the first free name instead, as freeName finds it.
// Anything else is refused with FILE_NAME_TAKEN unless the upload says
// replace, and then the path to delete is the printer's file. A folder is
// never deleted to make room.
func (d *devices) destination(
	ctx context.Context, entry *device, id string, upload httpapi.DeviceUpload,
	content connectorapi.Content, reuse bool,
) (string, string, error) {
	held, found := heldAt(ctx, entry, upload.Path)
	if !found {
		return "", upload.Path, nil
	}

	if reuse && !upload.Replace && !held.Folder && d.copies.holds(id, upload, identityOfContent(content), held) {
		return "", upload.Path, errHeldSame
	}

	if upload.KeepBoth {
		at, free := freeName(ctx, entry, upload.Path)
		if !free {
			return "", "", nameTaken(id, upload.Path, false)
		}

		return "", at, nil
	}

	if !upload.Replace || held.Folder {
		return "", "", nameTaken(id, upload.Path, held.ReadOnly)
	}

	return held.Path, upload.Path, nil
}

// freeNameTries bounds how many numbered names keep_both asks the printer
// about. Each one is a listing of one path.
//
// The probe is linear, one listing per number. A printer that holds every
// one of them is refused after the last.
const freeNameTries = 99

// freeName finds the first name the printer holds nothing at, numbering the
// file: part.bgcode becomes part[1].bgcode, then part[2].bgcode. The printer fetches one file at a time, so no transfer of
// this server can take the name between the probe and the send.
func freeName(ctx context.Context, entry *device, at string) (string, bool) {
	for n := 1; n <= freeNameTries; n++ {
		candidate := numberedName(at, n)
		if _, held := heldAt(ctx, entry, candidate); !held {
			return candidate, true
		}
	}

	return "", false
}

// numberedName puts [n] before the file's extension, keeping a two-part
// extension such as .gcode.gz whole.
func numberedName(at string, n int) string {
	dir, name := path.Split(at)

	extension := path.Ext(name)
	if strings.HasSuffix(strings.ToLower(name), ".gcode.gz") {
		extension = name[len(name)-len(".gcode.gz"):]
	}

	return dir + strings.TrimSuffix(name, extension) + "[" + strconv.Itoa(n) + "]" + extension
}

// nameTaken refuses a send onto a name the printer holds. readOnly is the
// printer listing its file read-only, which it refuses to delete.
func nameTaken(id, at string, readOnly bool) error {
	return fault.PrinterFileNameTaken.
		New("a file of that name is already on the printer").
		With(
			fault.StringParam("device_id", id), fault.StringParam("path", at),
			fault.BoolParam("read_only", readOnly),
		)
}

// refusedAsHeld reports whether a failed transfer is the printer refusing a
// name it holds, which the probe before the send could not see.
func refusedAsHeld(err error) bool {
	if !errors.Is(err, connectorapi.ErrTransferRefused) {
		return false
	}

	carried, ok := errors.AsType[*fault.Fault](err)
	if !ok {
		return false
	}

	for _, param := range carried.Params() {
		if param.Key == "machine_reason" && param.Value.String() == machineFileExists {
			return true
		}
	}

	return false
}

// heldAt asks the printer about the one path a file lands on, and reports
// false when it holds nothing there or does not answer.
//
// It asks about that path and not about its folder. A folder on a full USB
// stick takes seconds to list, and one entry is all this needs.
func heldAt(ctx context.Context, entry *device, at string) (connectorapi.File, bool) {
	listing, err := entry.session.List(ctx, at)
	if err != nil {
		return connectorapi.File{}, false
	}

	held := listing.Directory
	if !namesFile(held, path.Base(at)) {
		return connectorapi.File{}, false
	}

	return held, true
}

// namesFile reports whether an entry is the file a name addresses. The storage
// is FAT, where one name in two spellings is one entry, so it is matched
// without case against the long name and the short one.
func namesFile(entry connectorapi.File, name string) bool {
	for _, spelling := range []string{entry.DisplayName, entry.Name, path.Base(entry.Path)} {
		if spelling != "" && strings.EqualFold(spelling, name) {
			return true
		}
	}

	return false
}

// identityOfContent is the version of the stored file a transfer reads, and
// empty for a source that cannot say.
func identityOfContent(content connectorapi.Content) string {
	versioned, ok := content.(interface{ Identity() string })
	if !ok {
		return ""
	}

	return versioned.Identity()
}

// sentCopies remembers, per printer and destination, which version of which
// stored file this process sent there and how the printer reported the copy
// afterwards. The zero value is ready to use.
//
// It lives in memory. After a restart nothing is remembered and the person is
// asked, which costs a question and never a wrong print.
type sentCopies struct {
	mu     sync.Mutex
	copies map[sentKey]sentCopy
}

// sentKey is a destination on one printer. The path is folded to lower case,
// because the storage is FAT and finds a name without regard to case.
type sentKey struct {
	device string
	path   string
}

type sentCopy struct {
	// at is the destination as it was sent, which names the file in the
	// spelling the caller used.
	at       string
	storage  string
	source   string
	identity string
	size     int64
	modified int64
}

// sentCopiesLimit bounds the memo. Each entry is a few short strings.
//
// ponytail: a full memo forgets an arbitrary entry, and that print asks once
// more; a least-recently-used order is the upgrade if that ever shows.
const sentCopiesLimit = 4096

func keyOf(device, at string) sentKey {
	return sentKey{device: device, path: strings.ToLower(at)}
}

// remember records the copy the printer reports after a send. A version with
// no identity, or a printer copy with no time, is not remembered, so it is
// never taken for the same file later.
func (c *sentCopies) remember(device string, upload httpapi.DeviceUpload, identity string, held connectorapi.File) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := keyOf(device, upload.Path)

	if identity == "" || held.Modified == 0 {
		delete(c.copies, key)

		return
	}

	if c.copies == nil {
		c.copies = make(map[sentKey]sentCopy)
	}

	if _, kept := c.copies[key]; !kept && len(c.copies) >= sentCopiesLimit {
		for other := range c.copies {
			delete(c.copies, other)

			break
		}
	}

	c.copies[key] = sentCopy{
		at: upload.Path, storage: upload.Storage, source: upload.Source, identity: identity,
		size: held.Size, modified: held.Modified,
	}
}

// forget drops what is remembered for a destination whose copy is gone or
// unknown.
func (c *sentCopies) forget(device, at string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.copies, keyOf(device, at))
}

// holds reports whether the printer's copy is the one this process sent from
// the same version of the same stored file. The printer's own length and
// modification time must be what it reported right after that send, so a file
// put there since, by hand or by another client, is not taken for it.
//
// The destination is matched the way the printer's entry is: in the same
// folder, by either of the file's names and without regard to case.
func (c *sentCopies) holds(device string, upload httpapi.DeviceUpload, identity string, held connectorapi.File) bool {
	if identity == "" {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	sent, found := c.copies[keyOf(device, upload.Path)]
	if !found {
		sent, found = c.sameFolderLocked(device, upload.Path, held)
	}

	return found &&
		sent.storage == upload.Storage &&
		sent.source == upload.Source &&
		sent.identity == identity &&
		sent.size == held.Size &&
		sent.modified == held.Modified
}

// sameFolderLocked finds a copy sent to the folder of at under a name the
// printer's entry also answers to, such as its 8.3 spelling. The caller holds
// the lock.
func (c *sentCopies) sameFolderLocked(device, at string, held connectorapi.File) (sentCopy, bool) {
	folder := strings.ToLower(path.Dir(at))

	for key, sent := range c.copies {
		if key.device == device && strings.ToLower(path.Dir(sent.at)) == folder && namesFile(held, path.Base(sent.at)) {
			return sent, true
		}
	}

	return sentCopy{}, false
}
