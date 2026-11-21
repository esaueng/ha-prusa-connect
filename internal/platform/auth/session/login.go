package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/prusa3d/prusa-connect/internal/platform/auth"
	"github.com/prusa3d/prusa-connect/internal/platform/auth/oidc"
)

// Errors reported by a login. None of them reaches a client as a difference:
// the handler above renders one refusal.
var (
	// ErrUnknownState reports a callback whose state this server did not start,
	// or started and has already consumed. It covers a stale bookmark, a replay
	// and a cross-site forgery alike.
	ErrUnknownState = errors.New("session: unknown or spent login state")
	// ErrLoginExpired reports a login that took longer than PendingTTL.
	ErrLoginExpired = errors.New("session: login expired before the callback")
	// ErrTooManyPending reports a login refused because MaxPending logins are
	// already in flight and none of them has expired. Begin refuses the new
	// login and keeps the ones that are waiting.
	ErrTooManyPending error = tooManyPendingError{}
)

// tooManyPendingError is ErrTooManyPending. It has a Busy method because the HTTP
// transport may not import this package, and it still has to answer 429 and
// not 500.
type tooManyPendingError struct{}

func (tooManyPendingError) Error() string { return "session: too many logins are in flight" }

// Busy reports that the refusal ends once logins in flight finish or expire.
func (tooManyPendingError) Busy() bool { return true }

const (
	// PendingTTL bounds how long an unfinished login is remembered. A user has
	// to get through the issuer's screens in this time, and an abandoned login
	// costs nothing after it.
	PendingTTL = 10 * time.Minute

	// MaxPending bounds how many logins wait for a callback at once. Begin
	// takes no credential, so without a bound a caller could fill memory by
	// starting logins and never finishing them.
	MaxPending = 1024

	// sweepInterval is the least time between two sweeps of expired logins
	// while the table has room. A sweep reads every entry, so running it on
	// every Begin would make a burst of logins cost the square of its size.
	sweepInterval = 30 * time.Second

	// stateBytes and nonceBytes are 32 bytes each, which is 256 bits. Both
	// values exist to be unguessable.
	stateBytes = 32
	nonceBytes = 32
)

// Login starts an authorization code flow with PKCE and turns a successful
// callback into a session credential this server issued.
//
// The issuer establishes who somebody is, once. Everything afterwards resolves
// the credential locally, so a login does not put the issuer in the path of
// every request.
type Login struct {
	client   *oidc.Client
	store    auth.Store
	subjects auth.OIDCSubjects
	now      func() time.Time

	mu        sync.Mutex
	pending   map[string]pendingLogin
	lastSweep time.Time
}

// Redirect is where the browser is sent to authenticate.
type Redirect struct {
	URL   string
	State string
}

// Result is a completed login.
type Result struct {
	// Token is the session credential, returned once. The handler above sets it
	// as an HttpOnly cookie and does not log it.
	Token      auth.Token
	Credential auth.Credential
	Claims     oidc.Claims
	// Principal is the bare user id the subject maps to.
	Principal string
	// Profile is what the issuer says the person is called.
	Profile oidc.Profile
	// ProfileErr is why the userinfo endpoint could not complete Profile. The
	// login stands without it.
	ProfileErr error
}

// pendingLogin is one login in flight. The verifier and the nonce never leave
// this process, and they bind the callback to the request that started it.
type pendingLogin struct {
	pkce    oidc.PKCE
	nonce   string
	started time.Time
}

// NewLogin returns a login runner over an issuer and a credential store. A nil
// clock means time.Now.
func NewLogin(client *oidc.Client, store auth.Store, now func() time.Time) *Login {
	if now == nil {
		now = time.Now
	}

	return &Login{client: client, store: store, now: now, pending: make(map[string]pendingLogin)}
}

// WithSubjects returns a login runner that records every person who signs in,
// so an id derived from a subject can be read back as that subject and a name.
func (l *Login) WithSubjects(subjects auth.OIDCSubjects) *Login {
	l.subjects = subjects

	return l
}

