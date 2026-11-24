// Package routes is the HTTP surface of this module.
//
// A module opens no HTTP port. The server receives the request, works out who
// is calling, and decides whether that person may call this module: a read
// takes view_resource on the instance, and anything else operate_resource. A
// token with the read scope reads, and one with the operate scope also slices.
//
// Changing what is installed is an administrator's act, because it downloads
// and runs a program on the server. So those routes also ask the server
// whether the person may configure modules, configure_resource on the module
// kind, which only the administer scope reaches. A person sees and cancels
// their own jobs, and an administrator sees and cancels every job.
package routes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/prusa3d/prusa-connect/sdk/accessapi"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"

	"github.com/prusa3d/server-slicing/internal/feed"
	"github.com/prusa3d/server-slicing/internal/install"
	"github.com/prusa3d/server-slicing/internal/jobs"
	"github.com/prusa3d/server-slicing/internal/project"
	"github.com/prusa3d/server-slicing/internal/runner"
	"github.com/prusa3d/server-slicing/internal/slicers"
)

// MaxBodyBytes is the largest JSON body a handler reads.
const MaxBodyBytes = 16 << 10

// MaxUploadBytes is the largest project the upload route takes, which is what
// the server passes through a module's mount. A larger project is uploaded
// into a storage and sliced from there.
const MaxUploadBytes = routesapi.MaxModuleBody

// maxListed is the most jobs one listing answers.
const maxListed = 200

// Access asks the server about a person.
type Access interface {
	Check(ctx context.Context, params accessapi.CheckParams) (accessapi.Decision, error)
}

// Status is what the module reports about itself.
type Status struct {
	Ready bool `json:"ready"`
	// Problem is why the module cannot slice, when it cannot.
	Problem string `json:"problem,omitempty"`
	// Selections says whether this build slices a chosen bed or chosen
	// objects, which needs a 3MF library.
	Selections bool               `json:"selections"`
	Jobs       map[jobs.State]int `json:"jobs"`
	Limits     Limits             `json:"limits"`
}

// Limits are the bounds the operator set, as a page shows them. MemoryMiB is
// not a setting: it is the memory one run may hold under the host's memory
// budget, 0 when the host set none.
type Limits struct {
	MaxJobs        int   `json:"max_jobs"`
	MaxQueued      int   `json:"max_queued"`
	TimeoutMinutes int   `json:"timeout_minutes"`
	MemoryMiB      int64 `json:"memory_mib"`
	Threads        int   `json:"threads"`
	MaxProjectMiB  int64 `json:"max_project_mib"`
	MaxUploadBytes int   `json:"max_upload_bytes"`
}

// State is what the module holds once the host sent its settings.
type State struct {
	Ready   bool
	Problem string
	Jobs    *jobs.Manager
	Slicers *slicers.Service
	Limits  Limits
	// Selections is true when a 3MF library prepares the projects.
	Selections  bool
	STLProfiles []string
	// Background is the context installs run under, which ends when the
	// module stops rather than when the request does.
	Background context.Context
}

// Service is what the routes read.
type Service struct {
	State  func() State
	Access Access
}

// New is the whole HTTP surface of this module. Every path is relative to the
// mount the host assigned.
func New(s Service) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /status", s.acting(s.status, false))
	mux.HandleFunc("GET /slicers", s.acting(s.listSlicers, true))
	mux.HandleFunc("POST /slicers/install", s.acting(s.administering(s.installSlicer), true))
	mux.HandleFunc("POST /slicers/refresh", s.acting(s.administering(s.refreshSlicers), true))
	mux.HandleFunc("DELETE /slicers/{version}", s.acting(s.administering(s.removeSlicer), true))
	mux.HandleFunc("PUT /slicers/default", s.acting(s.administering(s.pinSlicer), true))
	mux.HandleFunc("GET /jobs", s.acting(s.listJobs, true))
	mux.HandleFunc("POST /jobs", s.acting(s.submit, true))
	mux.HandleFunc("POST /jobs/upload", s.acting(s.upload, true))
	mux.HandleFunc("GET /jobs/{id}", s.acting(s.getJob, true))
	mux.HandleFunc("POST /jobs/{id}/cancel", s.acting(s.cancel, true))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		answer(w, http.StatusNotFound, map[string]string{"module_saw": r.URL.Path})
	})

	return mux
}

