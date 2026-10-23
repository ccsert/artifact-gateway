package nexusmigration

import (
	"encoding/json"
	"strings"
	"testing"

	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
)

func TestEntryResponseHidesPrivateMigrationMetadata(t *testing.T) {
	for _, tc := range []struct {
		format, metadataPath string
	}{
		{"npm", m.NPMPackumentPath},
		{"pypi", m.PyPISimplePath},
	} {
		t.Run(tc.format, func(t *testing.T) {
			entry := m.Entry{ID: "e1d04569-1166-4c04-aa70-7148bb3061a3", BatchID: "712ad47a-0940-497c-b02c-46c90f012aea", TargetRepositoryID: "62dc1b55-5b75-4478-9ff3-9f6c060665da", Format: tc.format, Actor: "private-actor", SessionID: "private-session", Assets: []m.Asset{
				{SourcePath: "private-metadata-source", TargetPath: tc.metadataPath, Size: 19, NPMPackument: []byte("private-packument"), PyPISimple: []byte("private-simple")},
				{SourcePath: "release-file", TargetPath: "release-file", Size: 7, SHA256: strings.Repeat("a", 64), Uploaded: true, Verified: true},
			}}
			response := entryResponse(entry)
			if len(response.Assets) != 1 || response.Assets[0].TargetPath != "release-file" || response.Assets[0].Size != 7 || !response.Assets[0].Verified {
				t.Fatalf("public asset evidence changed: %#v", response.Assets)
			}
			body, err := json.Marshal(response)
			if err != nil || strings.Contains(string(body), "private-") || strings.Contains(string(body), tc.metadataPath) {
				t.Fatalf("private metadata escaped: %s, %v", body, err)
			}
			if len(entry.Assets) != 2 || entry.Assets[0].TargetPath != tc.metadataPath {
				t.Fatal("response filtering mutated the durable inventory")
			}
		})
	}
	entry := m.Entry{ID: "e1d04569-1166-4c04-aa70-7148bb3061a3", BatchID: "712ad47a-0940-497c-b02c-46c90f012aea", TargetRepositoryID: "62dc1b55-5b75-4478-9ff3-9f6c060665da", Format: "raw", Assets: []m.Asset{{TargetPath: m.PyPISimplePath, Size: 3}}}
	if got := entryResponse(entry); len(got.Assets) != 1 {
		t.Fatal("a raw asset was mistaken for private PyPI metadata")
	}
}
