package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpfault"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// The api credential paths. They sit under the login ceremony's prefix because
// they are the same subject: how a caller gets a credential on this server.
// gosec reads a constant named credential as a hardcoded one; these are routes.
const (
	credentialsPath = "/api/v1/auth/credentials"              //nolint:gosec // a route, not a credential
	credentialPath  = "/api/v1/auth/credentials/{credential}" //nolint:gosec // a route, not a credential
)

// Credentials mints and revokes api credentials, the bearer tokens a script,
// a service or a cross-origin client presents. It is declared here because
// this is where it is consumed. The composition root satisfies it, because
// only the composition root may name the credential store.
//
// No method takes a caller. Who may mint for whom is asked here, on the auth
// resource kind, so a second transport asking it gets the same answer.
type Credentials interface {
	// Issue mints a named api credential and returns the token, once. Every
	// later read of the credential carries no secret.
	Issue(ctx context.Context, spec NewCredential) (IssuedCredential, error)
	// SignedIn reports whether the request carries a session, the credential a
	// person receives by signing in. A mint asks it, so an api credential
	// cannot mint another one.
	SignedIn(r *http.Request) bool
	// Fence reads the generations of the subject and of the minter. A mint
	// reads them before it checks the session and saves them on the row, so a
	// password change between the check and the save leaves the row refused.
	Fence(ctx context.Context, subject, minter, root string) (CredentialFence, error)
	// Root names the person at the top of the chain the request's credential
	// came through: the user a session belongs to, or the root recorded on
	// the pairing or the token presented. A mint or a pairing written with
	// that credential records it, so the root's password change ends them.
	// It is empty when the request carries no credential this server knows.
	Root(r *http.Request) string
	// List returns the api credentials one subject holds, oldest first.
	List(ctx context.Context, subject string) ([]APICredential, error)
	// Get returns one api credential by id, so the surface can ask whose it
	// is before it revokes it.
	Get(ctx context.Context, id string) (APICredential, error)
	// Revoke deletes one api credential. The credential is refused from the
	// next request on.
	Revoke(ctx context.Context, id string) error
	// Presented names the api credential the request carries, empty for any
	// other kind. A credential limited by scopes may revoke itself and no
	// other, and this is how the surface tells which one it is.
	Presented(r *http.Request) string
}

// The origins an api credential can have, as the listing renders them.
const (
	// OriginMint is a credential a person minted and copied by hand.
	OriginMint = "mint"
	// OriginAppPairing is a credential an app received through a pairing a
	// person approved.
	OriginAppPairing = "app_pairing"
)

// APICredential is one api credential as this surface renders it. There is no
// secret on it and no field one could travel in.
type APICredential struct {
	ID   string `json:"id"   doc:"The public half of the token, which is what a revocation names."`
	Name string `json:"name" doc:"The label it was minted under."`
	// Subject is the principal in tuple spelling, user:<id> or
	// service_account:<id>, which is what a grant names.
	Subject   string    `json:"subject"    doc:"The principal the credential speaks for, in tuple spelling."`
	CreatedAt time.Time `json:"created_at" doc:"When it was minted."`
	ExpiresAt time.Time `json:"expires_at" doc:"When this server stops accepting it."`
	// LastUsedAt is nil for a credential nobody has presented yet.
	LastUsedAt *time.Time `json:"last_used_at,omitempty" doc:"When it was last presented. Absent until it has been."`
	Scopes     []string   `json:"scopes"                 doc:"What the credential may use of its principal's grants: read, operate, administer. Each allows what the one before it allows."     nullable:"false" enum:"read,operate,administer"`
	Origin     string     `json:"origin"                 doc:"How it was made: mint for a credential a person minted, app_pairing for one an app received through a pairing a person approved."                  enum:"mint,app_pairing"`
}

