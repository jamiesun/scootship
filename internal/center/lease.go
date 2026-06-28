package center

import "net/http"

// handleLease is the E2 job-dispatch endpoint. Phase 1 is observation-only: the
// center authenticates the lease and validates the node, but never dispatches a
// job. EDGE.md E2 (schema'd, policy-clamped dispatch) is a later phase gated
// behind explicit opt-in; when built, this returns 0..N job envelopes as NDJSON.
//
// Keeping the endpoint present (and empty) lets an edge poll harmlessly and
// makes the observation/dispatch boundary explicit in the code.
func (s *Server) handleLease(w http.ResponseWriter, r *http.Request) {
	node := nodeFromCtx(r)
	if qn := r.URL.Query().Get("node"); qn != "" && qn != node {
		writeJSONError(w, http.StatusForbidden, "node_mismatch", "token does not match node query param")
		return
	}
	// No work to advertise in Phase 1. An empty NDJSON body means "no jobs".
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Scootship-Dispatch", "disabled-phase1")
	w.WriteHeader(http.StatusOK)
}
