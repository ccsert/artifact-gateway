package repository

import (
	"errors"
	"strings"
	"testing"
)

func TestOCIBearerConfigurationValidation(t *testing.T) {
	var disabled *OCIBearer
	if err := disabled.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, realm := range []string{"https://auth.example.test/token", "https://auth.example.test:443/token", "https://auth.example.test:8443/oauth/token"} {
		if err := (&OCIBearer{Realm: realm, Service: strings.Repeat("a", 256)}).Validate(); err != nil {
			t.Fatalf("valid issuer/audience rejected: %v", err)
		}
	}
	for name, config := range map[string]OCIBearer{
		"empty realm":        {Service: "registry"},
		"opaque":             {Realm: "https:auth.example.test/token", Service: "registry"},
		"invalid port":       {Realm: "https://auth.example.test:65536/token", Service: "registry"},
		"empty fragment":     {Realm: "https://auth.example.test/token#", Service: "registry"},
		"empty query":        {Realm: "https://auth.example.test/token?", Service: "registry"},
		"query":              {Realm: "https://auth.example.test/token?scope=x", Service: "registry"},
		"userinfo":           {Realm: "https://user:secret@auth.example.test/token", Service: "registry"},
		"realm whitespace":   {Realm: "https://auth.example.test/token ", Service: "registry"},
		"unicode whitespace": {Realm: "https://auth.example.test/token", Service: "registry\u00a0service"},
		"unicode control":    {Realm: "https://auth.example.test/token", Service: "registry\u0085service"},
		"multibyte length":   {Realm: "https://auth.example.test/token", Service: strings.Repeat("界", 86)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := config.Validate(); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestMemoryStoreOCIBearerConfigurationIsolation(t *testing.T) {
	for _, idempotent := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "idempotent create"}[idempotent], func(t *testing.T) {
			store := NewMemoryStore()
			config := &OCIBearer{Realm: "https://auth.example.test/token", Service: "registry"}
			repo := HostedRepository{ID: "oci-1", Name: "oci-proxy", Format: FormatOCI, Type: RepositoryTypeProxy, Endpoint: "https://registry.example.test", OCIBearer: config}
			var created HostedRepository
			var err error
			if idempotent {
				created, _, err = store.CreateHostedRepositoryIdempotently(t.Context(), repo, "admin", "create-oci", "payload")
			} else {
				created, err = store.CreateHostedRepository(t.Context(), repo)
			}
			if err != nil {
				t.Fatal(err)
			}
			config.Service = "caller mutation"
			created.OCIBearer.Service = "return mutation"
			check := func() {
				t.Helper()
				stored, err := store.GetHostedRepository(t.Context(), repo.ID)
				if err != nil || stored.OCIBearer == nil || stored.OCIBearer.Service != "registry" {
					t.Fatalf("stored issuer/audience changed: %+v err=%v", stored.OCIBearer, err)
				}
			}
			check()
			byID, _ := store.GetHostedRepository(t.Context(), repo.ID)
			byID.OCIBearer.Service = "read mutation"
			byName, _ := store.GetHostedRepositoryByName(t.Context(), repo.Name)
			byName.OCIBearer.Service = "name mutation"
			listed, _, _ := store.ListHostedRepositories(t.Context(), 50, "")
			listed[0].OCIBearer.Service = "list mutation"
			if idempotent {
				replayed, replay, err := store.CreateHostedRepositoryIdempotently(t.Context(), repo, "admin", "create-oci", "payload")
				if err != nil || !replay || replayed.OCIBearer.Service != "registry" {
					t.Fatalf("replay=%+v replayed=%v err=%v", replayed.OCIBearer, replay, err)
				}
				replayed.OCIBearer.Service = "replay mutation"
			}
			check()
			updatedInput, _ := store.GetHostedRepository(t.Context(), repo.ID)
			updated, err := store.UpdateHostedRepository(t.Context(), updatedInput, "1")
			if err != nil || updated.Version != "2" {
				t.Fatalf("update version=%q err=%v", updated.Version, err)
			}
			updatedInput.OCIBearer.Service = "update input mutation"
			updated.OCIBearer.Service = "update return mutation"
			check()
			if _, err := store.UpdateHostedRepository(t.Context(), HostedRepository{ID: repo.ID}, "1"); !errors.Is(err, ErrVersionConflict) {
				t.Fatalf("stale clear error=%v", err)
			}
			check()
			cleared, err := store.UpdateHostedRepository(t.Context(), HostedRepository{ID: repo.ID, Endpoint: repo.Endpoint}, "2")
			if err != nil || cleared.OCIBearer != nil || cleared.Version != "3" {
				t.Fatalf("clear=%+v err=%v", cleared, err)
			}
		})
	}
}
