package sql

import (
	"regexp"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ti/common-go/dependencies/database"
)

// Identifiers (table names, condition keys, update keys, sort and select
// fields) become part of the statement text; values are always bound
// parameters. A caller may build a key from request data (a filter field, an
// update mask, a sort parameter), so every identifier is checked against a
// strict grammar before a statement is assembled, and anything else is
// InvalidArgument. Quoting alone would not do: MySQL and PostgreSQL escape
// quotes differently and the PostgreSQL path rewrites the statement.
var (
	// A column, or a table (optionally schema-qualified).
	identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	tableRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)
	// A condition key: a column, optionally with an array marker and a JSON
	// path: name, name.sub.path, name[*].sub, name[*]., name[2].sub.
	condKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\[(\*|[0-9]+)\])?(\.[A-Za-z0-9_.]*)?$`)
)

func invalidIdent(what, name string) error {
	return status.Errorf(codes.InvalidArgument, "invalid %s name %q", what, name)
}

func checkTable(table string) error {
	if !tableRE.MatchString(table) {
		return invalidIdent("table", table)
	}
	return nil
}

func checkConds(conds database.C) error {
	for _, c := range conds {
		if !condKeyRE.MatchString(c.Key) {
			return invalidIdent("condition field", c.Key)
		}
	}
	return nil
}

// checkSet checks update keys; "-name[*].sub" (remove from a JSON array) is
// the one form with a prefix.
func checkSet(doc database.D) error {
	for _, e := range doc {
		if !condKeyRE.MatchString(strings.TrimPrefix(e.Key, "-")) {
			return invalidIdent("update field", e.Key)
		}
	}
	return nil
}

// checkFields checks sort ("-name") and select ("-name", "$name") fields.
func checkFields(what string, fields []string) error {
	for _, f := range fields {
		name := strings.TrimPrefix(strings.TrimPrefix(f, "-"), "$")
		if !identRE.MatchString(name) {
			return invalidIdent(what, f)
		}
	}
	return nil
}

// checkSelect checks select fields. Entries shorter than two characters are
// ignored by ParseSelect and therefore never reach the statement.
func checkSelect(fields []string) error {
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		if err := checkFields("select field", []string{f}); err != nil {
			return err
		}
	}
	return nil
}
