package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// The paths that read what a device holds. Each is under the device it belongs
// to, because a storage is not a resource of its own. It exists while the
// device reporting it is connected, and it is addressed only through that
// device.
const (
	deviceStoragesPath     = "/api/v1/devices/{device_id}/storages"
	deviceFilesPath        = "/api/v1/devices/{device_id}/files"
	deviceThumbnailPath    = deviceFilesPath + "/thumbnail"
	deviceFoldersPath      = "/api/v1/devices/{device_id}/folders"
	deviceTransfersPath    = "/api/v1/devices/{device_id}/transfers"
	deviceTransferStopPath = deviceTransfersPath + "/{transfer_id}/stop"
)

// DeviceTransfer is one file this server moved, or is moving, onto a printer.
//
// The bytes go through this server: the printer is never handed a URL to fetch,
// so every row here is a file that left one of this deployment's storages and
// was relayed by the core. BytesSent is what this server put on the wire. The
// server cannot know how many bytes the printer stored.
type DeviceTransfer struct {
	TransferID string     `json:"transfer_id"        doc:"The id this server gave the transfer. It is what the stop route and the event stream name it by."`
	Storage    string     `json:"storage"            doc:"The storage resource the file came from."`
	Source     string     `json:"source"             doc:"The file's path inside that storage."`
	Path       string     `json:"path"               doc:"Where the file lands on the printer, storage-prefixed."`
	BytesTotal int64      `json:"bytes_total"        doc:"The file's length as this server measured it before the first byte left."`
	BytesSent  int64      `json:"bytes_sent"         doc:"How many of the file's bytes this server has put on the wire. A byte the printer asks for again counts once."`
	State      string     `json:"state"              doc:"One of queued, sending, done, failed, cancelled or interrupted. Interrupted is a transfer that was running when this server stopped."`
	Reason     string     `json:"reason,omitempty"   doc:"Why it ended that way. Empty for a transfer that finished."`
	Warning    string     `json:"warning,omitempty"  doc:"What this server could not establish about a transfer that finished, such as a printer that took the file and cannot be asked whether the file is on its storage afterwards. It is a note beside a transfer that succeeded and never a failure, and it is absent for a transfer whose file was confirmed."`
	StartedAt  time.Time  `json:"started_at"         doc:"When it began, as an RFC 3339 string."`
	EndedAt    *time.Time `json:"ended_at,omitempty" doc:"When it ended, as an RFC 3339 string. Absent while it runs."`
}

// DeviceStorage is one storage device the printer reported.
//
// Mountpoint is the identity: two devices can report the same name and the same
// type, and a caller that addressed one by either would act on the wrong one.
type DeviceStorage struct {
	Mountpoint string `json:"mountpoint" doc:"The printer-side path, with its leading slash. It is what identifies the device."`
	Name       string `json:"name"       doc:"The first path segment of every file on this storage, such as usb."`
	Type       string `json:"type"       doc:"The device class as the printer spelled it, such as USB. The set is the firmware's, so read it, do not switch on it exhaustively."`
	// FreeSpace is null, not zero, for a printer that reported no figure.
	// Zero means a full device, and an upload to it fails. An upload to a
	// device that reported nothing may work.
	FreeSpace *int64 `json:"free_space" doc:"Bytes left, or null when the printer reported no figure. Zero is a full device."`
	ReadOnly  bool   `json:"read_only"  doc:"Whether the printer refuses writes to this device."`
}

