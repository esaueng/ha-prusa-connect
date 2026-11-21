package app

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/kernel/devicestate"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
)

// A module reads which verbs a device takes in its state, and whether it is
// ready, from what the connector declared and never from the state's name.
func TestModuleDevice_acceptsAndReady(t *testing.T) {
	t.Parallel()

	const start, ready, pause = "core.command.start_print", "core.command.set_printer_ready", "core.command.pause_print"

	guarded := connectorapi.StateSet{
		States: []connectorapi.DeviceState{
			{Name: "STANDBY", Accepts: []connectorapi.Capability{start, ready}},
			{Name: "ARMED", Accepts: []connectorapi.Capability{start}},
			{Name: "WORKING", Accepts: []connectorapi.Capability{pause}},
		},
		Transitions: []connectorapi.Transition{
			{From: "STANDBY", To: "ARMED", Verb: ready},
			{From: "ARMED", To: "WORKING", Verb: start},
			{From: "WORKING", To: "STANDBY"},
		},
	}
	unguarded := connectorapi.StateSet{
		States: []connectorapi.DeviceState{{Name: "STANDBY"}, {Name: "ARMED"}},
		Transitions: []connectorapi.Transition{
			{From: "STANDBY", To: "ARMED", Verb: ready}, {From: "ARMED", To: "STANDBY"},
		},
	}

	fleet := moduleFleet{states: map[string]connectorapi.StateSet{"guarded": guarded, "unguarded": unguarded}}

	tests := []struct {
		name      string
		driver    string
		state     string
		connected bool
		accepts   []string
		ready     bool
	}{
		{name: "at rest", driver: "guarded", state: "STANDBY", connected: true, accepts: []string{ready, start}},
		{name: "made ready", driver: "guarded", state: "ARMED", connected: true, accepts: []string{start}, ready: true},
		{
			name: "a verb the device does not carry is left out", driver: "guarded", state: "WORKING",
			connected: true, accepts: []string{},
		},
		{name: "away takes nothing", driver: "guarded", state: "ARMED", accepts: []string{}},
		{
			name:      "an undeclared state takes nothing",
			driver:    "guarded",
			state:     "OTHER",
			connected: true,
			accepts:   []string{},
		},
		{name: "no guard declared", driver: "unguarded", state: "ARMED", connected: true, ready: true},
		{name: "no state set declared", driver: "silent", state: "ARMED", connected: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			state, err := json.Marshal(test.state)
			require.NoError(t, err)

			got := fleet.device(httpapi.Device{
				Driver:       test.driver,
				Connected:    test.connected,
				Capabilities: []string{start, ready},
				State:        httpapi.DeviceState{printerStatePath: state},
			})

			assert.Equal(t, test.accepts, got.Accepts)
			assert.Equal(t, test.ready, got.Ready)
		})
	}
}

// A device names its connector instance, so that is what its state set is
// found by.
func TestStatesByInstance_keysEveryInstance(t *testing.T) {
	t.Parallel()

	set := connectorapi.StateSet{
		States: []connectorapi.DeviceState{
			{Name: "STANDBY", DisplayName: "Standby"}, {Name: "ARMED", DisplayName: "Armed"},
		},
		Transitions: []connectorapi.Transition{{From: "STANDBY", To: "ARMED"}, {From: "ARMED", To: "STANDBY"}},
	}

	installed, err := devicestate.Place(devicestate.Declaration{
		Namespace: "prusa3d.xbuddy", Kind: "printer", Connector: "prusa3d.xbuddy/buddy",
		Instances: []string{"lan", "wan"}, States: set,
	})
	require.NoError(t, err)

	got := statesByInstance(installed)

	assert.Len(t, got, 2)
	assert.True(t, got["lan"].Has("ARMED"))
	assert.True(t, got["wan"].Has("ARMED"))
}
