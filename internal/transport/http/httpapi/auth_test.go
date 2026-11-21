package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/transport/http/httpfault"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// testCookieName is the name the fake session implementation owns. The real one
// comes from the package that mints credentials, which is the point of the
// Cookie method: this package never writes the name down.
const testCookieName = "pcl_session"

// fakeSessions is a login ceremony with the issuer taken out. Begin hands back
// a URL, Complete accepts one state and one code, and Revoke records what it
// was asked to end.
type fakeSessions struct {
	redirect  string
	state     string
	code      string
	token     string
	expiresAt time.Time

	beginErr    error
	revokeErr   error
	completeErr error

	revoked []string
}

func (s *fakeSessions) Begin(context.Context) (Redirect, error) {
	if s.beginErr != nil {
		return Redirect{}, s.beginErr
	}

	return Redirect{URL: s.redirect}, nil
}

func (s *fakeSessions) Complete(_ context.Context, state, code string) (Session, error) {
	if s.completeErr != nil {
		return Session{}, s.completeErr
	}

	if state != s.state || code != s.code {
		return Session{}, fault.AuthTokenInvalid.New("the credential is not one this server accepts")
	}

	return Session{Token: s.token, ExpiresAt: s.expiresAt}, nil
}

func (s *fakeSessions) Revoke(_ context.Context, r *http.Request) error {
	if s.revokeErr != nil {
		return s.revokeErr
	}

	cookie, err := r.Cookie(testCookieName)
	if err != nil {
		//nolint:nilerr // a request carrying no cookie has no session to end.
		return nil
	}

	s.revoked = append(s.revoked, cookie.Value)

	return nil
}

func (s *fakeSessions) Cookie() string { return testCookieName }

func newFakeSessions() *fakeSessions {
	return &fakeSessions{
		redirect:  "https://issuer.invalid/authorize?state=s1&code_challenge=abc",
		state:     "s1",
		code:      "c1",
		token:     "pcl_tokenvalue",
		expiresAt: time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC),
	}
}

func TestBeginningALoginRedirectsToTheIssuer(t *testing.T) {
	t.Parallel()

	sessions := newFakeSessions()
	rec := call(t, serverWithSessions(t, sessions, false).Handler(), http.MethodGet, loginPath, "")

	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, sessions.redirect, rec.Header().Get("Location"))
	require.Empty(t, rec.Header().Values("Set-Cookie"), "a login that has not happened sets no credential")
}

// TestTheLoginRoutesTakeNoIdentity is what lets a caller with no credential
// reach the one operation that hands out credentials. It reads the published
// document rather than a list beside the router, which is where the middleware
// reads it from too.
func TestTheLoginRoutesTakeNoIdentity(t *testing.T) {
	t.Parallel()

	handler := serverWithoutIdentity(t).Handler()

	for name, path := range map[string]string{
		"begin":    loginPath,
		"callback": callbackPath + "?state=s1&code=c1",
	} {
		rec := call(t, handler, http.MethodGet, path, "")
		require.Equalf(t, http.StatusSeeOther, rec.Code,
			"%s must complete for a caller the server can identify no other way", name)
	}
}

func TestALoginNobodyStartedIsRefused(t *testing.T) {
	t.Parallel()

	sessions := newFakeSessions()
	handler := serverWithSessions(t, sessions, false).Handler()

	rec := call(t, handler, http.MethodGet, callbackPath+"?state=forged&code=c1", "")

	body := requireProblem(t, rec, http.StatusUnauthorized)
	require.Equal(t, fault.AuthTokenInvalid.Code(), body.Code)
	require.Empty(t, rec.Header().Values("Set-Cookie"), "a refused callback sets no credential")
}

// TestARefusedCallbackReadsTheSameWhicheverHalfWasWrong is the property the
// auth package fought for, held at the transport: naming which half failed
// tells a forger which one to keep.
func TestARefusedCallbackReadsTheSameWhicheverHalfWasWrong(t *testing.T) {
	t.Parallel()

	handler := serverWithSessions(t, newFakeSessions(), false).Handler()

	wrongState := call(t, handler, http.MethodGet, callbackPath+"?state=nope&code=c1", "")
	wrongCode := call(t, handler, http.MethodGet, callbackPath+"?state=s1&code=nope", "")
	neither := call(t, handler, http.MethodGet, callbackPath, "")

	require.Equal(t, http.StatusUnauthorized, wrongState.Code)
	require.Equal(t, redactTrace(wrongState.Body.String()), redactTrace(wrongCode.Body.String()))
	require.Equal(t, redactTrace(wrongState.Body.String()), redactTrace(neither.Body.String()))
}

