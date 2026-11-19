// Package migrate is a small forward-only SQL migration runner for
// PostgreSQL and MySQL.
//
// Migrations are files in an fs.FS (usually an embed.FS), named
//
//	0001_create_notes.sql            every dialect
//	0002_add_index.postgres.sql      PostgreSQL only
//	0002_add_index.mysql.sql         MySQL only
//
// A file is cut into statements at semicolons (Split). A statement whose body
// contains bare semicolons cannot be cut that way: a MySQL stored procedure,
// function, trigger or event with BEGIN ... END, and a PostgreSQL function
// with BEGIN ATOMIC ... END. Put such an object alone in a file whose first
// line is
//
//	-- migrate:no-split
//
// and the whole file is sent as one statement. Load refuses those bodies in
// a file without the directive, and DELIMITER (a command of the mysql
// client, not SQL) anywhere. PostgreSQL bodies in dollar quotes ($$ ... $$)
// need no directive.
//
// The leading number is the version; versions are applied in ascending order
// and must be unique per dialect. There are no down migrations: a mistake is
// corrected by a new migration.
//
// Up records each applied migration with a SHA-256 of its content in a
// bookkeeping table (default "schema_migrations"). A recorded migration whose
// file changed, or disappeared, fails the run: history is immutable.
//
// Up is safe to call from every replica at startup. It takes a session lock
// on a dedicated connection (pg_advisory_lock on PostgreSQL, GET_LOCK on
// MySQL), so replicas run one after the other and the later ones find nothing
// to do.
//
// On PostgreSQL each migration and its bookkeeping row are one transaction: a
// failing migration leaves no trace. MySQL commits implicitly on every DDL
// statement, so a failing MySQL migration can be applied in part; write MySQL
// migrations as one statement each, or idempotently (IF NOT EXISTS).
package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Dialects.
const (
	Postgres = "postgres"
	MySQL    = "mysql"
)

// DefaultTable is the bookkeeping table.
const DefaultTable = "schema_migrations"

type config struct {
	table       string
	dir         string
	lockTimeout time.Duration
	logf        func(format string, args ...any)
}

// Option configures Up.
type Option func(*config)

// WithTable sets the bookkeeping table name (default "schema_migrations").
// Give each independently migrated component of one database its own table.
func WithTable(name string) Option { return func(c *config) { c.table = name } }

// WithDir sets the directory inside the file system that holds the files
// (default "."; with `//go:embed migrations/*.sql` pass "migrations").
func WithDir(dir string) Option { return func(c *config) { c.dir = dir } }

// WithLockTimeout bounds the wait for another replica that is migrating
// (default 2 minutes).
func WithLockTimeout(d time.Duration) Option { return func(c *config) { c.lockTimeout = d } }

// WithLogf reports each applied migration.
func WithLogf(logf func(format string, args ...any)) Option { return func(c *config) { c.logf = logf } }

// Migration is one migration file.
type Migration struct {
	Version  int64
	Name     string
	File     string
	SQL      string
	Checksum string
	// NoSplit: the file starts with the directive `-- migrate:no-split` and
	// is sent to the engine as one statement.
	NoSplit bool
}

// Bodies with bare semicolons cannot be split at semicolons.
var (
	noSplitRE     = regexp.MustCompile(`\A\s*--\s*migrate:no-split\b`)
	beginAtomicRE = regexp.MustCompile(`(?i)\bBEGIN\s+ATOMIC\b`)
	delimiterRE   = regexp.MustCompile(`(?im)^\s*DELIMITER\b`)
	compoundRE    = regexp.MustCompile(`(?is)\bCREATE\b[^;]*\b(PROCEDURE|FUNCTION|TRIGGER|EVENT)\b[^;]*\bBEGIN\b`)
)

