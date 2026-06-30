package center

import (
	"encoding/json"
	"net/http"
	"strconv"
)

const maxLeaseCapacity = 64

// handleLease is the E2 job-dispatch endpoint. The endpoint remains node-bound:
// a token can only lease jobs for its own node, and only jobs already persisted
// in the dispatch queue are returned. An empty NDJSON body means "no jobs".
func (s *Server) handleLease(w http.ResponseWriter, r *http.Request) {
	node := nodeFromCtx(r)
	q := r.URL.Query()
	qn := q.Get("node")
	if qn == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_node", "node query param is required")
		return
	}
	if qn != node {
		writeJSONError(w, http.StatusForbidden, "node_mismatch", "token does not match node query param")
		return
	}
	capacity, err := strconv.Atoi(q.Get("capacity"))
	if err != nil || capacity < 1 || capacity > maxLeaseCapacity {
		writeJSONError(w, http.StatusBadRequest, "bad_capacity", "capacity must be a positive integer no greater than 64")
		return
	}
	jobs, err := s.store.LeaseJobs(node, capacity, s.now().UnixMilli())
	if err != nil {
		s.log.Error("lease jobs", "node", node, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "store_error", "failed to lease jobs")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Scootship-Dispatch", "enabled-phase2")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	for _, job := range jobs {
		if err := enc.Encode(job); err != nil {
			s.log.Error("write lease response", "node", node, "err", err)
			return
		}
	}
}
