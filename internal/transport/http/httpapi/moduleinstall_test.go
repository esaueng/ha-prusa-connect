package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// What the old backlog asks of the install surface: the three addresses answer,
// the permission is asked before anything is staged, and a build with no
// installer says so rather than serving fewer paths than the release does.

// recordingInstaller is the installer these cases drive, holding what it was
// asked for so a test can say whether the handler passed the body through.
type recordingInstaller struct {
	installed  ModuleInstallation
	offers     []ModuleOffer
	fail       error
	releases   []ModuleRelease
	namespaces []string
	// market is the marketplace this installer reads, and nil reads none.
	market      *ModuleMarketplace
	switches    []bool
	communities []*bool
}

func (i *recordingInstaller) Install(
	_ context.Context, _ string, release ModuleRelease,
) (ModuleInstallation, error) {
	i.releases = append(i.releases, release)

	if i.fail != nil {
		return ModuleInstallation{}, i.fail
	}

	return i.installed, nil
}

func (i *recordingInstaller) Remove(_ context.Context, _ string, namespace string, _ bool) error {
	i.namespaces = append(i.namespaces, namespace)

	return i.fail
}

func (i *recordingInstaller) Index(context.Context, string) (ModuleIndex, error) {
	return ModuleIndex{Items: i.offers}, nil
}

func (i *recordingInstaller) Marketplace(context.Context) (ModuleMarketplace, bool, error) {
	if i.market == nil {
		return ModuleMarketplace{}, false, nil
	}

	return *i.market, true, i.fail
}

func (i *recordingInstaller) LeftDatabases(context.Context, string) ([]ModuleDatabaseLeft, error) {
	return nil, nil
}

func (i *recordingInstaller) PurgeDatabases(context.Context, string, ref.Ref, bool) ([]ModuleDatabasePurged, error) {
	return nil, nil
}

func (i *recordingInstaller) SetMarketplace(
	_ context.Context,
	enabled bool,
	community *bool,
) (ModuleMarketplace, bool, error) {
	i.switches = append(i.switches, enabled)
	i.communities = append(i.communities, community)

	if i.market == nil {
		return ModuleMarketplace{}, false, nil
	}

	i.market.Enabled = enabled

	if community != nil {
		i.market.Community = *community
	}

	return *i.market, true, i.fail
}

// permittingGrants answers yes to every question, so a case that is about the
// install surface is refused by nothing but the install surface.
type permittingGrants struct{ *fakeGrantStore }

func (permittingGrants) Can(
	context.Context, authz.Subject, authz.Action, authz.Object,
) (bool, error) {
	return true, nil
}

// serverWithInstaller is the surface these cases call, with the graph deciding
// what the case is about.
func serverWithInstaller(t *testing.T, installer ModuleInstaller, grants GrantStore) *Server {
	t.Helper()

	cfg := exampleConfig(&fakeFleet{}, seededStore(), seededPresets())
	cfg.ModuleInstall = installer
	cfg.Grants = grants

	server, err := New(cfg)
	require.NoError(t, err)

	return server
}