// DeviceFile is one entry in a printer's own file tree.
//
// Modified is a pointer because a board with no clock reports no timestamp. A
// zero instant would be a date the printer never gave.
type DeviceFile struct {
	Path        string     `json:"path"                   doc:"The storage-prefixed path, such as usb/models/part.bgcode. It is what the files endpoint takes."`
	Name        string     `json:"name"                   doc:"What the printer addresses the entry by. On a FAT storage that is the short name, which is not what a person reads."`
	DisplayName string     `json:"display_name,omitempty" doc:"The name written for a person, absent when the printer reports only one name."`
	Folder      bool       `json:"folder"                 doc:"Whether the entry holds other entries."`
	Size        int64      `json:"size"                   doc:"Bytes the entry occupies, zero for a folder."`
	Modified    *time.Time `json:"modified,omitempty"     doc:"When the entry last changed, as an RFC 3339 string. The printer reports whole seconds, so this value has no fraction of a second. It is absent for a board that reported no timestamp."`
	ReadOnly    bool       `json:"read_only"              doc:"Whether the printer refuses to change this entry."`
	// EstimatedSeconds comes with the answer about one file only, as the
	// picture does: a listing of a folder names no times.
	EstimatedSeconds int64   `json:"estimated_seconds,omitempty" doc:"The print time the slicer estimated, in whole seconds, as the printer read it out of the file. It is present only on the directory of an answer about one print file, and absent for a printer or a file that names none."`
	PrinterModel     string  `json:"printer_model,omitempty"     doc:"The printer the file was sliced for, such as XL2IS. Present only on the directory of an answer about one print file that names it."`
	LayerHeightMM    float64 `json:"layer_height_mm,omitempty"   doc:"The layer height the file was sliced with, in millimetres. Present only on the directory of an answer about one print file that names it."`
	Material         string  `json:"material,omitempty"          doc:"The material the file was sliced for, one for each tool joined by a semicolon, such as PLA;PETG. Present only on the directory of an answer about one print file that names it."`
}

// DeviceListing is one directory of a device's own file tree.
type DeviceListing struct {
	Directory    DeviceFile    `json:"directory"               doc:"The directory that was asked for."`
	Items        []DeviceFile  `json:"items"                   doc:"What is directly inside it, in the order the device reported them."`
	Stale        bool          `json:"stale,omitempty"         doc:"True when this server answered from the last listing the printer gave for this path, without asking it again. The printer may hold something else by now: refresh=true has it read the path again."`
	Reading      bool          `json:"reading,omitempty"       doc:"True while the printer is reading this path. The rows are what this server held when it answered, none when it held nothing. A read that lands publishes com.prusa.connect.device.files.changed, and the listing it read is then answered without refresh."`
	ReadingSince *time.Time    `json:"reading_since,omitempty" doc:"When the read in progress began, as an RFC 3339 string. It is the same for every caller who joins that read. Absent when no read is in progress."`
	Fault        *ListingFault `json:"fault,omitempty"         doc:"Why the last read of this path failed. It is held until the next read of the path starts, which refresh=true does. Absent when the last read landed."`
}

// ListingFault is why the last read of a path failed. The read ran on no
// request of its own, so the trace id is the read's and not the request's.
type ListingFault struct {
	fault.Payload

	// TraceID shadows the payload's, whose doc names the request's trace.
	TraceID string `json:"trace_id,omitempty" doc:"The W3C trace id of the read that failed, when it had one, which an operator finds in the log. It is not the trace of the request that answered it."`
}

// DeviceStorages is what a printer said its storage devices are, and whether it
// has said at all. An empty list can mean a printer with no drive in it or a
// printer that has not answered yet. A person puts a drive in for the first
// and waits for the second, so Reported tells the two apart.
type DeviceStorages struct {
	Items    []DeviceStorage `json:"items"    doc:"The storage devices, in the order the printer reported them."`
	Reported bool            `json:"reported" doc:"True once the printer has answered on the connection this server holds now. False means items is the last answer of an earlier connection, or empty because none came yet, and says nothing about the drives in it now."`
}

type listStoragesInput struct {
	DeviceRef

	Refresh bool `query:"refresh" doc:"Ask the printer what storages it has now, and wait up to a second for its answer, rather than answer from what it last said." example:"true"`
}

type listStoragesOutput struct {
	Body DeviceStorages
}

type listFilesInput struct {
	DeviceRef

	// maxLength bounds the path a client may send. A printer's own filesystem
	// has far shorter limits than this.
	Path string `query:"path" doc:"The storage-prefixed directory to list, such as usb or usb/models. Empty lists the storages themselves." maxLength:"1024" example:"usb/models"`

	Refresh bool `query:"refresh" doc:"Have the printer read the path again even when this server holds a listing of it, or join the read in progress. The answer does not wait for that read: it is what this server holds, with reading set, and com.prusa.connect.device.files.changed is published when the read lands. Use it when the printer's own screen changed the path, which the printer reports to nobody." example:"true"`
}

type listFilesOutput struct {
	Body DeviceListing
}

