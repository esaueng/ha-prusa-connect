package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
)

// rootMount is the prefix the cases below mount the planner at, the way an
// operator would mount a GraphQL module.
const rootMount = "/graphql"

// theRootedFarm is theDescribedFarm with the operator's root mount on it: the
// same module, the same instance, the same graph, and its document rebased
// under the prefix the way the plane rebases it.
func theRootedFarm(t *testing.T, who string, mounts ...Mount) (*Server, *fakeModuleAccess) {
	t.Helper()

	instance, err := ref.Parse(moduleInstance)
	require.NoError(t, err)

	fragment, err := routesapi.ParseFragment([]byte(farmFragment))
	require.NoError(t, err)

	rebased, err := fragment.Rebase(rootMount)
	require.NoError(t, err)

	access := &fakeModuleAccess{
		who:      "user:operator",
		instance: instance,
		actions:  []authz.Action{authz.ActionViewResource, authz.ActionOperateResource},
	}

	plane := fakeModulePlane{
		namespace: moduleNamespace, instance: instance, handler: moduleEcho(), openapi: rebased, mount: rootMount,
	}

	cfg := mountConfig(mounts)
	cfg.Principals = fixedPrincipal(who)
	cfg.Modules = plane
	cfg.ModuleAccess = access

	server, err := New(cfg)
	require.NoError(t, err)

	return server, access
}

// TestARootMountIsHeldToTheGrammarAndTheReservedPaths is what an operator's
// service.mount is refused on before anything is mounted: the shape of the
// prefix, the paths this server serves, and the paths a deployment mounted
// beside it.
func TestARootMountIsHeldToTheGrammarAndTheReservedPaths(t *testing.T) {
	t.Parallel()

	beside := []string{"/p/ws", "/p/*", "/*"}

	accepted := []string{"/graphql", "/app/api", "/compat-v1", "/a.b_c~d"}
	for _, prefix := range accepted {
		require.NoErrorf(t, ValidateModuleMount(prefix, beside), "%s is a prefix a module may hold", prefix)
	}

	invalid := map[string]string{
		"not absolute":       "graphql",
		"the origin root":    "/",
		"a trailing slash":   "/graphql/",
		"an empty segment":   "/graph//ql",
		"a dot segment":      "/graphql/../api",
		"a space":            "/graph ql",
		"a query character":  "/graphql?x",
		"a percent escape":   "/graph%20ql",
		"a path parameter":   "/{namespace}",
		"a wildcard segment": "/graphql/*",
	}
	for name, prefix := range invalid {
		require.ErrorIsf(t, ValidateModuleMount(prefix, beside), ErrModuleMountInvalid,
			"%s (%q) is not a root prefix", name, prefix)
	}

	reserved := map[string]string{
		"the api root":              "/api",
		"under the api":             "/api/v1/graphql",
		"over the api":              "/api/v2",
		"the document":              "/openapi.json",
		"under the document":        "/openapi.json/extra",
		"the yaml document":         "/openapi.yaml",
		"the reference":             "/docs",
		"under the reference":       "/docs/graphql",
		"the metrics":               "/metrics",
		"the developer page":        "/dev",
		"under the developer page":  "/dev/tools",
		"a frozen device wire":      "/p/ws",
		"under a connector prefix":  "/p/graphql",
		"over the connector prefix": "/p",
	}
	for name, prefix := range reserved {
		err := ValidateModuleMount(prefix, beside)
		require.ErrorIsf(t, err, ErrModuleMountReserved, "%s (%q) is this server's", name, prefix)
		require.Contains(t, err.Error(), prefix, "the refusal does not name the prefix")
	}
}

