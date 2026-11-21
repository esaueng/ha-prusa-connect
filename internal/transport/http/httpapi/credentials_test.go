package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// The credentials the fake store holds: one of the caller's own, and one held
// by somebody else. The ids are shaped like real public ids.
const (
	ownCredentialID    = "7Kp2mQ9xLw4nRt8vBc3zYe"
	othersCredentialID = "Xw3bN8qLp5tR2mKv7cJz1H"
	fakeToken          = "pcl_minted_token_for_the_test"
)

// fakeCredentials is a credential store with the minting taken out. It records
// what it was asked to do, so a test can hold the surface to asking the store
// rather than to answering by itself.
type fakeCredentials struct {
	mu sync.Mutex

	rows map[string]APICredential

	// notSignedIn makes every request look like one that carries an api
	// credential and not a session.
	notSignedIn bool

	// fence is what Fence answers. fenceRead and checkedAfterFence record
	// whether a session check followed the read.
	fence             CredentialFence
	fenceRead         bool
	checkedAfterFence bool

	// root is what Root answers: the root of the caller's credential.
	root string

	// presented is what Presented answers: the api credential the request
	// carries.
	presented string

	issued  []NewCredential
	listed  []string
	revoked []string
}

var _ Credentials = (*fakeCredentials)(nil)

func newFakeCredentials() *fakeCredentials {
	return &fakeCredentials{rows: map[string]APICredential{
		ownCredentialID: {
			ID: ownCredentialID, Name: "nightly export", Subject: "user:operator",
			CreatedAt: exampleTime, ExpiresAt: exampleTime.Add(credentialExampleLife),
		},
		othersCredentialID: {
			ID: othersCredentialID, Name: "hers", Subject: "user:grace",
			CreatedAt: exampleTime, ExpiresAt: exampleTime.Add(credentialExampleLife),
		},
	}}
}

func (c *fakeCredentials) Issue(_ context.Context, spec NewCredential) (IssuedCredential, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.issued = append(c.issued, spec)

	return IssuedCredential{
		APICredential: APICredential{
			ID: "n3wCr3d3nt1al0000000ab", Name: spec.Name, Subject: spec.Subject,
			CreatedAt: exampleTime, ExpiresAt: exampleTime.Add(credentialExampleLife),
			Scopes: spec.Scopes, Origin: spec.Origin,
		},
		Token: fakeToken,
	}, nil
}

func (c *fakeCredentials) SignedIn(*http.Request) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.checkedAfterFence = c.fenceRead

	return !c.notSignedIn
}

// Root answers the root the fake was given, empty unless a test sets one.
func (c *fakeCredentials) Root(*http.Request) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.root
}

// Presented answers the credential the fake was given.
func (c *fakeCredentials) Presented(*http.Request) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.presented
}

// Fence answers the generations the fake was given, and records that they
// were read.
func (c *fakeCredentials) Fence(context.Context, string, string, string) (CredentialFence, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.fenceRead = true
	c.checkedAfterFence = false

	return c.fence, nil
}

func (c *fakeCredentials) List(_ context.Context, subject string) ([]APICredential, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.listed = append(c.listed, subject)

	items := make([]APICredential, 0)

	for _, row := range c.rows {
		if row.Subject == subject {
			items = append(items, row)
		}
	}

	return items, nil
}

func (c *fakeCredentials) Get(_ context.Context, id string) (APICredential, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	row, ok := c.rows[id]
	if !ok {
		return APICredential{}, UnknownCredential(id)
	}

	return row, nil
}

func (c *fakeCredentials) Revoke(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.revoked = append(c.revoked, id)
	delete(c.rows, id)

	return nil
}