// DeviceUpload names a file this deployment is holding and where it should
// land on the printer.
//
// It names a file and does not carry one. The printer fetches the bytes over
// its own connection while the transfer runs. A body carrying the file would
// have to be held for as long as the printer takes to read it, and nothing in
// this path may allocate in proportion to a file's length.
type DeviceUpload struct {
	Storage string `json:"storage"           doc:"The storage resource holding the file, by the name it is declared under in the resources block."                                                                                                                                                                                                                    maxLength:"128"`
	Source  string `json:"source"            doc:"The file's path inside that storage, relative to its root. A path a temporary upload answered (upload-storage-object with temporary=true) names a copy the server deletes once the file has landed or the transfer was stopped. After a failed send it is kept for one hour, so the same source can be sent again." maxLength:"1024"`
	Path    string `json:"path"              doc:"Where the file lands on the printer: storage-prefixed and relative, such as usb/part.bgcode."                                                                                                                                                                                                                       maxLength:"1024"`
	Replace bool   `json:"replace,omitempty" doc:"Delete a file the printer already holds at path before sending this one. Without it such a file is refused with core.printer/FILE_NAME_TAKEN, so a page can ask the person first."`
	// KeepBoth sits after Replace in DevicePrint too, so one converts to the
	// other.
	KeepBoth bool `json:"keep_both,omitempty" doc:"Send under a free name when the printer already holds a file at path, such as usb/part[1].bgcode, and keep the printer's file. The transfer names the path the file lands at. Refused together with replace."`
}

type sendFileInput struct {
	DeviceRef

	Body DeviceUpload
}

type sendFileOutput struct {
	Body struct {
		Path     string         `json:"path"     doc:"Where the file landed on the printer."`
		Transfer DeviceTransfer `json:"transfer" doc:"The transfer as it ended, including how many bytes this server relayed."`
	}
}

type listTransfersOutput struct {
	Body struct {
		Items []DeviceTransfer `json:"items"`
	}
}

type stopTransferInput struct {
	DeviceRef

	TransferID string `path:"transfer_id" doc:"The transfer to stop, as the send route or the listing named it." maxLength:"64" example:"01M1GRZD931RAKEXTQ7V15GC1K"`
}

// fileErrors adds to the printer refusals what asking a printer about a path
// answers with. 400 is a path that leaves the storage it names. 409 is the
// three conflicts: a printer whose resolved set does not carry the verb the
// listing is spoken through, a printer that refused to report the path, and a
// printer whose session has ended.
var fileErrors = append([]int{http.StatusBadRequest, http.StatusConflict}, deviceErrors...)

// registerDeviceFiles publishes what a printer holds: the storage devices it
// reported, and one directory of its own file tree at a time.
//
// There is no endpoint for a whole tree. A printer answers one path per frame
// and runs commands one after another, so a tree would be an unbounded number
// of commands on one request.
func registerDeviceFiles(api huma.API, fleet DeviceFleet) {
	registerListStorages(api, fleet)
	registerListFiles(api, fleet)
	registerDeviceThumbnail(api, fleet)
	registerSendFile(api, fleet)
	registerPrintFile(api, fleet)
	registerListTransfers(api, fleet)
	registerStopTransfer(api, fleet)
	registerRunningTransfers(api, fleet)
	registerDeleteFile(api, fleet)
	registerCreateFolder(api, fleet)
	registerDeleteFolder(api, fleet)
}

// deleteFileInput and createFolderInput carry the path the same way the listing
// does, because it is the same namespace: storage-prefixed and relative.
type deleteFileInput struct {
	DeviceRef

	Path string `query:"path" required:"true" doc:"The storage-prefixed file to remove, such as usb/part.bgcode." maxLength:"1024" example:"usb/bracket.bgcode"`
}

type createFolderInput struct {
	DeviceRef

	Body struct {
		Path string `json:"path" doc:"The storage-prefixed folder to create, such as usb/models." maxLength:"1024"`
	}
}

// folderOutput names what was created, so a caller has the path it can list.
type folderOutput struct {
	Body struct {
		Path string `json:"path" doc:"The folder that now exists on the printer."`
	}
}

func registerDeleteFile(api huma.API, fleet DeviceFleet) {
	register(api, huma.Operation{
		OperationID: "delete-device-file",
		Method:      http.MethodDelete,
		Path:        deviceFilesPath,
		Summary:     "Remove a file from a printer's storage",
		Description: "Deletes one file. A path naming a folder is refused, because the printer treats " +
			"the two as separate operations: use the folders endpoint for a folder. A path carrying a " +
			".. segment is refused here and never reaches the printer. It takes operate on the " +
			"printer, because it changes the machine's storage.",
		Tags:   []string{"devices"},
		Errors: fileErrors,
	}, func(ctx context.Context, in *deleteFileInput) (*struct{}, error) {
		if err := fleet.DeleteFile(ctx, principalFrom(ctx), in.DeviceID, in.Path); err != nil {
			return nil, err
		}

		return nil, nil //nolint:nilnil // 204: the deletion is the whole answer.
	})
}