// handler is one route, with the person and the state already read.
type handler func(w http.ResponseWriter, r *http.Request, state State, by string)

// acting refuses a request the server attributed to nobody, one that arrived
// before the settings, and, for a route that slices, one to a module that
// cannot.
func (s Service) acting(next handler, slicing bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		by := routesapi.PrincipalOf(r)
		if strings.TrimSpace(by) == "" {
			refuse(w, NoPrincipal, "the host named no person for this request")

			return
		}

		state := s.State()
		if !state.Ready {
			refuse(w, NotReady, "the host has not sent the settings yet. Try again in a moment.")

			return
		}

		if slicing && state.Jobs == nil {
			refuse(w, NoData, state.Problem)

			return
		}

		next(w, r, state, by)
	}
}

// administering refuses a person who may not configure modules. The question
// is asked while the request is open, so the server holds it to the scopes of
// the person's credential.
func (s Service) administering(next handler) handler {
	return func(w http.ResponseWriter, r *http.Request, state State, by string) {
		allowed, err := s.administers(r.Context(), by)
		if err != nil {
			refuse(w, AccessUnknown, AccessUnknown.Title+". Try again in a moment.")

			return
		}

		if !allowed {
			refuse(w, NotAdministrator, "installing, removing and choosing a slicer takes configure_resource "+
				"on modules, which an administrator holds, and a credential with the administer scope")

			return
		}

		next(w, r, state, by)
	}
}

// administers asks whether by may configure modules.
func (s Service) administers(ctx context.Context, by string) (bool, error) {
	decision, err := s.Access.Check(ctx, accessapi.CheckParams{
		Subject: by, Action: "configure_resource", ObjectType: "resource_kind", ObjectID: "sink",
	})
	if err != nil {
		return false, err
	}

	return decision.Allowed, nil
}

func (s Service) status(w http.ResponseWriter, _ *http.Request, state State, _ string) {
	out := Status{Ready: state.Ready, Problem: state.Problem, Selections: state.Selections, Limits: state.Limits}

	if state.Jobs != nil {
		out.Jobs = state.Jobs.Counts()
	}

	answer(w, http.StatusOK, out)
}

// listSlicers answers the slicers. Only an administrator is told where each
// executable is on this server; a failed question is answered as no.
func (s Service) listSlicers(w http.ResponseWriter, r *http.Request, state State, by string) {
	administrator, err := s.administers(r.Context(), by)

	view := state.Slicers.View(err == nil && administrator)
	view.STLProfiles = state.STLProfiles
	answer(w, http.StatusOK, view)
}

// administratorView answers the slicers with the executable of each on this
// server. Only a handler behind administering calls it.
func administratorView(state State) slicers.View {
	return state.Slicers.View(true)
}

// versionBody is the body of the default route.
type versionBody struct {
	Version string `json:"version"`
}

// installBody is the body of the install route.
type installBody struct {
	Version string `json:"version"`
	// Unverified confirms the install of a release that publishes no
	// SHA-256, whose download cannot be checked.
	Unverified bool `json:"unverified"`
}

func (s Service) installSlicer(w http.ResponseWriter, r *http.Request, state State, _ string) {
	var body installBody
	if !readJSON(w, r, &body) {
		return
	}

	err := state.Slicers.Install(state.Background, body.Version, body.Unverified, nil)

	switch {
	case errors.Is(err, slicers.ErrUnverified):
		refuse(w, Unverified, err.Error())
	case errors.Is(err, install.ErrNotOffered):
		refuse(w, NotOffered, err.Error())
	case errors.Is(err, install.ErrBusy):
		refuse(w, SlicerBusy, err.Error())
	case err != nil:
		refuse(w, StoreFailed, err.Error())
	default:
		answer(w, http.StatusAccepted, administratorView(state))
	}
}

