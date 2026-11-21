package webhook_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestARedirectIsNotFollowed keeps the headers the operator set, which often
// hold a secret, at the host the operator named. An endpoint that answers 307
// would otherwise have this server send the same POST and headers to a host of
// the endpoint's choosing.
func TestARedirectIsNotFollowed(t *testing.T) {
	t.Parallel()

	var elsewhere atomic.Int32

	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		elsewhere.Add(1)
	}))
	defer target.Close()

	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()

	p := newProcessor(t, map[string]any{
		"url":    redirecting.URL,
		"header": map[string]any{"X-Webhook-Secret": "do-not-forward"},
	})

	p.PrinterDeleted("one")

	err := p.Close()
	require.Error(t, err, "a redirect is not a delivery")
	assert.Contains(t, err.Error(), "failed")
	assert.Zero(t, elsewhere.Load(), "the event and its headers reached the host the redirect named")
}
