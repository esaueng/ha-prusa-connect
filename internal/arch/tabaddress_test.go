package arch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The web app routes a module's printer tab as
// /printers/<id>/<namespace>.<tab>, and the server reads the same shape to
// isolate that page. Each file below holds one part of the shape, so a change
// on one side alone fails here.
func TestTheServerReadsATabAddressAsTheAppWritesIt(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	for file, shape := range map[string]string{
		"frontend/src/lib/routes.ts":                   "printerDetailTab: '/printers/:id/:tab'",
		"packages/svelte/src/slots/module-tabs.ts":     "`${namespace}.${id}`",
		"internal/transport/http/httpapi/isolation.go": `printerTabPrefix = "printers"`,
	} {
		body, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}

		if !strings.Contains(string(body), shape) {
			t.Errorf("%s no longer holds %s. Change the tab's address in the other two files with it", file, shape)
		}
	}
}
