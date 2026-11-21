package app

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/app/builtin"
	"github.com/prusa3d/prusa-connect/internal/app/config"
	"github.com/prusa3d/prusa-connect/internal/app/resource"
	"github.com/prusa3d/prusa-connect/internal/app/resourcehost"
	"github.com/prusa3d/prusa-connect/internal/extension/registry"
	"github.com/prusa3d/prusa-connect/internal/kernel/condition"
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
)

// settle bounds every wait in this file. The default start ladder scatters the
// first attempt over two seconds so that a deployment does not dial everything
// at once, which is latency here and nothing else.
const settle = 20 * time.Second

// TestTheCoreServesAndStopsCleanly is the composition root end to end: the real
// configuration, the real store, the real supervision plane and the real HTTP
// surface, driven over a socket and then asked to stop.
func TestTheCoreServesAndStopsCleanly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "storage")

	running := start(t, dir, `
core:
  log:
    level: error
resources:
  storage:
    local:
      type: filesystem
      enabled: true
      required: true
      config:
        root: `+slashed(root)+`
`)

	require.Equal(t, http.StatusOK, running.get(t, "/api/v1/health").status)
	require.Equal(t, http.StatusOK, running.get(t, "/openapi.json").status)
	require.Equal(t, http.StatusOK, running.get(t, "/docs").status)

	// The declared instance reaches ready through the real supervisor, and the
	// storage root the type was asked for exists because it built one.
	require.Eventually(t, func() bool {
		return running.instanceState(t, "storage/local") == condition.StateReady().String()
	}, settle, 20*time.Millisecond, "the declared resource never reported ready")

	info, err := os.Stat(root)
	require.NoError(t, err)
	require.True(t, info.IsDir())

	// A write through the API lands in the overlay, never in the operator's file.
	created := running.put(t, "/api/v1/resources/storage/scratch",
		`{"type":"filesystem","enabled":true,"required":false,"config":{"root":"`+
			slashed(filepath.Join(dir, "scratch"))+`"}}`)
	require.Equal(t, http.StatusCreated, created.status, created.body)

	overlay, err := os.ReadFile(filepath.Join(dir, "config.d", "resources.json"))
	require.NoError(t, err)
	require.Contains(t, string(overlay), "scratch")

	original, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(original), "scratch", "the API never rewrites the operator's file")

	running.stopAndWait(t)
}

// TestABrokenConfigurationRefusesToStart is the whole point of validating
// before constructing: a rejected configuration binds no port and starts
// nothing, and the error names what is wrong with it.
//
// What is here is what nothing can be trusted after: the core block, and an
// address that will not bind. A resource instance that will not load is not
// here, because it fails alone and the server serves the rest of the file. That
// case is TestABrokenResourceInstanceFailsAloneAndTheServerServes.
func TestABrokenConfigurationRefusesToStart(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		config      string
		names       string
		useFileAddr bool
	}{
		"an unknown key in the core block": {
			config: lines("core:", "  api:", "    port: 8080"),
			names:  "port",
		},
		"an address with no port": {
			config:      lines("core:", "  api:", `    addr: "127.0.0.1"`),
			names:       "core.api.addr",
			useFileAddr: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			addr := freeAddr(t)
			write(t, filepath.Join(dir, "config.yaml"), tc.config)

			options := Options{ConfigPath: filepath.Join(dir, "config.yaml"), LogLevel: "error", LoopbackOnly: true}
			if !tc.useFileAddr {
				options.APIAddr = addr
			}

			require.ErrorContains(t, runBounded(t, options), tc.names)

			// Nothing was left listening, so the refusal was total.
			listener, listenErr := net.Listen("tcp", addr)
			require.NoError(t, listenErr, "a refused start must leave no listener behind")
			require.NoError(t, listener.Close())
		})
	}
}

