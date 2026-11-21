package routes

import (
	"encoding/json"
	"net/http"
	"sort"
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
	Invalid = Code{Name: "simple-queue/INVALID", Status: http.StatusBadRequest,
		Title: "the module did not accept what was sent"}
	BodyTooLarge = Code{Name: "simple-queue/BODY_TOO_LARGE", Status: http.StatusRequestEntityTooLarge,
		Title: "the request body is larger than this module reads"}
	NoPrincipal = Code{Name: "simple-queue/NO_PRINCIPAL", Status: http.StatusForbidden,
		Title: "the request carries no signed-in person"}
	NotReady = Code{Name: "simple-queue/NOT_READY", Status: http.StatusServiceUnavailable,
		Title: "the module is still starting", Retryable: true}
	AccessUnknown = Code{Name: "simple-queue/ACCESS_UNKNOWN", Status: http.StatusServiceUnavailable,
		Title: "the server did not say what you may do", Retryable: true}

	PrinterNotFound = Code{Name: "simple-queue/PRINTER_NOT_FOUND", Status: http.StatusNotFound,
		Title: "there is no such printer"}
	PrinterUnknown = Code{Name: "simple-queue/PRINTER_UNKNOWN", Status: http.StatusServiceUnavailable,
		Title: "the server did not answer about the printer", Retryable: true}
	NotOperator = Code{Name: "simple-queue/NOT_OPERATOR", Status: http.StatusForbidden,
		Title: "you may not print on this printer"}
	NotAPerson = Code{Name: "simple-queue/NOT_A_PERSON", Status: http.StatusForbidden,
		Title: "only a person may add to a queue"}

	FileNotFound = Code{Name: "simple-queue/FILE_NOT_FOUND", Status: http.StatusNotFound,
		Title: "there is no such file"}
	FileDenied = Code{Name: "simple-queue/FILE_DENIED", Status: http.StatusForbidden,
		Title: "you may not read that storage"}
	NotPrintable = Code{Name: "simple-queue/NOT_PRINTABLE", Status: http.StatusUnsupportedMediaType,
		Title: "the file is not a print file"}

	QueueFull = Code{Name: "simple-queue/QUEUE_FULL", Status: http.StatusConflict,
		Title: "the queue of this printer is full"}
	QueueEmpty = Code{Name: "simple-queue/QUEUE_EMPTY", Status: http.StatusConflict,
		Title: "nothing is waiting in the queue"}
	EntryNotFound = Code{Name: "simple-queue/ENTRY_NOT_FOUND", Status: http.StatusNotFound,
		Title: "the queue holds no such entry"}
	EntryActive = Code{Name: "simple-queue/ENTRY_ACTIVE", Status: http.StatusConflict,
		Title: "the entry is on its way to the printer or printing"}
	PrinterBusy = Code{Name: "simple-queue/PRINTER_BUSY", Status: http.StatusConflict,
		Title: "the printer already has a print from the queue", Retryable: true}
	PrinterNotFree = Code{Name: "simple-queue/PRINTER_NOT_FREE", Status: http.StatusConflict,
		Title: "the printer cannot start a print now", Retryable: true}
	NoStorage = Code{Name: "simple-queue/NO_STORAGE", Status: http.StatusConflict,
		Title: "the printer has no storage the file can be written to"}
	SendFailed = Code{Name: "simple-queue/SEND_FAILED", Status: http.StatusBadGateway,
		Title: "the printer did not start the print"}
	ReadyUnsupported = Code{Name: "simple-queue/READY_UNSUPPORTED", Status: http.StatusConflict,
		Title: "this printer cannot be marked as ready"}
	StoreFailed = Code{Name: "simple-queue/STORE_FAILED", Status: http.StatusInternalServerError,
		Title: "the module could not save the queue", Retryable: true}
)

// Codes answers every code this module can return, sorted by name.
func Codes() []Code {
	held := []Code{
		Invalid, BodyTooLarge, NoPrincipal, NotReady, AccessUnknown,
		PrinterNotFound, PrinterUnknown, NotOperator, NotAPerson,
		FileNotFound, FileDenied, NotPrintable,
		QueueFull, QueueEmpty, EntryNotFound, EntryActive, PrinterBusy, PrinterNotFree,
		NoStorage, SendFailed, ReadyUnsupported, StoreFailed,
	}

	sort.Slice(held, func(a, b int) bool { return held[a].Name < held[b].Name })

	return held
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
