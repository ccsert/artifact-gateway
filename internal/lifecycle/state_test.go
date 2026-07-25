package lifecycle

import "testing"

func TestArtifactStateTransitions(t *testing.T) {
	for _, transition := range []struct {
		from, to State
		allowed  bool
	}{
		{Staged, Visible, true},
		{Visible, Tombstoned, true},
		{Staged, Tombstoned, false},
		{Tombstoned, Visible, false},
	} {
		if got := CanTransition(transition.from, transition.to); got != transition.allowed {
			t.Fatalf("CanTransition(%q, %q) = %v, want %v", transition.from, transition.to, got, transition.allowed)
		}
	}
}
