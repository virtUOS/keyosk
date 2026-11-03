package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"

	"github.com/virtuos/keyosk/internal/database/migrations"
)

var (
	sqlSpace  = regexp.MustCompile(`\s+`)
	sqlPadded = regexp.MustCompile(`\s*([(),])\s*`)
)

// normalizeSQL collapses the whitespace in a statement SQLite stored, and
// drops it around parentheses and commas. ALTER TABLE ADD COLUMN puts the new
// column's text after whatever closed the original statement, so a table that
// grew column by column and one created whole differ in nothing else.
func normalizeSQL(s string) string {
	s = sqlSpace.ReplaceAllString(strings.TrimSpace(s), " ")
	return sqlPadded.ReplaceAllString(s, "$1")
}

// dumpSchema describes every table, index and trigger in db as SQLite
// reports them, leaving out SQLite's own tables and bun's. The pragmas give
// each column, foreign key and index; the statement adds what they leave out,
// such as AUTOINCREMENT, collations and a partial index's WHERE.
//
// Indexes and foreign keys are listed by name and column rather than in the
// order they were created, which differs between a schema built in steps and
// one built at once.
func dumpSchema(t *testing.T, db *bun.DB) string {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder

	query := func(q string, args ...any) *sql.Rows {
		t.Helper()
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		t.Cleanup(func() { rows.Close() })
		return rows
	}
	scan := func(rows *sql.Rows, dest ...any) {
		t.Helper()
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
	}

	type object struct{ name, sql string }
	objects := func(typ, table string) []object {
		t.Helper()
		rows := query(`SELECT name, COALESCE(sql, '') FROM sqlite_master
			WHERE type = ? AND (? = '' OR tbl_name = ?)
			  AND name NOT LIKE 'sqlite\_%' ESCAPE '\' AND name NOT LIKE 'bun\_%' ESCAPE '\'
			ORDER BY name`, typ, table, table)
		var objs []object
		for rows.Next() {
			var o object
			scan(rows, &o.name, &o.sql)
			objs = append(objs, o)
		}
		return objs
	}

	for _, table := range objects("table", "") {
		fmt.Fprintf(&b, "table %s\n  %s\n", table.name, normalizeSQL(table.sql))

		rows := query(`SELECT cid, name, type, "notnull", dflt_value, pk
			FROM pragma_table_info(?) ORDER BY cid`, table.name)
		for rows.Next() {
			var cid, notNull, pk int
			var name, typ string
			var dflt sql.NullString
			scan(rows, &cid, &name, &typ, &notNull, &dflt, &pk)
			def := "none"
			if dflt.Valid {
				def = dflt.String
			}
			fmt.Fprintf(&b, "  column %d %s %s notnull=%d default=%s pk=%d\n",
				cid, name, typ, notNull, def, pk)
		}

		rows = query(`SELECT "from", "table", "to", on_update, on_delete, "match"
			FROM pragma_foreign_key_list(?) ORDER BY "from", "table", "to"`, table.name)
		for rows.Next() {
			var from, ref, to, onUpdate, onDelete, match string
			scan(rows, &from, &ref, &to, &onUpdate, &onDelete, &match)
			fmt.Fprintf(&b, "  foreign key %s -> %s(%s) on update %s on delete %s match %s\n",
				from, ref, to, onUpdate, onDelete, match)
		}

		rows = query(`SELECT il.name, il."unique", il.origin, il.partial, COALESCE(m.sql, '')
			FROM pragma_index_list(?) AS il
			LEFT JOIN sqlite_master AS m ON m.type = 'index' AND m.name = il.name
			ORDER BY il.name`, table.name)
		type index struct {
			name, origin, sql string
			unique, partial   int
		}
		var indexes []index
		for rows.Next() {
			var ix index
			scan(rows, &ix.name, &ix.unique, &ix.origin, &ix.partial, &ix.sql)
			indexes = append(indexes, ix)
		}
		for _, ix := range indexes {
			var cols []string
			rows := query(`SELECT name, "desc", coll FROM pragma_index_xinfo(?)
				WHERE "key" = 1 ORDER BY seqno`, ix.name)
			for rows.Next() {
				var name, coll string
				var desc int
				scan(rows, &name, &desc, &coll)
				dir := "ASC"
				if desc == 1 {
					dir = "DESC"
				}
				cols = append(cols, name+" "+dir+" "+coll)
			}
			fmt.Fprintf(&b, "  index %s unique=%d origin=%s partial=%d (%s)\n",
				ix.name, ix.unique, ix.origin, ix.partial, strings.Join(cols, ", "))
			if ix.sql != "" {
				fmt.Fprintf(&b, "    %s\n", normalizeSQL(ix.sql))
			}
		}

		for _, trigger := range objects("trigger", table.name) {
			fmt.Fprintf(&b, "  trigger %s\n    %s\n", trigger.name, normalizeSQL(trigger.sql))
		}
	}
	return b.String()
}

// migrateThrough runs every migration up to and including the one with the
// given name.
func migrateThrough(t *testing.T, s *Store, name string) {
	t.Helper()
	ctx := context.Background()
	ms := migrate.NewMigrations()
	for _, m := range migrations.Migrations.Sorted() {
		if m.Name <= name {
			ms.Add(m)
		}
	}
	m := newMigrator(s.db, ms)
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
}

// lineDiff lists the lines only want has, marked -, and those only got has,
// marked +.
func lineDiff(want, got string) string {
	count := func(s string) map[string]int {
		n := map[string]int{}
		for _, l := range strings.Split(s, "\n") {
			n[l]++
		}
		return n
	}
	wantN, gotN := count(want), count(got)
	var b strings.Builder
	for _, l := range strings.Split(want, "\n") {
		if gotN[l] > 0 {
			gotN[l]--
		} else {
			fmt.Fprintf(&b, "- %s\n", l)
		}
	}
	for _, l := range strings.Split(got, "\n") {
		if wantN[l] > 0 {
			wantN[l]--
		} else {
			fmt.Fprintf(&b, "+ %s\n", l)
		}
	}
	return b.String()
}

// A database that starts on this release and one that passed through 0.9.0
// must have the same schema, or code tested against one breaks on the other.
// testdata/schema-v0.9.0.txt was written by dumpSchema from a database built
// by the fifteen migrations up to 0.9.0, before the baseline replaced them.
func TestBaselineBuildsTheReleasedSchema(t *testing.T) {
	want, err := os.ReadFile("testdata/schema-v0.9.0.txt")
	if err != nil {
		t.Fatal(err)
	}
	s := testStore(t, t.Name())
	migrateThrough(t, s, migrations.BaselineName)
	if got := dumpSchema(t, s.db); got != string(want) {
		t.Errorf("the baseline's schema differs from 0.9.0's:\n%s", lineDiff(string(want), got))
	}
}
