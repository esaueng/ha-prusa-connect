package deviceapi

import (
	"context"
	"fmt"
)

// The limits of one print request, the same as on the HTTP print route.
const (
	// MaxStorageBytes is the longest name of a storage.
	MaxStorageBytes = 128

	// MaxSourceBytes is the longest path of a file inside that storage.
	MaxSourceBytes = 1024
)

// ModuleDataStorage is the storage name of the calling module's own print
// folder: print/ in the data directory the operator granted it. Source is
// then relative to print/. A module with no data directory is refused with
// ErrDenied.
const ModuleDataStorage = "module:data"

// PrintParams is the parameters of MethodPrint. Storage and Source name a
// file in one of the server's storages, and Path is where it goes on the
// printer. With neither, Path names a file already on the printer. Storage
// and Source must be set together.
type PrintParams struct {
	// Principal is who the call is made for, and empty for the module's own
	// work.
	Principal string `json:"principal,omitempty"`

	ID string `json:"id"`

	Storage string `json:"storage,omitempty"`
	Source  string `json:"source,omitempty"`
	Path    string `json:"path"`

	// KeepBoth sends the file under a free name when the printer holds
	// another file at Path, such as usb/part[1].bgcode, and keeps the
	// printer's file. Started.Path names the file that is printed. It needs a
	// storage.
	KeepBoth bool `json:"keep_both,omitempty"`
}

// Validate checks one print request against the limits of this contract. The
// client and the host both call it.
func (p PrintParams) Validate() error {
	if err := validPrincipal(p.Principal); err != nil {
		return err
	}

	if err := validID(p.ID); err != nil {
		return err
	}

	switch {
	case p.Path == "":
		return fmt.Errorf("%w: the call names no file to print", ErrInvalidQuery)
	case len(p.Path) > MaxPathBytes:
		return fmt.Errorf("%w: the path is %d bytes and the limit is %d",
			ErrInvalidQuery, len(p.Path), MaxPathBytes)
	case len(p.Storage) > MaxStorageBytes:
		return fmt.Errorf("%w: the storage name is %d bytes and the limit is %d",
			ErrInvalidQuery, len(p.Storage), MaxStorageBytes)
	case len(p.Source) > MaxSourceBytes:
		return fmt.Errorf("%w: the source path is %d bytes and the limit is %d",
			ErrInvalidQuery, len(p.Source), MaxSourceBytes)
	case p.Storage != "" && p.Source == "":
		return fmt.Errorf("%w: the call names a storage and no path inside it", ErrInvalidQuery)
	case p.Storage == "" && p.Source != "":
		return fmt.Errorf("%w: the call names a path inside no storage", ErrInvalidQuery)
	case p.Storage == "" && p.KeepBoth:
		return fmt.Errorf("%w: keep_both sends a file from a storage and the call names none", ErrInvalidQuery)
	}

	return nil
}

// Started is the answer of MethodPrint. One of the two ids is always set.
// TransferID is set when the server is sending the file to the printer.
// CommandID is set when the file was already on the printer and the start
// command was sent. Follow the rest on the events device.transfer.settled
// and device.command.settled.
type Started struct {
	TransferID string `json:"transfer_id,omitempty"`
	CommandID  string `json:"command_id,omitempty"`

	// Path is the file on the printer that is printed. With KeepBoth it may
	// be a numbered name and not the path asked for. A host older than
	// KeepBoth leaves it empty.
	Path string `json:"path,omitempty"`
}

// Print sends a file from one of the server's storages to a printer and
// starts it. It needs operate on the printer, and read_storage when a storage
// is named.
//
// It returns when the transfer has started, not when the print has begun. The
// host sends the start command when the file has arrived, and its outcome
// comes on device.command.settled. For a file already on the printer, a
// refused start is ErrPrintRefused and a failed one is ErrPrintFailed.
//
// A path with a ".." segment is ErrInvalidQuery. An unknown storage and a
// missing file are both ErrNoFile. A path where the printer already has
// another file is ErrNameTaken.
func (c *Client) Print(ctx context.Context, params PrintParams) (Started, error) {
	if err := params.Validate(); err != nil {
		return Started{}, fmt.Errorf("deviceapi: %s: %w", MethodPrint, err)
	}

	var started Started
	if err := c.call(ctx, MethodPrint, params, &started); err != nil {
		return Started{}, err
	}

	return started, nil
}