// TestRequiredDecidesReadiness is an earlier design decision in both directions, over the real
// supervisor and a resource that genuinely cannot be constructed.
func TestRequiredDecidesReadiness(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		required bool
		ready    bool
	}{
		"an optional resource that will not start leaves the process ready": {required: false, ready: true},
		"a required resource that will not start makes it unready":          {required: true, ready: false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			// A regular file is not a directory, so the built-in storage type
			// refuses to construct against it every time.
			blocked := filepath.Join(dir, "not-a-directory")
			write(t, blocked, "")

			probes := wire(t, dir, lines("resources:", "  storage:", "    local:",
				"      type: filesystem",
				"      required: "+yes(tc.required),
				"      config:",
				"        root: "+slashed(blocked)))

			instance, err := ref.New(ref.KindStorage, "local")
			require.NoError(t, err)

			require.Eventually(t, func() bool {
				status, supervised := probes.host.Status(instance)

				return supervised && !status.Serving &&
					(status.State == condition.StateRetrying().String() ||
						status.State == condition.StateFailed().String())
			}, settle, 20*time.Millisecond, "the resource never reported a failure: %v", probes.states())

			current := probes.verdict()
			require.Equal(t, tc.ready, current.Ready, "%+v %v", current, probes.states())

			if tc.ready {
				require.Empty(t, current.Blocked)

				return
			}

			require.Equal(t, []string{"storage:local"}, current.Blocked)
		})
	}
}

// TestFlagsOverrideTheFile keeps the precedence one way round: a flag is what
// somebody typed just now, so it wins over what the file says.
func TestFlagsOverrideTheFile(t *testing.T) {
	t.Parallel()

	core := config.Default()
	core.API.Addr = ":8080"
	core.Log.Level = "info"

	overridden := Options{
		APIAddr:     "127.0.0.1:9999",
		PrinterAddr: "127.0.0.1:4443",
		LogLevel:    "debug",
		Overlay:     "/tmp/o.json",
	}.applyTo(core)

	require.Equal(t, "127.0.0.1:9999", overridden.API.Addr)
	require.Equal(t, "debug", overridden.Log.Level)
	require.Equal(t, "/tmp/o.json", overridden.Overlay)

	untouched := Options{}.applyTo(core)
	require.Equal(t, ":8080", untouched.API.Addr)
	require.Equal(t, "info", untouched.Log.Level)
}

// TestLoopbackOnlySwitchesDiscoveryOffWhateverTheFileSays keeps a test core or
// a demo off the local network even when its file asks for an announcement.
func TestLoopbackOnlySwitchesDiscoveryOffWhateverTheFileSays(t *testing.T) {
	t.Parallel()

	on := true
	core := config.Default()
	core.Discovery.Enabled = &on

	require.False(t, Options{LoopbackOnly: true}.applyTo(core).Discovery.On(),
		"a core that must stay on loopback still announces itself")
	require.True(t, Options{}.applyTo(core).Discovery.On(),
		"a core started without the option lost the announcement its file asks for")
}

// runBounded runs the core and requires it to have returned inside settle. A
// configuration that wrongly starts fails the test instead of holding the suite
// open until its own timeout.
func runBounded(t *testing.T, options Options) error {
	t.Helper()

	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	defer cancel()

	done := make(chan error, 1)

	go func() { done <- Run(ctx, options) }()

	select {
	case err := <-done:
		require.Error(t, err, "a broken configuration must be refused")

		return err
	case <-time.After(settle):
		cancel()
		<-done
		t.Fatal("a broken configuration started the process instead of refusing it")

		return nil
	}
}

// wire builds a store over a running supervision plane, which is what the
// composition root does and what readiness reads.
func wire(t *testing.T, dir, content string) readiness {
	t.Helper()

	write(t, filepath.Join(dir, "config.yaml"), content)

	types, err := registry.New()
	require.NoError(t, err)

	host, err := resourcehost.New(resourcehost.Config{Registry: types, HostKeys: builtin.HostKeys})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	stopped := make(chan error, 1)

	go func() { stopped <- host.Run(ctx) }()

	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-stopped)
	})

	store, err := resource.New(resource.Config{Paths: resource.Paths{
		File:    filepath.Join(dir, "config.yaml"),
		Overlay: filepath.Join(dir, "config.d", "resources.json"),
	}, Plane: host, Graph: newGraph(t)})
	require.NoError(t, err)

	require.NoError(t, store.Reload(t.Context()))

	return readiness{store: store, host: host}
}