// TestEveryRouteTheServerServesIsRefusedAsAModuleMount is the gate that keeps
// the reserved list honest: it is derived from the constants the routes are
// registered with, and here the built router says whether it covers every
// route. A route registered at a new root without a matching refusal fails
// New, and this case shows the refusal on a router with such a route.
func TestEveryRouteTheServerServesIsRefusedAsAModuleMount(t *testing.T) {
	t.Parallel()

	server, _ := theFarm(t)

	var routes int

	require.NoError(t, chi.Walk(server.mux,
		func(_, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			routes++

			require.ErrorIsf(t, ValidateModuleMount(rootOf(route), nil), ErrModuleMountReserved,
				"%s is served and a module could be mounted over its root %s", route, rootOf(route))

			return nil
		}))
	require.Positive(t, routes, "the walk enumerated no routes")

	stray := chi.NewMux()
	stray.Get("/api/v1/health", func(http.ResponseWriter, *http.Request) {})
	stray.Get("/graphql", func(http.ResponseWriter, *http.Request) {})

	err := verifyRootsReserved(stray, nil)
	require.ErrorIs(t, err, ErrRootNotReserved)
	require.Contains(t, err.Error(), "/graphql")
	require.NotContains(t, err.Error(), "/api/v1/health", "a route under the api root was reported")

	require.NoError(t, verifyRootsReserved(stray, []string{"/graphql"}),
		"a pattern the deployment declared as a mount was reported")
}

// TestAModuleMountedAtARootPrefixIsReachedThereWithThePrincipal is ADR-20260827-wmv
// rule 12's second sentence on the wire: the operator names the mount, the
// module is reached there with the prefix taken off, the caller was resolved
// and authorized by this server, and the namespace path no longer answers.
func TestAModuleMountedAtARootPrefixIsReachedThereWithThePrincipal(t *testing.T) {
	t.Parallel()

	server, access := theRootedFarm(t, "user:operator")

	answered := ask(t, server, http.MethodGet, rootMount+"/spools?colour=black", "")
	require.Equal(t, http.StatusOK, answered.Code, answered.Body.String())

	var echoed map[string]string
	require.NoError(t, json.Unmarshal(answered.Body.Bytes(), &echoed))

	require.Equal(t, "/spools", echoed["path"], "the module was handed the prefix, so it cannot write its own routes")
	require.Equal(t, "colour=black", echoed["query"], "the query was dropped on the way in")
	require.Equal(t, "user:operator", echoed["principal"], "the module was not told who the host authorized")
	require.Equal(t, []authz.Action{authz.ActionViewResource}, access.asked,
		"a read did not ask the graph for view_resource on the module's instance")

	// The prefix alone is the module's root.
	bare := ask(t, server, http.MethodGet, rootMount, "")
	require.Equal(t, http.StatusOK, bare.Code, bare.Body.String())
	require.NoError(t, json.Unmarshal(bare.Body.Bytes(), &echoed))
	require.Equal(t, "/", echoed["path"])

	written := ask(t, server, http.MethodPost, rootMount+"/spools", `{}`)
	require.Equal(t, http.StatusOK, written.Code, written.Body.String())
	require.Equal(t, authz.ActionOperateResource, access.asked[len(access.asked)-1],
		"a write did not ask the graph for operate_resource")

	// One address, not two. The namespace path is unrouted for a module the
	// operator moved to a root prefix, so a client cannot reach it both ways.
	moved := ask(t, server, http.MethodGet, ModuleMount(moduleNamespace)+"/spools", "")
	require.Equal(t, http.StatusNotFound, moved.Code, moved.Body.String())

	// A prefix that merely starts with the mount is not under it.
	beside := ask(t, server, http.MethodGet, rootMount+"ql/spools", "")
	require.Equal(t, http.StatusNotFound, beside.Code, beside.Body.String())

	// And a traversal out of the mount is refused before the module sees it.
	escaped := ask(t, server, http.MethodGet, rootMount+"/../openapi.json", "")
	require.Equal(t, http.StatusNotFound, escaped.Code, escaped.Body.String())
}

