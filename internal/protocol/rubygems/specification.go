package rubygems

import (
	"regexp"
	"time"

	"go.yaml.in/yaml/v3"
)

// String dates go through Gem::Specification#date=, whereas Psych timestamp
// scalars arrive there as Time/Date. In particular a quoted ISO timestamp is
// not interchangeable with a timestamp node. Keep this preview to valid ISO
// calendar dates and the timestamp forms shared by the fixed official parser.
var specificationStringDate = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}(\s+[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]+\s*(Z|[-+][0-9]{2}:[0-9]{2}))?$`)
var rubyInstanceVariable = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validRubyYAMLObject(n *yaml.Node) bool {
	switch n.Tag {
	case "!ruby/object:Gem::Specification", "!ruby/object:Gem::Version", "!ruby/object:Gem::Requirement", "!ruby/object:Gem::Dependency", "!ruby/object:Gem::Platform":
		fields, err := mapping(n)
		if err != nil {
			return false
		}
		for field := range fields {
			if !rubyInstanceVariable.MatchString(field) {
				return false
			}
		}
		// Psych initializes every tagged node, including otherwise unused tail
		// fields. Validate the known active initializers throughout the tree,
		// while retaining ordinary bounded tail data and valid tagged versions.
		switch n.Tag {
		case "!ruby/object:Gem::Version":
			return ValidVersion(version(n))
		case "!ruby/object:Gem::Specification":
			_, platformOK := specificationPlatform(fields["platform"])
			return platformOK && validSpecificationDate(fields["date"]) && validSpecificationRequirePaths(fields["require_paths"])
		}
	}
	return true
}

func specificationPlatform(n *yaml.Node) (string, bool) {
	platform := "ruby"
	if n != nil {
		if n.Kind != yaml.ScalarNode || n.Tag != "!!str" && n.Tag != "!!null" {
			return "", false
		}
		if n.Tag == "!!str" && n.Value != "" {
			platform = n.Value
		}
	}
	return platform, ValidPlatform(platform)
}

func validSpecificationDate(n *yaml.Node) bool {
	if n == nil || n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return true
	}
	if n.Kind != yaml.ScalarNode {
		return false
	}
	if n.Tag == "!!timestamp" {
		var parsed time.Time
		return n.Decode(&parsed) == nil
	}
	if !isRubyString(n) || !specificationStringDate.MatchString(n.Value) {
		return false
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02 15:04:05.999999999 Z07:00", "2006-01-02 15:04:05.999999999Z07:00"} {
		if _, err := time.Parse(layout, n.Value); err == nil {
			return true
		}
	}
	return false
}

func validSpecificationRequirePaths(n *yaml.Node) bool {
	if n == nil || n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return true
	}
	if n.Kind != yaml.SequenceNode || n.Tag != "!!seq" {
		return false
	}
	// Specification.from_yaml invokes flatten_require_paths after the date
	// and platform setters. Preserve its supported legacy array-of-arrays form;
	// a scalar has no #first and cannot be loaded by the official client.
	nested := len(n.Content) > 0 && n.Content[0].Kind == yaml.SequenceNode
	count := 0
	var stringsOnly func(*yaml.Node) bool
	stringsOnly = func(node *yaml.Node) bool {
		if node.Kind == yaml.SequenceNode && node.Tag == "!!seq" {
			for _, child := range node.Content {
				if !stringsOnly(child) {
					return false
				}
			}
			return true
		}
		count++
		return count <= 10000 && isRubyString(node)
	}
	for _, entry := range n.Content {
		if !nested && !isRubyString(entry) || !stringsOnly(entry) {
			return false
		}
	}
	return true
}
