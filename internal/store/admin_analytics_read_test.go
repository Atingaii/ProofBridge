package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

var errAdminRows = errors.New("administrator fixture interrupted during iteration")

// A driver-level failure after one valid row models an interrupted long scan.
// QueryContext itself succeeds, and Close succeeds, so only Rows.Err exposes it.
type adminRowsConnector struct{ failStage string }

func (c adminRowsConnector) Connect(context.Context) (driver.Conn, error) {
	return adminRowsConn{failStage: c.failStage}, nil
}
func (c adminRowsConnector) Driver() driver.Driver { return adminRowsDriver{} }

type adminRowsDriver struct{}

func (adminRowsDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type adminRowsConn struct{ failStage string }

func (adminRowsConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not supported") }
func (adminRowsConn) Close() error                        { return nil }
func (adminRowsConn) Begin() (driver.Tx, error)           { return nil, errors.New("not supported") }
func (c adminRowsConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	stage := "legacy"
	values := []driver.Value{"user-or-run", int64(100), "codex", `{"inputTokens":10}`}
	if strings.Contains(query, "FROM runs r") {
		stage = "chat"
		values = []driver.Value{"user-or-run", int64(100), `{"input_tokens":10}`}
	} else if strings.Contains(query, "FROM orchestration_usage_events ue JOIN") {
		stage = "ledger"
		values = []driver.Value{"user-or-run", int64(100), "codex", "unknown", int64(10), int64(2), int64(3), int64(0)}
		if strings.Contains(query, "COUNT(*)") {
			values = append(values, int64(1))
		}
	}
	return &adminFailingRows{values: values, fail: stage == c.failStage}, nil
}

type adminFailingRows struct {
	values []driver.Value
	fail   bool
	read   bool
}

func (r *adminFailingRows) Columns() []string {
	columns := make([]string, len(r.values))
	for i := range columns {
		columns[i] = fmt.Sprint(i)
	}
	return columns
}
func (*adminFailingRows) Close() error { return nil }
func (r *adminFailingRows) Next(dest []driver.Value) error {
	if !r.fail {
		return io.EOF
	}
	if r.read {
		return errAdminRows
	}
	r.read = true
	copy(dest, r.values)
	return nil
}

func TestAdminUsagePropagatesIterationFailures(t *testing.T) {
	for _, stage := range []string{"chat", "ledger", "legacy"} {
		t.Run(stage, func(t *testing.T) {
			db := sql.OpenDB(adminRowsConnector{failStage: stage})
			db.SetMaxOpenConns(1)
			defer db.Close()
			st := &Store{db: db}
			if rows, err := st.adminUsageEvents(context.Background(), 0, 0); !errors.Is(err, errAdminRows) || rows != nil {
				t.Fatalf("overview returned partial rows=%v error=%v", rows, err)
			}
			if rows, err := st.adminUserConversationUsage(context.Background(), "user", 0); !errors.Is(err, errAdminRows) || rows != nil {
				t.Fatalf("detail returned partial rows=%v error=%v", rows, err)
			}
		})
	}
}

func TestAdminLegacyUsageQueriesUseSparseIndex(t *testing.T) {
	st := openTestStore(t)
	for _, userScoped := range []bool{false, true} {
		query := `EXPLAIN QUERY PLAN
			SELECT o.user_id, e.created_at, COALESCE(e.cli,''), e.data_json
			FROM orchestration_events e JOIN orchestration_runs o ON o.id = e.run_id
			WHERE e.kind = 'turn.usage' AND e.data_json IS NOT NULL AND e.data_json != ''
				AND NOT EXISTS (SELECT 1 FROM orchestration_usage_events ue WHERE ue.run_id = e.run_id)
				AND (? = 0 OR e.created_at >= ?)`
		args := []any{0, 0}
		if userScoped {
			query += ` AND o.user_id = ?`
			args = append(args, "user")
		}
		rows, err := st.db.Query(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		used := false
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			details = append(details, detail)
			used = used || strings.Contains(detail, "idx_orchestration_events_legacy_usage")
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if !used {
			t.Fatalf("user-scoped=%v: legacy usage scans full event history: %v", userScoped, details)
		}
	}
}
