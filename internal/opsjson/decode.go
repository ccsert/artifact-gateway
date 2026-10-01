// Package opsjson decodes typed operator evidence without ambiguous fields.
package opsjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Decode accepts one non-null JSON value, ASCII field names, and UTF-8 values.
// Struct destinations reject unknown fields. Repeated names, including case-alias collisions, are rejected before
// struct decoding. A single ASCII case variant retains encoding/json behavior.
// Its error never includes an excerpt of operator evidence.
func Decode(data []byte, dst any) error {
	invalid := errors.New("invalid or ambiguous operator JSON")
	if !utf8.Valid(data) || !validUnicodeEscapes(data) || bytes.Equal(bytes.TrimSpace(data), []byte("null")) || !uniqueKeys(data) {
		return invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(dst) != nil || decoder.Decode(new(any)) != io.EOF {
		return invalid
	}
	return nil
}

// encoding/json replaces isolated UTF-16 surrogates with U+FFFD. Evidence keys
// must retain their exact identity, so reject that lossy conversion as well.
func validUnicodeEscapes(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		value, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value < 0xd800 || value > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}

func uniqueKeys(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var consume func(int) bool
	consume = func(depth int) bool {
		if depth > 32 {
			return false
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		keys := make(map[string]bool)
		for decoder.More() {
			if delim == '{' {
				key, keyErr := decoder.Token()
				name, valid := key.(string)
				if keyErr != nil || !valid || !ascii(name) || keys[strings.ToLower(name)] {
					return false
				}
				keys[strings.ToLower(name)] = true
			}
			if !consume(depth + 1) {
				return false
			}
		}
		end, endErr := decoder.Token()
		return endErr == nil && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
	}
	if !consume(0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func ascii(name string) bool {
	for i := range len(name) {
		if name[i] >= 128 {
			return false
		}
	}
	return true
}
