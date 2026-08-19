package consoletheme

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const (
	SchemaVersion = 1
	MaxFileBytes  = 256 << 10
)

type Mode string

const (
	ModeDark  Mode = "dark"
	ModeLight Mode = "light"
)

// Token is the stable Ant Design Seed/Alias token subset accepted by Console
// theme packages. Component geometry remains owned by the Console so a theme
// cannot unexpectedly change page layout or control density.
type Token struct {
	ColorPrimary          string `json:"colorPrimary"`
	ColorSuccess          string `json:"colorSuccess"`
	ColorWarning          string `json:"colorWarning"`
	ColorError            string `json:"colorError"`
	ColorInfo             string `json:"colorInfo"`
	ColorTextBase         string `json:"colorTextBase"`
	ColorBgBase           string `json:"colorBgBase"`
	ColorText             string `json:"colorText,omitempty"`
	ColorTextSecondary    string `json:"colorTextSecondary,omitempty"`
	ColorTextTertiary     string `json:"colorTextTertiary,omitempty"`
	ColorTextQuaternary   string `json:"colorTextQuaternary,omitempty"`
	ColorTextDisabled     string `json:"colorTextDisabled,omitempty"`
	ColorBgContainer      string `json:"colorBgContainer,omitempty"`
	ColorBgElevated       string `json:"colorBgElevated,omitempty"`
	ColorBgLayout         string `json:"colorBgLayout,omitempty"`
	ColorBgSpotlight      string `json:"colorBgSpotlight,omitempty"`
	ColorBorder           string `json:"colorBorder,omitempty"`
	ColorBorderSecondary  string `json:"colorBorderSecondary,omitempty"`
	ColorFillAlter        string `json:"colorFillAlter,omitempty"`
	ColorFillContent      string `json:"colorFillContent,omitempty"`
	ColorFillContentHover string `json:"colorFillContentHover,omitempty"`
	ColorLink             string `json:"colorLink,omitempty"`
	ColorLinkHover        string `json:"colorLinkHover,omitempty"`
	ColorLinkActive       string `json:"colorLinkActive,omitempty"`
	ColorPrimaryHover     string `json:"colorPrimaryHover,omitempty"`
	ColorPrimaryActive    string `json:"colorPrimaryActive,omitempty"`
	ColorPrimaryBg        string `json:"colorPrimaryBg,omitempty"`
	ControlOutline        string `json:"controlOutline,omitempty"`
}

