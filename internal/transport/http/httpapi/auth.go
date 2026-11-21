package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/prusa3d/prusa-connect/internal/transport/http/httpfault"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// The login ceremony's paths. The first two carry the empty security
// requirement, because a caller who already had a credential would not be
// asking for one.
const (
	loginPath    = "/api/v1/auth/login"
	callbackPath = "/api/v1/auth/callback"
	logoutPath   = "/api/v1/auth/logout"
)

// Starting a login takes no credential and makes the server remember a state
// for ten minutes, so one address may start only so many. The bound sits well
// above what a person clicking "sign in" reaches, and well below what fills
// the server's table of logins in flight.
const (
	loginsPerAddress  = 30
	loginAddressSpan  = 10 * time.Minute
	loginAddressTable = 4096
)

// loginsBusyWait is the Retry-After a caller gets when the server already
// holds as many logins in flight as it will. Begin reclaims every expired
// login while the table is full, so a minute is enough for some to finish or
// expire.
const loginsBusyWait = time.Minute

// postLoginPath is where a completed login leaves the browser. It is the
// application root and not an operation: the person who started the login
// wanted the application, not a JSON document about their session.
const postLoginPath = "/"

// cookiePath scopes the session cookie to the whole origin, because the browser
// sends it to /api/v1 and to whatever serves the application.
const cookiePath = "/"

// Sessions is the browser login: an authorization code flow with PKCE against
// an issuer, ending in a credential this server minted. It is declared here
// because this is where it is consumed. The composition root satisfies it,
// because only the composition root may name both an issuer and a credential
// store.
//
// No flag switches it off. A deployment with no issuer wires an
// implementation that refuses to begin a login, the same way a deployment
// with no authentication wires a Principals that resolves nobody.
type Sessions interface {
	// Begin starts a login and says where to send the browser. An error with
	// a Busy method that reports true means it holds as many logins in flight
	// as it will, and the caller is answered 429.
	Begin(ctx context.Context) (Redirect, error)
	// Complete turns the issuer's callback into a credential this server
	// issued. Every refusal it gives reads the same, whatever caused it.
	Complete(ctx context.Context, state, code string) (Session, error)
	// Revoke ends the session a request carries. It takes the whole request
	// because the implementation decides where a credential travels.
	// Principals takes a request for the same reason.
	Revoke(ctx context.Context, r *http.Request) error
	// Cookie is the name of the cookie that carries a session credential. The
	// implementation owns it, so the name set here and the name read when
	// identity is resolved are always the same.
	Cookie() string
}

// SignInError is a login this server started that did not end in a session:
// the provider refused it, or its ID token or subject was refused here. It is
// told apart from a callback this server did not start, which stays a 401,
// because a person is waiting in the browser and the operator needs the cause.
type SignInError struct {
	// Reason is one of the SignInReason values. It reaches the browser.
	Reason string
	// Cause is what failed. It reaches the log and never the browser.
	Cause error
}

// The reasons a sign-in fails, as the sign-in page reads them from the
// sign_in_error query parameter.
const (
	// SignInReasonProvider is a provider that refused the login or the code.
	SignInReasonProvider = "provider"
	// SignInReasonIDToken is an ID token this server could not accept: its
	// algorithm, its signature or its claims.
	SignInReasonIDToken = "id_token"
	// SignInReasonSubject is a subject this server will not take.
	SignInReasonSubject = "subject"
	// SignInReasonExpired is a login the person took too long to finish.
	SignInReasonExpired = "expired"
	// SignInReasonFailed is anything else, such as a provider out of reach.
	SignInReasonFailed = "failed"
)

func (e *SignInError) Error() string { return "sign-in failed (" + e.Reason + "): " + e.Cause.Error() }

func (e *SignInError) Unwrap() error { return e.Cause }

// signInFailedPath is where a failed sign-in leaves the browser: the sign-in
// page, with the reason it shows.
func signInFailedPath(reason string) string {
	return postLoginPath + "?sign_in_error=" + url.QueryEscape(reason)
}

// Redirect is where a login sends the browser.
type Redirect struct {
	// URL is the issuer's authorization endpoint with this login's parameters.
	URL string
}

// Session is a completed login. Token is returned once and is never logged.
type Session struct {
	// Token is the credential the browser carries afterwards.
	Token string
	// ExpiresAt bounds the cookie, so a browser drops it when the server would
	// have refused it anyway.
	ExpiresAt time.Time
}

