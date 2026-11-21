package script_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/extension/script"
	"github.com/prusa3d/prusa-connect/sdk/eventsapi"
)

// TestASinkDoesNotFollowARedirect keeps a message at the address the operator
// declared. A destination that answers 307 would otherwise have this server
// send the body to a host the operator never named.
func TestASinkDoesNotFollowARedirect(t *testing.T) {
	t.Parallel()

	var elsewhere atomic.Int32

	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		elsewhere.Add(1)
	}))
	t.Cleanup(target.Close)

	var asked atomic.Int32

	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirecting.Close)

	sink, err := script.NewSink(`msg("main", "k", "{}")`,
		json.RawMessage(`{"endpoints":{"main":"`+redirecting.URL+`/publish"},"timeout":"2s"}`), nil, nil)
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, sink.Close()) })

	sink.PrinterStateChanged(eventsapi.PrinterState{ID: "prn-42", State: "PRINTING"})

	require.Equal(t, int32(1), asked.Load(), "the declared destination was not asked")
	require.Zero(t, elsewhere.Load(), "the message reached the host the redirect named")
}
