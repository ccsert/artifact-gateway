package objectstore

import (
	"errors"
	"reflect"
)

// Validate rejects absent object-store dependencies before a route or worker
// can use them. Reachability and bucket checks belong to runtime initialization.
func Validate(store Store) error {
	if store == nil {
		return errors.New("object store is required")
	}
	value := reflect.ValueOf(store)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return errors.New("object store is required")
		}
	}
	return nil
}