// NewCredential is what a caller supplies to mint one. The subject is already
// resolved: the handler decides whose it is before the store sees it.
type NewCredential struct {
	Subject string
	Name    string
	// ExpiresAt is zero for the default lifetime.
	ExpiresAt time.Time
	// MintedBy is the caller who minted it, in tuple spelling. A password
	// change of that person ends the credential too.
	MintedBy string
	// Fence is what the mint read before it checked the session. Nil reads
	// the current generations at the save.
	Fence *CredentialFence
	// RootCreator is the person at the top of the chain the mint came
	// through, in tuple spelling.
	RootCreator string
	// Scopes limit the credential. At least one.
	Scopes []string
	// Origin is OriginMint or OriginAppPairing.
	Origin string
}

// CredentialFence is the generations a mint read before its check.
type CredentialFence struct {
	Subject int64
	Minter  int64
	Root    int64
}

// IssuedCredential is a minted credential with the one thing a listing never
// carries: the token itself.
type IssuedCredential struct {
	APICredential

	Token string
}

// credentialBody is the create body.
type credentialBody struct {
	Name string `json:"name" doc:"A label for the listing: which script or machine holds this one." maxLength:"128" minLength:"1"`
	// Subject is optional and defaults to the caller. Naming somebody else
	// takes the identity-administration grant.
	Subject   string     `json:"subject,omitempty"    doc:"The principal it speaks for, in tuple spelling: user:<id> or service_account:<id>. Empty means the caller."                         maxLength:"128"`
	ExpiresAt *time.Time `json:"expires_at,omitempty" doc:"When this server stops accepting it. Empty takes ninety days. The latest allowed is 365 days away."`
	Scopes    []string   `json:"scopes,omitempty"     doc:"What it may use of the principal's grants: read, operate, administer. Each allows what the one before it allows. Empty takes read."                 nullable:"false" enum:"read,operate,administer" maxItems:"3"`
}

type createCredentialInput struct {
	Body credentialBody
}

// createdCredentialBody is the one answer that carries the token. It spells
// out the members and does not embed APICredential, so the document shows them
// in the order a reader copies them.
type createdCredentialBody struct {
	ID         string     `json:"id"                     doc:"The public half of the token, which is what a revocation names."`
	Name       string     `json:"name"                   doc:"The label it was minted under."`
	Subject    string     `json:"subject"                doc:"The principal the credential speaks for, in tuple spelling."`
	CreatedAt  time.Time  `json:"created_at"             doc:"When it was minted."`
	ExpiresAt  time.Time  `json:"expires_at"             doc:"When this server stops accepting it."`
	LastUsedAt *time.Time `json:"last_used_at,omitempty" doc:"Absent: a credential that has just been minted has not been presented."`
	Scopes     []string   `json:"scopes"                 doc:"What the credential may use of its principal's grants."                                                                           nullable:"false" enum:"read,operate,administer"`
	Origin     string     `json:"origin"                 doc:"How it was made: mint, or app_pairing."                                                                                                            enum:"mint,app_pairing"`
	Token      string     `json:"token"                  doc:"The credential, shown once. Present it as Authorization: Bearer <token>. This server keeps only a hash and cannot show it again."`
}

// createdCredentialOf renders a minted credential with its token.
func createdCredentialOf(issued IssuedCredential) createdCredentialBody {
	return createdCredentialBody{
		ID:         issued.ID,
		Name:       issued.Name,
		Subject:    issued.Subject,
		CreatedAt:  issued.CreatedAt,
		ExpiresAt:  issued.ExpiresAt,
		LastUsedAt: issued.LastUsedAt,
		Scopes:     issued.Scopes,
		Origin:     issued.Origin,
		Token:      issued.Token,
	}
}

type createCredentialOutput struct {
	Status int
	Body   createdCredentialBody
}

type listCredentialsInput struct {
	Subject string `query:"subject" doc:"Whose credentials to list, in tuple spelling. Empty means the caller's own; another principal's takes configure_resource on the auth resource kind." maxLength:"128" example:"user:operator"`
	Origin  string `query:"origin"  doc:"Only the credentials made this way: mint, or app_pairing for the apps a person paired. Empty lists both."                                                            example:"app_pairing"   enum:"mint,app_pairing"`
}