func registerCreateFolder(api huma.API, fleet DeviceFleet) {
	register(api, huma.Operation{
		OperationID: "create-device-folder",
		Method:      http.MethodPost,
		Path:        deviceFoldersPath,
		Summary:     "Make a folder on a printer's storage",
		Description: "Creates one folder. A name the storage already holds is refused rather than " +
			"treated as success, because a caller that asked to create it wanted an empty one. It " +
			"takes operate on the printer.",
		Tags:   []string{"devices"},
		Errors: fileErrors,
	}, func(ctx context.Context, in *createFolderInput) (*folderOutput, error) {
		if err := fleet.CreateFolder(ctx, principalFrom(ctx), in.DeviceID, in.Body.Path); err != nil {
			return nil, err
		}

		out := &folderOutput{}
		out.Body.Path = in.Body.Path

		return out, nil
	})
}

func registerDeleteFolder(api huma.API, fleet DeviceFleet) {
	register(api, huma.Operation{
		OperationID: "delete-device-folder",
		Method:      http.MethodDelete,
		Path:        deviceFoldersPath,
		Summary:     "Remove a folder from a printer's storage",
		Description: "Deletes one folder. Whether the entries inside it go with it is the printer's " +
			"own rule and not this server's. A path naming a file is refused: use the files endpoint " +
			"for a file. It takes operate on the printer.",
		Tags:   []string{"devices"},
		Errors: fileErrors,
	}, func(ctx context.Context, in *deleteFileInput) (*struct{}, error) {
		if err := fleet.DeleteFolder(ctx, principalFrom(ctx), in.DeviceID, in.Path); err != nil {
			return nil, err
		}

		return nil, nil //nolint:nilnil // 204: the deletion is the whole answer.
	})
}

// registerSendFile publishes putting a file on a printer.
//
// It is a POST to the same path the listing is a GET of, because it adds an
// entry to that directory tree. What it takes is a name and not a file: the
// printer pulls the bytes over its own connection while the transfer runs, so
// the request names a file this deployment already holds.
func registerSendFile(api huma.API, fleet DeviceFleet) {
	register(api, huma.Operation{
		OperationID: "send-device-file",
		Method:      http.MethodPost,
		Path:        deviceFilesPath,
		Summary:     "Put a file on a printer's storage",
		Description: "Sends a file held by one of this deployment's storage resources to the " +
			"printer, which fetches it over its own connection. It returns when the printer reports " +
			"the file arrived, so it takes as long as the transfer does. A client that stops waiting " +
			"does not stop the transfer: it runs until the file arrives, the stop route ends it, or " +
			"core.api.stream_timeout passes. A file the printer already holds at `path` is refused with " +
			"core.printer/FILE_NAME_TAKEN, and the request sent again with `replace` true deletes it and " +
			"sends this one. A path carrying a .. segment is refused here and never reaches the printer, " +
			"on the source as well as on the destination. It takes operate on the printer, " +
			"because it writes to the machine, and read_storage on the storage kind, because the " +
			"source is a storage's contents.",
		Tags:     []string{"devices"},
		Errors:   fileErrors,
		Metadata: streams(),
	}, func(ctx context.Context, in *sendFileInput) (*sendFileOutput, error) {
		// The write deadline is lifted before the transfer starts. It is
		// already set when this runs, and a transfer that outlasts it would
		// complete on the printer and could not be reported to the caller.
		if err := liftWriteDeadline(ctx); err != nil {
			return nil, err
		}

		transfer, err := fleet.Send(ctx, principalFrom(ctx), in.DeviceID, in.Body)
		if err != nil {
			return nil, err
		}

		out := &sendFileOutput{}
		out.Body.Path = in.Body.Path
		out.Body.Transfer = transfer

		return out, nil
	})
}

