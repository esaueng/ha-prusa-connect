package manifest_test

import (
	"strings"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/manifest"
)

// A printer tab may ask for its page to be sent cross-origin isolated, so a
// device page it frames can use SharedArrayBuffer.

func TestAPrinterTabMayAskForAnIsolatedPage(t *testing.T) {
	t.Parallel()

	held, err := manifest.Parse("module.yaml", []byte(withUI(`{"entry":"ui/index.js","library":">=0.0.1",
	  "contributes":{
	    "device_tab":[
	      {"id":"console","title":"Device page","cross_origin_isolated":true,"devices":{"resident":true}},
	      {"id":"log","title":"Log","cross_origin_isolated":false},
	      {"id":"plain","title":"Plain","cross_origin_isolated":null},
	      {"id":"other","title":"Other"}]}}`)), decode)
	if err != nil {
		t.Fatalf("cross_origin_isolated on a printer tab was refused: %v", err)
	}

	tabs := held.UI.Contributes.Sections[manifest.SlotDeviceTab]
	if len(tabs) != 4 {
		t.Fatalf("the printer tabs read as %+v", tabs)
	}

	if !tabs[0].CrossOriginIsolated || !tabs[0].Devices.Resident {
		t.Errorf("a tab that asks for isolation read as %+v", tabs[0])
	}

	for _, tab := range tabs[1:] {
		if tab.CrossOriginIsolated {
			t.Errorf("a tab that asks for no isolation read as isolated: %+v", tab)
		}
	}
}

func TestOnlyAPrinterTabMayAskForAnIsolatedPage(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ contributes, refuses string }{
		"a printer section": {
			`{"device_section":[{"id":"x","title":"X","cross_origin_isolated":true}]}`, "carries cross_origin_isolated",
		},
		"a printer card line": {
			`{"device_card":[{"id":"x","title":"X","cross_origin_isolated":true}]}`, "carries cross_origin_isolated",
		},
		"a file tab": {
			`{"file_tab":[{"id":"x","title":"X","cross_origin_isolated":true}]}`, "carries cross_origin_isolated",
		},
		"a settings pane": {
			`{"settings":[{"id":"x","title":"X","cross_origin_isolated":true}]}`, "carries cross_origin_isolated",
		},
		"a printer action": {
			`{"device_action":[{"id":"x","title":"X","cross_origin_isolated":true}]}`, "carries cross_origin_isolated",
		},
		"a page": {
			`{"pages":[{"path":"x","title":"X","cross_origin_isolated":true}]}`, "carries cross_origin_isolated",
		},
		"not a truth": {
			`{"device_tab":[{"id":"x","title":"X","cross_origin_isolated":"yes"}]}`, "expected true or false",
		},
		"a bare isolation key": {
			`{"device_tab":[{"id":"x","title":"X","isolation":true}]}`, "carries isolation",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := manifest.Parse("module.yaml", []byte(withUI(`{"entry":"ui/index.js","library":">=0.0.1",
			  "contributes":`+tc.contributes+`}`)), decode)
			if err == nil || !strings.Contains(err.Error(), tc.refuses) {
				t.Fatalf("the refusal is %v and does not name %q", err, tc.refuses)
			}
		})
	}
}