// beginLoginOutput sends the browser to the issuer. The status is 303 and not
// 302, because a 303 always makes the browser follow with GET.
type beginLoginOutput struct {
	Location string `header:"Location" doc:"The issuer's authorization endpoint, carrying this login's state, nonce and PKCE challenge."`
}

// callbackInput is what the issuer sends the browser back with. Neither
// parameter is declared required. A login the person refused comes back
// without a code, and a schema violation in answer would tell a caller which
// half of the callback this server refused.
type callbackInput struct {
	State string `query:"state" doc:"The value this server minted when the login began."                     example:"n8Qv3LxT0pZ9kW2rY6mB4c"`
	Code  string `query:"code"  doc:"The authorization code to exchange."                                    example:"SplxlOBeZQQYbYS6WxSbIA"`
	Error string `query:"error" doc:"The error the issuer refused the login with, RFC 6749 section 4.1.2.1." example:"access_denied"`
}

// callbackOutput sets the credential and returns the browser to the
// application. The cookie is HttpOnly, so no script can read it, and
// SameSite=Lax, so it does not ride a cross-site form post. A failed sign-in
// expires the cookie instead and returns the browser to the sign-in page.
type callbackOutput struct {
	Location  string      `header:"Location"   doc:"Where the browser goes once the session is set."`
	SetCookie http.Cookie `header:"Set-Cookie" doc:"The session credential, HttpOnly and SameSite=Lax."`
}

// logoutOutput clears the cookie whether or not there was a credential behind
// it, so a browser holding one this server has already forgotten does not keep
// presenting it.
type logoutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie" doc:"The session cookie, expired."`
}

// registerAuth publishes the login ceremony. The operations are registered
// whatever the process was wired with, so the document a client generates from
// does not depend on whether this deployment has an issuer.
func registerAuth(api huma.API, cfg Config) {
	limiter := newAddressLimiter(loginsPerAddress, loginAddressSpan, loginAddressTable)

	registerBeginLogin(api, cfg, limiter)
	registerCompleteLogin(api, cfg)
	registerLogout(api, cfg)
}

func registerBeginLogin(api huma.API, cfg Config, limiter *addressLimiter) {
	register(api, huma.Operation{
		OperationID: "begin-login",
		Method:      http.MethodGet,
		Path:        loginPath,
		Summary:     "Start a login",
		Description: "Redirects to the configured issuer with a fresh state, nonce and PKCE challenge. " +
			"The verifier stays on this server, which is what binds the callback to the request that " +
			"started it. A deployment with no issuer refuses here rather than admitting the caller. " +
			"One address may start 30 logins in ten minutes. Past that, or while the server holds as " +
			"many unfinished logins as it will, the answer is 429 with Retry-After.",
		Tags:          []string{"auth"},
		Security:      noSecurity,
		DefaultStatus: http.StatusSeeOther,
		Errors:        []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusInternalServerError},
	}, func(ctx context.Context, _ *struct{}) (*beginLoginOutput, error) {
		address := clientAddress(cfg, requestFrom(ctx))
		if address != "" && !limiter.take(address, time.Now()) {
			return nil, beginLimitedFault("this address has started too many logins", loginAddressSpan)
		}

		redirect, err := cfg.Sessions.Begin(ctx)
		if err != nil {
			return nil, beginFault(err)
		}

		return &beginLoginOutput{Location: redirect.URL}, nil
	})
}

