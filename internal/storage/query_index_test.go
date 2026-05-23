package storage

import (
	"database/sql"
	"strings"
	"testing"
)

// queryPlan returns the concatenated EXPLAIN QUERY PLAN detail rows for a
// statement. The driver requires every placeholder to be bound, so callers pass
// a dummy argument per "?"; the bound values do not affect the access path the
// optimizer reports.
func queryPlan(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v\nquery: %s", err, query)
	}
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		b.WriteString(detail)
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	return b.String()
}

// TestHotQueriesUseIndexes asserts that the timestamp-range queries on the
// large, frequently-scanned tables resolve through an index rather than a full
// table scan. Wrapping the indexed column in replace(col,'T',' ') defeats the
// index, so these assertions fail until the workaround is removed.
func TestHotQueriesUseIndexes(t *testing.T) {
	db, _ := openRaw(t)
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cases := []struct {
		name    string
		query   string
		args    []any
		mustNot string // a full-scan signature that must be absent
	}{
		{"segments by camera+date", sqlSegmentsForDateByCamera, []any{"cam", "a", "b"}, "SCAN segments"},
		{"events by camera+date", sqlEventsForDateByCamera, []any{"cam", "a", "b"}, "SCAN events"},
		{"segments ending before", sqlSegmentsEndingBefore, []any{"a"}, "SCAN segments"},
	}
	for _, tc := range cases {
		plan := queryPlan(t, db, tc.query, tc.args...)
		if !strings.Contains(plan, "USING INDEX") {
			t.Errorf("%s: expected an index search, got plan:\n%s", tc.name, plan)
		}
		if strings.Contains(plan, tc.mustNot) {
			t.Errorf("%s: query still does a full table scan (%q):\n%s", tc.name, tc.mustNot, plan)
		}
	}
}
