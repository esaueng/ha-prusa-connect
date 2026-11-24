// Package settings holds the settings an operator writes for this module to
// the bounds it accepts, and turns them into the limits the jobs and the
// installs run under.
package settings

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"
)

// The defaults, which module.yaml publishes beside each key.
const (
	DefaultMaxJobs        = 1
	DefaultMaxQueued      = 16
	DefaultTimeoutMinutes = 30
	DefaultThreads        = 0
	DefaultMaxProjectMiB  = 512
	DefaultKeepJobs       = 200
	DefaultGitHubReleases = true
)

// Settings is what the operator writes under this instance's config block.
// A field is a pointer where zero is a value an operator may mean, so an
// absent key takes the default.
type Settings struct {
	MaxJobs        *int              `json:"max_jobs"`
	MaxQueued      *int              `json:"max_queued"`
	TimeoutMinutes *int              `json:"timeout_minutes"`
	Threads        *int              `json:"threads"`
	MaxProjectMiB  *int              `json:"max_project_mib"`
	KeepJobs       *int              `json:"keep_jobs"`
	GitHubReleases *bool             `json:"github_releases"`
	GitHubToken    string            `json:"github_token"`
	ManifestURL    string            `json:"manifest_url"`
	LocalBinaries  []string          `json:"local_binaries"`
	STLProfiles    map[string]string `json:"stl_profiles"`
}

// Values are the settings once checked.
type Values struct {
	MaxJobs        int
	MaxQueued      int
	Timeout        time.Duration
	Threads        int
	MaxInputBytes  int64
	KeepJobs       int
	GitHubReleases bool
	GitHubToken    string
	ManifestURL    string
	LocalBinaries  []string
	STLProfiles    map[string]string
}

// ErrInvalid reports a setting outside its bounds. The module refuses to
// start, and the operator reads the reason on the instance.
var ErrInvalid = errors.New("slicer: a setting is outside its bounds")

// The highest value each whole-number setting takes.
const (
	highestMaxJobs        = 16
	highestMaxQueued      = 1000
	highestTimeoutMinutes = 24 * 60
	highestThreads        = 256
	highestMaxProjectMiB  = 4096
	highestKeepJobs       = 10_000
)

// mebibyte is the shift from MiB to bytes.
const mebibyte = 20

// Checked holds every setting to its bounds and fills the defaults.
func (s Settings) Checked() (Values, error) {
	var (
		out             Values
		minutes, larger int
	)

	errs := []error{
		bounded(s.MaxJobs, DefaultMaxJobs, 1, highestMaxJobs, "max_jobs", &out.MaxJobs),
		bounded(s.MaxQueued, DefaultMaxQueued, 1, highestMaxQueued, "max_queued", &out.MaxQueued),
		bounded(s.TimeoutMinutes, DefaultTimeoutMinutes, 1, highestTimeoutMinutes, "timeout_minutes", &minutes),
		bounded(s.Threads, DefaultThreads, 0, highestThreads, "threads", &out.Threads),
		bounded(s.MaxProjectMiB, DefaultMaxProjectMiB, 1, highestMaxProjectMiB, "max_project_mib", &larger),
		bounded(s.KeepJobs, DefaultKeepJobs, 1, highestKeepJobs, "keep_jobs", &out.KeepJobs),
	}

	out.Timeout = time.Duration(minutes) * time.Minute
	out.MaxInputBytes = int64(larger) << mebibyte

	out.GitHubReleases = DefaultGitHubReleases
	if s.GitHubReleases != nil {
		out.GitHubReleases = *s.GitHubReleases
	}

	out.GitHubToken = s.GitHubToken

	errs = append(errs, checkManifest(s.ManifestURL), checkBinaries(s.LocalBinaries))
	out.ManifestURL = s.ManifestURL
	out.LocalBinaries = s.LocalBinaries
	profiles, err := checkedProfiles(s.STLProfiles)
	errs = append(errs, err)
	out.STLProfiles = profiles

	if joined := errors.Join(errs...); joined != nil {
		return Values{}, joined
	}

	return out, nil
}

// moduleReserve is what the module itself holds besides its slicer runs.
const moduleReserve = 64 << mebibyte

// RunMemoryBytes is the memory one slicer run may hold: the host's budget for
// this process less the module's own reserve, shared by the runs that may go
// at once. It is 0, no bound, when the host set no budget.
func RunMemoryBytes(budget int64, ok bool, maxJobs int) int64 {
	if !ok {
		return 0
	}

	return max((budget-moduleReserve)/int64(max(maxJobs, 1)), 1<<mebibyte)
}

// bounded writes a whole-number setting into into, or the default when it is
// absent, and refuses one outside low to high.
func bounded(held *int, fallback, low, high int, key string, into *int) error {
	if held == nil {
		*into = fallback

		return nil
	}

	if *held < low || *held > high {
		return fmt.Errorf("%w: %s is %d, and it takes %d to %d", ErrInvalid, key, *held, low, high)
	}

	*into = *held

	return nil
}

// checkManifest takes an https address or an absolute path, or nothing.
func checkManifest(manifest string) error {
	if manifest == "" || filepath.IsAbs(manifest) {
		return nil
	}

	address, err := url.Parse(manifest)
	if err != nil || address.Scheme != "https" || address.Host == "" {
		return fmt.Errorf("%w: manifest_url is %q, and it takes an https address or an absolute path",
			ErrInvalid, manifest)
	}

	return nil
}

// checkBinaries takes absolute paths only, so a binary never depends on the
// folder the server started in.
func checkBinaries(binaries []string) error {
	for _, binary := range binaries {
		if !filepath.IsAbs(binary) {
			return fmt.Errorf("%w: local_binaries holds %q, which is not an absolute path", ErrInvalid, binary)
		}
	}

	return nil
}