func TestACompletedLoginSetsTheSessionCookie(t *testing.T) {
	t.Parallel()

	sessions := newFakeSessions()
	rec := call(t, serverWithSessions(t, sessions, false).Handler(),
		http.MethodGet, callbackPath+"?state=s1&code=c1", "")

	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, postLoginPath, rec.Header().Get("Location"))

	cookie := requireSetCookie(t, rec)

	require.Equal(t, sessions.token, cookie.Value)
	require.True(t, cookie.HttpOnly, "a script must not be able to read a session credential")
	require.True(t, cookie.Secure, "the default deployment is https")
	require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	require.Equal(t, cookiePath, cookie.Path)
	require.Equal(t, sessions.expiresAt, cookie.Expires.UTC())
}

// TestAPlainHTTPDeploymentSetsACookieTheBrowserWillSendBack pins the one thing
// InsecureCookies decides. A Secure cookie on an origin a browser reaches over
// http is one the browser never returns, so the login would appear to succeed
// and every request afterwards would be anonymous.
func TestAPlainHTTPDeploymentSetsACookieTheBrowserWillSendBack(t *testing.T) {
	t.Parallel()

	rec := call(t, serverWithSessions(t, newFakeSessions(), true).Handler(),
		http.MethodGet, callbackPath+"?state=s1&code=c1", "")

	cookie := requireSetCookie(t, rec)

	require.False(t, cookie.Secure)
	require.True(t, cookie.HttpOnly, "plain http costs the Secure attribute and nothing else")
	require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
}

func TestLoggingOutRevokesTheCredentialItWasSent(t *testing.T) {
	t.Parallel()

	sessions := newFakeSessions()
	server := serverWithSessions(t, sessions, false)

	req := httptest.NewRequest(http.MethodPost, logoutPath, nil)
	req.AddCookie(&http.Cookie{Name: testCookieName, Value: "pcl_theonepresented"})

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, []string{"pcl_theonepresented"}, sessions.revoked)

	cookie := requireSetCookie(t, rec)
	require.Empty(t, cookie.Value)
	require.Equal(t, -1, cookie.MaxAge, "the browser is told to drop it now")
}

func TestLoggingOutNeedsAnIdentity(t *testing.T) {
	t.Parallel()

	rec := call(t, serverWithoutIdentity(t).Handler(), http.MethodPost, logoutPath, "")

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestAServerWithNoIssuerRefusesToBeginALogin is the shape that replaces an
// enabled flag: the deployment without a provider wires one that refuses, and
// the refusal is a credential refusal rather than a missing route.
func TestAServerWithNoIssuerRefusesToBeginALogin(t *testing.T) {
	t.Parallel()

	sessions := newFakeSessions()
	sessions.beginErr = fault.AuthTokenInvalid.New("the credential is not one this server accepts")

	rec := call(t, serverWithSessions(t, sessions, false).Handler(), http.MethodGet, loginPath, "")

	body := requireProblem(t, rec, http.StatusUnauthorized)
	require.Equal(t, fault.AuthTokenInvalid.Code(), body.Code)
}

// TestALoginThatFailedForServerReasonsIsNotACredentialRefusal keeps a caller
// from being sent to look at credentials that were never the problem.
func TestALoginThatFailedForServerReasonsIsNotACredentialRefusal(t *testing.T) {
	t.Parallel()

	sessions := newFakeSessions()
	sessions.beginErr = errors.New("the random source failed")

	rec := call(t, serverWithSessions(t, sessions, false).Handler(), http.MethodGet, loginPath, "")

	body := requireProblem(t, rec, http.StatusInternalServerError)
	require.Equal(t, httpfault.CodeUnexpected(), body.Code)
}

// TestOneAddressMayStartOnlySoManyLogins keeps a caller with no credential
// from filling the server's table of logins in flight from one address. Another
// address is still let through.
func TestOneAddressMayStartOnlySoManyLogins(t *testing.T) {
	t.Parallel()

	handler := serverWithSessions(t, newFakeSessions(), false).Handler()

	for attempt := range loginsPerAddress {
		rec := call(t, handler, http.MethodGet, loginPath, "")
		require.Equalf(t, http.StatusSeeOther, rec.Code, "attempt %d was refused inside the allowance", attempt+1)
	}

	rec := call(t, handler, http.MethodGet, loginPath, "")
	body := requireProblem(t, rec, http.StatusTooManyRequests)
	require.Equal(t, loginLimited.Code(), body.Code)
	require.Equal(t, strconv.Itoa(int(loginAddressSpan/time.Second)), rec.Header().Get("Retry-After"))

	other := httptest.NewRequest(http.MethodGet, loginPath, nil)
	other.RemoteAddr = "198.51.100.7:4321"
	otherRec := httptest.NewRecorder()
	handler.ServeHTTP(otherRec, other)
	require.Equal(t, http.StatusSeeOther, otherRec.Code, "one address's limit refused another address")
}

// busyLoginsError is how a Sessions implementation reports that it holds as many
// logins in flight as it will.
type busyLoginsError struct{}

func (busyLoginsError) Error() string { return "too many logins are in flight" }

func (busyLoginsError) Busy() bool { return true }

// TestAServerFullOfLoginsAsksTheCallerToWait keeps a full table from reading
// as the server's failure. The caller can start a login again once some finish
// or expire.
func TestAServerFullOfLoginsAsksTheCallerToWait(t *testing.T) {
	t.Parallel()

	sessions := newFakeSessions()
	sessions.beginErr = fmt.Errorf("begin: %w", busyLoginsError{})

	rec := call(t, serverWithSessions(t, sessions, false).Handler(), http.MethodGet, loginPath, "")

	body := requireProblem(t, rec, http.StatusTooManyRequests)
	require.Equal(t, loginLimited.Code(), body.Code)
	require.True(t, body.Retryable)
	require.Equal(t, strconv.Itoa(int(loginsBusyWait/time.Second)), rec.Header().Get("Retry-After"))
}

func requireSetCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()

	response := http.Response{Header: rec.Header()}

	for _, cookie := range response.Cookies() {
		if cookie.Name == testCookieName {
			return cookie
		}
	}

	t.Fatalf("no %q cookie in %v", testCookieName, rec.Header().Values("Set-Cookie"))

	return nil
}