func registerCompleteLogin(api huma.API, cfg Config) {
	register(api, huma.Operation{
		OperationID: "complete-login",
		Method:      http.MethodGet,
		Path:        callbackPath,
		Summary:     "Finish a login",
		Description: "Exchanges the issuer's authorization code, checks the ID token against the login " +
			"that started it, and sets the session credential as an HttpOnly cookie. The issuer's own " +
			"tokens are not kept and are never presented anywhere. A login this server started that " +
			"fails, because the issuer refused it or its ID token or subject was refused here, returns " +
			"the browser to `/?sign_in_error=<reason>` with the cookie expired, and the log says why. " +
			"A callback for a login this server did not start answers 401.",
		Tags:          []string{"auth"},
		Security:      noSecurity,
		DefaultStatus: http.StatusSeeOther,
		Errors:        []int{http.StatusUnauthorized},
	}, func(ctx context.Context, in *callbackInput) (*callbackOutput, error) {
		if in.Error != "" {
			cfg.Logger.WarnContext(ctx, "the identity provider refused the sign-in", "error", in.Error)

			return &callbackOutput{
				Location:  signInFailedPath(SignInReasonProvider),
				SetCookie: expiredCookie(cfg),
			}, nil
		}

		session, err := cfg.Sessions.Complete(ctx, in.State, in.Code)
		if failed, ok := errors.AsType[*SignInError](err); ok {
			cfg.Logger.WarnContext(ctx, "a sign-in at the identity provider failed",
				"reason", failed.Reason, "cause", failed.Cause)

			return &callbackOutput{
				Location:  signInFailedPath(failed.Reason),
				SetCookie: expiredCookie(cfg),
			}, nil
		}

		if err != nil {
			cfg.Logger.WarnContext(ctx, "a sign-in callback was refused", "cause", err)

			return nil, identityFault(err)
		}

		return &callbackOutput{
			Location:  postLoginPath,
			SetCookie: sessionCookie(cfg, session),
		}, nil
	})
}

func registerLogout(api huma.API, cfg Config) {
	register(api, huma.Operation{
		OperationID: "logout",
		Method:      http.MethodPost,
		Path:        logoutPath,
		Summary:     "End this session",
		Description: "Revokes the credential the request carries and expires the cookie. The credential " +
			"is refused on the next request rather than at its next expiry, which is the property an " +
			"opaque credential exists to have.",
		Tags:   []string{"auth"},
		Errors: []int{http.StatusUnauthorized},
	}, func(ctx context.Context, _ *struct{}) (*logoutOutput, error) {
		request := requestFrom(ctx)
		if request == nil {
			return nil, httpfault.Unexpected("the logout handler ran outside the operation middleware")
		}

		if err := cfg.Sessions.Revoke(ctx, request); err != nil {
			return nil, identityFault(err)
		}

		return &logoutOutput{SetCookie: expiredCookie(cfg)}, nil
	})
}

// sessionCookie is the credential as a browser holds it. Secure is on unless
// the deployment told this server its own callback is plain HTTP. A browser
// never sends a Secure cookie back to an origin it reaches over http. The
// scheme comes from the redirect URI, so there is no second switch to set.
func sessionCookie(cfg Config, session Session) http.Cookie {
	//nolint:gosec // G124: Secure follows the deployment's own callback scheme, for the reason above.
	return http.Cookie{
		Name:     cfg.Sessions.Cookie(),
		Value:    session.Token,
		Path:     cookiePath,
		Expires:  session.ExpiresAt,
		HttpOnly: true,
		Secure:   !cfg.InsecureCookies,
		SameSite: http.SameSiteLaxMode,
	}
}

// expiredCookie is the same cookie with nothing in it. Every attribute except
// the value matches the one that was set, because a browser matches on name,
// path and domain when it decides which cookie is being replaced.
func expiredCookie(cfg Config) http.Cookie {
	//nolint:gosec // G124: Secure follows the deployment's own callback scheme, for the reason above.
	return http.Cookie{
		Name:     cfg.Sessions.Cookie(),
		Value:    "",
		Path:     cookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   !cfg.InsecureCookies,
		SameSite: http.SameSiteLaxMode,
	}
}

// beginFault keeps a refusal the implementation raised and turns anything else
// into the server's own failure. A login that could not start because the
// process has no issuer is a refusal. A login that could not start because the
// random source failed is the server's failure, and a refusal would send the
// caller to check their credentials.
//
// An error with a Busy method that reports true is a server holding as many
// logins in flight as it will. The caller can try again later, so it is a 429
// and not the server's failure.
func beginFault(err error) error {
	if raised, ok := errors.AsType[*fault.Fault](err); ok {
		return raised
	}

	if busy, ok := errors.AsType[interface {
		error
		Busy() bool
	}](err); ok && busy.Busy() {
		return beginLimitedFault("too many logins are waiting on this server", loginsBusyWait)
	}

	return httpfault.Unexpected("start a login")
}

// beginLimitedFault answers a login that may not start yet with the wait, in
// the body and in Retry-After.
func beginLimitedFault(detail string, wait time.Duration) error {
	seconds := int64(wait / time.Second)
	limited := loginLimited.New(detail).With(fault.IntParam(RetryAfterParam, seconds))

	return huma.ErrorWithHeaders(limited, http.Header{"Retry-After": []string{strconv.FormatInt(seconds, 10)}})
}