// checkSplittable refuses a file that Split would cut inside a statement, so
// that the mistake is reported when the migrations are loaded and not as a
// syntax error from the engine half-way through.
func checkSplittable(m Migration, dialect string) error {
	const hint = "put the object alone in a file that starts with the line `-- migrate:no-split`"
	switch {
	case dialect == MySQL && delimiterRE.MatchString(m.SQL):
		return fmt.Errorf("migrate: %s: DELIMITER is a command of the mysql client, not SQL; %s and drop DELIMITER", m.File, hint)
	case m.NoSplit:
		return nil
	case dialect == Postgres && beginAtomicRE.MatchString(m.SQL):
		return fmt.Errorf("migrate: %s: a BEGIN ATOMIC body contains semicolons; %s, or use a dollar-quoted body", m.File, hint)
	case dialect == MySQL && compoundRE.MatchString(m.SQL):
		return fmt.Errorf("migrate: %s: a BEGIN ... END body contains semicolons; %s", m.File, hint)
	}
	return nil
}

var (
	fileRE  = regexp.MustCompile(`^(\d+)_([A-Za-z0-9_\-]+?)(?:\.(postgres|mysql))?\.sql$`)
	tableRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Load reads the migrations of fsys/dir that apply to dialect, sorted by
// version. A dialect-specific file wins over a generic one with the same
// version.
func Load(fsys fs.FS, dir, dialect string) ([]Migration, error) {
	if dir == "" {
		dir = "."
	}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("migrate: read %s: %w", dir, err)
	}
	byVersion := make(map[int64]Migration)
	specific := make(map[int64]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		m := fileRE.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migrate: %s: want NNNN_name.sql or NNNN_name.<postgres|mysql>.sql", e.Name())
		}
		if m[3] != "" && m[3] != dialect {
			continue
		}
		version, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("migrate: %s: version must be a positive number", e.Name())
		}
		if prev, dup := byVersion[version]; dup {
			if specific[version] == (m[3] != "") {
				return nil, fmt.Errorf("migrate: version %d is used by %s and %s", version, prev.File, e.Name())
			}
			if specific[version] {
				continue // the dialect-specific file already won
			}
		}
		body, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("migrate: %w", err)
		}
		sum := sha256.Sum256(body)
		mig := Migration{
			Version: version, Name: m[2], File: e.Name(), SQL: string(body), Checksum: hex.EncodeToString(sum[:]),
			NoSplit: noSplitRE.Match(body),
		}
		if err := checkSplittable(mig, dialect); err != nil {
			return nil, err
		}
		byVersion[version] = mig
		specific[version] = m[3] != ""
	}
	out := make([]Migration, 0, len(byVersion))
	for _, m := range byVersion {
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b Migration) int { return int(a.Version - b.Version) })
	return out, nil
}

// Up applies the migrations of fsys that are not recorded yet, in order.
// dialect is Postgres or MySQL.
func Up(ctx context.Context, db *sql.DB, dialect string, fsys fs.FS, opts ...Option) error {
	cfg := config{table: DefaultTable, dir: ".", lockTimeout: 2 * time.Minute, logf: func(string, ...any) {}}
	for _, o := range opts {
		o(&cfg)
	}
	if dialect != Postgres && dialect != MySQL {
		return fmt.Errorf("migrate: unsupported dialect %q", dialect)
	}
	if !tableRE.MatchString(cfg.table) {
		return fmt.Errorf("migrate: invalid table name %q", cfg.table)
	}
	migrations, err := Load(fsys, cfg.dir, dialect)
	if err != nil {
		return err
	}
	// Session locks belong to a connection: lock, work and unlock must all
	// use the same one, so it is taken out of the pool for the whole run.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	defer func() { _ = conn.Close() }()
	r := &runner{conn: conn, dialect: dialect, cfg: cfg}
	if err := r.lock(ctx); err != nil {
		return err
	}
	// Unlock even when ctx was cancelled meanwhile; closing the connection
	// would release the lock too, but returning it to the pool would not.
	defer r.unlock(context.WithoutCancel(ctx))
	return r.up(ctx, migrations)
}

type runner struct {
	conn    *sql.Conn
	dialect string
	cfg     config
}

