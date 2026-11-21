package app

import (
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/app/config/install"
	"github.com/prusa3d/prusa-connect/internal/app/resource"
	"github.com/prusa3d/prusa-connect/internal/extension/wirehost"
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
)

// The module these cases declare: one bundle with three printer tabs, two of
// which ask for a cross-origin isolated page. The console tab is limited to
// the printer the server runs on, and no printer here is resident, yet its
// address is isolated.
const (
	consoleNamespace = "com.github.aux.acme.console"
	consoleTabPage   = "/printers/one/" + consoleNamespace + ".console"
	consoleLogPage   = "/printers/one/" + consoleNamespace + ".log"
	consolePanelPage = "/printers/one/" + consoleNamespace + ".panel"
)

// aConsoleBundle writes the module's bundle under dir and returns its folder
// and the digest a declaration pins it with.
func aConsoleBundle(t *testing.T, dir string) (string, string) {
	t.Helper()

	bundle := filepath.Join(dir, "console")
	write(t, filepath.Join(bundle, "module.yaml"), lines(
		"name: console", "kind: asset", "license: MIT", "version: 1.0.0", "ui:", "  entry: ui/index.js",
		`  library: ">=0.1.0 <1.0.0"`, "  contributes:", "    device_tab:",
		"      - id: console", "        title: Device page", "        cross_origin_isolated: true",
		"        devices:", "          resident: true",
		"      - id: log", "        title: Log",
		"      - id: panel", "        title: Panel", "        cross_origin_isolated: true"))
	write(t, filepath.Join(bundle, "ui", "index.js"), "export default () => {}\n")

	digest, err := wirehost.DigestDir(bundle)
	require.NoError(t, err)

	return bundle, string(digest)
}

// TestATabThatAsksForIsolationIsolatesItsOwnPageAlone reads the module's
// declaration from a configuration file through Run to the answers of the app.
// It holds the composition root where it hands the module registry to the
// app's mount, the registry lookup, and the code that sets the headers. The app
// is the fixture bundle (Options.appMount).
func TestATabThatAsksForIsolationIsolatesItsOwnPageAlone(t *testing.T) {
	t.Parallel()

	running := launchConsole(t)

	// The bundle is opened by the instance's own start, a moment after the
	// server is ready.
	require.Eventually(t, func() bool {
		served := running.call(t, http.MethodGet, consoleTabPage, "")

		return served.status == http.StatusOK && served.header.Get("Cross-Origin-Opener-Policy") == "same-origin"
	}, settle, 20*time.Millisecond, "the tab's page never came isolated")

	page := running.call(t, http.MethodGet, consoleTabPage, "")
	require.Equal(t, "require-corp", page.header.Get("Cross-Origin-Embedder-Policy"))

	for _, path := range []string{
		"/", "/printers/one", "/printers/one/status", consoleLogPage, "/docs",
		"/printers/one/com.github.aux.acme.other.console", "/printers/one/com.github.aux.other.console",
		"/printers/one/com.github.aux.acme.console", // the parent namespace, with the tab's id
	} {
		served := running.call(t, http.MethodGet, path, "")

		require.Equal(t, http.StatusOK, served.status, path+": "+served.body)
		require.Empty(t, served.header.Values("Cross-Origin-Opener-Policy"), path)
		require.Empty(t, served.header.Values("Cross-Origin-Embedder-Policy"), path)
	}
}

// TestAHiddenTabIsNotIsolated holds the registry lookup to the tabs nobody
// hides. The operator hides one tab, so its page is not isolated, while the
// module's other tab that asks for isolation shows the module is running.
func TestAHiddenTabIsNotIsolated(t *testing.T) {
	t.Parallel()

	running := launchConsole(t, "      hidden_contributions: [device_tab/console]")

	require.Eventually(t, func() bool {
		served := running.call(t, http.MethodGet, consolePanelPage, "")

		return served.header.Get("Cross-Origin-Opener-Policy") == "same-origin"
	}, settle, 20*time.Millisecond, "the module's tab that is not hidden never came isolated")

	page := running.call(t, http.MethodGet, consoleTabPage, "")
	require.Equal(t, http.StatusOK, page.status, page.body)
	require.Empty(t, page.header.Values("Cross-Origin-Opener-Policy"), "the page of a hidden tab came isolated")
	require.Empty(t, page.header.Values("Cross-Origin-Embedder-Policy"))
}

