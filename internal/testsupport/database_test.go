package testsupport

import "testing"

func TestValidateIsolatedDatabaseURL(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "unset", value: ""},
		{name: "isolated", value: "postgres://gateway:secret@localhost:5432/gateway_test?sslmode=disable"},
		{name: "development database", value: "postgres://gateway:secret@localhost:5432/gateway?sslmode=disable", wantErr: true},
		{name: "missing database", value: "postgres://gateway:secret@localhost:5432", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateIsolatedDatabaseURL(test.value)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateIsolatedDatabaseURL() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}