// TestARootMountedDocumentIsServedAtBothAddresses is ADR-20260909-c7x rule 9 for a
// module at a root prefix: the same document, rebased under the prefix, at
// <prefix>/openapi.json and at the modules path, so a generator finds it at
// one address whatever the operator chose.
func TestARootMountedDocumentIsServedAtBothAddresses(t *testing.T) {
	t.Parallel()

	server, access := theRootedFarm(t, "user:operator")

	for _, address := range []string{ModuleRootOpenAPIPath(rootMount), ModuleOpenAPIPath(moduleNamespace)} {
		answered := ask(t, server, http.MethodGet, address, "")
		require.Equal(t, http.StatusOK, answered.Code, "%s: %s", address, answered.Body.String())
		require.Contains(t, answered.Header().Get("Content-Type"), "application/json")

		var document struct {
			Paths map[string]json.RawMessage `json:"paths"`
		}

		require.NoError(t, json.Unmarshal(answered.Body.Bytes(), &document))
		require.Contains(t, document.Paths, rootMount+"/spools",
			"%s does not carry the route under the prefix", address)
		require.NotContains(t, document.Paths, ModuleMount(moduleNamespace)+"/spools",
			"%s was rebased under the namespace, where the module is not reached", address)
	}

	require.Equal(t, []authz.Action{authz.ActionViewResource, authz.ActionViewResource}, access.asked,
		"reading the document did not take view_resource on the module's instance at both addresses")

	// The document is the server's, so it takes a GET and nothing else.
	posted := ask(t, server, http.MethodPost, ModuleRootOpenAPIPath(rootMount), `{}`)
	require.Equal(t, http.StatusMethodNotAllowed, posted.Code, posted.Body.String())
}

// TestARootMountIsRefusedForACallerWhoHoldsNothing is the refusal beside the
// permit, on the same prefix and the same module. The module must not run at
// all, which is what a module in its own process needs to be true.
func TestARootMountIsRefusedForACallerWhoHoldsNothing(t *testing.T) {
	t.Parallel()

	server, access := theRootedFarm(t, "user:stranger")

	for _, target := range []string{rootMount + "/spools", rootMount, ModuleRootOpenAPIPath(rootMount)} {
		answered := ask(t, server, http.MethodGet, target, "")
		require.Equal(t, http.StatusForbidden, answered.Code, "%s: %s", target, answered.Body.String())
		require.Contains(t, answered.Body.String(), "ACCESS_DENIED")
		require.NotContains(t, answered.Body.String(), `"path"`, "the module answered a caller the graph refused")
	}

	require.Len(t, access.asked, 3, "a refused request did not go through the graph")
}

// greedyRootPlane claims every path it is asked about, which is what a plane
// would do if it trusted a module's word for its mount. The router must not
// hand it a path of this server's own whatever it says.
type greedyRootPlane struct{ instance ref.Ref }

func (p greedyRootPlane) Module(string) (ModuleSurface, bool) { return ModuleSurface{}, false }

func (p greedyRootPlane) Root(path string) (ModuleSurface, bool) {
	return ModuleSurface{Instance: p.instance, Handler: moduleEcho(), Mount: rootOf(path)}, true
}

// TestARootMountNeverShadowsThisServersOwnPaths holds the order: an operation,
// the document, the reference and a mount beside the API all answer as
// themselves, and only a path none of them routed reaches the plane.
func TestARootMountNeverShadowsThisServersOwnPaths(t *testing.T) {
	t.Parallel()

	instance, err := ref.Parse(moduleInstance)
	require.NoError(t, err)

	access := &fakeModuleAccess{
		who: "user:operator", instance: instance, actions: []authz.Action{authz.ActionViewResource},
	}

	cfg := mountConfig([]Mount{{Pattern: "/p/ws", Handler: mountedText("connector"), Reason: "a frozen device wire"}})
	cfg.Modules = greedyRootPlane{instance: instance}
	cfg.ModuleAccess = access

	server, err := New(cfg)
	require.NoError(t, err)

	answers := map[string]string{
		healthPath:   `"status":"ok"`,
		specJSONPath: `"openapi"`,
		"/p/ws":      "connector",
	}

	// The printer build leaves the reference page out and answers 404 there.
	if page, ok := ReferencePage(); ok {
		answers[page] = "<"
	}

	for path, want := range answers {
		answered := ask(t, server, http.MethodGet, path, "")
		require.Equal(t, http.StatusOK, answered.Code, "%s: %s", path, answered.Body.String())
		require.Contains(t, answered.Body.String(), want, "%s was answered by the module", path)
	}

	// An unrouted path under this server's own roots is the router's 404 and
	// never the plane's, even though the plane would claim it.
	for _, path := range []string{"/api/v1/nothing-here", specJSONPath + "/extra", "/docs/nothing"} {
		answered := ask(t, server, http.MethodGet, path, "")
		require.Equal(t, http.StatusNotFound, answered.Code, "%s: %s", path, answered.Body.String())
		require.Contains(t, answered.Body.String(), "ROUTE_NOT_FOUND", "%s reached the module", path)
	}

	require.Empty(t, access.asked, "a path of this server's own reached the graph as a module route")

	// A path nothing routed does reach the plane, through the same permission.
	answered := ask(t, server, http.MethodGet, "/graphql/spools", "")
	require.Equal(t, http.StatusOK, answered.Code, answered.Body.String())
	require.Equal(t, []authz.Action{authz.ActionViewResource}, access.asked)
}

