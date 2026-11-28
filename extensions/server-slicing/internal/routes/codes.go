package routes

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/prusa3d/server-slicing/internal/jobs"
)

// Every error this module answers a request with, in one place.
//
// The core keeps its own registry of codes and a module's codes are strings
// outside it, so each one starts with this module's short name to stay
// readable in a log beside the core's. A client branches on the code and shows
// the title, so the title is one phrase that never varies and the detail is
// what went wrong this time.

// Code is one error this module answers with.
type Code struct {
	Name      string
	Status    int
	Title     string
	Retryable bool
}

// The codes this module answers with.
//
//nolint:gochecknoglobals // the code table; a const cannot hold a struct.
var (
	Invalid = Code{Name: string(jobs.CodeInvalid), Status: http.StatusBadRequest,
		Title: "the module did not accept what was sent"}
	BodyTooLarge = Code{Name: "server-slicing/BODY_TOO_LARGE", Status: http.StatusRequestEntityTooLarge,
		Title: "the request body is larger than this module reads"}
	NoPrincipal = Code{Name: "server-slicing/NO_PRINCIPAL", Status: http.StatusForbidden,
		Title: "the request carries no signed-in person"}
	NotReady = Code{Name: "server-slicing/NOT_READY", Status: http.StatusServiceUnavailable,
		Title: "the module is still starting", Retryable: true}
	NoData = Code{Name: "server-slicing/NO_DATA_DIRECTORY", Status: http.StatusServiceUnavailable,
		Title: "the module was granted no data directory, so it cannot slice"}
	NotAdministrator = Code{Name: "server-slicing/NOT_ADMINISTRATOR", Status: http.StatusForbidden,
		Title: "only a person who may configure modules may change the installed slicers"}
	AccessUnknown = Code{Name: "server-slicing/ACCESS_UNKNOWN", Status: http.StatusServiceUnavailable,
		Title: "the server did not say what you may do", Retryable: true}

	SourceNotFound = Code{Name: string(jobs.CodeSourceNotFound), Status: http.StatusNotFound,
		Title: "there is no such project"}
	SourceDenied = Code{Name: string(jobs.CodeSourceDenied), Status: http.StatusForbidden,
		Title: "you may not read that storage"}
	SourceTooLarge = Code{Name: string(jobs.CodeSourceTooLarge), Status: http.StatusRequestEntityTooLarge,
		Title: "the project is larger than this module slices"}
	NotAProject = Code{Name: string(jobs.CodeNotAProject), Status: http.StatusUnsupportedMediaType,
		Title: "the file is not a 3MF project"}
	OutputDenied = Code{Name: string(jobs.CodeOutputDenied), Status: http.StatusForbidden,
		Title: "you may not write into that storage"}
	NoSlicer = Code{Name: string(jobs.CodeNoSlicer), Status: http.StatusConflict,
		Title: "no installed slicer can take the job"}
	QueueFull = Code{Name: string(jobs.CodeQueueFull), Status: http.StatusServiceUnavailable,
		Title: "too many jobs are waiting", Retryable: true}
	JobNotFound = Code{Name: string(jobs.CodeNotFound), Status: http.StatusNotFound,
		Title: "there is no such job"}
	JobFinished = Code{Name: string(jobs.CodeFinished), Status: http.StatusConflict,
		Title: "the job has finished already"}
	JobNotPrintable = Code{Name: "server-slicing/JOB_NOT_PRINTABLE", Status: http.StatusConflict,
		Title: "the slicing job has no finished print file"}
	PrintQueueUnavailable = Code{Name: "server-slicing/PRINT_QUEUE_UNAVAILABLE", Status: http.StatusServiceUnavailable,
		Title: "the Print queue module is not running", Retryable: true}
	PrintQueueNotApproved = Code{Name: "server-slicing/PRINT_QUEUE_NOT_APPROVED", Status: http.StatusForbidden,
		Title: "the operator must approve the Print queue permission for Slicing"}
	PrintQueueDenied = Code{Name: "server-slicing/PRINT_QUEUE_DENIED", Status: http.StatusForbidden,
		Title: "you may not add prints to this queue"}
	PrintQueuePrinterNotFound = Code{Name: "server-slicing/PRINT_QUEUE_PRINTER_NOT_FOUND", Status: http.StatusNotFound,
		Title: "the Print queue cannot find this printer"}
	PrintQueuePrinterUnknown = Code{
		Name:      "server-slicing/PRINT_QUEUE_PRINTER_UNKNOWN",
		Status:    http.StatusServiceUnavailable,
		Title:     "the Print queue could not read this printer",
		Retryable: true,
	}
	PrintQueueFull = Code{Name: "server-slicing/PRINT_QUEUE_FULL", Status: http.StatusConflict,
		Title: "the queue of this printer is full"}
	PrintQueueFileNotFound = Code{Name: "server-slicing/PRINT_QUEUE_FILE_NOT_FOUND", Status: http.StatusNotFound,
		Title: "the sliced file is no longer in storage"}
	PrintQueueFailed = Code{Name: "server-slicing/PRINT_QUEUE_FAILED", Status: http.StatusBadGateway,
		Title: "the Print queue did not confirm the request"}

	NotOffered = Code{Name: "server-slicing/VERSION_NOT_OFFERED", Status: http.StatusNotFound,
		Title: "no manifest offers that version for this server"}
	NotInstalled = Code{Name: "server-slicing/VERSION_NOT_INSTALLED", Status: http.StatusNotFound,
		Title: "that version is not installed"}
	SlicerBusy = Code{Name: "server-slicing/VERSION_BUSY", Status: http.StatusConflict,
		Title: "that version is being installed or is slicing a job", Retryable: true}
	Unverified = Code{Name: "server-slicing/UNVERIFIED", Status: http.StatusConflict,
		Title: "the release publishes no checksum, so its download cannot be checked"}
	RefreshTooSoon = Code{Name: "server-slicing/REFRESH_TOO_SOON", Status: http.StatusTooManyRequests,
		Title: "the release list was read a moment ago", Retryable: true}
	StoreFailed = Code{Name: "server-slicing/STORE_FAILED", Status: http.StatusInternalServerError,
		Title: "the module could not write its files", Retryable: true}
)

