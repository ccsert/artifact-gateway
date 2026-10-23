package nexusmigration

import (
	"context"
	"testing"
)

func TestOperatorIdentityCannotBeInferredFromDisplayName(t *testing.T) {
	for _, test := range []struct {
		identity                OperatorIdentity
		actor, wantKind, wantID string
	}{
		{OperatorIdentity{}, "api-key:existing", "", ""},
		{OperatorIdentity{Actor: "api-key:existing", AuthenticationKind: "static_admin"}, "api-key:existing", "static_admin", ""},
		{OperatorIdentity{Actor: "user:existing", AuthenticationKind: "local_session"}, "user:existing", "local_session", ""},
		{OperatorIdentity{Actor: "user:existing", AuthenticationKind: "local_session", UserID: "stable-id"}, "user:existing", "local_session", "stable-id"},
		{OperatorIdentity{Actor: "user:other", AuthenticationKind: "local_session", UserID: "other-id"}, "user:existing", "", ""},
		{OperatorIdentity{Actor: "api-key:existing", AuthenticationKind: "api_key"}, "api-key:existing", "api_key", "existing"},
	} {
		kind, id := operatorBinding(WithOperatorIdentity(context.Background(), test.identity), test.actor)
		if kind != test.wantKind || id != test.wantID {
			t.Fatalf("binding %q %q want %q %q", kind, id, test.wantKind, test.wantID)
		}
	}
}