// Begin starts a login and returns where to send the browser. It returns
// ErrTooManyPending when MaxPending logins are in flight and none has expired.
func (l *Login) Begin(_ context.Context) (Redirect, error) {
	pkce, err := oidc.NewPKCE()
	if err != nil {
		return Redirect{}, err
	}

	state, err := randomURLSafe(stateBytes)
	if err != nil {
		return Redirect{}, fmt.Errorf("mint login state: %w", err)
	}

	nonce, err := randomURLSafe(nonceBytes)
	if err != nil {
		return Redirect{}, fmt.Errorf("mint login nonce: %w", err)
	}

	now := l.now()

	if err = l.remember(state, pendingLogin{pkce: pkce, nonce: nonce, started: now}); err != nil {
		return Redirect{}, err
	}

	return Redirect{URL: l.client.AuthorizationURL(state, nonce, pkce), State: state}, nil
}

// remember records a login in flight, or refuses it when the table is full of
// logins that have not expired. A full table refuses the newcomer rather than
// drop a login a person is still finishing.
func (l *Login) remember(state string, entry pendingLogin) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.pending) >= MaxPending || entry.started.Sub(l.lastSweep) >= sweepInterval {
		l.sweep(entry.started)
	}

	if len(l.pending) >= MaxPending {
		return ErrTooManyPending
	}

	l.pending[state] = entry

	return nil
}

// Complete finishes a login: it consumes the state, exchanges the code for
// tokens while proving the PKCE verifier, checks the ID token, and issues a
// session credential for the subject the issuer named.
//
// The state is consumed whether or not the rest succeeds, so a code cannot be
// presented twice against one login.
func (l *Login) Complete(ctx context.Context, state, code string) (Result, error) {
	now := l.now()

	l.mu.Lock()
	entry, ok := l.pending[state]
	delete(l.pending, state)
	l.mu.Unlock()

	if !ok {
		return Result{}, auth.Refuse(ErrUnknownState)
	}

	if now.Sub(entry.started) > PendingTTL {
		return Result{}, auth.Refuse(fmt.Errorf("%w after %s", ErrLoginExpired, now.Sub(entry.started)))
	}

	tokens, err := l.client.Exchange(ctx, code, entry.pkce)
	if err != nil {
		return Result{}, auth.Refuse(err)
	}

	claims, err := l.client.VerifyIDToken(ctx, tokens.IDToken, entry.nonce)
	if err != nil {
		return Result{}, auth.Refuse(err)
	}

	if err = oidc.CheckSubject(claims.Subject); err != nil {
		return Result{}, auth.Refuse(err)
	}

	principal := l.client.PrincipalID(claims.Subject)
	profile, profileErr := l.client.Profile(ctx, claims, tokens.AccessToken)

	if l.subjects != nil {
		err = l.subjects.Record(ctx, auth.OIDCSubject{
			Principal:   principal,
			Issuer:      l.client.ConfiguredIssuer(),
			Subject:     claims.Subject,
			DisplayName: profile.DisplayName(),
			Email:       profile.Email,
			FirstSeen:   now,
			LastSeen:    now,
		})
		if err != nil {
			return Result{}, auth.Refuse(err)
		}
	}

	token, cred, err := auth.Issue(ctx, l.store, principal, auth.KindSession, now)
	if err != nil {
		return Result{}, auth.Refuse(err)
	}

	return Result{
		Token: token, Credential: cred, Claims: claims, Principal: principal,
		Profile: profile, ProfileErr: profileErr,
	}, nil
}

// Pending reports how many logins are in flight, for a metric and for a test
// that asserts a state was consumed.
func (l *Login) Pending() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.pending)
}

// sweep drops logins nobody came back for. It runs on Begin and not on a timer,
// so an idle server starts no goroutine. Begin calls it once per sweepInterval,
// and again whenever the table is full.
func (l *Login) sweep(now time.Time) {
	l.lastSweep = now

	for state, entry := range l.pending {
		if now.Sub(entry.started) > PendingTTL {
			delete(l.pending, state)
		}
	}
}

func randomURLSafe(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(raw), nil
}