// lockKey identifies the lock: one per bookkeeping table (the current
// database is part of the MySQL lock name; PostgreSQL advisory locks are
// already per database).
func (r *runner) lockKey() int64 {
	return int64(crc32.ChecksumIEEE([]byte("common-go/migrate:" + r.cfg.table)))
}

func (r *runner) lock(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.lockTimeout)
	defer cancel()
	if r.dialect == Postgres {
		if _, err := r.conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", r.lockKey()); err != nil {
			return fmt.Errorf("migrate: lock: %w", err)
		}
		return nil
	}
	var got sql.NullInt64
	err := r.conn.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT(DATABASE(), ?), ?)",
		":migrate:"+r.cfg.table, int(r.cfg.lockTimeout.Seconds())).Scan(&got)
	if err != nil {
		return fmt.Errorf("migrate: lock: %w", err)
	}
	if !got.Valid || got.Int64 != 1 {
		return errors.New("migrate: lock: timed out waiting for another migration run")
	}
	return nil
}

func (r *runner) unlock(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if r.dialect == Postgres {
		_, _ = r.conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", r.lockKey())
		return
	}
	_, _ = r.conn.ExecContext(ctx, "SELECT RELEASE_LOCK(CONCAT(DATABASE(), ?))", ":migrate:"+r.cfg.table)
}

func (r *runner) up(ctx context.Context, migrations []Migration) error {
	create := "CREATE TABLE IF NOT EXISTS " + r.cfg.table + " (" +
		"version BIGINT NOT NULL PRIMARY KEY, name VARCHAR(255) NOT NULL, " +
		"checksum CHAR(64) NOT NULL, applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)"
	if _, err := r.conn.ExecContext(ctx, create); err != nil {
		return fmt.Errorf("migrate: create %s: %w", r.cfg.table, err)
	}
	applied, err := r.applied(ctx)
	if err != nil {
		return err
	}
	byVersion := make(map[int64]Migration, len(migrations))
	for _, m := range migrations {
		byVersion[m.Version] = m
	}
	var last int64
	for version, checksum := range applied {
		m, ok := byVersion[version]
		switch {
		case !ok:
			return fmt.Errorf("migrate: version %d is recorded in %s but its file is missing", version, r.cfg.table)
		case strings.TrimSpace(checksum) != m.Checksum:
			return fmt.Errorf("migrate: %s was changed after it was applied; add a new migration instead", m.File)
		}
		last = max(last, version)
	}
	for _, m := range migrations {
		if _, done := applied[m.Version]; done {
			continue
		}
		if m.Version < last {
			return fmt.Errorf("migrate: %s is older than the applied version %d; migrations are applied in order",
				m.File, last)
		}
		if err := r.apply(ctx, m); err != nil {
			return fmt.Errorf("migrate: %s: %w", m.File, err)
		}
		r.cfg.logf("migrate: applied %s", m.File)
	}
	return nil
}

func (r *runner) applied(ctx context.Context) (map[int64]string, error) {
	rows, err := r.conn.QueryContext(ctx, "SELECT version, checksum FROM "+r.cfg.table)
	if err != nil {
		return nil, fmt.Errorf("migrate: read %s: %w", r.cfg.table, err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64]string)
	for rows.Next() {
		var (
			version  int64
			checksum string
		)
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", r.cfg.table, err)
		}
		out[version] = checksum
	}
	return out, rows.Err()
}