type listCredentialsOutput struct {
	Body struct {
		Items []APICredential `json:"items"`
	}
}

// CredentialRef is the credential a path names. It is exported because Huma
// binds parameters by reflection and cannot write into an unexported embedded
// field.
type CredentialRef struct {
	Credential string `path:"credential" doc:"The credential's id, as the listing renders it." maxLength:"22" example:"7Kp2mQ9xLw4nRt8vBc3zYe"`
}

type revokeCredentialInput struct {
	CredentialRef
}

type revokeCredentialOutput struct {
	Status int
}

// registerCredentials publishes the api credential surface. The operations are
// registered whatever the process was wired with, so the document a client
// generates from does not depend on whether this deployment mints any.
func registerCredentials(api huma.API, cfg Config) {
	registerCreateCredential(api, cfg)
	registerListCredentials(api, cfg)
	registerRevokeCredential(api, cfg)
}

func registerCreateCredential(api huma.API, cfg Config) {
	register(api, huma.Operation{
		OperationID: "create-credential",
		Method:      http.MethodPost,
		Path:        credentialsPath,
		Summary:     "Mint an api credential",
		Description: "Mints a bearer token and returns it once; this server keeps only a hash of it. " +
			"It is accepted wherever a session is on the HTTP surface and resolves to the same " +
			"principal, with the same grants. A mint takes a sign-in, which is a session or a Prusa " +
			"Account token that a pairing names a person with. An api credential cannot mint another " +
			"one, and neither can a token paired to a service account. The " +
			"expiry is at most 365 days away. Your own takes no grant beyond being signed in. One " +
			"for another person or for a service account takes configure_resource on the auth " +
			"resource kind, which is what administering this server's identity means in the graph. " +
			"The scopes limit what the credential may use of the principal's grants: read observes, " +
			"operate also acts on printers and writes files, administer allows every grant the " +
			"principal holds. A credential never reaches more than its principal does. Empty scopes " +
			"take read.",
		Tags:          []string{"auth"},
		DefaultStatus: http.StatusCreated,
		Errors: []int{
			http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity,
		},
	}, func(ctx context.Context, in *createCredentialInput) (*createCredentialOutput, error) {
		subject, err := credentialSubject(ctx, cfg, in.Body.Subject)
		if err != nil {
			return nil, err
		}

		// A token that could mint another would outlive its own revocation.
		// So only a person who signed in may mint.
		minter, err := caller(ctx)
		if err != nil {
			return nil, err
		}

		root := callerRoot(ctx, cfg, minter)

		// Read before the session check, so a password change between the
		// check and the save leaves the row refused.
		fence, err := cfg.Credentials.Fence(ctx, subject.String(), minter.String(), root)
		if err != nil {
			return nil, credentialFault(err, "read the credential generations")
		}

		if !cfg.Credentials.SignedIn(requestFrom(ctx)) {
			return nil, fault.AuthSessionRequired.New(
				"minting takes a sign-in as a person. An api credential cannot mint, and neither can a " +
					"Prusa Account token paired to a service account")
		}

		scopes, err := requestedScopes(in.Body.Scopes)
		if err != nil {
			return nil, err
		}

		spec := NewCredential{
			Subject: subject.String(), Name: in.Body.Name, MintedBy: minter.String(), Fence: &fence,
			RootCreator: root, Scopes: authz.ScopeNames(scopes), Origin: OriginMint,
		}
		if in.Body.ExpiresAt != nil {
			spec.ExpiresAt = *in.Body.ExpiresAt
		}

		issued, err := cfg.Credentials.Issue(ctx, spec)
		if err != nil {
			return nil, credentialFault(err, "mint the credential")
		}

		return &createCredentialOutput{Status: http.StatusCreated, Body: createdCredentialOf(issued)}, nil
	})
}

