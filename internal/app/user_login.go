package app

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// userLoginHandler exchanges local username/password credentials for a
// stateless session token. Unknown users, wrong passwords, and disabled
// accounts all return the same error to avoid user enumeration.
func userLoginHandler(users repository.UserStore, authenticator Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || body.Username == "" || body.Password == "" {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "username and password are required")
			return
		}
		user, err := users.GetUserByName(r.Context(), body.Username)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "login failed")
			return
		}
		if errors.Is(err, repository.ErrNotFound) || user.State != repository.UserActive || !authorization.VerifyPassword(user.SecretHash, body.Password) {
			writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "invalid credentials")
			return
		}
		token := authenticator.IssueUserSession(user.ID)
		writeNativeMavenJSON(w, http.StatusOK, map[string]string{"token": token, "username": user.Name, "role": user.Role})
	}
}
