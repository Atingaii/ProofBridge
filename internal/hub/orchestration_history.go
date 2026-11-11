package hub

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/tencent/codex-bridge/internal/serverutil"
	"github.com/tencent/codex-bridge/internal/store"
)

func (s *Server) handleListOrchestrations(w http.ResponseWriter, r *http.Request, uid string) {
	query := r.URL.Query()
	filter := store.OrchestrationHistoryFilter{AgentID: query.Get("agentId"), Query: query.Get("q"), Status: query.Get("status"), Archived: query.Get("archived"), Cursor: query.Get("cursor")}
	if query.Has("limit") {
		limit, err := strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 1 || limit > 200 {
			serverutil.WriteError(w, http.StatusBadRequest, "BAD_FILTER", "limit must be between 1 and 200")
			return
		}
		filter.Limit = limit
	}
	page, err := s.store.ListOrchestrationHistory(r.Context(), uid, filter)
	if err != nil {
		if errors.Is(err, store.ErrInvalidOrchestrationHistory) {
			serverutil.WriteError(w, http.StatusBadRequest, "BAD_FILTER", err.Error())
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to list orchestration runs")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, page)
}

func (s *Server) handleUpdateOrchestration(w http.ResponseWriter, r *http.Request, uid string) {
	var req struct {
		Title    *string `json:"title"`
		Archived *bool   `json:"archived"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid orchestration metadata")
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "expected a single metadata object")
		return
	}
	run, err := s.store.UpdateOrchestrationMetadata(r.Context(), r.PathValue("runID"), uid, req.Title, req.Archived)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
		case errors.Is(err, store.ErrConflict):
			serverutil.WriteError(w, http.StatusConflict, "RUN_ACTIVE", "only a terminal orchestration run can be archived")
		case errors.Is(err, store.ErrInvalidOrchestrationHistory):
			serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		default:
			serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to update orchestration metadata")
		}
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"run": run})
}
