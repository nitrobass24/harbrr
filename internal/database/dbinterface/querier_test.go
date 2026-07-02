package dbinterface

import "testing"

func TestRebind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		dialect Dialect
		in      string
		want    string
	}{
		{"sqlite passthrough", DialectSQLite, "SELECT 1 FROM t WHERE a = ? AND b = ?", "SELECT 1 FROM t WHERE a = ? AND b = ?"},
		{"unknown dialect passthrough", Dialect("mysql"), "WHERE a = ?", "WHERE a = ?"},
		{"empty dialect passthrough", Dialect(""), "WHERE a = ?", "WHERE a = ?"},
		{"postgres single", DialectPostgres, "WHERE a = ?", "WHERE a = $1"},
		{"postgres multiple", DialectPostgres, "VALUES (?, ?, ?)", "VALUES ($1, $2, $3)"},
		{"postgres none", DialectPostgres, "SELECT count(*) FROM t", "SELECT count(*) FROM t"},
		{
			"postgres upsert (matches appmeta/sessionstore)",
			DialectPostgres,
			"INSERT INTO app_meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
			"INSERT INTO app_meta (key, value) VALUES ($1, $2) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		},
		{"postgres skips ? in single quotes", DialectPostgres, "SELECT 'a?b' WHERE x = ?", "SELECT 'a?b' WHERE x = $1"},
		{"postgres skips ? in double quotes", DialectPostgres, `SELECT "c?l" WHERE x = ?`, `SELECT "c?l" WHERE x = $1`},
		{"postgres numbers only unquoted", DialectPostgres, "? '?' ? \"?\" ?", "$1 '?' $2 \"?\" $3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Rebind(tt.dialect, tt.in); got != tt.want {
				t.Errorf("Rebind(%q, %q) = %q, want %q", tt.dialect, tt.in, got, tt.want)
			}
		})
	}
}
