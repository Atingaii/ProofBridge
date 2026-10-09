package hub

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tencent/codex-bridge/internal/serverutil"
)

// Leave time for the browser's 25-second deadline to receive a useful error.
const orchestrationStatsTimeout = 20 * time.Second

func writeUsageReadError(w http.ResponseWriter, r *http.Request, message string) {
	if errors.Is(r.Context().Err(), context.DeadlineExceeded) {
		serverutil.WriteError(w, http.StatusGatewayTimeout, "USAGE_TIMEOUT", "statistics request timed out; please retry")
		return
	}
	if errors.Is(r.Context().Err(), context.Canceled) {
		serverutil.WriteError(w, http.StatusRequestTimeout, "USAGE_CANCELED", "statistics request canceled")
		return
	}
	serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", message)
}