func registerListStorages(api huma.API, fleet DeviceFleet) {
	register(api, huma.Operation{
		OperationID: "list-device-storages",
		Method:      http.MethodGet,
		Path:        deviceStoragesPath,
		Summary:     "The storage devices a printer reported",
		Description: "What the printer said its storages are the last time it was asked: every time " +
			"it connects, every time its file tree is listed from the root and this server holds no " +
			"listing of the root or refresh=true is set, and every time this route is called with " +
			"refresh=true. Buddy firmware also reports a drive put in or pulled out on its own, and " +
			"refresh=true catches a change it did not report. Asks that arrive together share one question " +
			"to the printer. A printer this server has no connection to is not asked and answers " +
			"what it last said. One that is busy, such as while it lists a full USB drive, answers what " +
			"it last said after a second, and an answer that comes later is published. reported " +
			"tells a printer with no storage from one that has not answered yet, which reports none " +
			"rather than an error. Every answer that changes the list is published as " +
			"com.prusa.connect.device.storages.changed. It takes observe on the printer, and operate " +
			"with refresh=true, because asking puts a frame on the wire.",
		Tags:   []string{"devices"},
		Errors: fileErrors,
	}, func(ctx context.Context, in *listStoragesInput) (*listStoragesOutput, error) {
		storages, err := fleet.Storages(ctx, principalFrom(ctx), in.DeviceID, in.Refresh)
		if err != nil {
			return nil, err
		}

		return &listStoragesOutput{Body: storages}, nil
	})
}

func registerListFiles(api huma.API, fleet DeviceFleet) {
	register(api, huma.Operation{
		OperationID: "list-device-files",
		Method:      http.MethodGet,
		Path:        deviceFilesPath,
		Summary:     "One directory of a printer's file tree",
		Description: "Answers at once with what this server holds for a path, and never waits for the " +
			"printer beyond about a second. An empty path lists the storage devices themselves. A " +
			"printer can take more than the request budget to list a full storage, so this server " +
			"reads a path on the printer in the background and keeps the last listing of each path. " +
			"A held listing is answered with stale set to true and starts no read. With nothing held, " +
			"or with refresh=true, a read of the path starts, or the one in progress is joined, and " +
			"the answer says reading with reading_since, the moment that read began; with nothing " +
			"held its items are empty. A read that fails is answered as fault, held until the next " +
			"read of the path starts. A read that lands, a file or folder sent, created or deleted " +
			"through this server, and a change the printer reports from another client, publish " +
			"com.prusa.connect.device.files.changed: ask again " +
			"without refresh for the listing it left. A listing is forgotten when the printer loses " +
			"its connection. A path carrying a .. segment is refused here and never reaches the " +
			"printer. It takes operate on the printer, because answering it can put a frame on the wire.",
		Tags:   []string{"devices"},
		Errors: fileErrors,
	}, func(ctx context.Context, in *listFilesInput) (*listFilesOutput, error) {
		listing, err := fleet.Files(ctx, principalFrom(ctx), in.DeviceID, in.Path, in.Refresh)
		if err != nil {
			return nil, err
		}

		return &listFilesOutput{Body: listing}, nil
	})
}

// deviceThumbnailInput is one printer file's picture, the version of the file
// the caller listed, and what the caller already holds of the picture.
type deviceThumbnailInput struct {
	DeviceRef

	Path     string `query:"path"     required:"true" doc:"The storage-prefixed print file, such as usb/part.bgcode."                                                                                                                                                       maxLength:"1024" example:"usb/bracket.bgcode"`
	Size     int64  `query:"size"                     doc:"The file's size as the listing reported it. With modified, it names the version of the file, and a picture this server holds for that version is served without asking the printer. Absent or zero always asks."                  example:"4096"               minimum:"0"`
	Modified int64  `query:"modified"                 doc:"The file's modification time as the listing reported it, in seconds since the Unix epoch. Zero is a printer that reported none."                                                                                                  example:"1750000000"         minimum:"0"`

	IfNoneMatch string `header:"If-None-Match" doc:"The entity tag of the picture this client already has. A picture with the same tag answers 304 with no body."`
}

