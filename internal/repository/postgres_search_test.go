package repository

import "testing"

func TestEscapeLikePrefixPreservesLiteralSearchTerms(t *testing.T) {
	tests := map[string]string{
		"":     "",
		"a_b":  `a\_b`,
		"a%b":  `a\%b`,
		`a\\b`: `a\\\\b`,
	}
	for input, want := range tests {
		if got := escapeLikePrefix(input); got != want {
			t.Fatalf("escapeLikePrefix(%q)=%q, want %q", input, got, want)
		}
	}
}
