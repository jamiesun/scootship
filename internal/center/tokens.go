package center

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jamiesun/scootship/internal/tokens"
)

const managedTokenMinLength = 16

func (s *Server) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	lang := requestLang(r)
	if err := r.ParseForm(); err != nil {
		s.renderTokensMessage(w, r, user, formMessage{Error: tr(lang, "form.read_failed")})
		return
	}
	nodeID, secret, errKey := validateTokenForm(r.PostFormValue("node_id"), r.PostFormValue("token"), r.PostFormValue("confirm_token"))
	if errKey != "" {
		s.renderTokensMessage(w, r, user, formMessage{Error: tr(lang, errKey)})
		return
	}
	if err := s.tokens.UpsertManaged(nodeID, secret); err != nil {
		s.renderTokensMessage(w, r, user, formMessage{Error: tokenErrorMessage(lang, err, "form.token_create_failed")})
		return
	}
	s.renderTokensMessage(w, r, user, formMessage{OK: tr(lang, "form.token_created")})
}

func (s *Server) handleTokenRotate(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	lang := requestLang(r)
	if err := r.ParseForm(); err != nil {
		s.renderTokensMessage(w, r, user, formMessage{Error: tr(lang, "form.read_failed")})
		return
	}
	nodeID, secret, errKey := validateTokenForm(r.PathValue("id"), r.PostFormValue("token"), r.PostFormValue("confirm_token"))
	if errKey != "" {
		s.renderTokensMessage(w, r, user, formMessage{Error: tr(lang, errKey)})
		return
	}
	if err := s.tokens.UpsertManaged(nodeID, secret); err != nil {
		s.renderTokensMessage(w, r, user, formMessage{Error: tokenErrorMessage(lang, err, "form.token_rotate_failed")})
		return
	}
	s.renderTokensMessage(w, r, user, formMessage{OK: tr(lang, "form.token_rotated")})
}

func (s *Server) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	lang := requestLang(r)
	nodeID := strings.TrimSpace(r.PathValue("id"))
	if !validManagedNodeID(nodeID) {
		s.renderTokensMessage(w, r, user, formMessage{Error: tr(lang, "form.token_node_invalid")})
		return
	}
	if err := s.tokens.RevokeManaged(nodeID); err != nil {
		s.renderTokensMessage(w, r, user, formMessage{Error: tokenErrorMessage(lang, err, "form.token_revoke_failed")})
		return
	}
	s.renderTokensMessage(w, r, user, formMessage{OK: tr(lang, "form.token_revoked")})
}

func (s *Server) renderTokensMessage(w http.ResponseWriter, r *http.Request, username string, msg formMessage) {
	s.render(w, "tokens", s.tokensPage(r, username, msg))
}

func validateTokenForm(nodeID, token, confirm string) (string, string, string) {
	nodeID = strings.TrimSpace(nodeID)
	token = strings.TrimSpace(token)
	confirm = strings.TrimSpace(confirm)
	if !validManagedNodeID(nodeID) {
		return "", "", "form.token_node_invalid"
	}
	if token == "" || len(token) < managedTokenMinLength {
		return "", "", "form.token_secret_invalid"
	}
	if token != confirm {
		return "", "", "form.token_secret_mismatch"
	}
	return nodeID, token, ""
}

func validManagedNodeID(nodeID string) bool {
	if nodeID == "" || len(nodeID) > 128 {
		return false
	}
	return !strings.ContainsAny(nodeID, "/\\ \t\r\n")
}

func tokenErrorMessage(lang string, err error, fallbackKey string) string {
	if errors.Is(err, tokens.ErrInvalidEntry) {
		return tr(lang, "form.token_node_invalid")
	}
	if errors.Is(err, tokens.ErrDuplicateToken) {
		return tr(lang, "form.token_duplicate")
	}
	return tr(lang, fallbackKey)
}
