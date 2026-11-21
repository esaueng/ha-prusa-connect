package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/prusa3d/prusa-connect/internal/transport/http/httpfault"
)

// Errors ValidateModuleMount reports. The plane turns each into the fault code
// an operator reads off the instance, so the two are kept apart here.
var (
	// ErrModuleMountInvalid reports a mount that is not a root prefix this
	// server can route: not absolute, ending in a slash, carrying a dot
	// segment, or spelt with characters a path segment does not hold.
	ErrModuleMountInvalid = errors.New("httpapi: the module mount is not a root prefix")
	// ErrModuleMountReserved reports a mount on a path this server serves
	// itself, or on one a deployment mounted beside the API such as a device
	// wire. A module there would either shadow the core or never be reached.
	ErrModuleMountReserved = errors.New("httpapi: the module mount is a path this server serves")
	// ErrRootNotReserved reports a router serving a route at a root that
	// ValidateModuleMount would let a module claim. New refuses to build one,
	// so the reserved set is checked against the routes on every start.
	ErrRootNotReserved = errors.New("httpapi: the router serves a root a module mount could claim")
)

// rootPaths is every address this server answers outside apiRoot: the four
// document routes and the two pages registered on the origin root. It is built
// from the constants the routes are registered with. verifyRootsReserved walks
// the built router on every New and refuses a route this list does not cover.
func rootPaths() []string {
	return append([]string{strings.TrimSuffix(apiRoot, "/"), metricsPath, devPagePath}, ownPaths...)
}

// verifyRootsReserved walks every route the router serves and reports the ones
// whose root a module mount would not be refused on. mounted is the patterns
// Config.Mounts declared, which the plane is told about separately.
func verifyRootsReserved(mux chi.Routes, mounted []string) error {
	var unreserved []string

	walk := func(_, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if slices.Contains(mounted, route) {
			return nil
		}

		if !errors.Is(ValidateModuleMount(rootOf(route), nil), ErrModuleMountReserved) {
			unreserved = append(unreserved, route)
		}

		return nil
	}

	if err := chi.Walk(mux, walk); err != nil {
		return fmt.Errorf("walk the router: %w", err)
	}

	if len(unreserved) == 0 {
		return nil
	}

	slices.Sort(unreserved)

	return fmt.Errorf("%w: %s; add the root to rootPaths", ErrRootNotReserved,
		strings.Join(slices.Compact(unreserved), ", "))
}

// rootReserved reports a path under one of this server's own roots, the same
// list a module's mount is refused on. It is asked before the plane is, so a
// plane that claimed such a path would still never be handed one.
func rootReserved(path string) bool {
	for _, own := range rootPaths() {
		if path == own || strings.HasPrefix(path, own+"/") {
			return true
		}
	}

	return false
}

// rootOf is the first segment of a route, which is the prefix a module mount
// there would have to share.
func rootOf(route string) string {
	first, _, _ := strings.Cut(strings.TrimPrefix(route, "/"), "/")

	return "/" + first
}

// ValidateModuleMount holds a root prefix an operator wrote for a module to
// what this server can mount there.
//
// beside is the patterns a deployment mounted next to the API, as PatternsOf
// renders them: a frozen device wire, a connector prefix. The catch-all at the
// origin root is not in the way, because a module's root mount is decided
// before the frontend is asked, the same way an operation is.
func ValidateModuleMount(prefix string, beside []string) error {
	if err := mountGrammar(prefix); err != nil {
		return err
	}

	for _, own := range rootPaths() {
		if overlaps(prefix, own) {
			return fmt.Errorf("%w: %s is under or over %s", ErrModuleMountReserved, prefix, own)
		}
	}

	for _, pattern := range beside {
		if pattern == rootCatchAll {
			continue
		}

		claimed := strings.TrimSuffix(pattern, "/*")
		if overlaps(prefix, claimed) {
			return fmt.Errorf("%w: %s is under or over the mounted %s", ErrModuleMountReserved, prefix, pattern)
		}
	}

	return nil
}