// launchConsole starts a server whose file declares the module, with extra
// lines added to its declaration.
func launchConsole(t *testing.T, extra ...string) *process {
	t.Helper()

	dir := t.TempDir()
	bundle, digest := aConsoleBundle(t, dir)
	declaration := append([]string{
		"core:", "  log:", "    level: error",
		"resources:", "  asset:", "    console:",
		"      type: " + consoleNamespace + "/console", "      asset:",
		"        path: '" + filepath.ToSlash(bundle) + "'", "        checksum: " + digest,
	}, extra...)
	write(t, filepath.Join(dir, "config.yaml"), lines(declaration...))
	recordOpenSetup(t, dir)

	running := launchOptions(t, Options{
		ConfigPath:   filepath.Join(dir, "config.yaml"),
		APIAddr:      freeAddr(t),
		Version:      "test",
		Stderr:       io.Discard,
		LoopbackOnly: true,
		appMount:     testAppMount,
	})
	waitUntilReady(t, running)

	return running
}

// TestTheLookupIsolatesADeclaredTabThatAsksAndIsNotHidden is tabIsolation over
// each state one declared instance can be in. The instance the host runs as
// the module of the namespace settles the answer, and every other instance is
// skipped.
func TestTheLookupIsolatesADeclaredTabThatAsksAndIsNotHidden(t *testing.T) {
	t.Parallel()

	instance, err := ref.New(ref.KindAsset, "console")
	require.NoError(t, err)

	declared := func(change func(*resource.Spec)) resource.Declaration {
		spec := resource.Spec{Type: consoleNamespace + "/console", Enabled: true, Runtime: install.RuntimeAsset}
		if change != nil {
			change(&spec)
		}

		return resource.Declaration{Ref: instance, Spec: spec}
	}

	tabs := httpapi.ResourceUI{Contributes: httpapi.ResourceContributes{DeviceTab: []httpapi.ResourceSection{
		{
			ID: "console", Title: "Device page", CrossOriginIsolated: true,
			Devices: &httpapi.ResourceDeviceSelector{Resident: true},
		},
		{ID: "log", Title: "Log"},
	}}}
	running := func(ref.Ref) (httpapi.ResourceUI, bool) { return tabs, true }
	stopped := func(ref.Ref) (httpapi.ResourceUI, bool) { return httpapi.ResourceUI{}, false }
	hiding := func(ref.Ref) (httpapi.ResourceUI, bool) {
		surface := tabs
		surface.Hidden = []string{"device_tab/console"}

		return surface, true
	}

	cases := []struct {
		name            string
		declared        resource.Declaration
		ui              func(ref.Ref) (httpapi.ResourceUI, bool)
		namespace, tab  string
		owner, isolated bool
	}{
		{"the tab, on every printer", declared(nil), running, consoleNamespace, "console", true, true},
		{"a tab that does not ask", declared(nil), running, consoleNamespace, "log", true, false},
		{"a tab the module does not declare", declared(nil), running, consoleNamespace, "other", true, false},
		{
			"another module's tab of the same id", declared(nil), running,
			"com.github.aux.acme.other", "console", false, false,
		},
		{
			"switched off", declared(func(s *resource.Spec) { s.Enabled = false }), running,
			consoleNamespace, "console", false, false,
		},
		{"not running", declared(nil), stopped, consoleNamespace, "console", false, false},
		{
			"hidden by the operator",
			declared(func(s *resource.Spec) { s.HiddenContributions = []string{"device_tab/console"} }),
			running, consoleNamespace, "console", true, false,
		},
		{"hidden by the module", declared(nil), hiding, consoleNamespace, "console", true, false},
		{
			"not a module", declared(func(s *resource.Spec) { s.Runtime = install.RuntimeBuiltin }), running,
			consoleNamespace, "console", false, false,
		},
	}

	for _, tc := range cases {
		owner, isolated := tabIsolation(tc.declared, tc.ui, tc.namespace, tc.tab)
		require.Equalf(t, tc.owner, owner, "%s: owner", tc.name)
		require.Equalf(t, tc.isolated, isolated, "%s: isolated", tc.name)
	}

	// A switched-off copy declared first is skipped, and the running one
	// answers; one that is not the namespace's module is skipped too.
	off := declared(func(s *resource.Spec) { s.Enabled = false })
	other := declared(func(s *resource.Spec) { s.Type = "com.github.aux.acme.other/console" })
	require.True(t,
		isolatedAmong([]resource.Declaration{off, other, declared(nil)}, running, consoleNamespace, "console"))
	require.False(t, isolatedAmong([]resource.Declaration{off, other}, running, consoleNamespace, "console"))
	require.False(t, isolatedAmong(nil, running, consoleNamespace, "console"))

	// An enabled copy declared first that the host refused, because the
	// second copy holds the namespace, is skipped too, and the second answers.
	second, err := ref.New(ref.KindAsset, "second-console")
	require.NoError(t, err)

	refused := declared(nil)
	held := declared(nil)
	held.Ref = second
	runsSecond := func(asked ref.Ref) (httpapi.ResourceUI, bool) {
		if asked == second {
			return tabs, true
		}

		return httpapi.ResourceUI{}, false
	}
	require.True(t, isolatedAmong([]resource.Declaration{refused, held}, runsSecond, consoleNamespace, "console"))
}