// refreshSlicers reads the GitHub release list again. A list that cannot be
// read is not a refusal: the page shows the list on disk and why.
func (s Service) refreshSlicers(w http.ResponseWriter, r *http.Request, state State, _ string) {
	err := state.Slicers.Refresh(r.Context())
	if errors.Is(err, feed.ErrTooSoon) {
		refuse(w, RefreshTooSoon, err.Error())

		return
	}

	answer(w, http.StatusOK, administratorView(state))
}

func (s Service) removeSlicer(w http.ResponseWriter, r *http.Request, state State, _ string) {
	err := state.Slicers.Remove(r.PathValue("version"))

	switch {
	case errors.Is(err, install.ErrNotInstalled):
		refuse(w, NotInstalled, err.Error())
	case errors.Is(err, install.ErrBusy):
		refuse(w, SlicerBusy, err.Error())
	case err != nil:
		refuse(w, StoreFailed, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s Service) pinSlicer(w http.ResponseWriter, r *http.Request, state State, _ string) {
	var body versionBody
	if !readJSON(w, r, &body) {
		return
	}

	err := state.Slicers.Pin(body.Version)

	switch {
	case errors.Is(err, install.ErrNotInstalled):
		refuse(w, NotInstalled, err.Error())
	case err != nil:
		refuse(w, StoreFailed, err.Error())
	default:
		answer(w, http.StatusOK, administratorView(state))
	}
}

// JobList is what GET /jobs answers.
type JobList struct {
	Jobs  []jobs.Job `json:"jobs"`
	Total int        `json:"total"`
	// All says the list holds every person's jobs, which an administrator
	// sees.
	All bool `json:"all"`
}

func (s Service) listJobs(w http.ResponseWriter, r *http.Request, state State, by string) {
	query := r.URL.Query()

	limit := maxListed
	if text := query.Get("limit"); text != "" {
		parsed, err := strconv.Atoi(text)
		if err != nil || parsed < 1 || parsed > maxListed {
			refuse(w, Invalid, "limit takes 1 to "+strconv.Itoa(maxListed))

			return
		}

		limit = parsed
	}

	wanted := jobs.State(query.Get("state"))
	if wanted != "" &&
		!slices.Contains([]jobs.State{jobs.Queued, jobs.Running, jobs.Done, jobs.Failed, jobs.Canceled}, wanted) {
		refuse(w, Invalid, "state takes queued, running, done, failed or canceled")

		return
	}

	all, err := s.administers(r.Context(), by)
	if err != nil {
		refuse(w, AccessUnknown, AccessUnknown.Title+". Try again in a moment.")

		return
	}

	listed, total := state.Jobs.List(by, all, wanted, limit)
	answer(w, http.StatusOK, JobList{Jobs: listed, Total: total, All: all})
}

// SubmitBody is the body of POST /jobs.
type SubmitBody struct {
	Source    jobs.Source       `json:"source"`
	Output    jobs.Target       `json:"output"`
	Slicer    string            `json:"slicer"`
	Profile   string            `json:"profile"`
	Selection project.Selection `json:"selection"`
	Format    runner.Format     `json:"format"`
	Overwrite bool              `json:"overwrite"`
}

func (s Service) submit(w http.ResponseWriter, r *http.Request, state State, by string) {
	var body SubmitBody
	if !readJSON(w, r, &body) {
		return
	}

	if body.Source.Upload != "" {
		refuse(w, Invalid, "an uploaded project is sent to POST /jobs/upload")

		return
	}

	job, err := state.Jobs.Submit(r.Context(), jobs.Request{
		By: by, Source: body.Source, Output: body.Output, Slicer: body.Slicer, Profile: body.Profile,
		Selection: body.Selection, Format: body.Format, Overwrite: body.Overwrite,
	})
	taken(w, job, err)
}

func (s Service) upload(w http.ResponseWriter, r *http.Request, state State, by string) {
	query := r.URL.Query()

	selection := project.Selection{}

	if text := query.Get("setup"); text != "" {
		setup, err := strconv.Atoi(text)
		if err != nil {
			refuse(w, Invalid, "setup is a whole number")

			return
		}

		selection.Setup = setup
	}

	if text := query.Get("bed"); text != "" {
		bed, err := strconv.Atoi(text)
		if err != nil {
			refuse(w, Invalid, "bed is a whole number")

			return
		}

		selection.Bed = bed
	}

	if text := query.Get("items"); text != "" {
		for part := range strings.SplitSeq(text, ",") {
			item, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				refuse(w, Invalid, "items is a list of whole numbers, such as 0,2,5")

				return
			}

			selection.Items = append(selection.Items, item)
		}
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, MaxUploadBytes+1))
	if err != nil {
		refuse(w, Invalid, "the body could not be read")

		return
	}

	if len(data) > MaxUploadBytes {
		refuse(w, BodyTooLarge, "an uploaded project is at most "+strconv.Itoa(MaxUploadBytes)+
			" bytes. Upload a larger one into a storage and slice it from there")

		return
	}

	if len(data) == 0 {
		refuse(w, Invalid, "the body holds no project")

		return
	}

	job, err := state.Jobs.Submit(r.Context(), jobs.Request{
		By:     by,
		Source: jobs.Source{Upload: query.Get("name")},
		Upload: data,
		Output: jobs.Target{Storage: query.Get("output_storage"), Path: query.Get("output_path")},
		Slicer: query.Get(
			"slicer",
		),
		Profile:   query.Get("profile"),
		Selection: selection,
		Format:    runner.Format(query.Get("format")),
		Overwrite: query.Get("overwrite") == "true",
	})
	taken(w, job, err)
}

