package app

import "github.com/artifact-gateway/artifact-gateway/internal/authorization"

// These aliases keep the composition root and existing callers stable while
// authentication and authorization live in their protocol-independent module.
type Principal = authorization.Principal
type Authenticator = authorization.Authenticator
type OIDCConfig = authorization.OIDCConfig
type OIDCIdentity = authorization.OIDCIdentity
type OIDCValidator = authorization.OIDCValidator
type RepositoryOperation = authorization.RepositoryOperation
type AuthorizationDecision = authorization.AuthorizationDecision
type RepositoryAuthorizer = authorization.RepositoryAuthorizer

const (
	RepositoryRead  = authorization.RepositoryRead
	RepositoryWrite = authorization.RepositoryWrite
	RepositoryAdmin = authorization.RepositoryAdmin
)

var NewOIDCValidator = authorization.NewOIDCValidator
var ManagedGroupMemberDecision = authorization.ManagedGroupMemberDecision
