package center

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jamiesun/scootship/internal/protocol"
	"github.com/jamiesun/scootship/internal/store"
)

// Bounds for the operator-facing dispatch creation form. These are UI-side
// sanity limits, not part of the wire contract; protocol.JobBody.Validate
// still enforces the actual EDGE.md constraints server-side.
const (
	maxDispatchDeadlineMinutes     = 1440
	maxDispatchRetries             = 5
	defaultDispatchDeadlineMinutes = 60
)

// dispatchCreatePage renders the "/dispatch/new" form. Node targeting is
// explicit (a select of known nodes); there is no broad fan-out option by
// design, matching store.DispatchRequest's node-targeted shape.
type dispatchCreatePage struct {
	basePage
	Nodes  []dispatchNodeOption
	Form   dispatchCreateForm
	Create formMessage
}

type dispatchNodeOption struct {
	NodeID   string
	Online   bool
	Selected bool
}

// dispatchCreateForm carries both the default values for a fresh GET and the
// operator's submitted values echoed back on a validation error, so a failed
// submission never silently discards what they typed.
type dispatchCreateForm struct {
	NodeID          string
	Goal            string
	RequestedPolicy string
	DeadlineMinutes string
	MaxRetries      string
	RequiredLabels  string
	RequiredTools   string
	RequiredSkills  string
}

func defaultDispatchCreateForm() dispatchCreateForm {
	return dispatchCreateForm{
		RequestedPolicy: protocol.PolicyReadonly,
		DeadlineMinutes: strconv.Itoa(defaultDispatchDeadlineMinutes),
		MaxRetries:      "0",
	}
}

func (s *Server) handleDispatchCreatePage(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	s.render(w, "dispatch_new", dispatchCreatePage{
		basePage: s.base(r, user, "dispatch", "page.dispatch_new"),
		Nodes:    s.dispatchNodeOptions(""),
		Form:     defaultDispatchCreateForm(),
	})
}

