package center

import (
	"errors"
	"net/http"

	"github.com/jamiesun/scootship/internal/operators"
	"github.com/jamiesun/scootship/internal/version"
)

type formMessage struct {
	OK    string
	Error string
}

type accountPage struct {
	basePage
	Operator operators.Snapshot
	Profile  formMessage
	Password formMessage
}

type operatorRow struct {
	operators.Snapshot
	LastLoginAgo string
}

type operatorsPage struct {
	basePage
	Operators []operatorRow
	Create    formMessage
}

type operatorEditPage struct {
	basePage
	Operator operators.Snapshot
	Profile  formMessage
	Password formMessage
}

type operatorCreatePage struct {
	basePage
	Create formMessage
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	op, ok := s.operators.Get(user)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "operator_missing", "current operator no longer exists")
		return
	}
	s.render(w, "account", accountPage{
		basePage: basePage{Title: "Account", Version: version.Version, User: user, Active: "settings"},
		Operator: op,
	})
}

func (s *Server) handleAccountUpdate(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	if err := r.ParseForm(); err != nil {
		s.renderAccountMessage(w, user, formMessage{Error: "Could not read the form."}, formMessage{})
		return
	}
	err := s.operators.UpdateProfile(user, r.PostFormValue("display_name"), r.PostFormValue("email"), s.now())
	if err != nil {
		s.renderAccountMessage(w, user, formMessage{Error: "Could not update profile."}, formMessage{})
		return
	}
	s.renderAccountMessage(w, user, formMessage{OK: "Profile updated."}, formMessage{})
}

func (s *Server) handleAccountPassword(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	if err := r.ParseForm(); err != nil {
		s.renderAccountMessage(w, user, formMessage{}, formMessage{Error: "Could not read the form."})
		return
	}
	newPass := r.PostFormValue("new_password")
	if newPass == "" || newPass != r.PostFormValue("confirm_password") {
		s.renderAccountMessage(w, user, formMessage{}, formMessage{Error: "New passwords do not match."})
		return
	}
	err := s.operators.ChangePassword(user, r.PostFormValue("current_password"), newPass, s.now())
	if err != nil {
		msg := "Could not change password."
		if errors.Is(err, operators.ErrBadCredentials) {
			msg = "Current password is incorrect."
		}
		s.renderAccountMessage(w, user, formMessage{}, formMessage{Error: msg})
		return
	}
	s.renderAccountMessage(w, user, formMessage{}, formMessage{OK: "Password changed."})
}

func (s *Server) renderAccountMessage(w http.ResponseWriter, username string, profile, password formMessage) {
	op, ok := s.operators.Get(username)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "operator_missing", "current operator no longer exists")
		return
	}
	s.render(w, "account", accountPage{
		basePage: basePage{Title: "Account", Version: version.Version, User: username, Active: "settings"},
		Operator: op,
		Profile:  profile,
		Password: password,
	})
}

func (s *Server) handleOperators(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	s.render(w, "operators", operatorsPage{
		basePage:  basePage{Title: "Operators", Version: version.Version, User: user, Active: "operators"},
		Operators: s.operatorRows(),
	})
}

func (s *Server) handleOperatorCreatePage(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	s.render(w, "operator_new", operatorCreatePage{
		basePage: basePage{Title: "Create operator", Version: version.Version, User: user, Active: "operators"},
	})
}

func (s *Server) handleOperatorCreate(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	if err := r.ParseForm(); err != nil {
		s.renderOperatorCreateMessage(w, user, formMessage{Error: "Could not read the form."})
		return
	}
	password := r.PostFormValue("password")
	if password == "" || password != r.PostFormValue("confirm_password") {
		s.renderOperatorCreateMessage(w, user, formMessage{Error: "Passwords do not match."})
		return
	}
	err := s.operators.Create(r.PostFormValue("username"), r.PostFormValue("display_name"), r.PostFormValue("email"), password, s.now())
	if err != nil {
		msg := "Could not create operator."
		if errors.Is(err, operators.ErrDuplicate) {
			msg = "Operator already exists."
		}
		if errors.Is(err, operators.ErrInvalid) {
			msg = "Username and password are required."
		}
		s.renderOperatorCreateMessage(w, user, formMessage{Error: msg})
		return
	}
	s.renderOperatorsMessage(w, user, formMessage{OK: "Operator created."})
}

func (s *Server) renderOperatorsMessage(w http.ResponseWriter, username string, create formMessage) {
	s.render(w, "operators", operatorsPage{
		basePage:  basePage{Title: "Operators", Version: version.Version, User: username, Active: "operators"},
		Operators: s.operatorRows(),
		Create:    create,
	})
}

func (s *Server) renderOperatorCreateMessage(w http.ResponseWriter, username string, create formMessage) {
	s.render(w, "operator_new", operatorCreatePage{
		basePage: basePage{Title: "Create operator", Version: version.Version, User: username, Active: "operators"},
		Create:   create,
	})
}

func (s *Server) operatorRows() []operatorRow {
	snaps := s.operators.List()
	rows := make([]operatorRow, 0, len(snaps))
	for _, snap := range snaps {
		row := operatorRow{Snapshot: snap, LastLoginAgo: "never"}
		if snap.LastLoginMS != 0 {
			row.LastLoginAgo = s.ago(snap.LastLoginMS)
		}
		rows = append(rows, row)
	}
	return rows
}

func (s *Server) handleOperatorEdit(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	username := r.PathValue("username")
	op, ok := s.operators.Get(username)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not_found", "unknown operator")
		return
	}
	s.render(w, "operator_edit", operatorEditPage{
		basePage: basePage{Title: "Operator " + op.Username, Version: version.Version, User: user, Active: "operators"},
		Operator: op,
	})
}

func (s *Server) handleOperatorUpdate(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	username := r.PathValue("username")
	if err := r.ParseForm(); err != nil {
		s.renderOperatorEditMessage(w, user, username, formMessage{Error: "Could not read the form."}, formMessage{})
		return
	}
	action := r.PostFormValue("action")
	if action == "password" {
		newPass := r.PostFormValue("new_password")
		if newPass == "" || newPass != r.PostFormValue("confirm_password") {
			s.renderOperatorEditMessage(w, user, username, formMessage{}, formMessage{Error: "Passwords do not match."})
			return
		}
		if err := s.operators.SetPassword(username, newPass, s.now()); err != nil {
			s.renderOperatorEditMessage(w, user, username, formMessage{}, formMessage{Error: "Could not reset password."})
			return
		}
		s.renderOperatorEditMessage(w, user, username, formMessage{}, formMessage{OK: "Password reset."})
		return
	}
	if err := s.operators.UpdateProfile(username, r.PostFormValue("display_name"), r.PostFormValue("email"), s.now()); err != nil {
		s.renderOperatorEditMessage(w, user, username, formMessage{Error: "Could not update profile."}, formMessage{})
		return
	}
	s.renderOperatorEditMessage(w, user, username, formMessage{OK: "Profile updated."}, formMessage{})
}

func (s *Server) renderOperatorEditMessage(w http.ResponseWriter, currentUser, username string, profile, password formMessage) {
	op, ok := s.operators.Get(username)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not_found", "unknown operator")
		return
	}
	s.render(w, "operator_edit", operatorEditPage{
		basePage: basePage{Title: "Operator " + op.Username, Version: version.Version, User: currentUser, Active: "operators"},
		Operator: op,
		Profile:  profile,
		Password: password,
	})
}

func (s *Server) handleAPIOperators(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"now_ms":    s.now().UnixMilli(),
		"operators": s.operators.List(),
	})
}
