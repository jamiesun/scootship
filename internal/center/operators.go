package center

import (
	"errors"
	"net/http"

	"github.com/jamiesun/scootship/internal/operators"
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
	LastLoginAgo     string
	CapabilityLabels []string
}

type operatorsPage struct {
	basePage
	Operators []operatorRow
	Create    formMessage
}

type operatorEditPage struct {
	basePage
	Operator          operators.Snapshot
	CapabilityOptions []capabilityOption
	Profile           formMessage
	Password          formMessage
}

type operatorCreatePage struct {
	basePage
	CapabilityOptions []capabilityOption
	Create            formMessage
}

type capabilityOption struct {
	Value   string
	Label   string
	Checked bool
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	op, ok := s.operators.Get(user)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "operator_missing", "current operator no longer exists")
		return
	}
	s.render(w, "account", accountPage{
		basePage: s.base(r, user, "account", "page.account"),
		Operator: op,
	})
}

func (s *Server) handleAccountUpdate(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	lang := requestLang(r)
	if err := r.ParseForm(); err != nil {
		s.renderAccountMessage(w, r, user, formMessage{Error: tr(lang, "form.read_failed")}, formMessage{})
		return
	}
	err := s.operators.UpdateProfile(user, r.PostFormValue("display_name"), r.PostFormValue("email"), s.now())
	if err != nil {
		s.renderAccountMessage(w, r, user, formMessage{Error: tr(lang, "form.update_profile_failed")}, formMessage{})
		return
	}
	s.renderAccountMessage(w, r, user, formMessage{OK: tr(lang, "form.profile_updated")}, formMessage{})
}

func (s *Server) handleAccountPassword(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	lang := requestLang(r)
	if err := r.ParseForm(); err != nil {
		s.renderAccountMessage(w, r, user, formMessage{}, formMessage{Error: tr(lang, "form.read_failed")})
		return
	}
	newPass := r.PostFormValue("new_password")
	if newPass == "" || newPass != r.PostFormValue("confirm_password") {
		s.renderAccountMessage(w, r, user, formMessage{}, formMessage{Error: tr(lang, "form.new_password_mismatch")})
		return
	}
	err := s.operators.ChangePassword(user, r.PostFormValue("current_password"), newPass, s.now())
	if err != nil {
		msg := tr(lang, "form.change_password_failed")
		if errors.Is(err, operators.ErrBadCredentials) {
			msg = tr(lang, "form.current_password_incorrect")
		}
		s.renderAccountMessage(w, r, user, formMessage{}, formMessage{Error: msg})
		return
	}
	s.renderAccountMessage(w, r, user, formMessage{}, formMessage{OK: tr(lang, "form.password_changed")})
}

func (s *Server) renderAccountMessage(w http.ResponseWriter, r *http.Request, username string, profile, password formMessage) {
	op, ok := s.operators.Get(username)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "operator_missing", "current operator no longer exists")
		return
	}
	s.render(w, "account", accountPage{
		basePage: s.base(r, username, "account", "page.account"),
		Operator: op,
		Profile:  profile,
		Password: password,
	})
}

func (s *Server) handleOperators(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	s.render(w, "operators", operatorsPage{
		basePage:  s.base(r, user, "operators", "page.operators"),
		Operators: s.operatorRows(requestLang(r)),
	})
}

func (s *Server) handleOperatorCreatePage(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	lang := requestLang(r)
	s.render(w, "operator_new", operatorCreatePage{
		basePage:          s.base(r, user, "operators", "page.create_operator"),
		CapabilityOptions: capabilityOptions(lang, operators.AllCapabilities()),
	})
}

func (s *Server) handleOperatorCreate(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	lang := requestLang(r)
	if err := r.ParseForm(); err != nil {
		s.renderOperatorCreateMessage(w, r, user, formMessage{Error: tr(lang, "form.read_failed")})
		return
	}
	password := r.PostFormValue("password")
	if password == "" || password != r.PostFormValue("confirm_password") {
		s.renderOperatorCreateMessage(w, r, user, formMessage{Error: tr(lang, "form.password_mismatch")})
		return
	}
	caps := capabilitiesFromForm(r)
	err := s.operators.Create(r.PostFormValue("username"), r.PostFormValue("display_name"), r.PostFormValue("email"), password, caps, s.now())
	if err != nil {
		msg := tr(lang, "form.create_operator_failed")
		if errors.Is(err, operators.ErrDuplicate) {
			msg = tr(lang, "form.operator_duplicate")
		}
		if errors.Is(err, operators.ErrInvalid) {
			msg = tr(lang, "form.operator_invalid")
		}
		s.renderOperatorCreateMessage(w, r, user, formMessage{Error: msg})
		return
	}
	s.renderOperatorsMessage(w, r, user, formMessage{OK: tr(lang, "form.operator_created")})
}

func (s *Server) renderOperatorsMessage(w http.ResponseWriter, r *http.Request, username string, create formMessage) {
	s.render(w, "operators", operatorsPage{
		basePage:  s.base(r, username, "operators", "page.operators"),
		Operators: s.operatorRows(requestLang(r)),
		Create:    create,
	})
}

