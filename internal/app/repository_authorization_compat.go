package app

import "github.com/artifact-gateway/artifact-gateway/internal/authorization"

// These aliases keep the composition root and existing callers stable while
// authentication and authorization live in their protocol-independent module.
type Principal = authorization.Principal
type Role = authorization.Role
type Authenticator = authorization.Authenticator
type OIDCConfig = authorization.OIDCConfig
type OIDCRoleMapping = authorization.OIDCRoleMapping
type OIDCIdentity = authorization.OIDCIdentity
type OIDCValidator = authorization.OIDCValidator
type OIDCClient = authorization.OIDCClient
type OIDCClientConfig = authorization.OIDCClientConfig
type RepositoryOperation = authorization.RepositoryOperation
type AuthorizationDecision = authorization.AuthorizationDecision
type RepositoryAuthorizer = authorization.RepositoryAuthorizer

const (
	RoleReader      = authorization.RoleReader
	RoleWriter      = authorization.RoleWriter
	RoleAdmin       = authorization.RoleAdmin
	RepositoryRead  = authorization.RepositoryRead
	RepositoryWrite = authorization.RepositoryWrite
	RepositoryAdmin = authorization.RepositoryAdmin
)

var NewOIDCValidator = authorization.NewOIDCValidator
var NewOIDCClient = authorization.NewOIDCClient
var ManagedGroupMemberDecision = authorization.ManagedGroupMemberDecision
