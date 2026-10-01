package opsjson

import (
	"strings"
	"testing"
)

func TestDecodeRejectsAmbiguousOrMalformedUnicodeEvidence(t *testing.T) {
	for _, input := range []string{
		`{"key":"synthetic-sensitive-marker","Key":"other"}`,
		`{"key":"first","Key":"other"}`,
		`{"key":"\ud800"}`, `{"key":"\udfff"}`, `{"key":"\ud800\u0041"}`,
		"{\"key\":\"\xff\"}", `null`, `{"unknown":"synthetic-sensitive-marker"}`, `{"key":"ok"}{}`,
	} {
		var dst struct {
			Key string `json:"key"`
		}
		if err := Decode([]byte(input), &dst); err == nil || strings.Contains(err.Error(), "synthetic-sensitive-marker") {
			t.Fatalf("input=%q error=%v", input, err)
		}
	}
}

func TestDecodePreservesValidUnicodeAndEscapedBackslashes(t *testing.T) {
	for input, want := range map[string]string{
		`{"key":"合成标记"}`:         "合成标记",
		`{"key":"\ud83d\ude00"}`: "😀",
		`{"key":"\ufffd"}`:       "�",
		`{"key":"\\ud800"}`:      `\ud800`,
	} {
		var dst struct {
			Key string `json:"key"`
		}
		if err := Decode([]byte(input), &dst); err != nil || dst.Key != want {
			t.Fatalf("input=%s key=%q error=%v", input, dst.Key, err)
		}
	}
}
