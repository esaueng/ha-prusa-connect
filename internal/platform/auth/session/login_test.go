package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/platform/auth"
	"github.com/prusa3d/prusa-connect/internal/platform/auth/oidc"
)

// The whole login runs against a real issuer: discovery, an authorization
// request carrying an S256 challenge, a login page, a code on the redirect, an
// exchange proving the verifier, an ID token checked against the issuer's JWKS,
// and a session credential issued for the subject it named.
func TestALoginAgainstARealIssuerEndToEnd(t *testing.T) {
	t.Parallel()

	client := newIssuerClient(t)
	store := auth.NewMemoryStore()
	seen := &recordedSubjects{}
	login := NewLogin(client, store, nil).WithSubjects(seen)

	redirect, err := login.Begin(t.Context())
	require.NoError(t, err)

	asked, err := url.Parse(redirect.URL)
	require.NoError(t, err)

	query := asked.Query()
	t.Logf("authorization request: %s%s?%s", asked.Host, asked.Path, query.Encode())

	require.Equal(t, "code", query.Get("response_type"))
	require.Equal(t, oidc.ChallengeMethodS256, query.Get("code_challenge_method"),
		"OAuth 2.1 removes plain, and this client must not offer it")
	require.NotEmpty(t, query.Get("code_challenge"))
	require.NotEmpty(t, query.Get("nonce"))
	require.Equal(t, redirect.State, query.Get("state"))
	require.Equal(t, defaultRedirectURI, query.Get("redirect_uri"))
	require.NotContains(t, redirect.URL, "code_verifier", "the verifier must never reach the issuer's front channel")

	code, state := authorize(t, redirect.URL)
	t.Logf("callback: code=%s state=%s", code, state)
	require.Equal(t, redirect.State, state, "the issuer returned a state this server did not start")
	require.Equal(t, 1, login.Pending(), "the login is still in flight before the callback is completed")

	result, err := login.Complete(t.Context(), state, code)
	require.NoError(t, err)

	require.Equal(t, testSubject, result.Claims.Subject)
	require.Equal(t, client.Issuer(), result.Claims.Issuer)
	require.Equal(t, testSubject, result.Credential.Subject)
	require.Equal(t, testSubject, result.Principal, "a subject that is already an id is kept as it is")
	require.Equal(t, auth.KindSession, result.Credential.Kind)

	recorded, err := seen.Get(t.Context(), testSubject)
	require.NoError(t, err)
	require.Equal(t, testSubject, recorded.Subject)
	require.Equal(t, "Operator", recorded.DisplayName)
	require.Equal(t, "operator@example.invalid", recorded.Email)
	require.True(t, strings.HasPrefix(string(result.Token), auth.Prefix))
	require.Zero(t, login.Pending(), "a completed login must not stay in flight")

	t.Logf("id token claims: sub=%s iss=%s name=%s email=%s",
		result.Claims.Subject, result.Claims.Issuer, result.Claims.Name, result.Claims.Email)
	t.Logf("session credential: %s… kind=%s subject=%s expires=%s",
		string(result.Token)[:len(auth.Prefix)+8], result.Credential.Kind,
		result.Credential.Subject, result.Credential.ExpiresAt.Format(time.RFC3339))

	// The credential the login produced is what every transport resolves from
	// here. The issuer is not consulted again.
	verifier := auth.NewVerifier(store, nil)

	for _, on := range []auth.Transport{auth.TransportHTTP, auth.TransportSSE} {
		cred, requireErr := Require(t.Context(), verifier, result.Token, on)
		require.NoError(t, requireErr, "a session credential must be accepted on %s", on)
		require.Equal(t, testSubject, cred.Subject)
	}

	_, err = Require(t.Context(), verifier, result.Token, auth.TransportMQTT)
	require.Error(t, err, "a session credential must be refused on MQTT")
}

// The issuer really checks proof of possession. The same code is presented
// twice, once with a verifier that does not derive the challenge and once with
// the one that does.
func TestTheIssuerChecksThePKCEVerifier(t *testing.T) {
	t.Parallel()

	client := newIssuerClient(t)

	matching, err := oidc.NewPKCE()
	require.NoError(t, err)

	other, err := oidc.NewPKCE()
	require.NoError(t, err)
	require.NotEqual(t, matching.Verifier, other.Verifier)

	// A code issued against `matching`'s challenge, replayed with `other`'s verifier.
	badCode, _ := authorize(t, client.AuthorizationURL("state-wrong", "nonce-wrong", matching))

	_, err = client.Exchange(t.Context(), badCode, other)
	require.ErrorIs(t, err, oidc.ErrExchangeRefused,
		"the issuer accepted a verifier that does not derive the challenge")
	t.Logf("wrong verifier: %v", err)

	// The same walk with the matching verifier succeeds, so the refusal above
	// is the verifier check and not a broken request.
	goodCode, _ := authorize(t, client.AuthorizationURL("state-right", "nonce-right", matching))

	tokens, err := client.Exchange(t.Context(), goodCode, matching)
	require.NoError(t, err)
	require.NotEmpty(t, tokens.IDToken)
	t.Logf("matching verifier: id_token %d bytes, token_type=%s, expires_in=%d",
		len(tokens.IDToken), tokens.TokenType, tokens.ExpiresIn)
}