// registerDeviceThumbnail serves the picture a printer reads out of a file on
// its own storage.
//
// The response is written by hand with the headers the storage thumbnail
// carries, for the reasons given there. The entity tag is the file's size and
// modification time, because that is all a printer says about a file's
// identity.
func registerDeviceThumbnail(api huma.API, fleet DeviceFleet) {
	register(api, huma.Operation{
		OperationID: "get-device-file-thumbnail",
		Method:      http.MethodGet,
		Path:        deviceThumbnailPath,
		Summary:     "The picture inside one file on a printer",
		Description: "Serves the thumbnail the printer reads out of a .gcode or .bgcode file on its own " +
			"storage, as image/png or image/jpeg. The printer is asked for it unless the request names " +
			"the version of the file with size and modified and this server already holds the picture " +
			"for that version. After a listing comes from the printer, and unless " +
			"core.device_thumbnail_prefetch is off, this server asks it for the picture " +
			"of each listed print file, one file at a time and only while the printer is idle, so such a " +
			"request usually finds the picture held. A version that carries no picture is remembered as " +
			"well. Unless core.device_thumbnail_disk is off, both are kept on disk too, so a restart does " +
			"not ask the printer again. It carries an ETag taken from the file's size and modification time, and a " +
			"client that sends it back in If-None-Match is answered 304 with no body. Cache-Control is " +
			"private, no-cache. A file that carries no picture, and a folder, answer 404 with " +
			"core.storage/NO_THUMBNAIL. It takes operate on the printer, because answering it can put " +
			"a frame on the wire.",
		Tags:      []string{"devices"},
		Errors:    fileErrors,
		Responses: thumbnailResponses("The file's size and modification time."),
	}, func(ctx context.Context, in *deviceThumbnailInput) (*huma.StreamResponse, error) {
		picture, err := fleet.Thumbnail(ctx, principalFrom(ctx), in.DeviceID, in.Path, in.Size, in.Modified)
		if err != nil {
			return nil, err
		}

		stamp := picture.Modified.UTC().Truncate(time.Second)
		tag := thumbnailTag(picture.Version)

		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			if thumbnailIsCurrent(tag, stamp, in.IfNoneMatch, "") {
				writeThumbnailHeaders(hctx, stamp, tag)
				hctx.SetStatus(http.StatusNotModified)

				return
			}

			writeThumbnail(hctx, picture, stamp, tag)
		}}, nil
	})
}

// registerListTransfers publishes what a printer has been sent.
//
// It answers from two places. The transfers this process is running carry the
// live byte count. The ended ones are the rows the history adapter folded from
// the two durable frames every transfer publishes. A build with no history
// adapter answers only with what is running.
func registerListTransfers(api huma.API, fleet DeviceFleet) {
	register(api, huma.Operation{
		OperationID: "list-device-transfers",
		Method:      http.MethodGet,
		Path:        deviceTransfersPath,
		Summary:     "The files this server has sent to a printer",
		Description: "Every transfer of this printer, newest first: the ones running now with the " +
			"bytes they have moved, and the ones that ended with how they ended. The same transfers " +
			"are on the event stream as they move, so a caller watching one subscribes rather than " +
			"polls. It takes observe on the printer.",
		Tags:   []string{"devices"},
		Errors: fileErrors,
	}, func(ctx context.Context, in *getDeviceInput) (*listTransfersOutput, error) {
		items, err := fleet.Transfers(ctx, principalFrom(ctx), in.DeviceID)
		if err != nil {
			return nil, err
		}

		out := &listTransfersOutput{}
		out.Body.Items = items

		return out, nil
	})
}

// registerStopTransfer ends a transfer that is running.
//
// It takes operate and not observe. Stopping a transfer changes what the
// machine is doing, and a caller who may only watch may not end somebody else's
// upload. It answers as soon as the transfer has been told to stop. The
// request that began the transfer writes the record of how it ended.
func registerStopTransfer(api huma.API, fleet DeviceFleet) {
	register(api, huma.Operation{
		OperationID: "stop-device-transfer",
		Method:      http.MethodPost,
		Path:        deviceTransferStopPath,
		Summary:     "Stop a transfer that is running",
		Description: "Tells the printer to abandon the transfer and closes the record as cancelled. " +
			"A transfer that has already ended is reported as not found, because there is nothing " +
			"left to stop. It takes operate on the printer.",
		Tags:   []string{"devices"},
		Errors: fileErrors,
	}, func(ctx context.Context, in *stopTransferInput) (*struct{}, error) {
		if err := fleet.StopTransfer(ctx, principalFrom(ctx), in.DeviceID, in.TransferID); err != nil {
			return nil, err
		}

		return nil, nil //nolint:nilnil // 204: stopping it is the whole answer.
	})
}