type Theme struct {
	Schema        string `json:"$schema,omitempty"`
	SchemaVersion int    `json:"schemaVersion"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Mode          Mode   `json:"mode"`
	Token         Token  `json:"token"`
}

//go:embed builtin/*.theme.json
var builtinFiles embed.FS

var (
	idPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)
	colorPattern = regexp.MustCompile(`(?i)^(#[0-9a-f]{3,8}|rgba?\([0-9.,% ]+\)|hsla?\([0-9.,% ]+\)|transparent|currentcolor)$`)
	builtins     []Theme
	builtinErr   error
	builtinOnce  sync.Once
)

func Parse(data []byte) (Theme, error) {
	if len(data) == 0 || len(data) > MaxFileBytes {
		return Theme{}, fmt.Errorf("theme file must contain 1 to %d bytes", MaxFileBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var theme Theme
	if err := decoder.Decode(&theme); err != nil {
		return Theme{}, fmt.Errorf("decode theme: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Theme{}, errors.New("theme file must contain one JSON object")
	}
	if err := theme.Validate(); err != nil {
		return Theme{}, err
	}
	return theme, nil
}

func LoadFile(path string) (Theme, error) {
	file, err := os.Open(path)
	if err != nil {
		return Theme{}, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return Theme{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxFileBytes {
		return Theme{}, fmt.Errorf("theme file must be a regular file no larger than %d bytes", MaxFileBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxFileBytes+1))
	if err != nil {
		return Theme{}, err
	}
	return Parse(data)
}

func (theme Theme) Validate() error {
	if theme.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schemaVersion must be %d", SchemaVersion)
	}
	if !idPattern.MatchString(theme.ID) {
		return errors.New("id must contain 2 to 64 lowercase letters, digits, or hyphens")
	}
	if !validText(theme.Name, 80) {
		return errors.New("name must contain 1 to 80 visible characters")
	}
	if theme.Description != "" && !validText(theme.Description, 240) {
		return errors.New("description must contain at most 240 visible characters")
	}
	if theme.Mode != ModeDark && theme.Mode != ModeLight {
		return errors.New("mode must be dark or light")
	}
	for name, value := range theme.Token.colors() {
		if value == "" {
			if theme.Token.requiredColor(name) {
				return fmt.Errorf("token.%s is required", name)
			}
			continue
		}
		if len(value) > 96 || !colorPattern.MatchString(strings.TrimSpace(value)) {
			return fmt.Errorf("token.%s must be a CSS color literal", name)
		}
	}
	return nil
}

func (token Token) colors() map[string]string {
	return map[string]string{
		"colorPrimary": token.ColorPrimary, "colorSuccess": token.ColorSuccess, "colorWarning": token.ColorWarning,
		"colorError": token.ColorError, "colorInfo": token.ColorInfo, "colorTextBase": token.ColorTextBase,
		"colorBgBase": token.ColorBgBase, "colorText": token.ColorText, "colorTextSecondary": token.ColorTextSecondary,
		"colorTextTertiary": token.ColorTextTertiary, "colorTextQuaternary": token.ColorTextQuaternary,
		"colorTextDisabled": token.ColorTextDisabled, "colorBgContainer": token.ColorBgContainer,
		"colorBgElevated": token.ColorBgElevated, "colorBgLayout": token.ColorBgLayout,
		"colorBgSpotlight": token.ColorBgSpotlight, "colorBorder": token.ColorBorder,
		"colorBorderSecondary": token.ColorBorderSecondary, "colorFillAlter": token.ColorFillAlter,
		"colorFillContent": token.ColorFillContent, "colorFillContentHover": token.ColorFillContentHover,
		"colorLink": token.ColorLink, "colorLinkHover": token.ColorLinkHover, "colorLinkActive": token.ColorLinkActive,
		"colorPrimaryHover": token.ColorPrimaryHover, "colorPrimaryActive": token.ColorPrimaryActive,
		"colorPrimaryBg": token.ColorPrimaryBg, "controlOutline": token.ControlOutline,
	}
}

func (Token) requiredColor(name string) bool {
	switch name {
	case "colorPrimary", "colorSuccess", "colorWarning", "colorError", "colorInfo", "colorTextBase", "colorBgBase":
		return true
	default:
		return false
	}
}

func validText(value string, maximum int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len([]rune(trimmed)) <= maximum && !strings.ContainsAny(trimmed, "\r\n\x00")
}

type Registry struct {
	directory string
}

func NewRegistry(directory string) *Registry {
	return &Registry{directory: strings.TrimSpace(directory)}
}

func (registry *Registry) List() ([]Theme, error) {
	themes, err := Builtins()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(themes))
	for _, theme := range themes {
		seen[theme.ID] = struct{}{}
	}
	if registry == nil || registry.directory == "" {
		return themes, nil
	}
	entries, err := os.ReadDir(registry.directory)
	if errors.Is(err, fs.ErrNotExist) {
		return themes, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Console theme directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".theme.json") {
			continue
		}
		theme, err := LoadFile(filepath.Join(registry.directory, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("load Console theme %s: %w", entry.Name(), err)
		}
		if _, exists := seen[theme.ID]; exists {
			return nil, fmt.Errorf("duplicate Console theme id %q", theme.ID)
		}
		seen[theme.ID] = struct{}{}
		themes = append(themes, theme)
	}
	sort.SliceStable(themes, func(i, j int) bool { return themes[i].Name < themes[j].Name })
	return themes, nil
}

func (registry *Registry) Find(id string) (Theme, bool, error) {
	themes, err := registry.List()
	if err != nil {
		return Theme{}, false, err
	}
	for _, theme := range themes {
		if theme.ID == id {
			return theme, true, nil
		}
	}
	return Theme{}, false, nil
}

func Builtins() ([]Theme, error) {
	builtinOnce.Do(func() {
		entries, err := fs.Glob(builtinFiles, "builtin/*.theme.json")
		if err != nil {
			builtinErr = err
		} else {
			for _, name := range entries {
				data, readErr := builtinFiles.ReadFile(name)
				if readErr != nil {
					builtinErr = readErr
					break
				}
				theme, parseErr := Parse(data)
				if parseErr != nil {
					builtinErr = fmt.Errorf("parse builtin %s: %w", name, parseErr)
					break
				}
				builtins = append(builtins, theme)
			}
		}
	})
	return append([]Theme(nil), builtins...), builtinErr
}
