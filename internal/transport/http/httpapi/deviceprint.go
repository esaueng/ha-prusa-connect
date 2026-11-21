package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/prusa3d/prusa-connect/internal/transport/http/httpfault"
)

// devicePrintPath is the one call that gets a machine printing. It is a verb
// and not a collection, so it sits under the device it acts on.
const devicePrintPath = "/api/v1/devices/{device_id}/print"

// StartPrintVerb is the capability key a print is spoken through, and
// StartPrintPathArgument is the argument that verb takes: the file on the
// machine, storage-prefixed and relative, such as usb/bracket.bgcode.
//
// The verb table is the driver's, and this server sends the same key a caller
// would send through send-device-command, so both paths start a print the
// same way. They are exported because the device plane composes the print out
// of them and must not spell either a second time.
const (
	StartPrintVerb         = "core.command.start_print"
	StartPrintPathArgument = "path"
)

// DevicePrint is the one request that prints a file.
//
// It has two shapes because a page has two starting points. A file already on
// the machine names only path, and this is one command. A file on one of this
// deployment's storages names storage and source too, and this server relays
// the bytes and then starts the print.
type DevicePrint struct {
	Storage string `json:"storage,omitempty" doc:"The storage resource holding the file, by the name it is declared under in the resources block. Absent prints a file the machine already holds."                                                                                                                maxLength:"128"`
	Source  string `json:"source,omitempty"  doc:"The file's path inside that storage, relative to its root. Required with storage and refused without it. A temporary copy is deleted as it is for a send."                                                                                                      maxLength:"1024"`
	Path    string `json:"path"              doc:"The file on the printer, storage-prefixed and relative, such as usb/bracket.bgcode. With a storage it is where the file lands; without one it is the file that is printed."                                                                                     maxLength:"1024"`
	Replace bool   `json:"replace,omitempty" doc:"With a storage, delete the file the printer holds at path before sending this one. Without it any file there other than this server's unchanged copy of the same stored file is refused with core.printer/FILE_NAME_TAKEN, so a page can ask the person first."`
	// KeepBoth sits after Replace in DeviceUpload too, so one converts to the
	// other.
	KeepBoth bool `json:"keep_both,omitempty" doc:"With a storage, send under a free name when the printer holds another file at path, such as usb/part[1].bgcode, keep the printer's file and print the one sent. The answer's path names the file that was started. Refused together with replace."`
}

type printFileInput struct {
	DeviceRef

	Body DevicePrint
}

// DevicePrintResult is what one print produced: the transfer, when the file
// came from a storage, and what the printer made of the start command.
//
// It is declared here and not inside the response, because the module wire
// starts a print through the same call and answers the same shape.
type DevicePrintResult struct {
	Path     string          `json:"path"               doc:"The file that was started, as the printer addresses it."`
	Transfer *DeviceTransfer `json:"transfer,omitempty" doc:"The transfer as it ended, absent when the file was already on the machine."`
	Command  PrinterResult   `json:"command"            doc:"What the printer made of the start command. ok false is the machine's refusal and carries fault_code."`
}

// printFileOutput answers both halves, so a page has the transfer to show
// progress against and the command id to read the print's record back under.
type printFileOutput struct {
	Body DevicePrintResult
}

// printErrors are the refusals of both halves: the transfer's and the
// command's. They are one table because one call can fail either way, and a
// client branches on the code and not on which half it reached.
var printErrors = append([]int{http.StatusBadRequest, http.StatusConflict}, deviceErrors...)