func TestInstallingAModuleReachesTheInstallerWithTheReleaseThatWasAskedFor(t *testing.T) {
	t.Parallel()

	installer := &recordingInstaller{installed: ModuleInstallation{
		Instance: "sink:spools", Namespace: "com.github.aux.acme.spools",
		Kind: "sink", Version: "1.4.0", Runtime: "service", Enabled: false,
		Requests: []string{"permission observe"},
	}}

	rec := call(t, serverWithInstaller(t, installer, permittingGrants{&fakeGrantStore{}}).Handler(),
		http.MethodPost, "/api/v1/modules",
		`{"module":"github.com/acme/spools","version":"^1.4.0","from":"/srv/releases/spools-1.4.0.tar.gz"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var body ModuleInstallation

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "sink:spools", body.Instance)
	require.False(t, body.Enabled, "without approve the module lands switched off")
	require.Equal(t, []string{"permission observe"}, body.Requests)

	require.Len(t, installer.releases, 1)
	require.Equal(t, ModuleRelease{
		Module: "github.com/acme/spools", Version: "^1.4.0",
		From: "/srv/releases/spools-1.4.0.tar.gz",
	}, installer.releases[0])

	t.Logf("observed: %s", rec.Body.String())
}

// The settings arrive as the object the caller wrote, so the installer writes
// them into the declaration exactly as sent. Anything else is refused before
// the installer is called.
func TestTheInstallRequestCarriesTheModulesSettings(t *testing.T) {
	t.Parallel()

	installer := &recordingInstaller{installed: ModuleInstallation{Instance: "sink:spools"}}
	handler := serverWithInstaller(t, installer, permittingGrants{&fakeGrantStore{}}).Handler()

	rec := call(t, handler, http.MethodPost, "/api/v1/modules",
		`{"module":"github.com/acme/spools","version":"1.4.0","from":"/srv/spools",`+
			`"approve":true,"settings":{"url":"http://127.0.0.1:8123"}}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	require.Len(t, installer.releases, 1)
	require.JSONEq(t, `{"url":"http://127.0.0.1:8123"}`, string(installer.releases[0].Settings))

	rec = call(t, handler, http.MethodPost, "/api/v1/modules",
		`{"module":"github.com/acme/spools","version":"1.4.0","from":"/srv/spools","settings":"url"}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	require.Len(t, installer.releases, 1, "settings that are not an object reached the installer")
}

// The two facts a page acts on: whether the module is running now, and whether
// it is still waiting on a restart of this server. An install this server
// reconciled says running and asks for no restart, which is what stops a page
// telling somebody to restart a server that has already started the module.
func TestTheInstallAnswerSaysWhetherTheModuleIsRunningOrWaitingOnARestart(t *testing.T) {
	t.Parallel()

	for _, probe := range []struct {
		name      string
		installed ModuleInstallation
	}{
		{
			name: "running, so no restart",
			installed: ModuleInstallation{
				Instance: "sink:spools", Enabled: true, Running: true, Restart: false,
			},
		},
		{
			name: "written but not picked up, so a restart",
			installed: ModuleInstallation{
				Instance: "sink:spools", Enabled: true, Running: false, Restart: true,
			},
		},
		{
			name: "declared switched off, so neither",
			installed: ModuleInstallation{
				Instance: "sink:spools", Enabled: false, Running: false, Restart: false,
			},
		},
	} {
		t.Run(probe.name, func(t *testing.T) {
			t.Parallel()

			installer := &recordingInstaller{installed: probe.installed}

			rec := call(t, serverWithInstaller(t, installer, permittingGrants{&fakeGrantStore{}}).Handler(),
				http.MethodPost, "/api/v1/modules",
				`{"module":"github.com/acme/spools","version":"1.4.0","from":"/srv/releases/spools-1.4.0.tar.gz"}`)

			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

			var body ModuleInstallation

			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.Equal(t, probe.installed.Running, body.Running)
			require.Equal(t, probe.installed.Restart, body.Restart)
		})
	}
}

// Both flags are always on the wire. A page reading `restart` off an answer
// that omitted it would read false for a server that is waiting on one, so
// neither field is omitempty and the answer carries both.
func TestTheInstallAnswerAlwaysCarriesBothFlags(t *testing.T) {
	t.Parallel()

	installer := &recordingInstaller{installed: ModuleInstallation{Instance: "sink:spools"}}

	rec := call(t, serverWithInstaller(t, installer, permittingGrants{&fakeGrantStore{}}).Handler(),
		http.MethodPost, "/api/v1/modules",
		`{"module":"github.com/acme/spools","version":"1.4.0","from":"/srv/releases/spools-1.4.0.tar.gz"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var body map[string]any

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Contains(t, body, "running")
	require.Contains(t, body, "restart")
}

// The graph decides before anything is staged. A caller who administers no
// resource kind never makes this server fetch somebody else's bytes.
func TestACallerWhoAdministersNoKindStagesNothing(t *testing.T) {
	t.Parallel()

	installer := &recordingInstaller{}

	rec := call(t, serverWithInstaller(t, installer, &fakeGrantStore{}).Handler(),
		http.MethodPost, "/api/v1/modules",
		`{"module":"github.com/acme/spools","version":"1.4.0","from":"/srv/releases/spools"}`)

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Empty(t, installer.releases, "the release was staged for a caller the graph refused")
}

// oneKindGrants is a caller who administers the sink kind and nothing else.
// It holds no admin role on the organization, so the graph refuses its read of
// the organization's grants.
type oneKindGrants struct{ *fakeGrantStore }

func (oneKindGrants) Can(
	_ context.Context, _ authz.Subject, action authz.Action, object authz.Object,
) (bool, error) {
	return action == authz.ActionConfigureResource && object.String() == "resource_kind:sink", nil
}

func (oneKindGrants) Grants(context.Context, authz.Subject, authz.Object) ([]authz.Tuple, error) {
	return nil, fault.New(authz.CodeAccessDenied(), "the subject holds no admin role on this org")
}

// A caller who administers one kind may not name where a release comes from.
// Such a release runs as this server's own user and its manifest names the
// kind, so a URL or a path would let that caller run any program here. The
// refusal comes before the installer reads anything, so the answer is the same
// whether the path exists or not.
func TestACallerWhoAdministersOneKindMayNotNameAReleasesAddress(t *testing.T) {
	t.Parallel()

	for name, from := range map[string]string{
		"a URL":                       "https://releases.example/spools-1.0.0.tar.gz",
		"a path that exists":          "/etc",
		"a path that does not exist":  "/no/such/release",
		"a path with spaces around":   "  /srv/releases/spools  ",
		"a relative path on the host": "releases/spools",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			installer := &recordingInstaller{installed: ModuleInstallation{Instance: "sink:spools"}}
			handler := serverWithInstaller(t, installer, oneKindGrants{&fakeGrantStore{}}).Handler()

			body, err := json.Marshal(ModuleRelease{
				Module: "github.com/acme/spools", Version: "1.0.0", From: from,
				Sha256: strings.Repeat("a", 64), Approve: true,
			})
			require.NoError(t, err)

			rec := call(t, handler, http.MethodPost, "/api/v1/modules", string(body))

			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "ACCESS_DENIED")
			require.Contains(t, rec.Body.String(), "org:local")
			require.Empty(t, installer.releases, "the installer read a release a one-kind caller named")
		})
	}
}