func registerListCredentials(api huma.API, cfg Config) {
	register(api, huma.Operation{
		OperationID: "list-credentials",
		Method:      http.MethodGet,
		Path:        credentialsPath,
		Summary:     "List api credentials",
		Description: "Returns the api credentials a principal holds: name, when it was minted, when it " +
			"expires and when it was last presented. Never the token. Your own take no grant beyond " +
			"being signed in; another principal's take configure_resource on the auth resource kind. " +
			"Any credential that carries the grant will do, an api credential included.",
		Tags:   []string{"auth"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden},
	}, func(ctx context.Context, in *listCredentialsInput) (*listCredentialsOutput, error) {
		subject, err := credentialSubject(ctx, cfg, in.Subject)
		if err != nil {
			return nil, err
		}

		items, err := cfg.Credentials.List(ctx, subject.String())
		if err != nil {
			return nil, credentialFault(err, "list the credentials")
		}

		out := &listCredentialsOutput{}
		out.Body.Items = make([]APICredential, 0, len(items))

		for _, item := range items {
			if in.Origin == "" || item.Origin == in.Origin {
				out.Body.Items = append(out.Body.Items, item)
			}
		}

		return out, nil
	})
}

func registerRevokeCredential(api huma.API, cfg Config) {
	register(api, huma.Operation{
		OperationID: "revoke-credential",
		Method:      http.MethodDelete,
		Path:        credentialPath,
		Summary:     "Revoke an api credential",
		Description: "Deletes the credential, so it is refused on the next request rather than at its " +
			"next expiry. Your own takes no grant beyond being signed in; another principal's takes " +
			"configure_resource on the auth resource kind. " +
			"Any credential that carries the grant will do, an api credential included. A credential " +
			"whose scopes stop short of administer may revoke itself and no other credential.",
		Tags:          []string{"auth"},
		DefaultStatus: http.StatusNoContent,
		Errors: []int{
			http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		},
	}, func(ctx context.Context, in *revokeCredentialInput) (*revokeCredentialOutput, error) {
		who, err := caller(ctx)
		if err != nil {
			return nil, err
		}

		held, err := cfg.Credentials.Get(ctx, in.Credential)
		if err != nil {
			return nil, credentialFault(err, "find the credential")
		}

		// Another principal's credential is refused as an unknown id, so a
		// caller cannot tell an id somebody holds from one nobody does.
		if held.Subject != who.String() {
			if adminErr := requireAccountAdmin(ctx, cfg); adminErr != nil {
				return nil, hideOthersCredential(adminErr, in.Credential)
			}
		}

		// Revoking another credential is administering access, so a
		// credential limited below administer may end itself and nothing
		// else. An app that is done with its token can still drop it.
		if authz.ScopesFrom(ctx) != nil && cfg.Credentials.Presented(requestFrom(ctx)) != held.ID {
			return nil, authz.RefuseBelowAdminister()
		}

		if err = cfg.Credentials.Revoke(ctx, in.Credential); err != nil {
			return nil, credentialFault(err, "revoke the credential")
		}

		return &revokeCredentialOutput{Status: http.StatusNoContent}, nil
	})
}

// hideOthersCredential turns a refusal to administer accounts into the answer
// an id no row has. A failure of the graph is not a refusal and stays as it is.
func hideOthersCredential(err error, id string) error {
	if raised, ok := errors.AsType[*fault.Fault](err); ok && raised.Code() == authz.CodeAccessDenied() {
		return UnknownCredential(id)
	}

	return err
}