// process is one core running on a real socket. The exit is waited for once and
// remembered, because both the test and its cleanup ask for it.
type process struct {
	addr   string
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
	err    error
}

// answer is one HTTP response, read whole.
type answer struct {
	status int
	body   string
	header http.Header
}

func start(t *testing.T, dir, content string) *process {
	t.Helper()

	write(t, filepath.Join(dir, "config.yaml"), content)
	recordOpenSetup(t, dir)

	running := launch(t, dir)
	waitUntilReady(t, running)

	return running
}

// startPending runs a core over dir and returns as soon as the process is up,
// without recording a first-run decision for it. It is the install nobody has
// set up, which serves the probes, the page and the first run and refuses every
// other path.
func startPending(t *testing.T, dir, content string) *process {
	t.Helper()

	write(t, filepath.Join(dir, "config.yaml"), content)

	running := launch(t, dir)
	waitUntilReady(t, running)

	return running
}

// launch runs a core over the configuration already written in dir. The core
// stays off the local network, whatever the file says about discovery.
func launch(t *testing.T, dir string) *process {
	t.Helper()

	return launchWith(t, dir, io.Discard)
}

// launchWith is launch with standard error kept, for a test that reads the
// setup code the core prints there.
func launchWith(t *testing.T, dir string, stderr io.Writer) *process {
	t.Helper()

	return launchOptions(t, Options{
		ConfigPath:   filepath.Join(dir, "config.yaml"),
		APIAddr:      freeAddr(t),
		Version:      "test",
		Stderr:       stderr,
		LoopbackOnly: true,
	})
}

// launchOptions runs a core with options. APIAddr names a free port, unless the
// test is about a start that finds the port held.
func launchOptions(t *testing.T, options Options) *process {
	t.Helper()

	addr := options.APIAddr
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	done := make(chan error, 1)

	go func() { done <- Run(ctx, options) }()

	running := &process{addr: addr, cancel: cancel, done: done}

	t.Cleanup(func() { _ = running.wait() })

	return running
}

// recordOpenSetup writes the first-run decision a server somebody has already
// set up carries.
//
// A core that has recorded none serves the two probes, the page and the first
// run, and refuses every other path. These tests are about what a server that
// has been set up answers, so the decision is written the way an operator's own
// call writes it, before the process starts. That call records both answers,
// and the one about printers is "let every printer in": these cases drive
// simulated printers with nobody at a keyboard to approve a code, and a server
// that has recorded no choice asks first. A configuration this loader cannot
// read is left alone: that core will not start either, and the test is about
// why it did not.
func recordOpenSetup(t *testing.T, dir string) {
	t.Helper()

	core, err := config.Load(filepath.Join(dir, "config.yaml"))
	if err != nil {
		return
	}

	document := map[string]any{"version": 1, "instances": []any{}}

	if existing, readErr := os.ReadFile(core.Overlay); readErr == nil {
		require.NoError(t, json.Unmarshal(existing, &document))
	}

	document["auth"] = map[string]any{"mode": resource.SetupOpen}
	document["admission"] = map[string]any{"mode": resource.AdmissionOpen}

	encoded, err := json.MarshalIndent(document, "", "  ")
	require.NoError(t, err)

	write(t, core.Overlay, string(encoded)+"\n")
}

