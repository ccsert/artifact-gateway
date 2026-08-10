package scanning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strconv"
)

type wireAsset struct {
	Part      string `json:"part"`
	Path      string `json:"path"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType,omitempty"`
}

type wireRequest struct {
	SchemaVersion string      `json:"schemaVersion"`
	RepositoryID  string      `json:"repositoryId"`
	Format        string      `json:"format"`
	Coordinate    string      `json:"coordinate"`
	Digest        string      `json:"digest"`
	Assets        []wireAsset `json:"assets"`
}

func writeArtifact(ctx context.Context, writer *multipart.Writer, artifact Artifact) error {
	assets := make([]wireAsset, 0, len(artifact.Assets))
	for index, asset := range artifact.Assets {
		assets = append(assets, wireAsset{
			Part: "asset-" + strconv.Itoa(index), Path: asset.Path, Digest: asset.Digest,
			Size: asset.Size, MediaType: asset.MediaType,
		})
	}
	metadata, err := json.Marshal(wireRequest{
		SchemaVersion: SchemaVersion, RepositoryID: artifact.RepositoryID,
		Format: string(artifact.Format), Coordinate: artifact.Coordinate,
		Digest: artifact.Digest, Assets: assets,
	})
	if err != nil {
		return ErrInvalidArtifact
	}
	metadataHeader := make(textproto.MIMEHeader)
	metadataHeader.Set("Content-Disposition", `form-data; name="metadata"`)
	metadataHeader.Set("Content-Type", "application/json")
	part, err := writer.CreatePart(metadataHeader)
	if err != nil {
		return ErrScannerUnavailable
	}
	if _, err = part.Write(metadata); err != nil {
		return ErrScannerUnavailable
	}
	for index, asset := range artifact.Assets {
		partName := "asset-" + strconv.Itoa(index)
		assetHeader := make(textproto.MIMEHeader)
		assetHeader.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": partName, "filename": asset.Path}))
		mediaType := asset.MediaType
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		assetHeader.Set("Content-Type", mediaType)
		part, err = writer.CreatePart(assetHeader)
		if err != nil {
			return ErrScannerUnavailable
		}
		body, openErr := asset.Open(ctx)
		if openErr != nil {
			return ErrScannerUnavailable
		}
		copyErr := copyVerified(part, body, asset.Size, asset.Digest)
		closeErr := body.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return ErrScannerUnavailable
		}
	}
	return nil
}

func copyVerified(destination io.Writer, source io.Reader, size int64, digest string) error {
	hash := sha256.New()
	written, err := io.Copy(destination, io.TeeReader(io.LimitReader(source, size+1), hash))
	if err != nil {
		return ErrScannerUnavailable
	}
	if written != size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != digest {
		return ErrAssetIntegrity
	}
	return nil
}
