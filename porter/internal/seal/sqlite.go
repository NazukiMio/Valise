package seal

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"

	_ "modernc.org/sqlite" // pure Go, works with CGO_ENABLED=0
)

func sqliteURI(path string, query string) string {
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query}).String()
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// snapshotDB writes a consistent copy of src to dst with VACUUM INTO, opening
// src read-only. When strip is non-empty those tables are emptied in the copy
// (never in src) and the copy is vacuumed again so deleted rows do not
// survive in free pages. It returns the tables actually stripped.
func snapshotDB(src, dst string, strip []string) ([]string, error) {
	db, err := sql.Open("sqlite", sqliteURI(src, "mode=ro&_pragma=busy_timeout(10000)"))
	if err != nil {
		return nil, err
	}
	// VACUUM INTO writes a rollback-journal database; keep WAL databases in
	// WAL mode so the restored file matches what the harness expects.
	var mode string
	db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	_, err = db.Exec("VACUUM INTO '" + strings.ReplaceAll(dst, "'", "''") + "'")
	db.Close()
	if err != nil {
		return nil, fmt.Errorf("VACUUM INTO: %w", err)
	}
	cp, err := sql.Open("sqlite", sqliteURI(dst, "_pragma=busy_timeout(10000)"))
	if err != nil {
		return nil, err
	}
	defer cp.Close()
	var stripped []string
	if len(strip) > 0 {
		for _, t := range strip {
			var n int
			if err := cp.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", t).Scan(&n); err != nil {
				return nil, err
			}
			if n == 0 {
				continue
			}
			if _, err := cp.Exec("DELETE FROM " + quoteIdent(t)); err != nil {
				return nil, fmt.Errorf("strip %s: %w", t, err)
			}
			stripped = append(stripped, t)
		}
		if len(stripped) > 0 {
			if _, err := cp.Exec("VACUUM"); err != nil {
				return nil, fmt.Errorf("VACUUM: %w", err)
			}
		}
	}
	var res string
	if err := cp.QueryRow("PRAGMA integrity_check").Scan(&res); err != nil {
		return nil, err
	}
	if res != "ok" {
		return nil, fmt.Errorf("integrity_check: %s", res)
	}
	if strings.EqualFold(mode, "wal") {
		if _, err := cp.Exec("PRAGMA journal_mode=WAL"); err != nil {
			return nil, fmt.Errorf("journal_mode: %w", err)
		}
	}
	return stripped, nil
}

// freeBytes reports the free space available to the user at dir.
func freeBytes(dir string) (uint64, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	return statfsFree(dir)
}