// The same caller may still install what the marketplace offers, which an
// organization administrator chose. The installer then holds the manifest's
// kind to the caller's grants.
func TestACallerWhoAdministersOneKindMayInstallFromTheMarketplace(t *testing.T) {
	t.Parallel()

	installer := &recordingInstaller{installed: ModuleInstallation{Instance: "sink:spools"}}
	handler := serverWithInstaller(t, installer, oneKindGrants{&fakeGrantStore{}}).Handler()

	rec := call(t, handler, http.MethodPost, "/api/v1/modules",
		`{"module":"github.com/acme/spools","version":"1.0.0","approve":true}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Len(t, installer.releases, 1)
	require.Empty(t, installer.releases[0].From)
}

func TestUninstallingAModuleNamesTheNamespaceItWasAskedFor(t *testing.T) {
	t.Parallel()

	installer := &recordingInstaller{}

	rec := call(t, serverWithInstaller(t, installer, permittingGrants{&fakeGrantStore{}}).Handler(),
		http.MethodDelete, "/api/v1/modules/com.github.aux.acme.spools", "")

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.Equal(t, []string{"com.github.aux.acme.spools"}, installer.namespaces)
}

// A segment no module could ever have been given is an unrouted path, not a
// refusal that says a module was looked for.
func TestASegmentThatIsNoNamespaceIsNotFound(t *testing.T) {
	t.Parallel()

	installer := &recordingInstaller{}

	rec := call(t, serverWithInstaller(t, installer, permittingGrants{&fakeGrantStore{}}).Handler(),
		http.MethodDelete, "/api/v1/modules/spools", "")

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	require.Empty(t, installer.namespaces)
}

func TestTheIndexListsWhatCanBeInstalled(t *testing.T) {
	t.Parallel()

	installer := &recordingInstaller{offers: []ModuleOffer{
		{Module: "github.com/acme/spools", Name: "Spools", Version: "1.4.0",
			Description: "Counts what each print used.", Author: "acme",
			Source: "https://example.test/spools-1.4.0.tar.gz", Installed: true, Instance: "sink:spools"},
	}}

	rec := call(t, serverWithInstaller(t, installer, permittingGrants{&fakeGrantStore{}}).Handler(),
		http.MethodGet, "/api/v1/modules/index", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var body struct {
		Items []ModuleOffer `json:"items"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Items, 1)
	require.True(t, body.Items[0].Installed)
	require.Equal(t, "sink:spools", body.Items[0].Instance)

	// The three a card prints. Without them a marketplace row is a name and a
	// button and nothing that helps anybody choose.
	require.Equal(t, "1.4.0", body.Items[0].Version)
	require.Equal(t, "Counts what each print used.", body.Items[0].Description)
	require.Equal(t, "acme", body.Items[0].Author)
}

// An index that offers nothing answers an empty list, so a page shows an empty
// screen rather than handling an error to get to one.
func TestAnIndexWithNothingInItAnswersAnEmptyList(t *testing.T) {
	t.Parallel()

	rec := call(t, serverWithInstaller(t, &recordingInstaller{},
		permittingGrants{&fakeGrantStore{}}).Handler(),
		http.MethodGet, "/api/v1/modules/index", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"items":[]}`, rec.Body.String())
}

// A build with no installer publishes the same three paths and says what it
// does not do, because the document is a contract.
func TestABuildWithNoInstallerSaysSo(t *testing.T) {
	t.Parallel()

	cfg := exampleConfig(&fakeFleet{}, seededStore(), seededPresets())
	cfg.Grants = permittingGrants{&fakeGrantStore{}}

	server, err := New(cfg)
	require.NoError(t, err)

	for _, probe := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/v1/modules", `{"module":"github.com/acme/spools","version":"1.0.0","from":"/tmp/x"}`},
		{http.MethodDelete, "/api/v1/modules/com.github.aux.acme.spools", ""},
		{http.MethodGet, "/api/v1/modules/index", ""},
	} {
		rec := call(t, server.Handler(), probe.method, probe.path, probe.body)

		require.Equal(t, http.StatusNotImplemented, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "core.resource/INSTALL_NOT_CONFIGURED")
	}
}

func (i *recordingInstaller) Plan(context.Context, string, ModuleReleaseRef) (ModulePlan, error) {
	return ModulePlan{}, nil
}

func (i *recordingInstaller) Removal(context.Context, string, string) (ModuleRemoval, error) {
	return ModuleRemoval{}, nil
}

func (i *recordingInstaller) Why(context.Context, string, string) ([]ModuleReason, error) {
	return nil, nil
}

func (i *recordingInstaller) WhyNot(context.Context, string, string, string) ([]ModuleReason, error) {
	return nil, nil
}

func (i *recordingInstaller) Outdated(context.Context, string) ([]ModuleOutdated, error) {
	return nil, nil
}
