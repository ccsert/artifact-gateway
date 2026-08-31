package repository

import (
	"context"
	"errors"
	"time"
)

var ErrUnsupportedBrowseFormat = errors.New("repository format does not support directory browsing")

type BrowseNodeKind string

const (
	BrowseNodeDirectory BrowseNodeKind = "directory"
	BrowseNodeNamespace BrowseNodeKind = "namespace"
	BrowseNodeComponent BrowseNodeKind = "component"
	BrowseNodeVersion   BrowseNodeKind = "version"
	BrowseNodeAsset     BrowseNodeKind = "asset"
)

// ArtifactBrowseParent is the decoded server-owned position of one lazy tree
// expansion. Clients receive only its signed opaque representation.
type ArtifactBrowseParent struct {
	Kind      BrowseNodeKind
	Namespace string
	Component string
	Version   string
	Path      string
}

// ArtifactBrowseNode is a format-aware navigation projection. Key is an
// internal stable sort position; it is never exposed as a client-owned ID.
type ArtifactBrowseNode struct {
	Key         string
	Kind        BrowseNodeKind
	Name        string
	HasChildren bool
	Namespace   string
	Component   string
	Version     string
	Path        string
	Coordinate  string
	Digest      string
	Size        int64
	ContentType string
	CreatedAt   time.Time
}

type ArtifactBrowseStore interface {
	ListArtifactBrowseNodes(context.Context, string, Format, ArtifactBrowseParent, int, string) ([]ArtifactBrowseNode, error)
}