func (s *Server) handleDispatchCreate(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	lang := requestLang(r)
	if err := r.ParseForm(); err != nil {
		s.renderDispatchCreateMessage(w, r, user, formMessage{Error: tr(lang, "form.read_failed")}, defaultDispatchCreateForm())
		return
	}
	form := dispatchCreateForm{
		NodeID:          strings.TrimSpace(r.PostFormValue("node_id")),
		Goal:            r.PostFormValue("goal"),
		RequestedPolicy: strings.TrimSpace(r.PostFormValue("requested_policy")),
		DeadlineMinutes: strings.TrimSpace(r.PostFormValue("deadline_minutes")),
		MaxRetries:      strings.TrimSpace(r.PostFormValue("max_retries")),
		RequiredLabels:  r.PostFormValue("required_labels"),
		RequiredTools:   r.PostFormValue("required_tools"),
		RequiredSkills:  r.PostFormValue("required_skills"),
	}

	if form.NodeID == "" {
		s.renderDispatchCreateMessage(w, r, user, formMessage{Error: tr(lang, "form.dispatch_node_required")}, form)
		return
	}
	if _, ok := s.store.Node(form.NodeID); !ok {
		s.renderDispatchCreateMessage(w, r, user, formMessage{Error: tr(lang, "form.dispatch_node_unknown")}, form)
		return
	}
	goal := strings.TrimSpace(form.Goal)
	if goal == "" {
		s.renderDispatchCreateMessage(w, r, user, formMessage{Error: tr(lang, "form.dispatch_goal_required")}, form)
		return
	}
	policy := form.RequestedPolicy
	if policy == "" {
		policy = protocol.PolicyReadonly
	}
	if !protocol.ValidPolicy(policy) {
		s.renderDispatchCreateMessage(w, r, user, formMessage{Error: tr(lang, "form.dispatch_policy_invalid")}, form)
		return
	}
	deadlineMinutes, err := parseIntForm(form.DeadlineMinutes)
	if err != nil || deadlineMinutes < 1 || deadlineMinutes > maxDispatchDeadlineMinutes {
		s.renderDispatchCreateMessage(w, r, user, formMessage{Error: tr(lang, "form.dispatch_deadline_invalid")}, form)
		return
	}
	maxRetries, err := parseIntForm(form.MaxRetries)
	if err != nil || maxRetries < 0 || maxRetries > maxDispatchRetries {
		s.renderDispatchCreateMessage(w, r, user, formMessage{Error: tr(lang, "form.dispatch_retries_invalid")}, form)
		return
	}

	jobID, idemKey, err := newDispatchIdentifiers()
	if err != nil {
		s.renderDispatchCreateMessage(w, r, user, formMessage{Error: tr(lang, "form.dispatch_create_failed")}, form)
		return
	}
	now := s.now()
	req := store.DispatchRequest{
		JobID:           jobID,
		IdemKey:         idemKey,
		NodeID:          form.NodeID,
		Goal:            goal,
		RequestedPolicy: policy, // the store still clamps this down to the node's own reported ceiling
		DeadlineTS:      now.Add(time.Duration(deadlineMinutes) * time.Minute).UnixMilli(),
		MaxRetries:      maxRetries,
		Requestor:       user,
		RequiredLabels:  splitDispatchList(form.RequiredLabels),
		RequiredTools:   splitDispatchList(form.RequiredTools),
		RequiredSkills:  splitDispatchList(form.RequiredSkills),
	}
	job, _, err := s.store.EnqueueJob(now.UnixMilli(), req)
	if err != nil {
		msg := tr(lang, "form.dispatch_create_failed")
		switch {
		case errors.Is(err, store.ErrUnknownNode):
			msg = tr(lang, "form.dispatch_node_unknown")
		case errors.Is(err, store.ErrDispatchQueueFull):
			msg = tr(lang, "form.dispatch_queue_full")
		}
		s.renderDispatchCreateMessage(w, r, user, formMessage{Error: msg}, form)
		return
	}
	s.log.Info("dispatch job created",
		"operator", user,
		"node_id", job.NodeID,
		"job_id", job.JobID,
		"requested_policy", job.RequestedPolicy,
	)
	s.render(w, "dispatch", s.dispatchPage(r, user, formMessage{OK: trf(lang, "form.dispatch_created", job.JobID)}))
}

func (s *Server) renderDispatchCreateMessage(w http.ResponseWriter, r *http.Request, username string, msg formMessage, form dispatchCreateForm) {
	s.render(w, "dispatch_new", dispatchCreatePage{
		basePage: s.base(r, username, "dispatch", "page.dispatch_new"),
		Nodes:    s.dispatchNodeOptions(form.NodeID),
		Form:     form,
		Create:   msg,
	})
}

func (s *Server) dispatchNodeOptions(selected string) []dispatchNodeOption {
	nodes := s.store.Nodes()
	opts := make([]dispatchNodeOption, 0, len(nodes))
	for _, n := range nodes {
		opts = append(opts, dispatchNodeOption{
			NodeID:   n.NodeID,
			Online:   s.online(n.LastSeenMS),
			Selected: n.NodeID == selected,
		})
	}
	return opts
}

// newDispatchIdentifiers generates a fresh job_id/idem_key pair server-side.
// These are unique identifiers, not secrets, so crypto/rand is used only for
// collision resistance.
func newDispatchIdentifiers() (jobID, idemKey string, err error) {
	jobID, err = randomDispatchID("job")
	if err != nil {
		return "", "", err
	}
	idemKey, err = randomDispatchID("idem")
	if err != nil {
		return "", "", err
	}
	return jobID, idemKey, nil
}

func randomDispatchID(prefix string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + "-" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// splitDispatchList parses a comma/newline separated textarea/input value into
// a trimmed, non-empty string list.
func splitDispatchList(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseIntForm(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty value")
	}
	return strconv.Atoi(s)
}