// Codes answers every code this module can return, sorted by name.
func Codes() []Code {
	held := []Code{
		JobNotPrintable, PrintQueueUnavailable, PrintQueueNotApproved, PrintQueueDenied,
		PrintQueuePrinterNotFound, PrintQueuePrinterUnknown, PrintQueueFull, PrintQueueFileNotFound, PrintQueueFailed,
		Invalid, BodyTooLarge, NoPrincipal, NotReady, NoData, NotAdministrator, AccessUnknown,
		SourceNotFound, SourceDenied, SourceTooLarge, NotAProject, OutputDenied, NoSlicer,
		QueueFull, JobNotFound, JobFinished, NotOffered, NotInstalled, SlicerBusy,
		StoreFailed, Unverified, RefreshTooSoon,
	}

	sort.Slice(held, func(a, b int) bool { return held[a].Name < held[b].Name })

	return held
}

// byJobCode is the answer to a request the jobs refused.
func byJobCode(code jobs.Code) Code {
	for _, held := range Codes() {
		if held.Name == string(code) {
			return held
		}
	}

	return Invalid
}

// problem is one error, in the body shape RFC 9457 defines. The client SDK
// reads it: code reaches ApiError.code and detail reaches ApiError.message.
type problem struct {
	Type      string         `json:"type"`
	Status    int            `json:"status"`
	Code      string         `json:"code"`
	Title     string         `json:"title"`
	Detail    string         `json:"detail"`
	Params    map[string]any `json:"params"`
	Retryable bool           `json:"retryable"`
}

// refuse writes one refusal.
func refuse(w http.ResponseWriter, code Code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(code.Status)

	_ = json.NewEncoder(w).Encode(problem{ //nolint:errchkjson // the caller has gone when this fails.
		Type: "about:blank", Status: code.Status, Code: code.Name, Title: code.Title,
		Detail: detail, Params: map[string]any{}, Retryable: code.Retryable,
	})
}

// answer writes a JSON body.
func answer(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(value) //nolint:errchkjson // the caller has gone when this fails.
}