// credentialSubject resolves whose credentials a request is about, and decides
// whether the caller may ask. An empty spelling is the caller; the caller's own
// spelling is the caller; anybody else takes the identity-administration grant.
//
// Only a person and a service account can hold an api credential. A device
// credential, a sink and a node authenticate another way, a group is not a
// principal that presents anything, and a userset names a position and not a
// holder. Refusing those here keeps a minted token from speaking for a subject
// the verifier cannot type.
func credentialSubject(ctx context.Context, cfg Config, spelling string) (authz.Subject, error) {
	who, err := caller(ctx)
	if err != nil {
		return authz.Subject{}, err
	}

	if spelling == "" || spelling == who.String() {
		return who, nil
	}

	subject, err := authz.ParseSubject(spelling)
	if err != nil {
		return authz.Subject{}, httpfault.RequestInvalid("subject", "type:id in tuple spelling",
			"the subject is not one the authorization graph can key on")
	}

	if subject.IsUserset() || !holdsAPICredentials(subject.Type()) {
		return authz.Subject{}, fault.AuthCredentialRefused.New(
			"only a person or a service account can hold an api credential").With(
			fault.StringParam("subject", subject.String()))
	}

	if err = requireAccountAdmin(ctx, cfg); err != nil {
		return authz.Subject{}, err
	}

	return subject, nil
}

// holdsAPICredentials reports whether a principal type can be handed a bearer
// token.
func holdsAPICredentials(typ authz.Type) bool {
	return typ == authz.TypeUser || typ == authz.TypeServiceAccount
}

// credentialFault keeps a refusal the implementation raised and turns anything
// else into the server's own failure. A credential store that could not be
// written is not the caller's mistake, and reporting it as one sends them
// looking at what they typed.
func credentialFault(err error, doing string) error {
	if raised, ok := errors.AsType[*fault.Fault](err); ok {
		return raised
	}

	return httpfault.Unexpected(doing)
}

// noCredentials is the credential minter of a server wired without one. It
// mints nothing, lists nothing and knows no id. A deployment that
// authenticates nobody would never check a token it minted, so it does not
// mint one.
type noCredentials struct{}

var _ Credentials = noCredentials{}

func (noCredentials) Issue(_ context.Context, spec NewCredential) (IssuedCredential, error) {
	return IssuedCredential{}, fault.AuthCredentialRefused.New(
		"this server authenticates nobody and mints no api credential").With(
		fault.StringParam("subject", spec.Subject))
}

// SignedIn says yes so that Issue gives the refusal, which names the reason.
func (noCredentials) SignedIn(*http.Request) bool { return true }

func (noCredentials) Fence(context.Context, string, string, string) (CredentialFence, error) {
	return CredentialFence{}, nil
}

func (noCredentials) Root(*http.Request) string { return "" }

// callerRoot is the person at the top of the chain the caller's credential
// came through. A caller whose credential names none is their own root.
func callerRoot(ctx context.Context, cfg Config, caller authz.Subject) string {
	if root := cfg.Credentials.Root(requestFrom(ctx)); root != "" {
		return root
	}

	return caller.String()
}

func (noCredentials) List(context.Context, string) ([]APICredential, error) {
	return []APICredential{}, nil
}

func (noCredentials) Get(_ context.Context, id string) (APICredential, error) {
	return APICredential{}, UnknownCredential(id)
}

func (noCredentials) Presented(*http.Request) string { return "" }

func (noCredentials) Revoke(_ context.Context, id string) error {
	return UnknownCredential(id)
}

// UnknownCredential is the refusal for an id no row answers to. It is exported
// so the composition root and the default above answer in the same words.
func UnknownCredential(id string) *fault.Fault {
	return fault.AuthCredentialUnknown.New("no api credential has this id").With(
		fault.StringParam("credential", id))
}

// requestedScopes reads the scopes a caller asked for. None takes read, the
// narrowest, so a credential is never wider than what was written down for it.
func requestedScopes(names []string) ([]authz.Scope, error) {
	if len(names) == 0 {
		return []authz.Scope{authz.ScopeRead}, nil
	}

	scopes, err := authz.ParseScopes(names)
	if err != nil {
		return nil, httpfault.RequestInvalid("body.scopes", "read, operate or administer",
			"a scope this server does not know")
	}

	return scopes, nil
}
