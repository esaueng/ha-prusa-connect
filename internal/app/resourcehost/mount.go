package resourcehost

import (
	"errors"
	"strings"

	"github.com/prusa3d/prusa-connect/internal/app/resource"
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// moduleMountOf is the root prefix one declaration's routes are served under,
// and empty for a module reached under its namespace or for anything that is
// not a module. The declaration derives it, so the listing and this claim
// agree on it.
func moduleMountOf(declared resource.Declaration) string {
	return declared.ModuleMount()
}

// claimMount checks that the router can route a module's root mount and
// records it. It refuses a prefix that overlaps one another running instance
// holds. The caller holds h.mu.
//
// It runs before anything is constructed, like the namespace claim, because a
// second module on one prefix must not launch a process whose routes nothing
// can reach. Every refusal carries a code, because the operator wrote the
// prefix and reads the answer in the status of the instance.
func (h *Host) claimMount(declared resource.Declaration) error {
	mount := moduleMountOf(declared)
	if mount == "" {
		return nil
	}

	if err := httpapi.ValidateModuleMount(mount, h.beside()); err != nil {
		return mountRefusal(declared.Ref, mount, err)
	}

	for held, instance := range h.mounts {
		if instance == declared.Ref || !overlaps(held, mount) {
			continue
		}

		return mountClaimed.New("the root mount is already served by another instance").
			With(
				fault.StringParam("resource", declared.Ref.String()),
				fault.StringParam("mount", mount),
				fault.StringParam("held_by", instance.String()),
			).
			Wrap(errors.New(held + " is served by " + instance.String() + ", and " + declared.Ref.String() +
				" declares " + mount))
	}

	h.mounts[mount] = declared.Ref

	return nil
}

// releaseMount removes the prefix one instance held, so the prefix of a
// stopped module is free for the next declaration. The caller holds h.mu.
func (h *Host) releaseMount(instance ref.Ref) {
	for held, holder := range h.mounts {
		if holder == instance {
			delete(h.mounts, held)
		}
	}
}

// beside is the patterns mounted next to the API, and none for a host that was
// given none.
func (h *Host) beside() []string {
	if h.cfg.Beside == nil {
		return nil
	}

	return h.cfg.Beside()
}

// mountRefusal gives the router's refusal the code that tells the operator
// what to do: rewrite the prefix, or choose one this server does not serve.
func mountRefusal(instance ref.Ref, mount string, err error) error {
	refused := mountInvalid
	if errors.Is(err, httpapi.ErrModuleMountReserved) {
		refused = mountReserved
	}

	return refused.New("the root mount was refused").
		With(
			fault.StringParam("resource", instance.String()),
			fault.StringParam("mount", mount),
			fault.StringParam("reason", err.Error()),
		).
		Wrap(err)
}

// overlaps reports two prefixes that would both match one path: the same one,
// or one under the other. The router applies the same rule. It is repeated
// here because a claim is checked against h.mounts before any request reaches
// the router.
func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// Root is the module a root-mounted path belongs to, and false when no module
// claimed a prefix over it. The router asks it only for a path none of its own
// routes answered, so a claim here never hides an operation of the core.
//
// A prefix that is claimed and has no module serving it is answered here and
// not by the router's not-found. The surface carries the instance, the prefix
// and why nothing is serving, and the transport answers 503 from it. A client
// can then tell "the module is not running on this server" from "this is the
// wrong server". A module that is declared, switched on and not installed is
// one such case.
func (h *Host) Root(path string) (httpapi.ModuleSurface, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for held, instance := range h.mounts {
		if path != held && !strings.HasPrefix(path, held+"/") {
			continue
		}

		running, ok := h.running[instance]
		if !ok {
			return unservedMount(instance, held, "the module is declared and nothing is supervising it"), true
		}

		if surface, serving := h.surfaceOf(instance, running); serving {
			return surface, true
		}

		return unservedMount(instance, held, mountReasonFor(running)), true
	}

	return httpapi.ModuleSurface{}, false
}

// unservedMount is the surface for a claimed prefix that no module serves:
// the instance, the prefix and the reason, with no handler.
func unservedMount(instance ref.Ref, mount, reason string) httpapi.ModuleSurface {
	return httpapi.ModuleSurface{Instance: instance, Mount: mount, Unavailable: reason}
}

// mountReasonFor says why one supervised module is not answering. It names
// the lifecycle state and no path or digest, so an identified caller without
// module access never sees how the server's disk is laid out.
func mountReasonFor(running *instance) string {
	return "the module is switched on and is not serving. Its instance is " +
		running.runner.Status().State.String() +
		"; a module that was never installed stays there until `module add` puts its files in place"
}
