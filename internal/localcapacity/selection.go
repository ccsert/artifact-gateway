package localcapacity

import (
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

// ParsePaths validates a bounded explicit selection without filesystem access.
// Errors never repeat input or paths.
func ParsePaths(raw string) (map[Alias]string, error) {
	if len(raw) > 16384 || !utf8.ValidString(raw) {
		return nil, ErrConfiguration
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	d := json.NewDecoder(strings.NewReader(raw))
	if first, err := d.Token(); err != nil || first != json.Delim('{') {
		return nil, ErrConfiguration
	}
	paths := make(map[Alias]string)
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok {
			return nil, ErrConfiguration
		}
		alias := Alias(name)
		if _, duplicate := paths[alias]; duplicate {
			return nil, ErrConfiguration
		}
		value, err := d.Token()
		path, ok := value.(string)
		if err != nil || !ok {
			return nil, ErrConfiguration
		}
		paths[alias] = path
		if validatePaths(paths) != nil {
			return nil, ErrConfiguration
		}
	}
	if last, err := d.Token(); err != nil || last != json.Delim('}') {
		return nil, ErrConfiguration
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrConfiguration
	}
	return paths, nil
}