// RFC 9700 says an authorization code is single use, and the issuer is observed
// enforcing it.
func TestAnAuthorizationCodeIsSpentOnce(t *testing.T) {
	t.Parallel()

	client := newIssuerClient(t)

	pkce, err := oidc.NewPKCE()
	require.NoError(t, err)

	code, _ := authorize(t, client.AuthorizationURL("state-once", "nonce-once", pkce))

	_, err = client.Exchange(t.Context(), code, pkce)
	require.NoError(t, err)

	_, err = client.Exchange(t.Context(), code, pkce)
	require.ErrorIs(t, err, oidc.ErrExchangeRefused, "the issuer let one code be spent twice")
	t.Logf("code replay: %v", err)
}

// TestALoginStateIsSpentOnce is this server's half of the same rule, and is
// also what refuses a callback nobody here started.
func TestALoginStateIsSpentOnce(t *testing.T) {
	t.Parallel()

	client := newIssuerClient(t)
	login := NewLogin(client, auth.NewMemoryStore(), nil)

	redirect, err := login.Begin(t.Context())
	require.NoError(t, err)

	code, state := authorize(t, redirect.URL)

	_, err = login.Complete(t.Context(), state, code)
	require.NoError(t, err)

	_, err = login.Complete(t.Context(), state, code)
	require.ErrorIs(t, err, ErrUnknownState)
	require.True(t, auth.Refused(err), "a spent state must refuse in the usual words")

	_, err = login.Complete(t.Context(), "a state this server never issued", code)
	require.ErrorIs(t, err, ErrUnknownState)
}

// TestTheNonceBindsTheIDTokenToThisLogin proves the ID token is checked against
// the request that asked for it, not merely against the issuer's signature.
func TestTheNonceBindsTheIDTokenToThisLogin(t *testing.T) {
	t.Parallel()

	client := newIssuerClient(t)

	pkce, err := oidc.NewPKCE()
	require.NoError(t, err)

	code, _ := authorize(t, client.AuthorizationURL("state-nonce", "the-matching-nonce", pkce))

	tokens, err := client.Exchange(t.Context(), code, pkce)
	require.NoError(t, err)

	claims, err := client.VerifyIDToken(t.Context(), tokens.IDToken, "the-matching-nonce")
	require.NoError(t, err)
	require.Equal(t, testSubject, claims.Subject)

	_, err = client.VerifyIDToken(t.Context(), tokens.IDToken, "a nonce from a different login")
	require.ErrorIs(t, err, oidc.ErrClaimRejected)
	t.Logf("wrong nonce: %v", err)
}

