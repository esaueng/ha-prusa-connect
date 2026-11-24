// Package slicers is every PrusaSlicer this module knows about: the releases
// GitHub publishes for this host, the builds the operator's manifest offers,
// the ones installed, the ones the operator named on disk, and the version an
// administrator chose as the default.
package slicers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/prusa3d/server-slicing/internal/catalog"
	"github.com/prusa3d/server-slicing/internal/feed"
	"github.com/prusa3d/server-slicing/internal/install"
)

// preferencesFile keeps what an administrator chose on the page.
const preferencesFile = "preferences.json"

// manifestTimeout bounds reading the operator's manifest.
const manifestTimeout = 30 * time.Second

// Config is where the service keeps its files and what the operator wrote.
type Config struct {
	// Dir is the module's data directory.
	Dir string
	// OS and Arch are the host's, in Go's names.
	OS, Arch string
	// GitHub offers the releases of prusa3d/PrusaSlicer.
	GitHub bool
	// GitHubURL is the release list, feed.ReleasesURL unless a test serves
	// its own, and GitHubToken an optional token for it.
	GitHubURL   string
	GitHubToken string
	// ManifestURL is the operator's manifest: an https address or an
	// absolute path. Empty is none.
	ManifestURL string
	// LocalBinaries are executables on this machine the operator named.
	LocalBinaries []string
	Client        *http.Client
	Log           *slog.Logger
}

// Problem is something the operator wrote that the service could not use.
type Problem struct {
	What  string `json:"what"`
	Error string `json:"error"`
}

// Service is the slicers of one module instance.
type Service struct {
	cfg   Config
	store *install.Store

	feed *feed.Feed

	mu       sync.Mutex
	operator catalog.Manifest
	local    []install.Slicer
	problems []Problem
	pinned   string
}

// Open reads what is installed and what was chosen. Load reads the
// manifests and the local binaries afterwards, because both may wait on the
// network or on a slow executable.
func Open(cfg Config) (*Service, error) {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}

	store, err := install.Open(filepath.Join(cfg.Dir, "slicers"), filepath.Join(cfg.Dir, "cache"), cfg.Client)
	if err != nil {
		return nil, err
	}

	service := &Service{cfg: cfg, store: store}

	if data, readErr := os.ReadFile(filepath.Join(cfg.Dir, preferencesFile)); readErr == nil {
		var held struct {
			Pinned string `json:"pinned"`
		}

		if json.Unmarshal(data, &held) == nil {
			service.pinned = held.Pinned
		}
	}

	if cfg.GitHub {
		if cfg.GitHubURL == "" {
			cfg.GitHubURL = feed.ReleasesURL
		}

		service.cfg = cfg
		service.feed = feed.Open(cfg.Dir, cfg.GitHubURL, cfg.Client, cfg.GitHubToken)
	}

	return service, nil
}

// Load reads the operator's manifest and asks each local binary its version.
func (s *Service) Load(ctx context.Context) {
	var (
		operator catalog.Manifest
		problems []Problem
		local    []install.Slicer
	)

	if s.cfg.ManifestURL != "" {
		held, err := s.readManifest(ctx)
		if err != nil {
			problems = append(problems, Problem{What: "manifest_url", Error: err.Error()})
		} else {
			operator = held
		}
	}

	for _, binary := range s.cfg.LocalBinaries {
		version, err := install.Probe(ctx, binary, nil)
		if err != nil {
			problems = append(problems, Problem{What: binary, Error: err.Error()})

			continue
		}

		channel := catalog.Stable
		if !version.Stable() {
			channel = catalog.Prerelease
		}

		local = append(
			local,
			install.Slicer{
				Version: version, Channel: channel, Source: install.FromLocal, Binary: binary,
				Bytes: install.DiskBytes(binary),
			},
		)
	}

	for _, problem := range problems {
		s.cfg.Log.WarnContext(ctx, "a slicer setting could not be used", "what", problem.What, "error", problem.Error)
	}

	s.mu.Lock()
	s.operator, s.local, s.problems = operator, local, problems
	s.mu.Unlock()
}

