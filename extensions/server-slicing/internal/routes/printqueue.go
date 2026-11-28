package routes

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/prusa3d/prusa-connect/sdk/routesapi"

	"github.com/prusa3d/server-slicing/internal/jobs"
)

// PrintQueueModule is the installed namespace of Print queue, as named in module.yaml.
const PrintQueueModule = "com.github.aux.prusa3d.simple-queue"

//nolint:gochecknoglobals // A printer ID is a DNS label and a job ID is eight bytes in hex.
var (
	deviceID = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	jobID    = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

type queueFile struct {
	Storage string `json:"storage"`
	Path    string `json:"path"`
}

type enqueueBody struct {
	Device string `json:"device_id"`
}

type enqueueResult struct {
	Waiting *int `json:"waiting,omitempty"`
}

func (s Service) enqueue(w http.ResponseWriter, r *http.Request, state State, by string) {
	var body enqueueBody
	if !readJSON(w, r, &body) {
		return
	}

	id := r.PathValue("id")
	if !jobID.MatchString(id) || !deviceID.MatchString(body.Device) {
		refuse(w, Invalid, "name a slicing job and a printer by their IDs")

		return
	}

	job, found := state.Jobs.Get(id)
	if !found || job.By != by {
		refuse(w, JobNotFound, JobNotFound.Title)

		return
	}

	result := job.Result
	if job.State != jobs.Done || result == nil || result.Path == "" || result.Storage == "" {
		refuse(w, JobNotPrintable, JobNotPrintable.Title)

		return
	}

	payload, err := json.Marshal(queueFile{Storage: result.Storage, Path: result.Path})
	if err != nil {
		refuse(w, PrintQueueFailed, PrintQueueFailed.Title)

		return
	}

	response, fault := s.callPrintQueue(r, by, body.Device, payload)
	if fault.Name != "" {
		refuse(w, fault, fault.Title)

		return
	}
	if response.Status != http.StatusCreated {
		fault = printQueueFault(response)
		refuse(w, fault, fault.Title)

		return
	}

	answer(w, http.StatusCreated, printQueueResult(response.Body))
}

func (s Service) checkPrintQueue(w http.ResponseWriter, r *http.Request, _ State, by string) {
	device := r.PathValue("device")
	if !deviceID.MatchString(device) {
		refuse(w, Invalid, "name a printer by its ID")

		return
	}

	// Print queue's OpenAPI puts the printer access check before body validation.
	// An empty object reaches that validation and cannot add an entry.
	response, fault := s.callPrintQueue(r, by, device, []byte(`{}`))
	if fault.Name != "" {
		refuse(w, fault, fault.Title)

		return
	}
	if response.Status == http.StatusBadRequest && printQueueCode(response.Body) == "simple-queue/INVALID" {
		w.WriteHeader(http.StatusNoContent)

		return
	}

	fault = printQueueFault(response)
	refuse(w, fault, fault.Title)
}

func (s Service) callPrintQueue(r *http.Request, by, device string, body []byte) (routesapi.Response, Code) {
	if !strings.HasPrefix(by, "user:") || by == "user:" {
		return routesapi.Response{}, PrintQueueDenied
	}
	if s.Calls == nil {
		return routesapi.Response{}, PrintQueueUnavailable
	}

	response, err := s.Calls.Call(r.Context(), routesapi.CallParams{
		Module: PrintQueueModule, Principal: by, Method: http.MethodPost,
		Path: "/printers/" + device + "/entries", Body: body,
		Headers: map[string][]string{"Content-Type": {"application/json"}},
	})
	switch {
	case err == nil:
		return response, Code{}
	case errors.Is(err, routesapi.ErrCallUnavailable):
		return routesapi.Response{}, PrintQueueUnavailable
	case errors.Is(err, routesapi.ErrCallNotDeclared):
		return routesapi.Response{}, PrintQueueNotApproved
	case errors.Is(err, routesapi.ErrCallDenied):
		return routesapi.Response{}, PrintQueueDenied
	default:
		return routesapi.Response{}, PrintQueueFailed
	}
}

func printQueueCode(body []byte) string {
	var fault struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &fault); err != nil {
		return ""
	}

	return fault.Code
}

func printQueueFault(response routesapi.Response) Code {
	switch printQueueCode(response.Body) {
	case "simple-queue/NOT_READY":
		return PrintQueueUnavailable
	case "simple-queue/NOT_OPERATOR", "simple-queue/NOT_A_PERSON", "simple-queue/FILE_DENIED":
		return PrintQueueDenied
	case "simple-queue/PRINTER_NOT_FOUND":
		return PrintQueuePrinterNotFound
	case "simple-queue/PRINTER_UNKNOWN", "simple-queue/ACCESS_UNKNOWN":
		return PrintQueuePrinterUnknown
	case "simple-queue/QUEUE_FULL":
		return PrintQueueFull
	case "simple-queue/FILE_NOT_FOUND":
		return PrintQueueFileNotFound
	case "simple-queue/NOT_PRINTABLE":
		return JobNotPrintable
	default:
		return PrintQueueFailed
	}
}

func printQueueResult(body []byte) enqueueResult {
	var view struct {
		Entries []struct {
			State string `json:"state"`
		} `json:"entries"`
	}
	// The 201 confirms the write even when this module cannot read the optional queue view.
	if err := json.Unmarshal(body, &view); err != nil {
		return enqueueResult{}
	}

	var result enqueueResult
	if view.Entries != nil {
		waiting := 0
		for _, entry := range view.Entries {
			if entry.State == "waiting" {
				waiting++
			}
		}
		result.Waiting = &waiting
	}
	return result
}