// redactTrace removes the one member two otherwise identical refusals differ
// in, so the comparison is of what the refusal says rather than of which
// request said it.
func redactTrace(body string) string {
	const marker = `"trace_id":"`

	start := strings.Index(body, marker)
	if start < 0 {
		return body
	}

	end := strings.Index(body[start+len(marker):], `"`)
	if end < 0 {
		return body
	}

	return body[:start+len(marker)] + body[start+len(marker)+end:]
}

// TestTheDocumentNamesTheCookieTheLoginActuallySets is the half a scheme entry
// cannot hold on its own. A published cookie name is a client instruction: a
// browser sends back what it was given, and a document naming a different
// cookie tells a reader to look for one nothing sets.
//
// Both halves come from one running server. The document is fetched over HTTP
// and the cookie is taken from the Set-Cookie the callback wrote, so a name
// written out in either place instead of read from the login ceremony fails.
func TestTheDocumentNamesTheCookieTheLoginActuallySets(t *testing.T) {
	t.Parallel()

	handler := serverWithSessions(t, newFakeSessions(), false).Handler()

	login := call(t, handler, http.MethodGet, callbackPath+"?state=s1&code=c1", "")
	require.Equal(t, http.StatusSeeOther, login.Code)

	set := requireSetCookie(t, login)

	spec := call(t, handler, http.MethodGet, specJSONPath, "")
	require.Equal(t, http.StatusOK, spec.Code)

	var document struct {
		Components struct {
			SecuritySchemes map[string]struct {
				Type string `json:"type"`
				In   string `json:"in"`
				Name string `json:"name"`
			} `json:"securitySchemes"`
		} `json:"components"`
	}

	require.NoError(t, json.Unmarshal(spec.Body.Bytes(), &document))

	scheme, published := document.Components.SecuritySchemes[cookieScheme]
	require.True(t, published, "the document names no cookie scheme")
	require.Equal(t, "apiKey", scheme.Type)
	require.Equal(t, "cookie", scheme.In)
	require.Equal(t, set.Name, scheme.Name,
		"the document names a cookie the login does not set")
}

// TestAFailedSignInReturnsToTheSignInPageWithItsReason: a login this server
// started that fails sends the person back to the sign-in page, which says
// why, and sets no credential.
func TestAFailedSignInReturnsToTheSignInPageWithItsReason(t *testing.T) {
	t.Parallel()

	sessions := newFakeSessions()
	sessions.completeErr = &SignInError{
		Reason: SignInReasonIDToken,
		Cause:  errors.New("signed with HS256, which is checked with the client secret"),
	}

	rec := call(t, serverWithSessions(t, sessions, false).Handler(),
		http.MethodGet, callbackPath+"?state=s1&code=c1", "")

	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "/?sign_in_error=id_token", rec.Header().Get("Location"))

	cookie := requireSetCookie(t, rec)
	require.Empty(t, cookie.Value, "a failed sign-in sets no credential")
	require.Negative(t, cookie.MaxAge)
}

// TestAProviderRefusalReturnsToTheSignInPage: a person who refused consent at
// the provider comes back with an error and no code.
func TestAProviderRefusalReturnsToTheSignInPage(t *testing.T) {
	t.Parallel()

	rec := call(t, serverWithSessions(t, newFakeSessions(), false).Handler(),
		http.MethodGet, callbackPath+"?state=s1&error=access_denied", "")

	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "/?sign_in_error=provider", rec.Header().Get("Location"))
}
