package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The api credential lane. It runs the shipping binary with the local password
// login on, mints a bearer token through the routes a person has, and measures
// what the token is worth: the same principal the session had, then nothing
// once it is revoked.
//
// It is here and not in internal/transport/http/httpapi because the question is
// not what the handler answers over a fake. It is whether a token minted by one
// request is accepted by the next, which is the only thing a client SDK cares
// about, and it needs the account store, the credential store, the verifier
// and the router in one process.

// mintedCredential is what the create route answers, as this lane reads it.
type mintedCredential struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Subject string `json:"subject"`
	Token   string `json:"token"`
}

// bearer is what a request carrying an api credential sends.
func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// streamKeepAlive is the core block that shortens the stream's keep-alive. The
// credential re-check rides it, so this is what bounds how long the revoked
// stream below stays open, and the default fifteen seconds would spend most of
// the lane's budget waiting.
const streamKeepAlive = "\n  events:\n    heartbeat: 250ms"

// credentialExpired is the frame the stream writes before it closes on a
// credential this server no longer accepts.
const credentialExpired = "prusa-connect.auth.expired"

// TestAnAPICredentialSpeaksForItsPrincipalUntilItIsRevoked is the whole lane.
//
// The subtests run in order and are not parallel: each one stands on the
// credential the one before it minted, so the sequence is the assertion.
//
//nolint:tparallel // the subtests are steps of one sequence, not independent cases
func TestAnAPICredentialSpeaksForItsPrincipalUntilItIsRevoked(t *testing.T) {
	t.Parallel()

	srv := startServerCore(t, localAuth+streamKeepAlive, "")
	srv.awaitReady(t)

	admin := srv.setupFirstAdmin(t)
	bob := srv.createPerson(t, admin, bobName, "")

	var own mintedCredential

	t.Run("a person mints their own with no grant", func(t *testing.T) {
		own = srv.mint(t, bob, `{"name":"nightly export"}`)

		if own.Subject != bob.subject {
			t.Fatalf("the credential speaks for %q, want %q", own.Subject, bob.subject)
		}

		if !strings.HasPrefix(own.Token, "pcl_") {
			t.Fatalf("the token %q is not shaped like one this server issues", own.Token)
		}
	})

	t.Run("the bearer resolves to the same principal as the session", func(t *testing.T) {
		got := srv.do(t, http.MethodGet, "/api/v1/principal", "", bearer(own.Token))
		if got.status != http.StatusOK {
			t.Fatalf("the bearer answered %d, want 200: %s", got.status, got.body)
		}

		var body struct {
			Principal string `json:"principal"`
		}

		if err := json.Unmarshal(got.body, &body); err != nil {
			t.Fatalf("parse the principal: %v", err)
		}

		if body.Principal != bob.subject {
			t.Fatalf("the bearer resolved to %q, want %q", body.Principal, bob.subject)
		}
	})

	t.Run("the listing names it and never the secret", func(t *testing.T) {
		got := srv.as(t, bob, http.MethodGet, "/api/v1/auth/credentials", "")
		if got.status != http.StatusOK {
			t.Fatalf("the listing answered %d, want 200: %s", got.status, got.body)
		}

		text := string(got.body)
		if !strings.Contains(text, own.ID) || !strings.Contains(text, "nightly export") {
			t.Fatalf("the listing does not carry the minted credential: %s", text)
		}

		if strings.Contains(text, own.Token) || strings.Contains(text, "\"token\"") {
			t.Fatalf("the listing carries the secret: %s", text)
		}

		if !strings.Contains(text, "last_used_at") {
			t.Fatalf("a presented credential shows no last-used time: %s", text)
		}
	})

	t.Run("a person cannot mint for somebody else", func(t *testing.T) {
		got := srv.as(t, bob, http.MethodPost, "/api/v1/auth/credentials",
			`{"name":"mallory","subject":"`+admin.subject+`"}`)
		if got.status != http.StatusForbidden {
			t.Fatalf("minting for the administrator answered %d, want 403: %s", got.status, got.body)
		}

		got = srv.as(t, bob, http.MethodPost, "/api/v1/auth/credentials",
			`{"name":"ci","subject":"service_account:ci"}`)
		if got.status != http.StatusForbidden {
			t.Fatalf("minting for a service account answered %d, want 403: %s", got.status, got.body)
		}
	})

	t.Run("an expiry that has passed is refused", func(t *testing.T) {
		got := srv.as(t, bob, http.MethodPost, "/api/v1/auth/credentials",
			`{"name":"old","expires_at":"2020-01-01T00:00:00Z"}`)
		if got.status != http.StatusUnprocessableEntity {
			t.Fatalf("an expiry in the past answered %d, want 422: %s", got.status, got.body)
		}

		if code := faultCode(t, got.body); code != "core.auth/CREDENTIAL_EXPIRED" {
			t.Fatalf("an expiry in the past was refused as %s", code)
		}
	})

	t.Run("the administrator mints for a service account and it speaks as one", func(t *testing.T) {
		service := srv.mint(t, admin, `{"name":"ci","subject":"service_account:ci"}`)

		got := srv.do(t, http.MethodGet, "/api/v1/principal", "", bearer(service.Token))
		if got.status != http.StatusOK {
			t.Fatalf("the service bearer answered %d, want 200: %s", got.status, got.body)
		}

		if !strings.Contains(string(got.body), `"service_account:ci"`) {
			t.Fatalf("the service bearer resolved to %s, want service_account:ci", got.body)
		}

		// The token speaks for the service account and not for the person who
		// minted it, so it holds nothing: the account listing takes a grant
		// the administrator has and the service account does not.
		got = srv.do(t, http.MethodGet, "/api/v1/auth/accounts", "", bearer(service.Token))
		if got.status != http.StatusForbidden {
			t.Fatalf("the service bearer reached the accounts with %d, want 403: %s", got.status, got.body)
		}
	})

	t.Run("the bearer opens the event stream until it is revoked", func(t *testing.T) {
		theirs := srv.mint(t, admin, `{"name":"the administrator's stream"}`)

		ctx, cancel := context.WithTimeout(t.Context(), settleBudget)
		defer cancel()

		frames, errs := srv.subscribeWith(ctx, t, bearer(theirs.Token))

		srv.attachAs(t, admin, "MK4", "E2E-STREAM-1")

		awaitFrame(ctx, t, frames, errs, func(f frame) bool {
			return f.event == "com.prusa.connect.device.arrived"
		})

		got := srv.as(t, admin, http.MethodDelete, "/api/v1/auth/credentials/"+theirs.ID, "")
		if got.status != http.StatusNoContent {
			t.Fatalf("revoking the stream's token answered %d, want 204: %s", got.status, got.body)
		}

		revokedAt := time.Now()

		awaitFrame(ctx, t, frames, errs, func(f frame) bool { return f.event == credentialExpired })

		// The frame is written and then the response ends, which is how a
		// reader tells a logout from a dropped connection: the close follows
		// the frame rather than arriving alone.
		select {
		case <-errs:
		case <-ctx.Done():
			t.Fatal("the stream told the reader the credential had gone and then stayed open")
		}

		if noticed := time.Since(revokedAt); noticed > 5*time.Second {
			t.Fatalf("the revocation reached the stream after %s", noticed)
		}

		got = srv.do(t, http.MethodGet, "/api/v1/events", "", bearer(theirs.Token))
		if got.status != http.StatusUnauthorized {
			t.Fatalf("the revoked token opened a new stream with %d, want 401: %s", got.status, got.body)
		}
	})

	t.Run("a person cannot revoke another person's", func(t *testing.T) {
		theirs := srv.mint(t, admin, `{"name":"the administrator's"}`)

		got := srv.as(t, bob, http.MethodDelete, "/api/v1/auth/credentials/"+theirs.ID, "")
		if got.status != http.StatusNotFound {
			t.Fatalf("revoking the administrator's answered %d, want 404: %s", got.status, got.body)
		}

		unknown := srv.as(t, bob, http.MethodDelete, "/api/v1/auth/credentials/unknown-credential-id", "")
		if unknown.status != http.StatusNotFound {
			t.Fatalf("revoking an unknown credential answered %d, want 404: %s", unknown.status, unknown.body)
		}

		var gotProblem, unknownProblem struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(got.body, &gotProblem); err != nil {
			t.Fatalf("parse the refusal for another person's credential: %v", err)
		}
		if err := json.Unmarshal(unknown.body, &unknownProblem); err != nil {
			t.Fatalf("parse the refusal for an unknown credential: %v", err)
		}
		if gotProblem.Code != "core.auth/CREDENTIAL_UNKNOWN" || gotProblem.Code != unknownProblem.Code {
			t.Fatalf("got code %q for another person's vs %q for unknown", gotProblem.Code, unknownProblem.Code)
		}
	})

	t.Run("revoked, the bearer is refused on the next request", func(t *testing.T) {
		got := srv.as(t, bob, http.MethodDelete, "/api/v1/auth/credentials/"+own.ID, "")
		if got.status != http.StatusNoContent {
			t.Fatalf("revoking answered %d, want 204: %s", got.status, got.body)
		}

		got = srv.do(t, http.MethodGet, "/api/v1/principal", "", bearer(own.Token))
		if got.status != http.StatusUnauthorized {
			t.Fatalf("the revoked bearer answered %d, want 401: %s", got.status, got.body)
		}

		got = srv.as(t, bob, http.MethodDelete, "/api/v1/auth/credentials/"+own.ID, "")
		if got.status != http.StatusNotFound {
			t.Fatalf("revoking it again answered %d, want 404: %s", got.status, got.body)
		}
	})
}

// mint creates an api credential as one person and returns what the route
// answered.
func (s *server) mint(t *testing.T, who person, body string) mintedCredential {
	t.Helper()

	got := s.as(t, who, http.MethodPost, "/api/v1/auth/credentials", body)
	if got.status != http.StatusCreated {
		t.Fatalf("minting %s answered %d, want 201: %s", body, got.status, got.body)
	}

	var minted mintedCredential
	if err := json.Unmarshal(got.body, &minted); err != nil {
		t.Fatalf("parse the minted credential: %v", err)
	}

	if minted.Token == "" || minted.ID == "" {
		t.Fatalf("the mint answered without a token or an id: %s", got.body)
	}

	return minted
}