// taken answers a submitted job, or why it was not taken.
func taken(w http.ResponseWriter, job jobs.Job, err error) {
	var refusal *jobs.RefusalError

	switch {
	case errors.As(err, &refusal):
		refuse(w, byJobCode(refusal.Code), refusal.Message)
	case err != nil:
		refuse(w, StoreFailed, StoreFailed.Title+". Try again in a moment.")
	default:
		answer(w, http.StatusAccepted, job)
	}
}

func (s Service) getJob(w http.ResponseWriter, r *http.Request, state State, by string) {
	job, found := state.Jobs.Get(r.PathValue("id"))
	if found && job.By != by {
		all, err := s.administers(r.Context(), by)
		found = err == nil && all
	}

	if !found {
		refuse(w, JobNotFound, "there is no job "+r.PathValue("id"))

		return
	}

	answer(w, http.StatusOK, job)
}

func (s Service) cancel(w http.ResponseWriter, r *http.Request, state State, by string) {
	all := false

	if job, found := state.Jobs.Get(r.PathValue("id")); found && job.By != by {
		allowed, err := s.administers(r.Context(), by)
		all = err == nil && allowed
	}

	job, err := state.Jobs.Cancel(r.PathValue("id"), by, all)

	var refusal *jobs.RefusalError

	switch {
	case errors.As(err, &refusal):
		refuse(w, byJobCode(refusal.Code), refusal.Message)
	case err != nil:
		refuse(w, StoreFailed, StoreFailed.Title)
	default:
		answer(w, http.StatusOK, job)
	}
}

// readJSON reads a bounded JSON body into into, and refuses one it cannot.
func readJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	data, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		refuse(w, Invalid, "the body could not be read")

		return false
	}

	if len(data) > MaxBodyBytes {
		refuse(w, BodyTooLarge, "a body is at most "+strconv.Itoa(MaxBodyBytes)+" bytes")

		return false
	}

	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()

	if err = decoder.Decode(into); err != nil {
		refuse(w, Invalid, "the body is not the JSON this route reads: "+err.Error())

		return false
	}

	return true
}
