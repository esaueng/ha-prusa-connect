package app

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/sdk/event"
)

// A device that changes what it is doing reaches the bus as one frame for
// each change, with the word before and after, so a reader waiting for a
// printer to come to rest need not read every measurement of every printer.
// The same word repeated is no change.
func TestAStateChangeIsOneFrameAndARepeatIsNone(t *testing.T) {
	t.Parallel()

	plane, bus, _ := planeBusAndGraph(t)
	session := arriveSpeaking(t, plane, "SIM-STATE")
	id := plane.idOf(session.identity)

	session.report(t, plane, printerStatePath, `"PRINTING"`)
	session.report(t, plane, printerStatePath, `"PRINTING"`)
	session.report(t, plane, printerStatePath, `"FINISHED"`)
	session.report(t, plane, printerStatePath, `"READY"`)

	frames := bus.awaitType(t, event.TypeDeviceStateChanged, 3)

	changes := make([]event.StateChanged, 0, len(frames))
	for _, frame := range frames {
		var change event.StateChanged
		require.NoError(t, json.Unmarshal(frame.Data, &change))
		require.Equal(t, id, change.DeviceID)
		require.Equal(t, "test", change.Driver)
		require.Equal(t, event.ClassState, frame.Class)
		changes = append(changes, event.StateChanged{From: change.From, To: change.To})
	}

	require.Equal(t, []event.StateChanged{
		{From: "", To: "PRINTING"},
		{From: "PRINTING", To: "FINISHED"},
		{From: "FINISHED", To: "READY"},
	}, changes, "the repeated PRINTING was published as a change, or a change is missing")

	// A device that goes away changed no state, so it is told as departed
	// and not as a change.
	session.closeOnce()
	bus.awaitType(t, event.TypeDeviceDeparted, 1)
	require.Len(t, bus.ofType(event.TypeDeviceStateChanged), 3, "a departure was published as a change of state")
}