// TestATamperedIDTokenIsRefused proves the signature is verified before a single
// claim is believed. The payload is rewritten to name a different subject and
// re-encoded, which is exactly what an attacker with the token would do.
func TestATamperedIDTokenIsRefused(t *testing.T) {
	t.Parallel()

	client := newIssuerClient(t)

	pkce, err := oidc.NewPKCE()
	require.NoError(t, err)

	code, _ := authorize(t, client.AuthorizationURL("state-tamper", "nonce-tamper", pkce))

	tokens, err := client.Exchange(t.Context(), code, pkce)
	require.NoError(t, err)

	parts := strings.Split(tokens.IDToken, ".")
	require.Len(t, parts, 3)

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)

	var claims map[string]any
	require.NoError(t, json.Unmarshal(payload, &claims))

	claims["sub"] = "somebody-else"
	rewritten, err := json.Marshal(claims)
	require.NoError(t, err)

	forged := parts[0] + "." + base64.RawURLEncoding.EncodeToString(rewritten) + "." + parts[2]

	_, err = client.VerifyIDToken(t.Context(), forged, "nonce-tamper")
	require.ErrorIs(t, err, oidc.ErrBadSignature)
	t.Logf("tampered subject: %v", err)

	// `alg: none`, the other half of the same attack.
	unsigned := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`)) +
		"." + base64.RawURLEncoding.EncodeToString(rewritten) + "."

	_, err = client.VerifyIDToken(t.Context(), unsigned, "nonce-tamper")
	require.ErrorIs(t, err, oidc.ErrUnsupportedAlgorithm)
	t.Logf("alg none: %v", err)
}

// TestALoginAgainstAnUnreachableIssuerRefuses is fail-closed at the login. A
// provider that has gone away produces a refusal, never an admission.
func TestALoginAgainstAnUnreachableIssuerRefuses(t *testing.T) {
	t.Parallel()

	live := newIssuerClient(t)

	// A real issuer that stops answering: the discovery document is served from
	// a local server pointing at the live provider's endpoints, and then that
	// server is closed. Discovery succeeds; the exchange has nowhere to go.
	var stub *httptest.Server

	stub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A handler runs on another goroutine, where a failed require would
		// abort the wrong one.
		if r.URL.Path != discoveryPathForTest {
			t.Errorf("a client asked for %q rather than the well-known path", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(map[string]any{
			"issuer":                           stub.URL,
			"authorization_endpoint":           stub.URL + "/authorize",
			"token_endpoint":                   stub.URL + "/oauth/token",
			"jwks_uri":                         stub.URL + "/.well-known/jwks.json",
			"code_challenge_methods_supported": []string{oidc.ChallengeMethodS256},
		}); err != nil {
			t.Errorf("writing the discovery document: %v", err)
		}
	}))

	client, err := oidc.New(t.Context(), oidc.Config{
		Issuer:      stub.URL,
		ClientID:    defaultClientID,
		RedirectURI: defaultRedirectURI,
		HTTPClient:  noRedirectClient(),
	})
	require.NoError(t, err, "the issuer answered while it was up")

	store := auth.NewMemoryStore()
	login := NewLogin(client, store, nil)

	redirect, err := login.Begin(t.Context())
	require.NoError(t, err)

	// Take the browser half against the provider that is still up, so the code
	// is a matching one and the only thing missing is the issuer at exchange time.
	code, _ := authorize(t, live.AuthorizationURL(redirect.State, "nonce-down", mustPKCE(t)))

	stub.Close()

	_, err = login.Complete(t.Context(), redirect.State, code)
	require.Error(t, err, "an unreachable issuer must refuse the login, not complete it")
	require.True(t, auth.Refused(err))
	require.Empty(t, store.Rows(), "no credential may be issued when the issuer could not be reached")
	t.Logf("issuer unreachable: %v", auth.UnwrapCause(err))
}

// discoveryPathForTest is the well-known path, restated here because the
// constant in the oidc package is unexported and this asserts on the wire.
const discoveryPathForTest = "/.well-known/openid-configuration"

func mustPKCE(t *testing.T) oidc.PKCE {
	t.Helper()

	pkce, err := oidc.NewPKCE()
	require.NoError(t, err)

	return pkce
}

// TestOnlyOneRacingCallbackCompletesALogin. Two browsers, one stolen callback
// URL, or a client that retries: the state must be spent by exactly one of
// them. Without the lock both would read the pending entry and both would get a
// session.
func TestOnlyOneRacingCallbackCompletesALogin(t *testing.T) {
	t.Parallel()

	const racers = 16

	client := newIssuerClient(t)
	store := auth.NewMemoryStore()
	login := NewLogin(client, store, nil)

	redirect, err := login.Begin(t.Context())
	require.NoError(t, err)

	code, state := authorize(t, redirect.URL)

	start := make(chan struct{})
	results := make(chan error, racers)

	var running sync.WaitGroup

	for range racers {
		running.Go(func() {
			<-start

			_, completeErr := login.Complete(t.Context(), state, code)
			results <- completeErr
		})
	}

	close(start)
	running.Wait()
	close(results)

	completed, refused := 0, 0

	for completeErr := range results {
		if completeErr == nil {
			completed++

			continue
		}

		refused++

		require.True(t, auth.Refused(completeErr))
	}

	require.Equal(t, 1, completed, "%d of %d racing callbacks completed the login", completed, racers)
	require.Equal(t, racers-1, refused)
	require.Len(t, store.Rows(), 1, "exactly one credential may exist for one login")
	require.Zero(t, login.Pending())
}

// recordedSubjects is auth.OIDCSubjects in a map, for a test that reads what a
// login wrote.
type recordedSubjects struct {
	mu   sync.Mutex
	rows map[string]auth.OIDCSubject
}

func (r *recordedSubjects) Record(_ context.Context, subject auth.OIDCSubject) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.rows == nil {
		r.rows = make(map[string]auth.OIDCSubject)
	}

	r.rows[subject.Principal] = subject

	return nil
}

func (r *recordedSubjects) Get(_ context.Context, principal string) (auth.OIDCSubject, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	found, ok := r.rows[principal]
	if !ok {
		return auth.OIDCSubject{}, auth.ErrNoOIDCSubject
	}

	return found, nil
}

// discoveryOnlyClient is a client for an issuer that answers discovery and
// nothing else. Begin never calls the issuer, so the tests of the table of
// logins in flight need no running provider.
func discoveryOnlyClient(t *testing.T) *oidc.Client {
	t.Helper()

	var stub *httptest.Server

	stub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(map[string]any{
			"issuer":                           stub.URL,
			"authorization_endpoint":           stub.URL + "/authorize",
			"token_endpoint":                   stub.URL + "/oauth/token",
			"jwks_uri":                         stub.URL + "/.well-known/jwks.json",
			"code_challenge_methods_supported": []string{oidc.ChallengeMethodS256},
		}); err != nil {
			t.Errorf("writing the discovery document: %v", err)
		}
	}))
	t.Cleanup(stub.Close)

	client, err := oidc.New(t.Context(), oidc.Config{
		Issuer:      stub.URL,
		ClientID:    defaultClientID,
		RedirectURI: defaultRedirectURI,
		HTTPClient:  noRedirectClient(),
	})
	require.NoError(t, err)

	return client
}

// clock is a time a test moves by hand.
type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func fillPending(t *testing.T, login *Login, n int) {
	t.Helper()

	for range n {
		_, err := login.Begin(t.Context())
		require.NoError(t, err)
	}
}

// TestAFullTableRefusesANewLoginAndKeepsTheWaitingOnes bounds the memory a
// caller with no credential can make the server hold. The new login is refused
// and the waiting ones are kept, because each of them may be a person halfway
// through the issuer's screens.
func TestAFullTableRefusesANewLoginAndKeepsTheWaitingOnes(t *testing.T) {
	t.Parallel()

	start := &clock{at: time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)}
	login := NewLogin(discoveryOnlyClient(t), auth.NewMemoryStore(), start.now)

	first, err := login.Begin(t.Context())
	require.NoError(t, err)

	fillPending(t, login, MaxPending-1)
	require.Equal(t, MaxPending, login.Pending())

	start.at = start.at.Add(PendingTTL - time.Second)

	_, err = login.Begin(t.Context())
	require.ErrorIs(t, err, ErrTooManyPending)
	require.Equal(t, MaxPending, login.Pending(), "a refused login must not push out one in flight")

	login.mu.Lock()
	_, kept := login.pending[first.State]
	login.mu.Unlock()
	require.True(t, kept, "the oldest login was dropped to make room")
}

// TestAFullTableTakesBackExpiredLogins shows that the cap refuses only while
// the logins it holds are still live. Once they expire, Begin reclaims them
// without waiting for the next periodic sweep.
func TestAFullTableTakesBackExpiredLogins(t *testing.T) {
	t.Parallel()

	start := &clock{at: time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)}
	login := NewLogin(discoveryOnlyClient(t), auth.NewMemoryStore(), start.now)

	fillPending(t, login, MaxPending)

	start.at = start.at.Add(PendingTTL + time.Second)

	_, err := login.Begin(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, login.Pending(), "the expired logins were not taken back")
}

// TestBeginSweepsAtMostOncePerInterval keeps a burst of logins from costing
// the square of its size. A login that expired is kept until sweepInterval has
// passed since the last sweep, as long as the table has room.
func TestBeginSweepsAtMostOncePerInterval(t *testing.T) {
	t.Parallel()

	start := &clock{at: time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)}
	login := NewLogin(discoveryOnlyClient(t), auth.NewMemoryStore(), start.now)
	origin := start.at

	// The first Begin sweeps and starts the interval.
	fillPending(t, login, 1)

	// This one sweeps too, a whole interval later, and finds nothing expired.
	start.at = origin.Add(PendingTTL - 10*time.Second)
	fillPending(t, login, 1)

	// The first login has now expired, but the last sweep was 15 seconds ago.
	start.at = origin.Add(PendingTTL + 5*time.Second)
	fillPending(t, login, 1)
	require.Equal(t, 3, login.Pending(), "Begin swept again inside the interval")

	// One interval after the last sweep, the expired login goes.
	start.at = origin.Add(PendingTTL - 10*time.Second + sweepInterval)
	fillPending(t, login, 1)
	require.Equal(t, 3, login.Pending(), "the expired login outlived the next sweep")
}
