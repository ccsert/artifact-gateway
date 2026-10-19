// Package artifactquery provides shared display-safe search text validation.
package artifactquery

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func InvalidRune(value rune) bool {
	return unicode.IsControl(value) || value == '\u061c' || value == '\u200e' || value == '\u200f' || value >= '\u202a' && value <= '\u202e' || value >= '\u2066' && value <= '\u2069'
}
func ValidKeyword(value string, limit int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit && strings.IndexFunc(value, InvalidRune) < 0
}