// registerPrintFile publishes printing in one call.
//
// It composes two existing operations: send-device-file relays the bytes and
// send-device-command starts the print. Both checks run here in the same order
// and with the same permissions, so a caller who may not write to the machine
// is refused before anything is transferred.
func registerPrintFile(api huma.API, fleet DeviceFleet) {
	register(api, huma.Operation{
		OperationID: "print-device-file",
		Method:      http.MethodPost,
		Path:        devicePrintPath,
		Summary:     "Print a file on a printer",
		Description: "Starts a print in one call. Name `path` alone to print a file the machine already " +
			"holds. Name `storage` and `source` as well to print a file one of this deployment's storages " +
			"holds: the file is relayed to the machine first and `path` is where it lands, so a page that " +
			"has just uploaded a file prints it without calling send-device-file and send-device-command " +
			"itself.\n\n" +
			"A printer refuses a file sent to a name it already holds. When this server sent that same " +
			"version of the stored file there since it started, and the printer's copy has not changed " +
			"since, that copy is printed and nothing is sent. Any other file there is refused with " +
			"core.printer/FILE_NAME_TAKEN, and the request sent again with `replace` true deletes it and " +
			"sends this one.\n\n" +
			"It returns when the printer has answered the start command, so with a storage it takes as " +
			"long as the transfer does. A client that stops waiting stops neither the transfer nor the " +
			"start command that follows it. A printer that refuses the " +
			"command answers 200 with `command.ok` false, the way send-device-command does, and the " +
			"transfer that already happened is reported beside it. A path carrying a .. segment is " +
			"refused here and never reaches the printer.\n\n" +
			"It takes operate on the printer, and read_storage on the storage kind when a storage is " +
			"named.",
		Tags:     []string{"devices"},
		Errors:   printErrors,
		Metadata: streams(),
	}, func(ctx context.Context, in *printFileInput) (*printFileOutput, error) {
		if err := ValidatePrint(in.Body); err != nil {
			return nil, err
		}

		if in.Body.Storage != "" {
			// The write deadline is lifted before the transfer starts. It is
			// already set when this runs, and a transfer that outlasts it would
			// complete on the printer and could not be reported to the caller.
			if err := liftWriteDeadline(ctx); err != nil {
				return nil, err
			}
		}

		result, err := fleet.Print(ctx, principalFrom(ctx), in.DeviceID, in.Body)
		if err != nil {
			return nil, err
		}

		return &printFileOutput{Body: result}, nil
	})
}

// ValidatePrint refuses a body with no path, a body that names only one of
// storage and source, and a body that sets replace without a storage. The
// server does not guess what the caller meant.
//
// It is exported because the module wire starts a print through the same call
// and has to refuse the same bodies.
func ValidatePrint(body DevicePrint) error {
	if strings.TrimSpace(body.Path) == "" {
		return httpfault.RequestInvalid("body.path", "the file to print",
			"name the file on the printer, storage-prefixed and relative, such as usb/bracket.bgcode")
	}

	if body.Storage != "" && strings.TrimSpace(body.Source) == "" {
		return httpfault.RequestInvalid("body.source", "the file inside that storage",
			"a storage was named, so name the path inside it too")
	}

	if body.Storage == "" && strings.TrimSpace(body.Source) != "" {
		return httpfault.RequestInvalid("body.storage", "the storage holding the file",
			"a source was named, so name the storage it is in too")
	}

	if body.Storage == "" && body.Replace {
		return httpfault.RequestInvalid("body.replace", "a file to send in place of the printer's",
			"replace sends a file from a storage, so name the storage and the source too")
	}

	if body.Storage == "" && body.KeepBoth {
		return httpfault.RequestInvalid("body.keep_both", "a file to send beside the printer's",
			"keep_both sends a file from a storage, so name the storage and the source too")
	}

	return validKeepBoth(body.Replace, body.KeepBoth)
}

// ValidateUpload holds one send to the rules its fields carry together.
func ValidateUpload(body DeviceUpload) error {
	return validKeepBoth(body.Replace, body.KeepBoth)
}

// validKeepBoth refuses asking for both answers to a name the printer holds.
func validKeepBoth(replace, keepBoth bool) error {
	if replace && keepBoth {
		return httpfault.RequestInvalid("body.keep_both", "one answer to a name the printer holds",
			"replace deletes the printer's file and keep_both keeps it, so send one of the two")
	}

	return nil
}
