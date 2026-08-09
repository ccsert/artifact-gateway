package authorization

import "testing"

func TestValidateOIDCProviderEndpointRequiresHTTPSForHTTPSIssuer(t *testing.T) {
	t.Parallel()

	if err := validateOIDCProviderEndpoint(
		"https://issuer.example.test",
		"http://issuer.example.test/authorize",
	); err == nil {
		t.Fatal("expected an insecure provider endpoint to be rejected")
	}
	if err := validateOIDCProviderEndpoint(
		"https://issuer.example.test",
		"https://login.example.test/authorize",
	); err != nil {
		t.Fatalf("expected HTTPS provider endpoint to be accepted: %v", err)
	}
}

func TestValidateOIDCProviderEndpointAllowsHTTPForTestIssuer(t *testing.T) {
	t.Parallel()

	if err := validateOIDCProviderEndpoint(
		"http://127.0.0.1:8080",
		"http://127.0.0.1:8080/token",
	); err != nil {
		t.Fatalf("expected HTTP test provider endpoint to be accepted: %v", err)
	}
}