// TestARootMountStandsInFrontOfTheFrontend is the deployment that ships: a
// single-page frontend catching every address, and a module at /graphql. The
// module answers under its prefix, the frontend answers everything else, and a
// method the frontend cannot use is still refused.
func TestARootMountStandsInFrontOfTheFrontend(t *testing.T) {
	t.Parallel()

	server, _ := theRootedFarm(t, "user:operator",
		Mount{Pattern: "/*", Handler: mountedText("frontend"), Reason: "the frontend at the root"})

	answered := ask(t, server, http.MethodGet, rootMount+"/spools", "")
	require.Equal(t, http.StatusOK, answered.Code, answered.Body.String())
	require.Contains(t, answered.Body.String(), `"path":"/spools"`, "the frontend answered the module's address")

	for _, path := range []string{"/", "/printers", "/graphqlx"} {
		page := ask(t, server, http.MethodGet, path, "")
		require.Equal(t, http.StatusOK, page.Code, path)
		require.Equal(t, "frontend", page.Body.String(), "%s did not reach the frontend", path)
	}

	posted := ask(t, server, http.MethodPost, "/printers", `{}`)
	require.Equal(t, http.StatusNotFound, posted.Code, "a POST at the frontend was answered with a page")
}

// TestAPrefixOfAModuleTheOperatorSwitchedOffAnswersModulesDisabled holds
// ADR-20261019-h76 rule 10 at a root prefix: the answer names the key and is not
// retried, because nothing changes until the operator restarts the server.
func TestAPrefixOfAModuleTheOperatorSwitchedOffAnswersModulesDisabled(t *testing.T) {
	t.Parallel()

	instance, err := ref.Parse(moduleInstance)
	require.NoError(t, err)

	cfg := mountConfig(nil)
	cfg.Principals = fixedPrincipal("user:operator")
	cfg.Modules = fakeModulePlane{
		namespace:   moduleNamespace,
		instance:    instance,
		mount:       rootMount,
		unavailable: "the module is installed",
		modulesOff:  true,
	}
	cfg.ModuleAccess = &fakeModuleAccess{who: "user:operator", instance: instance}

	server, err := New(cfg)
	require.NoError(t, err)

	answered := ask(t, server, http.MethodGet, rootMount+"/spools", "")
	require.Equal(t, http.StatusConflict, answered.Code, answered.Body.String())
	require.Contains(t, answered.Body.String(), "MODULES_DISABLED")
	require.Contains(t, answered.Body.String(), `"retryable":false`)
}

// TestSafeModeServesNoBundleOfADeclaredAssetModule holds the safe-mode answer
// at a held prefix: a surface with no handler under safe mode says SAFE_MODE,
// and not the code of the operator's key.
func TestSafeModeServesNoBundleOfADeclaredAssetModule(t *testing.T) {
	t.Parallel()

	instance, err := ref.Parse(moduleInstance)
	require.NoError(t, err)

	cfg := mountConfig(nil)
	cfg.Principals = fixedPrincipal("user:operator")
	cfg.Modules = fakeModulePlane{
		namespace:  moduleNamespace,
		instance:   instance,
		mount:      rootMount,
		modulesOff: true,
		safeMode:   true,
	}
	cfg.ModuleAccess = &fakeModuleAccess{who: "user:operator", instance: instance}

	server, err := New(cfg)
	require.NoError(t, err)

	answered := ask(t, server, http.MethodGet, rootMount+"/spools", "")
	require.Equal(t, http.StatusConflict, answered.Code, answered.Body.String())
	require.Contains(t, answered.Body.String(), "core.resource/SAFE_MODE")
	require.Contains(t, answered.Body.String(), `"retryable":false`)
}

