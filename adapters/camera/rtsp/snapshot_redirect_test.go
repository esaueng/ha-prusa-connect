package rtsp_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/adapters/camera/rtsp"
	"github.com/prusa3d/prusa-connect/sdk/cameraapi"
)

// TestASnapshotRedirectIsNotFollowed keeps the camera's credential at the host
// the operator named. The snapshot request carries it as Basic, and a camera
// that answers 302 would otherwise have this server send it on.
func TestASnapshotRedirectIsNotFollowed(t *testing.T) {
	t.Parallel()

	var elsewhere atomic.Int32

	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		elsewhere.Add(1)
	}))
	t.Cleanup(target.Close)

	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/snapshot.jpg", http.StatusFound)
	}))
	t.Cleanup(redirecting.Close)

	camera, err := rtsp.New(cameraapi.ProviderSpec{
		ID: "watch", Type: rtsp.Type, Printer: "p1",
		Config: map[string]any{
			"url":          "rtsp://192.0.2.50/live",
			"snapshot_url": redirecting.URL + "/snapshot.jpg",
			"username":     "operator",
			"password":     "do-not-forward",
		},
	})
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, camera.Close()) })

	_, err = camera.Snapshot(t.Context())
	require.ErrorIs(t, err, rtsp.ErrBadFrame, "a redirect is not an image")
	require.Zero(t, elsewhere.Load(), "the snapshot request reached the host the redirect named")
}
