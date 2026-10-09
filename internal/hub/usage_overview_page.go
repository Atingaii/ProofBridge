package hub

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type usageOverviewPage struct {
	HasMore     bool   `json:"hasMore"`
	NextCursor  string `json:"nextCursor,omitempty"`
	ScannedRuns int    `json:"scannedRuns"`
}

type usageOverviewCursor struct {
	Version        int    `json:"v"`
	History        string `json:"history"`
	Days           int    `json:"days"`
	TimezoneOffset int    `json:"timezoneOffset"`
	Cutoff         int64  `json:"cutoff"`
}

type usageOverviewPageRequest struct {
	Limit int
	usageOverviewCursor
}

func parseUsageOverviewPage(r *http.Request, days, timezoneOffset int, cutoff int64) (*usageOverviewPageRequest, error) {
	q := r.URL.Query()
	cursor := strings.TrimSpace(q.Get("cursor"))
	if !q.Has("limit") {
		if cursor != "" {
			return nil, errors.New("cursor requires limit")
		}
		return nil, nil // Preserve full-response semantics for existing clients.
	}
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit < 1 {
		return nil, errors.New("limit must be a positive integer")
	}
	request := &usageOverviewPageRequest{
		Limit:               min(limit, 3),
		usageOverviewCursor: usageOverviewCursor{Version: 1, Days: days, TimezoneOffset: timezoneOffset, Cutoff: cutoff},
	}
	if cursor == "" {
		return request, nil
	}
	if len(cursor) > 4096 {
		return nil, errors.New("cursor too long")
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	var saved usageOverviewCursor
	if err != nil || json.Unmarshal(raw, &saved) != nil || saved.Version != 1 || saved.History == "" ||
		saved.Days != days || saved.TimezoneOffset != timezoneOffset || saved.Cutoff < 0 || (saved.Cutoff == 0) != (days == 0) {
		return nil, errors.New("invalid cursor for this usage range")
	}
	// The nested Store history cursor binds the actual query to the owner and
	// archived=all filter. Retain the first page's cutoff across local midnight.
	request.usageOverviewCursor = saved
	return request, nil
}

func (r usageOverviewPageRequest) response(historyCursor string, scannedRuns int) *usageOverviewPage {
	page := &usageOverviewPage{HasMore: historyCursor != "", ScannedRuns: scannedRuns}
	if historyCursor != "" {
		r.History = historyCursor
		raw, _ := json.Marshal(r.usageOverviewCursor)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page
}
