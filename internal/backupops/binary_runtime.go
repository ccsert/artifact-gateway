package backupops

import "errors"

// These immutable children belong to the existing trusted upstream index
// sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab.
// Its original bytes are retained in testdata/binary-runtime-index.json. Select
// by the verified binary platform instead of a host-dependent cached index alias.
func binaryRuntimeImage(platform string) (string, error) {
	switch platform {
	case "linux/amd64":
		return "gcr.io/distroless/static-debian12:nonroot@sha256:52dcfbabb7457ea47c82f6e13af8c8a4a1d9f7b0145142b3ecab20f2b888411d", nil
	case "linux/arm64":
		return "gcr.io/distroless/static-debian12:nonroot@sha256:06c3c14b7fa252d9f4285242d2d665ce3b51f74b02d3c529464f220f2912aecb", nil
	default:
		return "", errors.New("unsupported binary runtime platform")
	}
}
