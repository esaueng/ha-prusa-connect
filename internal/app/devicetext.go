package app

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/prusa3d/prusa-connect/internal/kernel/machinetext"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
)

// defaultTreeBudget is how many bytes of one device's state tree this process
// retains before it stops taking new paths.
//
// The number is Kubernetes' bound on every annotation of one object, which is
// the same shape: the keys and the values one record carries between them. A
// full xBuddy tree is 20 paths and 1,293 bytes with the widest dialog the
// shipping firmware can render, so a real printer spends half a per cent of it.
//
// It is a default and an operator raises it, because it is the one number here
// that scales with the fleet and with the machine the fleet runs on
// (ADR-20260829-jdq rule 4).
const defaultTreeBudget = 256 << 10

// treeBudget is the budget one entry runs on. Zero and below take the default,
// which is what a plane built by a test that is not about retention wants.
func (d *devices) treeBudget() int {
	if d.stateBudget <= 0 {
		return defaultTreeBudget
	}

	return d.stateBudget
}

// bounded is what the host will retain of one patch: the value to write, and
// whether the patch was cut or refused outright.
//
// A refused patch writes nothing and leaves whatever the path already held. A
// device that has filled its budget goes on reporting the telemetry it was
// already reporting, so nothing can lock itself out of /state by describing
// itself too fully once (ADR-20260829-jdq rule 5).
type bounded struct {
	value   json.RawMessage
	clipped bool
	refused bool
}

// boundValue holds one patch value to the per-value bound.
//
// The cheap case is the whole of the traffic and costs one comparison: a
// telemetry reading is a number, and fifteen of them arrive per frame per
// printer. Only a value over the bound is decoded.
//
// A value that decodes as a JSON string is text a device chose, so it is cut on
// a rune boundary and marked. Anything else over the bound is refused rather
// than cut, because half an object is not JSON and a client reading the tree
// would fail to parse what this server wrote.
//
// The value is copied either way. It is the driver's own slice, and a driver
// that reuses the buffer would otherwise rewrite a tree every subscriber reads.
func boundValue(raw json.RawMessage) bounded {
	if len(raw) <= machinetext.MaxBytes {
		return bounded{value: slices.Clone(raw)}
	}

	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return bounded{refused: true}
	}

	cut := machinetext.Clip(text, machinetext.MaxBytes)
	if cut == text {
		return bounded{value: slices.Clone(raw)}
	}

	encoded, err := json.Marshal(cut)
	if err != nil {
		return bounded{refused: true}
	}

	return bounded{value: encoded, clipped: true}
}

// record applies one patch to the state tree, holding it to what this host
// retains. A null value removes the node, which is how "the printer stopped
// reporting a chamber" is told apart from "the chamber reads zero".
//
// It returns the patch as the host kept it, and false for one the host kept
// nothing of. The caller publishes what comes back, so the event and the tree
// carry the same bytes: a value the tree refused reaches no subscriber, and one
// the tree cut reaches every subscriber cut. The bound is applied once, here,
// and the bytes are shared by every subscriber of the publish.
//
// It marks the entry and acquires no database connection. What the store gets
// is the coalesced tree, on the flush timer.
func (e *device) record(patch connectorapi.StatePatch) (connectorapi.StatePatch, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// The frame is what says the machine is still there, whether or not this
	// path changed anything.
	e.lastSeen = time.Now()
	e.changed = true

	// A machine seen idle or ready between two flushes is what tells a print
	// that reuses a job number from a late report about the print that ended
	// (printidentity.go). The flush reads one tree per interval, so the
	// reading is noted here, where every one of them passes.
	if patch.Path == printerStatePath && quietState(jobText(patch.Value)) {
		e.quietSeen = true
	}

	// A removal of a node the tree does not hold changes nothing, so it reaches
	// no subscriber. A driver may remove what it cannot know is there, such as
	// a tool the machine switched off before this process started.
	if len(patch.Value) == 0 || string(patch.Value) == "null" {
		return patch, e.dropPathLocked(patch.Path)
	}

	// A path is never cut. A truncated path addresses a different node, so a
	// device that sends one gets nothing written rather than the wrong node
	// overwritten.
	if len(patch.Path) > machinetext.MaxBytes {
		e.refusedPaths++

		return patch, false
	}

	held := boundValue(patch.Value)
	if held.refused {
		e.refusedPaths++

		return patch, false
	}

	if held.clipped {
		e.clippedValues++
	}

	if !e.writePathLocked(patch.Path, held.value) {
		return patch, false
	}

	patch.Value = held.value

	return patch, true
}

// stateText is the state the device reported last, such as READY, and empty
// before it reported one.
func (e *device) stateText() string {
	e.mu.Lock()
	defer e.mu.Unlock()

	return jobText(e.state[printerStatePath])
}

// writePathLocked stores one bounded value, and refuses a path this tree does
// not already hold once the budget is spent. It reports whether it stored one.
func (e *device) writePathLocked(path string, value json.RawMessage) bool {
	previous, holding := e.state[path]
	cost := len(path) + len(value)

	if !holding && e.stateBytes+cost > e.budget {
		e.refusedPaths++

		return false
	}

	if holding {
		e.stateBytes -= len(path) + len(previous)
	}

	e.stateBytes += cost
	e.state[path] = value

	return true
}

// dropPathLocked removes a node and gives its bytes back to the budget. It
// reports whether the tree held the node.
func (e *device) dropPathLocked(path string) bool {
	held, holding := e.state[path]
	if !holding {
		return false
	}

	e.stateBytes -= len(path) + len(held)
	delete(e.state, path)

	return true
}

// textCounts is how many values this device has had cut and how many paths it
// has had refused. It is what an operator reads instead of measuring the heap
// (ADR-20260829-jdq rule 6).
func (e *device) textCounts() (clipped, refused uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.clippedValues, e.refusedPaths
}

// treeBytes is what this device's state tree costs, as the budget counts it.
func (e *device) treeBytes() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.stateBytes
}
