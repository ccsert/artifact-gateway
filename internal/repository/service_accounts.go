package repository

import (
	"context"
	"time"
)

type ServiceAccountState string

const (
	ServiceAccountActive   ServiceAccountState = "active"
	ServiceAccountDisabled ServiceAccountState = "disabled"
)

// ServiceAccount is a stable non-human authorization principal. Repository
// grants bind to service-account:<id>; credentials may rotate independently.
type ServiceAccount struct {
	ID          string
	Name        string
	Description string
	State       ServiceAccountState
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Version     string
}

type ServiceAccountUpdate struct {
	ID          string
	Description *string
	State       *ServiceAccountState
}

type ServiceAccountStore interface {
	CreateServiceAccount(context.Context, ServiceAccount) (ServiceAccount, error)
	ListServiceAccounts(context.Context, int, string) ([]ServiceAccount, error)
	GetServiceAccount(context.Context, string) (ServiceAccount, error)
	UpdateServiceAccount(context.Context, ServiceAccountUpdate, string) (ServiceAccount, error)
	CreateServiceAccountCredential(context.Context, APIKey) (APIKey, error)
	ListServiceAccountCredentials(context.Context, string, int, string) ([]APIKey, error)
	RevokeServiceAccountCredential(context.Context, string, string) (APIKey, error)
}
