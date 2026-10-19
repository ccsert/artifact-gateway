package repository

import "context"

// ConanObjectCollectionStore persists the irreversible delete boundary after
// atomically rechecking every repository and live publication snapshot sharing
// the object. It is composed separately from publishers and GatewayStore.
type ConanObjectCollectionStore interface {
	BeginConanObjectCollection(context.Context, string) (bool, error)
	CompleteConanObjectCollection(context.Context, string) error
}