func (r *runner) apply(ctx context.Context, m Migration) error {
	record := "INSERT INTO " + r.cfg.table + " (version, name, checksum) VALUES (?, ?, ?)"
	if r.dialect == Postgres {
		record = "INSERT INTO " + r.cfg.table + " (version, name, checksum) VALUES ($1, $2, $3)"
	}
	tx, err := r.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// On MySQL the DDL statements commit implicitly; the transaction then
	// only covers what the engine lets it cover.
	statements := []string{m.SQL}
	if !m.NoSplit {
		statements = Split(m.SQL, r.dialect)
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, record, m.Version, m.Name, m.Checksum); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Split cuts a script into statements at semicolons, leaving alone the
// semicolons inside quoted strings and identifiers, comments and, on
// PostgreSQL, dollar-quoted bodies. Statements are sent one by one, so
// drivers need no multi-statement mode. The lexical rules differ by dialect:
//
//	PostgreSQL  '..' and ".." end at the quote (a doubled quote stays inside;
//	            a backslash is an ordinary character, except in E'..'),
//	            $$..$$ and $tag$..$tag$, comments -- and /* */
//	MySQL       '..', ".." and `..` (a backslash escapes the next character in
//	            '..' and ".."), comments --, # and /* */
func Split(script, dialect string) []string {
	var (
		out   []string
		start int
		i     int
	)
	mysql := dialect == MySQL
	flush := func(end int) {
		if stmt := strings.TrimSpace(script[start:end]); stmt != "" && !onlyComments(stmt, mysql) {
			out = append(out, stmt)
		}
	}
	for i < len(script) {
		switch c := script[i]; {
		case c == ';':
			flush(i)
			i++
			start = i
		case c == '\'' || c == '"':
			escapes := mysql || (c == '\'' && escapePrefix(script, i))
			i = skipQuoted(script, i, escapes)
		case c == '`' && mysql:
			i = skipQuoted(script, i, false)
		case c == '-' && strings.HasPrefix(script[i:], "--"):
			i = skipTo(script, i, "\n")
		case c == '#' && mysql:
			i = skipTo(script, i, "\n")
		case c == '/' && strings.HasPrefix(script[i:], "/*"):
			i = skipTo(script, i+2, "*/")
		case c == '$' && !mysql:
			i = skipDollar(script, i)
		default:
			i++
		}
	}
	flush(len(script))
	return out
}

// escapePrefix reports whether the quote at i opens a PostgreSQL E'..'
// string: an E directly before it that is a token of its own, not the end
// of a word (... ESCAPE'\' is a plain string after a keyword).
func escapePrefix(s string, i int) bool {
	if i == 0 || (s[i-1] != 'E' && s[i-1] != 'e') {
		return false
	}
	return i == 1 || !identChar(s[i-2])
}

func identChar(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// skipQuoted returns the index after the quoted run starting at i. A doubled
// quote stays inside the run; so does a backslash-escaped character when the
// dialect has backslash escapes.
func skipQuoted(s string, i int, backslashEscapes bool) int {
	quote := s[i]
	for i++; i < len(s); i++ {
		switch {
		case s[i] == '\\' && backslashEscapes:
			i++
		case s[i] == quote:
			if i+1 < len(s) && s[i+1] == quote {
				i++
				continue
			}
			return i + 1
		}
	}
	return len(s)
}

func skipTo(s string, i int, end string) int {
	if j := strings.Index(s[i:], end); j >= 0 {
		return i + j + len(end)
	}
	return len(s)
}

// A dollar-quote tag is empty or an identifier: letters, digits (not first)
// and underscores.
var dollarTagRE = regexp.MustCompile(`^\$([A-Za-z_][A-Za-z0-9_]*)?\$`)

func skipDollar(s string, i int) int {
	tag := dollarTagRE.FindString(s[i:])
	if tag == "" {
		return i + 1 // a positional parameter or a plain dollar sign
	}
	return skipTo(s, i+len(tag), tag)
}

func onlyComments(stmt string, mysql bool) bool {
	for stmt != "" {
		switch {
		case strings.HasPrefix(stmt, "--") || (mysql && strings.HasPrefix(stmt, "#")):
			stmt = strings.TrimSpace(stmt[skipTo(stmt, 0, "\n"):])
		case strings.HasPrefix(stmt, "/*"):
			stmt = strings.TrimSpace(stmt[skipTo(stmt, 2, "*/"):])
		default:
			return false
		}
	}
	return true
}