func (s *Service) readManifest(ctx context.Context) (catalog.Manifest, error) {
	if filepath.IsAbs(s.cfg.ManifestURL) {
		data, err := os.ReadFile(s.cfg.ManifestURL)
		if err != nil {
			return catalog.Manifest{}, fmt.Errorf("read the manifest: %w", err)
		}

		return catalog.Parse(data)
	}

	ctx, cancel := context.WithTimeout(ctx, manifestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.ManifestURL, nil)
	if err != nil {
		return catalog.Manifest{}, fmt.Errorf("read the manifest: %w", err)
	}

	response, err := s.cfg.Client.Do(request)
	if err != nil {
		return catalog.Manifest{}, fmt.Errorf("read the manifest: %w", err)
	}

	defer func() { _ = response.Body.Close() }() // the read error below is the one worth reporting

	if response.StatusCode != http.StatusOK {
		return catalog.Manifest{}, fmt.Errorf("read the manifest: the server answered %s", response.Status)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, catalog.MaxManifestBytes+1))
	if err != nil {
		return catalog.Manifest{}, fmt.Errorf("read the manifest: %w", err)
	}

	return catalog.Parse(data)
}

// Installed is every slicer a job can run: the installed builds and the local
// binaries.
func (s *Service) Installed() []install.Slicer {
	s.mu.Lock()
	local := slices.Clone(s.local)
	s.mu.Unlock()

	return append(s.store.Installed(), local...)
}

// Pinned is the version an administrator chose as the default.
func (s *Service) Pinned() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.pinned
}

// Acquire marks a version as in use by a job.
func (s *Service) Acquire(version string) func() { return s.store.Acquire(version) }

// github is the GitHub releases this host can install, and the ones in the
// range it cannot.
func (s *Service) github() ([]catalog.Candidate, []catalog.Skipped) {
	if s.feed == nil {
		return nil, nil
	}

	releases, _, _ := s.feed.Releases()

	return catalog.ReadGitHub(releases, s.cfg.OS, s.cfg.Arch)
}

// Available is every build offered for this host, newest first.
func (s *Service) Available() []catalog.Candidate {
	github, _ := s.github()

	s.mu.Lock()
	defer s.mu.Unlock()

	return catalog.Candidates(s.cfg.OS, s.cfg.Arch, github, s.operator)
}

// ErrUnverified reports an install of a build whose source published no
// digest, which an administrator did not confirm.
var ErrUnverified = errors.New("the release publishes no SHA-256, so its download cannot be checked")

// Install starts installing one offered version. done is called when it ends.
// A build with no published digest is installed only when unverified is set,
// which the page sets after an administrator confirmed it.
func (s *Service) Install(ctx context.Context, version string, unverified bool, done func(error)) error {
	wanted, err := catalog.ParseVersion(version)
	if err != nil {
		return fmt.Errorf("%w: %w", install.ErrNotOffered, err)
	}

	for _, candidate := range s.Available() {
		if candidate.Version.Compare(wanted) != 0 {
			continue
		}

		if !candidate.Verified() && !unverified {
			return fmt.Errorf("%w: confirm the install of %s to go on without one", ErrUnverified, version)
		}

		return s.store.Start(ctx, candidate, done)
	}

	return fmt.Errorf("%w: %s for %s/%s", install.ErrNotOffered, version, s.cfg.OS, s.cfg.Arch)
}

// Refresh reads the GitHub release list again. A feed read less than a minute
// ago answers feed.ErrTooSoon.
func (s *Service) Refresh(ctx context.Context) error {
	if s.feed == nil {
		return nil
	}

	return s.feed.Refresh(ctx)
}