func (s *Server) renderOperatorCreateMessage(w http.ResponseWriter, r *http.Request, username string, create formMessage) {
	lang := requestLang(r)
	selected := capabilitiesFromForm(r)
	if len(selected) == 0 && r.PostForm == nil {
		selected = operators.AllCapabilities()
	}
	s.render(w, "operator_new", operatorCreatePage{
		basePage:          s.base(r, username, "operators", "page.create_operator"),
		CapabilityOptions: capabilityOptions(lang, selected),
		Create:            create,
	})
}

func (s *Server) operatorRows(lang string) []operatorRow {
	snaps := s.operators.List()
	rows := make([]operatorRow, 0, len(snaps))
	for _, snap := range snaps {
		row := operatorRow{Snapshot: snap, LastLoginAgo: tr(lang, "common.never")}
		if snap.LastLoginMS != 0 {
			row.LastLoginAgo = s.agoLang(lang, snap.LastLoginMS)
		}
		for _, cap := range snap.Capabilities {
			row.CapabilityLabels = append(row.CapabilityLabels, capabilityLabel(lang, cap))
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
	lang := requestLang(r)
	s.render(w, "operator_edit", operatorEditPage{
		basePage:          s.baseTitle(r, user, "operators", tr(lang, "operators.title")+" "+op.Username),
		Operator:          op,
		CapabilityOptions: capabilityOptions(lang, op.Capabilities),
	})
}

func (s *Server) handleOperatorUpdate(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	lang := requestLang(r)
	username := r.PathValue("username")
	if err := r.ParseForm(); err != nil {
		s.renderOperatorEditMessage(w, r, user, username, formMessage{Error: tr(lang, "form.read_failed")}, formMessage{})
		return
	}
	action := r.PostFormValue("action")
	if action == "password" {
		newPass := r.PostFormValue("new_password")
		if newPass == "" || newPass != r.PostFormValue("confirm_password") {
			s.renderOperatorEditMessage(w, r, user, username, formMessage{}, formMessage{Error: tr(lang, "form.password_mismatch")})
			return
		}
		if err := s.operators.SetPassword(username, newPass, s.now()); err != nil {
			s.renderOperatorEditMessage(w, r, user, username, formMessage{}, formMessage{Error: tr(lang, "form.reset_password_failed")})
			return
		}
		s.renderOperatorEditMessage(w, r, user, username, formMessage{}, formMessage{OK: tr(lang, "form.password_reset")})
		return
	}
	caps := capabilitiesFromForm(r)
	if username == user && !operators.HasCapability(caps, operators.CapabilityOperatorManage) {
		s.renderOperatorEditMessage(w, r, user, username, formMessage{Error: tr(lang, "form.operator_self_capability")}, formMessage{})
		return
	}
	if err := s.operators.UpdateProfileAndCapabilities(username, r.PostFormValue("display_name"), r.PostFormValue("email"), caps, s.now()); err != nil {
		s.renderOperatorEditMessage(w, r, user, username, formMessage{Error: tr(lang, "form.update_profile_failed")}, formMessage{})
		return
	}
	s.renderOperatorEditMessage(w, r, user, username, formMessage{OK: tr(lang, "form.profile_updated")}, formMessage{})
}

func (s *Server) renderOperatorEditMessage(w http.ResponseWriter, r *http.Request, currentUser, username string, profile, password formMessage) {
	op, ok := s.operators.Get(username)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not_found", "unknown operator")
		return
	}
	lang := requestLang(r)
	s.render(w, "operator_edit", operatorEditPage{
		basePage:          s.baseTitle(r, currentUser, "operators", tr(lang, "operators.title")+" "+op.Username),
		Operator:          op,
		CapabilityOptions: capabilityOptions(lang, op.Capabilities),
		Profile:           profile,
		Password:          password,
	})
}

func (s *Server) handleAPIOperators(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"now_ms":    s.now().UnixMilli(),
		"operators": s.operators.List(),
	})
}

func capabilitiesFromForm(r *http.Request) []operators.Capability {
	values := r.PostForm["capabilities"]
	out := make([]operators.Capability, 0, len(values))
	for _, v := range values {
		out = append(out, operators.Capability(v))
	}
	return out
}

func capabilityOptions(lang string, selected []operators.Capability) []capabilityOption {
	options := make([]capabilityOption, 0, len(operators.AllCapabilities()))
	for _, cap := range operators.AllCapabilities() {
		options = append(options, capabilityOption{
			Value:   string(cap),
			Label:   capabilityLabel(lang, cap),
			Checked: operators.HasCapability(selected, cap),
		})
	}
	return options
}

func capabilityLabel(lang string, cap operators.Capability) string {
	switch cap {
	case operators.CapabilityFleetView:
		return tr(lang, "capability.fleet_view")
	case operators.CapabilityTokenManage:
		return tr(lang, "capability.tokens_manage")
	case operators.CapabilityOperatorManage:
		return tr(lang, "capability.operators_manage")
	default:
		return string(cap)
	}
}
