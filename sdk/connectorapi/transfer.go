package connectorapi

import (
	"context"
	"errors"
	"io"
)

// VerbFileUpload is the capability a device declares when it can be sent a
// file. It is a feature, not a command. A driver maps it to its own protocol.
const VerbFileUpload Capability = CorePrefix + "feature.file_upload"

// VerbSetPrinterReady is the command that makes a device ready for its next
// job. A state a transition of this verb leads to is the device's ready state.
const VerbSetPrinterReady Capability = CorePrefix + "command.set_printer_ready"

// The errors of a transfer. Callers inspect them with errors.Is.
var (
	// ErrNoTransfers reports a session whose dialect cannot take a file.
	ErrNoTransfers = errors.New("connectorapi: the driver cannot put a file on the device")
	// ErrTransferRefused reports a device that would not take the file. The
	// device's reason is the fault's machine reason.
	ErrTransferRefused = errors.New("connectorapi: the device refused the transfer")
	// ErrTransferFailed reports a transfer the device started and did not
	// finish.
	ErrTransferFailed = errors.New("connectorapi: the transfer did not finish")
	// ErrTransferBusy reports a device already running a transfer. A device
	// runs one at a time.
	ErrTransferBusy = errors.New("connectorapi: the device is already running a transfer")
)

// Content is the bytes of one file a host offers to a device. A device may
// ask for any range in any order. An implementation must not keep the whole
// file in memory.
type Content interface {
	// Size is the file's length in bytes. It is known before the first read.
	Size() int64

	// Open returns the file's bytes from offset to its end. The caller closes
	// the reader and may open several during one transfer. An offset at or
	// past Size returns a reader at end of file, not an error.
	Open(ctx context.Context, offset int64) (io.ReadCloser, error)
}

// Progress is one report about a running transfer. Sent counts what the host
// has sent. Received counts what the device says it has taken, and is valid
// only when Known is true.
type Progress struct {
	// Total is the file's length, as the host told the device.
	Total int64
	// Sent is how many bytes of the file the host has sent. A byte the device
	// asks for again counts once, so Sent never passes Total.
	Sent int64
	// Received is how many bytes the device says it has taken.
	Received int64
	// Known is false when the device reports no Received number.
	Known bool
	// Ref is the dialect's own name for the transfer, empty until it has one.
	// It only correlates the reports of one transfer.
	Ref string
	// Warning is a note for a person about something the driver could not
	// confirm, such as the file being on the storage. It is not a failure. Set
	// it on the last report of the transfer.
	Warning string
}

// Upload is one file offered to a device.
type Upload struct {
	// Path is where the file goes on the device, in the form of File.Path,
	// such as "usb/part.bgcode". A path that leaves its storage is refused
	// before it reaches the device.
	Path string
	// Content is the bytes. It must stay usable until Send returns.
	Content Content
	// Progress, when not nil, is called when the device accepts the transfer,
	// as it moves, and when it ends. The driver never calls it concurrently.
	// A callback that blocks also blocks the transfer.
	Progress func(Progress)
}

// Transfers is an optional interface of a Session that can put a file on the
// device. A host reaches it with Uploads.
type Transfers interface {
	// Send puts one file on the device. It returns when the device finishes,
	// refuses or abandons the transfer, or when ctx ends. ctx bounds the whole
	// transfer. A second Send during a transfer returns ErrTransferBusy.
	//
	// A driver that can list the destination directory must confirm the name
	// and the length there before it returns, and return TransferNotOnStorage
	// when they do not match. A driver that cannot list it returns nil and
	// sets Progress.Warning on its last report.
	Send(ctx context.Context, up Upload) error
}

// Uploads returns the Transfers of a session. It reports false for a dialect
// that has none.
func Uploads(s Session) (Transfers, bool) {
	transfers, ok := s.(Transfers)

	return transfers, ok
}