// Keep reads the release list when it is stale, now and then every hour,
// until ctx ends. An install is never started here.
func (s *Service) Keep(ctx context.Context) {
	if s.feed == nil {
		return
	}

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		if s.feed.Stale() {
			if err := s.feed.Refresh(ctx); err != nil && !errors.Is(err, feed.ErrTooSoon) {
				s.cfg.Log.WarnContext(ctx, "the PrusaSlicer release list could not be read", "error", err)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Remove uninstalls one installed version. A local binary is the operator's
// and is never removed here. A pin on the removed version is cleared.
func (s *Service) Remove(version string) error {
	if err := s.store.Remove(version); err != nil {
		return err
	}

	if s.Pinned() == version {
		return s.Pin("")
	}

	return nil
}

// ErrNotInstalled reports a pin on a version that is not installed.
var ErrNotInstalled = install.ErrNotInstalled

// Pin chooses the version a job gets when it names none. Empty clears the
// choice, and a job then gets the newest final release.
func (s *Service) Pin(version string) error {
	if version != "" {
		if _, err := install.Choose(s.Installed(), version, "", 0); err != nil {
			return err
		}
	}

	data, err := json.Marshal(map[string]string{"pinned": version})
	if err != nil {
		return fmt.Errorf("write the preferences: %w", err)
	}

	if err = install.WriteFileAtomic(filepath.Join(s.cfg.Dir, preferencesFile), data); err != nil {
		return err
	}

	s.mu.Lock()
	s.pinned = version
	s.mu.Unlock()

	return nil
}

// Offer is one release as a page lists it: a build this host can install,
// or a release in the supported range it cannot, with the reason.
type Offer struct {
	Version  string          `json:"version"`
	Channel  catalog.Channel `json:"channel"`
	Source   catalog.Source  `json:"source"`
	Size     int64           `json:"size,omitempty"`
	Released string          `json:"released,omitempty"`
	Notes    string          `json:"notes,omitempty"`
	// Asset is the file name of the download, the last part of its address
	// without a query. An operator's address may carry a token in its query,
	// so the address itself is not answered.
	Asset string `json:"asset,omitempty"`
	// SHA256 is the digest the source published for the download, and empty
	// when it published none.
	SHA256 string `json:"sha256,omitempty"`
	// Verified says the download is checked against a SHA-256 its source
	// published. An install of one that is not takes a confirmation.
	Verified bool `json:"verified"`
	// Compatible is false for a release this host cannot install, and
	// Reason says why.
	Compatible bool   `json:"compatible"`
	Reason     string `json:"reason,omitempty"`
	Installed  bool   `json:"installed"`
}

// Catalogue says where the GitHub release list stands.
type Catalogue struct {
	// GitHub is false when the operator switched the GitHub releases off.
	GitHub bool `json:"github"`
	// FetchedAt is when the list on disk was read from GitHub.
	FetchedAt *time.Time `json:"fetched_at,omitempty"`
	// Error is why the last reading failed. The list on disk is still offered.
	Error string `json:"error,omitempty"`
	// Supported is the range of versions this module offers.
	Supported string `json:"supported"`
}

// Installed is one installed slicer as a page lists it. Path is the
// executable on this server, and only an administrator is told it.
type Installed struct {
	install.Slicer

	Path string `json:"path,omitempty"`
}

// MarshalJSON writes the slicer and the path beside it. The slicer's own
// MarshalJSON would otherwise stand for the whole row and leave the path out.
func (i Installed) MarshalJSON() ([]byte, error) {
	encoded, err := json.Marshal(i.Slicer)
	if err != nil || i.Path == "" {
		return encoded, err //nolint:wrapcheck // the encoder's own error, returned to the encoder
	}

	var fields map[string]any
	if err = json.Unmarshal(encoded, &fields); err != nil {
		return nil, err //nolint:wrapcheck // as above
	}

	fields["path"] = i.Path

	return json.Marshal(fields) //nolint:wrapcheck // as above
}

// View is everything a page shows about the slicers.
type View struct {
	STLProfiles []string           `json:"stl_profiles,omitempty"`
	Platform    string             `json:"platform"`
	Installed   []Installed        `json:"installed"`
	Available   []Offer            `json:"available"`
	Installing  []install.Progress `json:"installing"`
	Pinned      string             `json:"pinned,omitempty"`
	// Default is the version a job that names none gets, and empty when
	// nothing is installed.
	Default string `json:"default,omitempty"`
	// Update is the newest final release this host can install that is newer
	// than Default, and absent when there is none. Nothing installs it until
	// an administrator asks.
	Update    *Offer    `json:"update,omitempty"`
	Catalogue Catalogue `json:"catalogue"`
	Problems  []Problem `json:"problems"`
}

// View answers the slicers as a page shows them. An administrator is also told
// where each executable is.
func (s *Service) View(administrator bool) View {
	installed := s.Installed()

	view := View{
		Platform: s.cfg.OS + "/" + s.cfg.Arch, Installed: []Installed{}, Available: []Offer{},
		Installing: s.store.Running(), Pinned: s.Pinned(), Problems: []Problem{},
		Catalogue: s.catalogue(),
	}

	chosen, chooseErr := install.Choose(installed, "", view.Pinned, 0)
	if chooseErr == nil {
		view.Default = chosen.Name()
	}

	isInstalled := func(version catalog.Version) bool {
		return slices.ContainsFunc(installed, func(held install.Slicer) bool {
			return held.Source != install.FromLocal && held.Version.Compare(version) == 0
		})
	}

	for _, candidate := range s.Available() {
		offer := Offer{
			Version: candidate.Version.String(), Channel: candidate.Channel, Source: candidate.Source,
			Size: candidate.Build.Size, Released: candidate.Released, Notes: candidate.Notes,
			Asset: assetName(candidate.Build.URL), SHA256: candidate.Build.SHA256,
			Verified: candidate.Verified(), Compatible: true, Installed: isInstalled(candidate.Version),
		}

		view.Available = append(view.Available, offer)

		if chooseErr == nil && view.Update == nil && candidate.Channel == catalog.Stable &&
			candidate.Version.Compare(chosen.Version) > 0 && !offer.Installed {
			update := offer
			view.Update = &update
		}
	}

	_, skipped := s.github()
	for _, held := range skipped {
		view.Available = append(view.Available, Offer{
			Version: held.Version.String(), Channel: held.Channel, Source: catalog.FromGitHub,
			Released: held.Released, Notes: held.Notes, Reason: held.Reason,
		})
	}

	slices.SortStableFunc(view.Available, func(a, b Offer) int {
		left, _ := catalog.ParseVersion(a.Version)
		right, _ := catalog.ParseVersion(b.Version)

		return right.Compare(left)
	})

	s.mu.Lock()
	view.Problems = append(view.Problems, s.problems...)
	s.mu.Unlock()

	for _, held := range installed {
		row := Installed{Slicer: held}
		if administrator {
			row.Path = held.Binary
		}

		view.Installed = append(view.Installed, row)
	}

	return view
}

// assetName is the file name at the end of a download address, without its
// query, or empty when the address does not parse.
func assetName(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return ""
	}

	name := path.Base(parsed.Path)
	if name == "." || name == "/" {
		return ""
	}

	return name
}

// catalogue says where the GitHub release list stands.
func (s *Service) catalogue() Catalogue {
	out := Catalogue{
		GitHub:    s.feed != nil,
		Supported: fmt.Sprintf(">=%s <%d.0.0", catalog.MinVersion, catalog.BelowMajor),
	}

	if s.feed == nil {
		return out
	}

	_, fetched, err := s.feed.Releases()
	if !fetched.IsZero() {
		out.FetchedAt = &fetched
	}

	if err != nil {
		out.Error = err.Error()
	}

	return out
}

// IsBusy reports an install refused because one is running.
func IsBusy(err error) bool { return errors.Is(err, install.ErrBusy) }