// mountGrammar checks the form of a prefix before anything is asked about
// what it collides with. Nothing is cleaned. A prefix that needs cleaning is
// refused, because the plane matches it against a request path byte for byte,
// and a rewrite here would mount the module somewhere the operator did not
// write.
func mountGrammar(prefix string) error {
	if !strings.HasPrefix(prefix, "/") {
		return fmt.Errorf("%w: %q is not an absolute path", ErrModuleMountInvalid, prefix)
	}

	if prefix == "/" {
		return fmt.Errorf("%w: the origin root is not a module's", ErrModuleMountInvalid)
	}

	if strings.HasSuffix(prefix, "/") {
		return fmt.Errorf("%w: %q ends in a slash", ErrModuleMountInvalid, prefix)
	}

	for segment := range strings.SplitSeq(prefix[1:], "/") {
		switch {
		case segment == "":
			return fmt.Errorf("%w: %q carries an empty segment", ErrModuleMountInvalid, prefix)
		case segment == "." || segment == "..":
			return fmt.Errorf("%w: %q carries a dot segment", ErrModuleMountInvalid, prefix)
		case strings.ContainsFunc(segment, notInSegment):
			return fmt.Errorf("%w: %q carries a character a path segment does not hold",
				ErrModuleMountInvalid, prefix)
		}
	}

	return nil
}

// notInSegment reports a rune outside the unreserved set RFC 3986 lets a path
// segment carry as itself. Anything else would need escaping, and a client
// would then not type the mount the way the operator wrote it.
func notInSegment(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	case r == '-', r == '_', r == '.', r == '~':
		return false
	}

	return true
}

// overlaps reports two prefixes that would both match one path: the same one,
// or one under the other.
func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// ModuleRootOpenAPIPath is where a module mounted at a root prefix serves its
// document, beside the same document at the modules path.
func ModuleRootOpenAPIPath(mount string) string {
	return mount + openAPIDocumentName
}

// rootModules is the fallback the router asks before it says a path is
// unrouted: whether a module claimed a root prefix the path is under.
//
// It handles a request no route matched, so an operation, an infrastructure
// route and a mount beside the API all win over it, as they win over the
// frontend. The reserved check runs first, so a path of this server's own is
// never forwarded even if the plane claims one. A claimed path is handled as
// moduleHandler handles one under the namespace: the caller identified before
// anything about the module is said, the same request rewrite, and the same
// permission on the same instance.
func (s *Server) rootModules(cfg Config, fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.Modules == nil || rootReserved(r.URL.Path) {
			fallback.ServeHTTP(w, r)

			return
		}

		surface, claimed := cfg.Modules.Root(r.URL.Path)
		if !claimed || surface.Mount == "" || surface.Instance.IsZero() {
			fallback.ServeHTTP(w, r)

			return
		}

		// The caller is identified before the answer says anything about the
		// module, so a caller with no identity gets one 401 whether the module
		// serves or not, and never the instance the refusals below name.
		caller, identified := s.identifyModuleCaller(w, r, cfg)
		if !identified {
			return
		}

		// The prefix is claimed and nothing is answering on it. The address
		// belongs to this deployment, so the answer says why it is not served.
		// A plain 404 would read as an address this server does not know.
		if surface.Handler == nil && surface.ModulesOff {
			writeProblem(w, r, httpfault.ModulesHeld(surface.SafeMode))

			return
		}

		if surface.Handler == nil {
			writeProblem(w, r, httpfault.ModuleNotServing(
				surface.Instance.String(), surface.Mount, unavailableReason(surface)))

			return
		}

		if r.URL.Path == ModuleRootOpenAPIPath(surface.Mount) {
			s.serveRootDocument(w, r, cfg, caller, surface)

			return
		}

		inner, ok := mountedRequest(surface.Mount, r)
		if !ok {
			notFound(w, r)

			return
		}

		if !s.allowModule(w, r, cfg, caller, surface) {
			return
		}

		surface.Handler.ServeHTTP(w, inner.WithContext(withCaller(inner.Context(), caller)))
	})
}

// unavailableReason is what the refusal says, and a sentence of this package's
// own for a host that gave none. A caller reads it to tell a module that is
// switched off from one that is switched on and not installed.
func unavailableReason(surface ModuleSurface) string {
	if surface.Unavailable != "" {
		return surface.Unavailable
	}

	return "the module that claims this address is not serving"
}

// serveRootDocument answers the module's document at the root mount, under the
// permission the operation at the modules path takes: a GET, so allowModule
// asks for view_resource on the instance. This server answers it and the
// module never does, so a module cannot publish a different document at the
// address a generator reads.
func (s *Server) serveRootDocument(
	w http.ResponseWriter, r *http.Request, cfg Config, caller Caller, surface ModuleSurface,
) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)

		return
	}

	if !s.allowModule(w, r, cfg, caller, surface) {
		return
	}

	if len(surface.OpenAPI) == 0 {
		notFound(w, r)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(surface.OpenAPI); err != nil {
		s.logger.WarnContext(r.Context(), "writing a module's document", "error", err.Error())
	}
}
