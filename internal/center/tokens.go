package center

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/jamiesun/scootship/internal/tokens"
)

const managedTokenMinLength = 16
const managedTokenBytes = 32

func (s *Server) handleTokenCreatePage(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	s.render(w, "token_new", tokenCreatePage{
		basePage: s.base(r, user, "tokens", "page.create_token"),
	})
}

func (s *Server) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	lang := requestLang(r)
	if err := r.ParseForm(); err != nil {
		s.renderTokenCreateMessage(w, r, user, formMessage{Error: tr(lang, "form.read_failed")})
		return
	}
	nodeID, errKey := validateTokenNodeID(r.PostFormValue("node_id"))
	if errKey != "" {
		s.renderTokenCreateMessage(w, r, user, formMessage{Error: tr(lang, errKey)})
		return
	}
	secret, err := generateManagedTokenSecret()
	if err != nil {
		s.renderTokenCreateMessage(w, r, user, formMessage{Error: tr(lang, "form.token_create_failed")})
		return
	}
	if err := s.tokens.UpsertManaged(nodeID, secret); err != nil {
		s.renderTokenCreateMessage(w, r, user, formMessage{Error: tokenErrorMessage(lang, err, "form.token_create_failed")})
		return
	}
	s.renderTokensMessage(w, r, user, formMessage{OK: tr(lang, "form.token_created")}, tokenSecretReveal{
		NodeID: nodeID,
		Secret: secret,
		Action: tr(lang, "tokens.created_secret"),
	})
}

func (s *Server) handleTokenRotate(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	lang := requestLang(r)
	if err := r.ParseForm(); err != nil {
		s.renderTokensMessage(w, r, user, formMessage{Error: tr(lang, "form.read_failed")}, tokenSecretReveal{})
		return
	}
	nodeID, errKey := validateTokenNodeID(r.PathValue("id"))
	if errKey != "" {
		s.renderTokensMessage(w, r, user, formMessage{Error: tr(lang, errKey)}, tokenSecretReveal{})
		return
	}
	secret, err := generateManagedTokenSecret()
	if err != nil {
		s.renderTokensMessage(w, r, user, formMessage{Error: tr(lang, "form.token_rotate_failed")}, tokenSecretReveal{})
		return
	}
	if err := s.tokens.UpsertManaged(nodeID, secret); err != nil {
		s.renderTokensMessage(w, r, user, formMessage{Error: tokenErrorMessage(lang, err, "form.token_rotate_failed")}, tokenSecretReveal{})
		return
	}
	s.renderTokensMessage(w, r, user, formMessage{OK: tr(lang, "form.token_rotated")}, tokenSecretReveal{
		NodeID: nodeID,
		Secret: secret,
		Action: tr(lang, "tokens.rotated_secret"),
	})
}

func (s *Server) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	lang := requestLang(r)
	nodeID := strings.TrimSpace(r.PathValue("id"))
	if !validManagedNodeID(nodeID) {
		s.renderTokensMessage(w, r, user, formMessage{Error: tr(lang, "form.token_node_invalid")}, tokenSecretReveal{})
		return
	}
	if err := s.tokens.RevokeManaged(nodeID); err != nil {
		s.renderTokensMessage(w, r, user, formMessage{Error: tokenErrorMessage(lang, err, "form.token_revoke_failed")}, tokenSecretReveal{})
		return
	}
	s.renderTokensMessage(w, r, user, formMessage{OK: tr(lang, "form.token_revoked")}, tokenSecretReveal{})
}

func (s *Server) renderTokensMessage(w http.ResponseWriter, r *http.Request, username string, msg formMessage, reveal tokenSecretReveal) {
	s.render(w, "tokens", s.tokensPage(r, username, msg, reveal))
}

func (s *Server) renderTokenCreateMessage(w http.ResponseWriter, r *http.Request, username string, msg formMessage) {
	s.render(w, "token_new", tokenCreatePage{
		basePage: s.base(r, username, "tokens", "page.create_token"),
		Create:   msg,
	})
}

func validateTokenNodeID(nodeID string) (string, string) {
	nodeID = strings.TrimSpace(nodeID)
	if !validManagedNodeID(nodeID) {
		return "", "form.token_node_invalid"
	}
	return nodeID, ""
}

func validateTokenForm(nodeID, token, confirm string) (string, string, string) {
	nodeID, errKey := validateTokenNodeID(nodeID)
	if errKey != "" {
		return "", "", errKey
	}
	token = strings.TrimSpace(token)
	confirm = strings.TrimSpace(confirm)
	if token == "" || len(token) < managedTokenMinLength {
		return "", "", "form.token_secret_invalid"
	}
	if token != confirm {
		return "", "", "form.token_secret_mismatch"
	}
	return nodeID, token, ""
}

func generateManagedTokenSecret() (string, error) {
	raw := make([]byte, managedTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
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
