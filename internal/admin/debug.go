package admin

import (
	"context"
	"net/http"
	"time"

	"opencode2api/internal/httpx"
)

// modelRefreshBudget bounds an operator-triggered catalog refresh so a slow
// upstream cannot hold the request open indefinitely.
const modelRefreshBudget = 45 * time.Second

// handleDebugModels reports the model catalog, its per-model routing diagnosis,
// and the models.dev pricing state. It is the only diagnostic surface the
// console keeps: the request/attempt timelines and the per-key Playground were
// removed because new-api already owns request-level observability.
func (a *Server) handleDebugModels(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, a.debugModelsPayload())
}

// handleRefreshModels pulls the upstream catalogs on demand and answers with
// the same snapshot the routing table reads, so one click both refreshes and
// re-renders.
func (a *Server) handleRefreshModels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), modelRefreshBudget)
	defer cancel()
	a.manager.RefreshModels(ctx)
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, a.debugModelsPayload())
}

func (a *Server) debugModelsPayload() map[string]any {
	models, metadata := a.manager.DebugModels()
	return map[string]any{
		"models": models, "metadata": metadata, "catalog": a.manager.Resources().Models,
	}
}