// serverWithCredentials is a server whose credential minter and graph are the
// caller's, resolving every request to user:operator.
func serverWithCredentials(t *testing.T, credentials Credentials, grants GrantStore) *Server {
	t.Helper()

	server, err := New(Config{
		Version:     "test",
		Principals:  fixedPrincipal("user:operator"),
		Resources:   seededStore(),
		Presets:     seededPresets(),
		Endpoints:   seededEndpoints(),
		Grants:      grants,
		Sessions:    newFakeSessions(),
		Accounts:    &fakeAccounts{},
		Credentials: credentials,
		Readiness:   &fakeReadiness{report: readyReport()},
		Devices:     noPrinters{},
		Stores:      noStorages{},
		Events:      noEvents{},
	})
	require.NoError(t, err)

	return server
}

// TestAPersonMintsTheirOwnCredentialWithNoGrant is the permit half of the
// create route: being signed in is enough to mint for yourself, the token is
// in the answer once, and the graph is not asked.
func TestAPersonMintsTheirOwnCredentialWithNoGrant(t *testing.T) {
	t.Parallel()

	credentials := newFakeCredentials()
	grants := &grantingStore{allow: false}

	rec := call(t, serverWithCredentials(t, credentials, grants).Handler(),
		http.MethodPost, credentialsPath, `{"name":"nightly export"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	var body createdCredentialBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, fakeToken, body.Token)
	require.Equal(t, "user:operator", body.Subject)
	require.Equal(t, "nightly export", body.Name)
	require.Nil(t, body.LastUsedAt)

	require.Empty(t, grants.asked, "minting for yourself asked the graph")
	require.Equal(t, []NewCredential{{
		Subject: "user:operator", Name: "nightly export", MintedBy: "user:operator", Fence: &CredentialFence{},
		RootCreator: "user:operator", Scopes: []string{"read"}, Origin: OriginMint,
	}}, credentials.issued)
}

// TestAnAPICredentialCannotMintAnother is the stolen-token case. A token that
// could mint a sibling would outlive its own revocation, so a mint takes a
// session and nothing is minted without one.
func TestAnAPICredentialCannotMintAnother(t *testing.T) {
	t.Parallel()

	credentials := newFakeCredentials()
	credentials.notSignedIn = true

	rec := call(t, serverWithCredentials(t, credentials, &grantingStore{allow: true}).Handler(),
		http.MethodPost, credentialsPath, `{"name":"sibling"}`)

	problem := requireProblem(t, rec, http.StatusForbidden)
	require.Equal(t, fault.AuthSessionRequired.Code(), problem.Code)
	require.Empty(t, credentials.issued, "a caller with no session minted a credential")
}

// TestMintingForAnotherPrincipalTakesTheAuthResourceKind is where the
// permission comes from, in both directions: with configure_resource on the
// auth kind a service account gets a token, and without it nothing is minted.
func TestMintingForAnotherPrincipalTakesTheAuthResourceKind(t *testing.T) {
	t.Parallel()

	expiry := exampleTime.Add(48 * time.Hour)
	body := `{"name":"ci","subject":"service_account:ci","expires_at":"` + expiry.Format(time.RFC3339) + `"}`

	t.Run("permitted", func(t *testing.T) {
		t.Parallel()

		credentials := newFakeCredentials()
		grants := &grantingStore{allow: true}

		rec := call(t, serverWithCredentials(t, credentials, grants).Handler(),
			http.MethodPost, credentialsPath, body)
		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

		require.NotEmpty(t, grants.asked, "the surface answered without asking the graph")
		require.Equal(t, "resource_kind:auth", grants.asked[0].String())
		require.Equal(t, []NewCredential{{
			Subject: "service_account:ci", Name: "ci", ExpiresAt: expiry, MintedBy: "user:operator",
			Fence: &CredentialFence{}, RootCreator: "user:operator", Scopes: []string{"read"}, Origin: OriginMint,
		}},
			credentials.issued)
	})

	t.Run("refused", func(t *testing.T) {
		t.Parallel()

		credentials := newFakeCredentials()

		rec := call(t, serverWithCredentials(t, credentials, &grantingStore{allow: false}).Handler(),
			http.MethodPost, credentialsPath, body)

		problem := requireProblem(t, rec, http.StatusForbidden)
		require.Equal(t, authz.CodeAccessDenied(), problem.Code)
		require.Empty(t, credentials.issued, "a refused caller minted a credential")
	})
}

// TestOnlyAPersonOrAServiceAccountHoldsACredential is the refusal for a
// subject the verifier could never type: a group, a device credential, a
// userset. It is refused before the graph is asked, so an administrator gets
// the same answer as anybody else.
func TestOnlyAPersonOrAServiceAccountHoldsACredential(t *testing.T) {
	t.Parallel()

	for name, subject := range map[string]string{
		"a group":             "group:staff",
		"a device credential": "device_credential:printer",
		"a userset":           "org:local#admin",
		"an org":              "org:local",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			credentials := newFakeCredentials()
			grants := &grantingStore{allow: true}

			rec := call(t, serverWithCredentials(t, credentials, grants).Handler(),
				http.MethodPost, credentialsPath, `{"name":"x","subject":"`+subject+`"}`)

			problem := requireProblem(t, rec, http.StatusUnprocessableEntity)
			require.Equal(t, fault.AuthCredentialRefused.Code(), problem.Code)
			require.Empty(t, credentials.issued)
			require.Empty(t, grants.asked, "a subject that cannot hold one reached the graph")
		})
	}

	rec := call(t, serverWithCredentials(t, newFakeCredentials(), &grantingStore{allow: true}).Handler(),
		http.MethodPost, credentialsPath, `{"name":"x","subject":"not a subject"}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
}

// TestAnExpiryThatHasPassedIsRefusedWithItsOwnCode is the code a client acts
// on: the store refused the expiry, and the caller is told which field.
func TestAnExpiryThatHasPassedIsRefusedWithItsOwnCode(t *testing.T) {
	t.Parallel()

	refusing := &refusingCredentials{
		fakeCredentials: newFakeCredentials(),
		issueErr: fault.AuthCredentialExpired.New("the requested expiry is not in the future").With(
			fault.StringParam("expires_at", "2020-01-01T00:00:00Z")),
	}

	rec := call(t, serverWithCredentials(t, refusing, &grantingStore{}).Handler(),
		http.MethodPost, credentialsPath, `{"name":"old","expires_at":"2020-01-01T00:00:00Z"}`)

	problem := requireProblem(t, rec, http.StatusUnprocessableEntity)
	require.Equal(t, fault.AuthCredentialExpired.Code(), problem.Code)
}

// refusingCredentials is the fake with one refusal wired in front of Issue.
type refusingCredentials struct {
	*fakeCredentials

	issueErr error
}

func (r *refusingCredentials) Issue(ctx context.Context, spec NewCredential) (IssuedCredential, error) {
	if r.issueErr != nil {
		return IssuedCredential{}, r.issueErr
	}

	return r.fakeCredentials.Issue(ctx, spec)
}

// TestTheListingShowsYourOwnAndNeverTheSecret is the read: your own take no
// grant, the answer names and dates each credential, and nothing in it could
// be presented.
func TestTheListingShowsYourOwnAndNeverTheSecret(t *testing.T) {
	t.Parallel()

	credentials := newFakeCredentials()
	grants := &grantingStore{allow: false}

	rec := call(t, serverWithCredentials(t, credentials, grants).Handler(), http.MethodGet, credentialsPath, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var body struct {
		Items []APICredential `json:"items"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Items, 1)
	require.Equal(t, ownCredentialID, body.Items[0].ID)
	require.Equal(t, "nightly export", body.Items[0].Name)
	require.NotContains(t, rec.Body.String(), "token", "the listing carries a field a secret could travel in")
	require.NotContains(t, rec.Body.String(), othersCredentialID, "the listing showed another person's credential")

	require.Equal(t, []string{"user:operator"}, credentials.listed)
	require.Empty(t, grants.asked, "listing your own asked the graph")
}

// TestListingAnotherPrincipalsCredentialsTakesTheAuthResourceKind is the
// listing's other direction, permit and refusal.
func TestListingAnotherPrincipalsCredentialsTakesTheAuthResourceKind(t *testing.T) {
	t.Parallel()

	t.Run("permitted", func(t *testing.T) {
		t.Parallel()

		credentials := newFakeCredentials()
		grants := &grantingStore{allow: true}

		rec := call(t, serverWithCredentials(t, credentials, grants).Handler(),
			http.MethodGet, credentialsPath+"?subject=user:grace", "")
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
		require.Contains(t, rec.Body.String(), othersCredentialID)
		require.Equal(t, "resource_kind:auth", grants.asked[0].String())
	})

	t.Run("refused", func(t *testing.T) {
		t.Parallel()

		credentials := newFakeCredentials()

		rec := call(t, serverWithCredentials(t, credentials, &grantingStore{allow: false}).Handler(),
			http.MethodGet, credentialsPath+"?subject=user:grace", "")

		problem := requireProblem(t, rec, http.StatusForbidden)
		require.Equal(t, authz.CodeAccessDenied(), problem.Code)
		require.Empty(t, credentials.listed, "a refused caller reached the store")
	})
}

// TestRevokingIsYoursForFreeAndAnotherPersonsByGrant is the revoke route in
// its three shapes: your own, somebody else's with the grant, and somebody
// else's without it.
func TestRevokingIsYoursForFreeAndAnotherPersonsByGrant(t *testing.T) {
	t.Parallel()

	t.Run("your own takes no grant", func(t *testing.T) {
		t.Parallel()

		credentials := newFakeCredentials()
		grants := &grantingStore{allow: false}

		rec := call(t, serverWithCredentials(t, credentials, grants).Handler(),
			http.MethodDelete, credentialsPath+"/"+ownCredentialID, "")
		require.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())
		require.Equal(t, []string{ownCredentialID}, credentials.revoked)
		require.Empty(t, grants.asked)
	})

	t.Run("another person's with the grant", func(t *testing.T) {
		t.Parallel()

		credentials := newFakeCredentials()
		grants := &grantingStore{allow: true}

		rec := call(t, serverWithCredentials(t, credentials, grants).Handler(),
			http.MethodDelete, credentialsPath+"/"+othersCredentialID, "")
		require.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())
		require.Equal(t, []string{othersCredentialID}, credentials.revoked)
		require.Equal(t, "resource_kind:auth", grants.asked[0].String())
	})

	// The refusal is the one an id no row has, so a caller without the grant
	// cannot tell another person's id from one nobody holds.
	t.Run("another person's without it", func(t *testing.T) {
		t.Parallel()

		credentials := newFakeCredentials()
		handler := serverWithCredentials(t, credentials, &grantingStore{allow: false}).Handler()

		rec := call(t, handler, http.MethodDelete, credentialsPath+"/"+othersCredentialID, "")
		problem := requireProblem(t, rec, http.StatusNotFound)
		require.Equal(t, fault.AuthCredentialUnknown.Code(), problem.Code)
		require.Empty(t, credentials.revoked, "a refused caller revoked a credential")

		unknown := call(t, handler, http.MethodDelete, credentialsPath+"/n0Such0Cr3d3nt1al00000", "")
		require.Equal(t, rec.Code, unknown.Code)
		require.Equal(t, problem.Code, requireProblem(t, unknown, http.StatusNotFound).Code)
	})
}

// TestRevokingAnUnknownCredentialIsNotFound is the code for an id no row
// answers to, and the check that the surface asks before it deletes.
func TestRevokingAnUnknownCredentialIsNotFound(t *testing.T) {
	t.Parallel()

	credentials := newFakeCredentials()

	rec := call(t, serverWithCredentials(t, credentials, &grantingStore{allow: true}).Handler(),
		http.MethodDelete, credentialsPath+"/n0Such0Cr3d3nt1al00000", "")

	problem := requireProblem(t, rec, http.StatusNotFound)
	require.Equal(t, fault.AuthCredentialUnknown.Code(), problem.Code)
	require.Empty(t, credentials.revoked)
}

// TestTheCredentialRoutesTakeAnIdentity is the fail-closed half at the
// transport: with no caller, none of the three routes reaches the store.
func TestTheCredentialRoutesTakeAnIdentity(t *testing.T) {
	t.Parallel()

	credentials := newFakeCredentials()

	server, err := New(Config{
		Version:     "test",
		Principals:  fixedPrincipal(""),
		Resources:   seededStore(),
		Presets:     seededPresets(),
		Endpoints:   seededEndpoints(),
		Grants:      &fakeGrantStore{},
		Sessions:    newFakeSessions(),
		Accounts:    &fakeAccounts{},
		Credentials: credentials,
		Readiness:   &fakeReadiness{report: readyReport()},
		Devices:     noPrinters{},
		Stores:      noStorages{},
		Events:      noEvents{},
	})
	require.NoError(t, err)

	handler := server.Handler()

	for name, tc := range map[string]struct {
		method string
		path   string
		body   string
	}{
		"create": {method: http.MethodPost, path: credentialsPath, body: `{"name":"x"}`},
		"list":   {method: http.MethodGet, path: credentialsPath},
		"revoke": {method: http.MethodDelete, path: credentialsPath + "/" + ownCredentialID},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := call(t, handler, tc.method, tc.path, tc.body)
			require.Equal(t, http.StatusUnauthorized, rec.Code, "body: %s", rec.Body.String())
		})
	}

	require.Empty(t, credentials.issued)
	require.Empty(t, credentials.listed)
	require.Empty(t, credentials.revoked)
}

// TestAServerWiredWithoutAMinterRefusesToMint is the default: a server built
// with no Credentials publishes the routes and mints nothing, in the words a
// deployment that authenticates nobody uses.
func TestAServerWiredWithoutAMinterRefusesToMint(t *testing.T) {
	t.Parallel()

	handler := serverWithCredentials(t, nil, &grantingStore{allow: true}).Handler()

	rec := call(t, handler, http.MethodPost, credentialsPath, `{"name":"x"}`)
	problem := requireProblem(t, rec, http.StatusUnprocessableEntity)
	require.Equal(t, fault.AuthCredentialRefused.Code(), problem.Code)

	rec = call(t, handler, http.MethodGet, credentialsPath, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.JSONEq(t, `{"items":[]}`, rec.Body.String())

	rec = call(t, handler, http.MethodDelete, credentialsPath+"/"+ownCredentialID, "")
	problem = requireProblem(t, rec, http.StatusNotFound)
	require.Equal(t, fault.AuthCredentialUnknown.Code(), problem.Code)
}

// TestAdministeringIdentityTakesASession holds every route that takes the
// identity-administration grant, both ways. An act that creates or grants
// access takes a sign-in. A stolen administrator's api credential could
// otherwise pair a subject, create an account with a known password or reset
// somebody's, then sign in and mint. A read and a revocation take the grant
// alone, so an incident script holding a token can still list and revoke.
func TestAdministeringIdentityTakesASession(t *testing.T) {
	t.Parallel()

	grantsAccess := []struct{ method, path, body string }{
		{http.MethodPost, accountsPath, `{"username":"new@example.com","password":"a password of note"}`},
		{http.MethodDelete, "/api/v1/auth/accounts/grace", ""},
		{http.MethodPost, "/api/v1/auth/accounts/grace/password", `{"new_password":"a reset password"}`},
		{http.MethodPut, "/api/v1/auth/accounts/grace/role", `{"role":"admin"}`},
		{http.MethodPost, pairingsPath, `{"provider":"prusa-account","subject":"s1","principal":"user:operator"}`},
		{http.MethodPost, credentialsPath, `{"name":"ci","subject":"service_account:ci"}`},
	}

	for _, c := range grantsAccess {
		t.Run("refused "+c.method+" "+c.path, func(t *testing.T) {
			t.Parallel()

			credentials := newFakeCredentials()
			credentials.notSignedIn = true

			rec := call(t, serverWithCredentials(t, credentials, &grantingStore{allow: true}).Handler(),
				c.method, c.path, c.body)

			problem := requireProblem(t, rec, http.StatusForbidden)
			require.Equal(t, fault.AuthSessionRequired.Code(), problem.Code, "body: %s", rec.Body.String())
			require.Contains(t, rec.Body.String(), "sign-in as a person",
				"the refusal does not say what a token paired to a service account lacks")
			require.Empty(t, credentials.issued)
		})
	}

	readsAndRevocations := []struct{ method, path string }{
		{http.MethodGet, accountsPath},
		{http.MethodGet, pairingsPath},
		{http.MethodDelete, "/api/v1/auth/pairings/prusa-account/s1"},
		{http.MethodGet, credentialsPath + "?subject=user:grace"},
		{http.MethodDelete, credentialsPath + "/" + othersCredentialID},
	}

	for _, c := range readsAndRevocations {
		t.Run("allowed "+c.method+" "+c.path, func(t *testing.T) {
			t.Parallel()

			credentials := newFakeCredentials()
			credentials.notSignedIn = true

			rec := call(t, serverWithCredentials(t, credentials, &grantingStore{allow: true}).Handler(),
				c.method, c.path, "")

			require.NotContains(t, rec.Body.String(), fault.AuthSessionRequired.Code().String(),
				"a read or a revocation asked for a sign-in")
			require.Less(t, rec.Code, http.StatusInternalServerError, "body: %s", rec.Body.String())
			require.NotEqual(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
		})
	}
}

// TestAMintSavesTheGenerationsItReadBeforeTheSessionCheck holds the handler to
// the order the fence needs. It reads the generations, then checks the session,
// then saves what it read. With no check after the read, a password change
// between the last check and the read would go unseen.
func TestAMintSavesTheGenerationsItReadBeforeTheSessionCheck(t *testing.T) {
	t.Parallel()

	credentials := newFakeCredentials()
	credentials.fence = CredentialFence{Subject: 7, Minter: 9}

	rec := call(t, serverWithCredentials(t, credentials, &grantingStore{allow: true}).Handler(),
		http.MethodPost, credentialsPath, `{"name":"ci","subject":"service_account:ci"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	require.Len(t, credentials.issued, 1)
	require.Equal(t, &CredentialFence{Subject: 7, Minter: 9}, credentials.issued[0].Fence,
		"the mint did not save the generations it read")
	require.True(t, credentials.checkedAfterFence, "the mint did not check the session after reading the generations")
}

// TestAMintRecordsTheRootOfTheCallersChain is a mint made with a token that
// reached this server through a chain. The token was paired by ada's stolen
// session to bob, so the mint names bob as its minter and ada as its root.
func TestAMintRecordsTheRootOfTheCallersChain(t *testing.T) {
	t.Parallel()

	credentials := newFakeCredentials()
	credentials.root = "user:ada"

	rec := call(t, serverWithCredentials(t, credentials, &grantingStore{allow: true}).Handler(),
		http.MethodPost, credentialsPath, `{"name":"ci","subject":"service_account:ci"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	require.Len(t, credentials.issued, 1)
	require.Equal(t, "user:operator", credentials.issued[0].MintedBy)
	require.Equal(t, "user:ada", credentials.issued[0].RootCreator, "the mint did not record the caller's root")
}