// TestAClaimedRootPrefixWithNothingBehindItAnswers503 is earlier research
// one addition: a module that is declared, switched on and not serving
// answers a problem document naming it, not the 404 an unrouted path answers.
//
// It matters when a compatibility layer is switched on before its module is
// installed. /graphql is then a real address of this deployment with nothing
// behind it, and an integration has to be able to tell "not serving here" from
// "wrong server".
func TestAClaimedRootPrefixWithNothingBehindItAnswers503(t *testing.T) {
	t.Parallel()

	instance, err := ref.Parse(moduleInstance)
	require.NoError(t, err)

	cfg := mountConfig(nil)
	cfg.Principals = fixedPrincipal("user:operator")
	cfg.Modules = fakeModulePlane{
		namespace:   moduleNamespace,
		instance:    instance,
		mount:       rootMount,
		unavailable: "the module is switched on and is not serving",
	}
	cfg.ModuleAccess = &fakeModuleAccess{
		who:      "user:operator",
		instance: instance,
		actions:  []authz.Action{authz.ActionViewResource, authz.ActionOperateResource},
	}

	server, err := New(cfg)
	require.NoError(t, err)

	for _, path := range []string{rootMount, rootMount + "/spools"} {
		answered := ask(t, server, http.MethodGet, path, "")

		require.Equalf(t, http.StatusServiceUnavailable, answered.Code, "%s: %s", path, answered.Body.String())
		require.Equal(t, problemMediaType, answered.Header().Get("Content-Type"))

		body := answered.Body.String()
		require.Contains(t, body, "MODULE_NOT_SERVING")
		require.Containsf(t, body, moduleInstance, "%s did not name the instance", path)
		require.Containsf(t, body, rootMount, "%s did not name the prefix", path)
		require.Contains(t, body, "is not serving")
	}

	// A path the module never claimed is still an unrouted path. The refusal
	// belongs to the prefix, not to the whole origin.
	beside := ask(t, server, http.MethodGet, rootMount+"ql/spools", "")
	require.Equal(t, http.StatusNotFound, beside.Code, beside.Body.String())
}

// TestAClaimedRootPrefixNamesNothingToACallerWithNoIdentity holds the order at
// a root prefix. A caller with no identity gets a 401, and the answer names
// neither the instance nor why the module is not serving.
func TestAClaimedRootPrefixNamesNothingToACallerWithNoIdentity(t *testing.T) {
	t.Parallel()

	instance, err := ref.Parse(moduleInstance)
	require.NoError(t, err)

	for name, plane := range map[string]fakeModulePlane{
		"not serving": {
			namespace: moduleNamespace, instance: instance, mount: rootMount,
			unavailable: "the module is switched on and is not serving",
		},
		"switched off by the key": {
			namespace: moduleNamespace, instance: instance, mount: rootMount, modulesOff: true,
		},
		"serving": {
			namespace: moduleNamespace, instance: instance, mount: rootMount, handler: moduleEcho(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := mountConfig(nil)
			cfg.Principals = fixedPrincipal("")
			cfg.Modules = plane
			cfg.ModuleAccess = &fakeModuleAccess{who: "user:operator", instance: instance}

			server, newErr := New(cfg)
			require.NoError(t, newErr)

			for _, path := range []string{rootMount, rootMount + "/spools", ModuleRootOpenAPIPath(rootMount)} {
				answered := ask(t, server, http.MethodGet, path, "")

				require.Equalf(t, http.StatusUnauthorized, answered.Code, "%s: %s", path, answered.Body.String())
				require.NotContainsf(t, answered.Body.String(), moduleInstance, "%s named the instance", path)
				require.NotContainsf(t, answered.Body.String(), `"path"`, "%s reached the module", path)
			}
		})
	}
}
