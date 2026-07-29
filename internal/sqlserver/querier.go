package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/microsoft/go-mssqldb" // pure-Go driver; SSPI integrated auth on Windows
)

// Row is one result row keyed by lower-cased column name.
type Row map[string]any

// Querier executes a named read-only query. The name keys fixtures in tests
// and error reporting in collection health.
type Querier interface {
	Query(ctx context.Context, name, sqlText string) ([]Row, error)
	Close() error
}

// ConnectOptions describes how to reach one instance.
type ConnectOptions struct {
	Instance       Instance
	Auth           string // integrated | sqllogin
	Username       string
	PasswordFile   string // 0600 file, contents read at connect time, never logged
	ConnectTimeout time.Duration
}

// dbQuerier is the production Querier over database/sql + go-mssqldb.
type dbQuerier struct{ db *sql.DB }

// Connect opens a read-only session to a local instance. The connection is
// local-only (localhost); the agent never reaches other machines.
func Connect(o ConnectOptions) (Querier, error) {
	q := url.Values{}
	q.Set("app name", "ura-agent")
	q.Set("database", "master")
	q.Set("dial timeout", fmt.Sprintf("%d", int(o.ConnectTimeout.Seconds())))
	q.Set("encrypt", "optional") // local loopback; older instances lack TLS certs

	u := &url.URL{Scheme: "sqlserver", Host: "localhost", RawQuery: q.Encode()}
	if o.Instance.Name != "" && o.Instance.Name != "MSSQLSERVER" {
		u.Path = o.Instance.Name // named instance via SQL Browser
	} else if o.Instance.Port > 0 {
		u.Host = fmt.Sprintf("localhost:%d", o.Instance.Port)
	}
	if o.Auth == "sqllogin" {
		pw, err := readSecretFile(o.PasswordFile)
		if err != nil {
			return nil, fmt.Errorf("sql password file: %w", err)
		}
		u.User = url.UserPassword(o.Username, pw)
	}
	// With no credentials in the URL, go-mssqldb uses integrated auth (SSPI)
	// on Windows — the service account's identity, nothing stored.
	db, err := sql.Open("sqlserver", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one lightweight session per instance
	db.SetConnMaxIdleTime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), o.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &dbQuerier{db: db}, nil
}

func readSecretFile(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("sql.password_file not configured")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (d *dbQuerier) Query(ctx context.Context, name, sqlText string) ([]Row, error) {
	rows, err := d.db.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []Row
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := Row{}
		for i, c := range cols {
			r[strings.ToLower(c)] = vals[i]
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *dbQuerier) Close() error { return d.db.Close() }

// ---------------------------------------------------------------------------
// tolerant value extraction (DMVs return mixed int/decimal/[]byte types)
// ---------------------------------------------------------------------------

func (r Row) str(col string) string {
	switch v := r[strings.ToLower(col)].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

func (r Row) i64(col string) int64 {
	switch v := r[strings.ToLower(col)].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case float64:
		return int64(v)
	case []byte:
		var n int64
		fmt.Sscanf(string(v), "%d", &n)
		return n
	case string:
		var n int64
		fmt.Sscanf(v, "%d", &n)
		return n
	default:
		return 0
	}
}

func (r Row) u64(col string) uint64 {
	v := r.i64(col)
	if v < 0 {
		return 0
	}
	return uint64(v)
}

func (r Row) f64(col string) float64 {
	switch v := r[strings.ToLower(col)].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int64:
		return float64(v)
	case []byte:
		var f float64
		fmt.Sscanf(string(v), "%g", &f)
		return f
	case string:
		var f float64
		fmt.Sscanf(v, "%g", &f)
		return f
	default:
		return 0
	}
}

func (r Row) boolean(col string) bool {
	switch v := r[strings.ToLower(col)].(type) {
	case bool:
		return v
	case int64:
		return v != 0
	case []byte:
		return len(v) > 0 && (v[0] == '1' || v[0] == 't')
	case string:
		return v == "1" || strings.EqualFold(v, "true")
	default:
		return false
	}
}

func (r Row) time(col string) time.Time {
	switch v := r[strings.ToLower(col)].(type) {
	case time.Time:
		return v.UTC()
	case string:
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05"} {
			if t, err := time.Parse(layout, v); err == nil {
				return t.UTC()
			}
		}
	}
	return time.Time{}
}