// waitUntilReady polls the health route until the core answers, and gives up as
// soon as Run has returned instead.
//
// A core that could not take its port returns at once. A wait that only polls
// the socket spends the whole budget on a process that is already gone, and
// then reports that nothing answered, which names the symptom and throws the
// cause away. freeAddr hands out a port it has already closed, so losing that
// port to something else is a cause worth naming rather than reading as a slow
// start.
func waitUntilReady(t *testing.T, running *process) {
	t.Helper()

	deadline := time.Now().Add(settle)

	for {
		if running.try(t, http.MethodGet, "/api/v1/health", "") != nil {
			return
		}

		if over, err := running.exited(); over {
			t.Fatalf("the core on %s stopped before it answered: %v", running.addr, err)
		}

		if !time.Now().Before(deadline) {
			t.Fatalf("the core never answered on %s within %s", running.addr, settle)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

// stopAndWait cancels the context the way a signal does and proves the process
// came back on its own, cleanly.
func (p *process) stopAndWait(t *testing.T) {
	t.Helper()

	require.NoError(t, p.wait(), "a cancelled core must stop cleanly")

	_, err := net.DialTimeout("tcp", p.addr, time.Second)
	require.Error(t, err, "the listener must be closed once the core has stopped")
}

// wait asks the core to stop and returns what Run reported.
func (p *process) wait() error {
	p.once.Do(func() {
		p.cancel()
		p.err = <-p.done
	})

	return p.err
}

// exited reports whether Run has already returned, and what it returned.
//
// The answer is remembered the way wait remembers it, so a read here does not
// take the value the cleanup is waiting for.
func (p *process) exited() (over bool, err error) {
	select {
	case returned := <-p.done:
		p.once.Do(func() {
			p.cancel()
			p.err = returned
		})

		return true, p.err
	default:
		return false, nil
	}
}

func (p *process) get(t *testing.T, path string) answer {
	t.Helper()

	return p.call(t, http.MethodGet, path, "")
}

func (p *process) put(t *testing.T, path, body string) answer {
	t.Helper()

	return p.call(t, http.MethodPut, path, body)
}

// instanceState reads one instance's state through the API, which is the same
// field a client reads.
func (p *process) instanceState(t *testing.T, path string) string {
	t.Helper()

	read := p.get(t, "/api/v1/resources/"+path)
	if read.status != http.StatusOK {
		return ""
	}

	var instance httpapi.ResourceInstance
	require.NoError(t, json.Unmarshal([]byte(read.body), &instance))

	return instance.Status.State
}

func (p *process) call(t *testing.T, method, path, body string) answer {
	t.Helper()

	got := p.try(t, method, path, body)
	require.NotNil(t, got, "%s %s got no answer", method, path)

	return *got
}

func (p *process) try(t *testing.T, method, path, body string) *answer {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	request, err := http.NewRequestWithContext(t.Context(), method, "http://"+p.addr+path, reader)
	require.NoError(t, err)

	if body != "" {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("If-None-Match", "*")
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil
	}

	defer func() { _ = response.Body.Close() }()

	read, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	return &answer{status: response.StatusCode, body: string(read), header: response.Header}
}

// handedOut holds every address freeAddr has returned in this process. The
// system may offer a closed port again at once, to the same test asking for
// its second listener or to a test running beside it.
//
//nolint:gochecknoglobals // one set for the whole test binary is the point.
var handedOut = struct {
	mu    sync.Mutex
	addrs map[string]bool
}{addrs: map[string]bool{}}

// freeAddr returns an address nothing is listening on and that no other call in
// this process was given. The window between the close and the bind is
// unavoidable without handing a listener down through Run, which is a seam the
// production path has no use for.
func freeAddr(t *testing.T) string {
	t.Helper()

	handedOut.mu.Lock()
	defer handedOut.mu.Unlock()

	for {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		addr := listener.Addr().String()
		require.NoError(t, listener.Close())

		if !handedOut.addrs[addr] {
			handedOut.addrs[addr] = true

			return addr
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// lines joins configuration lines, which keeps a YAML fixture readable beside
// the case that uses it.
func lines(parts ...string) string { return strings.Join(parts, "\n") + "\n" }

// slashed renders a path so a Windows separator survives both YAML and JSON.
func slashed(path string) string { return filepath.ToSlash(path) }

func yes(value bool) string {
	if value {
		return "true"
	}

	return "false"
}
