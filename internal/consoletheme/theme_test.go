package consoletheme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinsAreValidAndComplete(t *testing.T) {
	themes, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins() error = %v", err)
	}
	if len(themes) != 4 {
		t.Fatalf("len(Builtins()) = %d, want 4", len(themes))
	}
	modes := map[Mode]bool{}
	for _, theme := range themes {
		modes[theme.Mode] = true
		if err := theme.Validate(); err != nil {
			t.Fatalf("theme %q is invalid: %v", theme.ID, err)
		}
		if theme.ID == "aerok-dark" {
			if theme.Token.ColorBgLayout != "#090D16" || theme.Token.ColorBgContainer != "#121722" || theme.Token.ColorBgElevated != "#1B2230" {
				t.Fatalf("Aerok Dark surface ladder is incomplete: %#v", theme.Token)
			}
			if theme.Token.ColorTextSecondary == "" || theme.Token.ColorFillContentHover == "" || theme.Token.ColorBorderSecondary == "" {
				t.Fatalf("Aerok Dark semantic aliases are incomplete: %#v", theme.Token)
			}
		}
	}
	if !modes[ModeDark] || !modes[ModeLight] {
		t.Fatalf("builtin modes = %v, want dark and light", modes)
	}
}

func TestParseRejectsUnknownAndUnsafeTokens(t *testing.T) {
	base := `{"schemaVersion":1,"id":"safe-theme","name":"Safe","mode":"dark","token":{"colorPrimary":"#123456","colorSuccess":"#123456","colorWarning":"#123456","colorError":"#123456","colorInfo":"#123456","colorTextBase":"#ffffff","colorBgBase":"#000000"}}`
	if _, err := Parse([]byte(base)); err != nil {
		t.Fatalf("Parse(valid) error = %v", err)
	}
	unknown := strings.Replace(base, `"colorBgBase":"#000000"`, `"colorBgBase":"#000000","componentPadding":9999`, 1)
	if _, err := Parse([]byte(unknown)); err == nil {
		t.Fatal("Parse accepted an unknown layout token")
	}
	fontOverride := strings.Replace(base, `"colorBgBase":"#000000"`, `"colorBgBase":"#000000","fontFamily":"Comic Sans MS"`, 1)
	if _, err := Parse([]byte(fontOverride)); err == nil {
		t.Fatal("Parse accepted a typography override")
	}
	unsafe := strings.Replace(base, `"colorPrimary":"#123456"`, `"colorPrimary":"red; background:url(https://example.test)"`, 1)
	if _, err := Parse([]byte(unsafe)); err == nil {
		t.Fatal("Parse accepted an unsafe CSS value")
	}
	for _, color := range []string{
		"#12345",
		"rgb(,,,)",
		"rgb(256, 0, 0)",
		"rgb(12%, 34, 56%)",
		"RGB(12, 34, 56)",
		"rgba(0, 0, 0, 1.1)",
		"HSL(240, 100%, 50%)",
		"hsl(0, 101%, 50%)",
		"hsla(0, 50%, 50%)",
		"transparent",
		"currentColor",
		" #123456",
	} {
		invalid := strings.Replace(base, `"colorPrimary":"#123456"`, `"colorPrimary":"`+color+`"`, 1)
		if _, err := ParseForInstall([]byte(invalid)); err == nil {
			t.Errorf("ParseForInstall accepted unstable color %q", color)
		}
	}
	for _, color := range []string{
		"#1234",
		"#12345678",
		"rgb(12, 34, 56)",
		"rgb(12%, 34%, 56%)",
		"rgba(12, 34, 56, 0.5)",
		"hsl(240, 100%, 50%)",
		"hsla(240, 100%, 50%, 25%)",
	} {
		valid := strings.Replace(base, `"colorPrimary":"#123456"`, `"colorPrimary":"`+color+`"`, 1)
		if _, err := ParseForInstall([]byte(valid)); err != nil {
			t.Errorf("ParseForInstall rejected stable color %q: %v", color, err)
		}
	}
}

func TestParseRetainsPublishedVersionOneColorCompatibility(t *testing.T) {
	base := `{"schemaVersion":1,"id":"legacy-theme","name":"Legacy","mode":"dark","token":{"colorPrimary":"#123456","colorSuccess":"#123456","colorWarning":"#123456","colorError":"#123456","colorInfo":"#123456","colorTextBase":"#ffffff","colorBgBase":"#000000"}}`
	for _, color := range []string{
		"#12345",
		"rgb(,,,)",
		"rgb(256, 0, 0)",
		"rgb(12%, 34, 56%)",
		"RGB(12, 34, 56)",
		"rgba(0, 0, 0, 1.1)",
		"HSL(240, 100%, 50%)",
		"hsl(0, 101%, 50%)",
		"hsla(0, 50%, 50%)",
		"transparent",
		"currentColor",
		" #123456",
	} {
		legacy := strings.Replace(base, `"colorPrimary":"#123456"`, `"colorPrimary":"`+color+`"`, 1)
		if _, err := Parse([]byte(legacy)); err != nil {
			t.Errorf("Parse broke v1 compatibility for %q: %v", color, err)
		}
	}
}

func TestRegistryHotLoadsExternalThemesAndRejectsBuiltinReplacement(t *testing.T) {
	directory := t.TempDir()
	external := `{"schemaVersion":1,"id":"customer-blue","name":"Customer Blue","mode":"light","token":{"colorPrimary":"#123456","colorSuccess":"#17803d","colorWarning":"#a15c00","colorError":"#b42318","colorInfo":"#026aa2","colorTextBase":"#101828","colorBgBase":"#ffffff"}}`
	if err := os.WriteFile(filepath.Join(directory, "customer-blue.theme.json"), []byte(external), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(directory)
	themes, err := registry.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(themes) != 5 {
		t.Fatalf("len(List()) = %d, want 5", len(themes))
	}
	if theme, ok, err := registry.Find("customer-blue"); err != nil || !ok || theme.Name != "Customer Blue" {
		t.Fatalf("Find(customer-blue) = %#v, %v, %v", theme, ok, err)
	}
	duplicate := strings.Replace(external, `"id":"customer-blue"`, `"id":"aerok-dark"`, 1)
	if err := os.WriteFile(filepath.Join(directory, "duplicate.theme.json"), []byte(duplicate), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.List(); err == nil {
		t.Fatal("List accepted a duplicate builtin id")
	}
}
