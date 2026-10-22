//go:build integration

package repository

import "testing"

func TestPostgresHelmStoreContract(t *testing.T) { testHelmStoreContract(t, newPostgresNuGetStore(t)) }
