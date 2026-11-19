package sql

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ti/common-go/dependencies/database"
)

type identRow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Every identifier a caller can pass is checked before a statement is built.
// The handle has no connection: a call that got past the check would panic.
func TestIdentifiersAreChecked(t *testing.T) {
	ctx := context.Background()
	for _, scheme := range []string{schemeMysql, schemePostgres} {
		s := &SQL{scheme: scheme}
		hostile := []string{
			"name` = 'x' OR `id", "name\" OR \"1\"=\"1", "name; DROP TABLE t", "name--", "na me", "name'", "?", "",
			"a.b'c", "a[*].b'", "a[x].b", "1name", "name`",
		}
		var rows []*identRow
		var one identRow
		for _, bad := range hostile {
			cond := database.C{{Key: bad, Value: "v"}}
			good := database.C{{Key: "id", Value: "v"}}
			checks := map[string]error{
				"Find cond":        s.Find(ctx, "t", cond, nil, 0, &rows),
				"Find sort":        s.Find(ctx, "t", good, []string{bad}, 0, &rows),
				"Find desc sort":   s.Find(ctx, "t", good, []string{"-" + bad}, 0, &rows),
				"Find table":       s.Find(ctx, bad, good, nil, 0, &rows),
				"FindOne cond":     s.FindOne(ctx, "t", cond, &one),
				"FindOne table":    s.FindOne(ctx, bad, good, &one),
				"FindAfter order":  s.FindAfter(ctx, "t", good, []string{bad}, []any{"x"}, 1, &rows),
				"FindAfter cond":   s.FindAfter(ctx, "t", cond, []string{"id"}, nil, 1, &rows),
				"InsertOne table":  s.InsertOne(ctx, bad, &identRow{ID: "1"}),
				"EnsureIndexes":    s.EnsureIndexes(ctx, "t", database.Index{Fields: []string{bad}}),
				"EnsureIndex name": s.EnsureIndexes(ctx, "t", database.Index{Fields: []string{"id"}, Name: bad + "x`"}),
				"IncrCounter":      s.IncrCounter(ctx, bad, "k", 0, 1),
			}
			_, checks["Count cond"] = s.Count(ctx, "t", cond)
			_, checks["Exist cond"] = s.Exist(ctx, "t", cond)
			_, checks["Delete cond"] = s.Delete(ctx, "t", cond)
			_, checks["Delete table"] = s.Delete(ctx, bad, good)
			_, checks["Update cond"] = s.Update(ctx, "t", cond, database.D{{Key: "name", Value: "x"}})
			_, checks["Update set"] = s.UpdateOne(ctx, "t", good, database.D{{Key: bad, Value: "x"}})
			_, checks["Update map"] = s.UpdateOne(ctx, "t", good, map[string]any{bad: "x"})
			_, checks["Update table"] = s.UpdateOne(ctx, bad, good, database.D{{Key: "name", Value: "x"}})
			_, checks["GetCounter"] = s.GetCounter(ctx, bad, "k")
			_, checks["PageQuery filter"] = PageQuery[identRow](ctx, s, "t", &database.PageQueryRequest{Filters: cond})
			_, checks["PageQuery sort"] = PageQuery[identRow](ctx, s, "t", &database.PageQueryRequest{Sort: []string{bad}})
			if len(bad) >= 2 { // shorter select entries are ignored, as before
				_, checks["PageQuery select"] = PageQuery[identRow](ctx, s, "t", &database.PageQueryRequest{Select: []string{bad}})
				_, checks["StreamQuery select"] = StreamQuery[identRow](ctx, s, "t", &database.StreamQueryRequest{Select: []string{"-" + bad}})
			}
			_, checks["PageQuery table"] = PageQuery[identRow](ctx, s, bad, &database.PageQueryRequest{})
			_, checks["StreamQuery filter"] = StreamQuery[identRow](ctx, s, "t", &database.StreamQueryRequest{Filters: cond})
			_, checks["StreamQuery table"] = StreamQuery[identRow](ctx, s, bad, &database.StreamQueryRequest{})
			for what, err := range checks {
				if status.Code(err) != codes.InvalidArgument {
					t.Errorf("%s %s with %q: got %v, want InvalidArgument", scheme, what, bad, err)
				}
			}
		}
	}
	// The documented key forms are accepted.
	for _, ok := range []string{"name", "_id", "user_id2", "profile.city", "groups[*].id", "groups[*].", "groups[2].id"} {
		if err := checkConds(database.C{{Key: ok}}); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	if err := checkSet(database.D{{Key: "-groups[*].id"}}); err != nil {
		t.Errorf("remove-from-array key rejected: %v", err)
	}
	if checkTable("app.users") != nil || checkFields("sort field", []string{"-created_at", "$name", "name"}) != nil {
		t.Error("valid table or fields rejected")
	}
}

// updateTime= turns ReplaceOne into update-then-insert on the root handle
// only, as before: views and transaction handles must not inherit it, or an
// unchanged match (0 rows on MySQL) would insert a duplicate.
func TestViewsDoNotInheritUpdateTime(t *testing.T) {
	root := &SQL{scheme: schemeMysql, updateDifferent: true}
	view, err := root.GetDatabase(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if view.(*SQL).updateDifferent {
		t.Error("a GetDatabase view inherited updateTime")
	}
	if view.(*SQL).project != "p1" || !root.updateDifferent || root.project != "" {
		t.Error("GetDatabase changed the root handle or lost the project")
	}
}

// An empty In is "false", an empty Nin "true"; `IN ()` is not valid SQL.
func TestEmptyInCondition(t *testing.T) {
	s := &SQL{scheme: schemePostgres}
	where, args, err := s.where(database.C{{Key: "id", Value: []string{}, C: database.In}, {Key: "a", Value: 1}})
	if err != nil || where != "1 = 0 AND `a` = ?" || len(args) != 1 {
		t.Fatalf("where = %q, %v, %v", where, args, err)
	}
	where, _, err = s.where(database.C{{Key: "id", Value: []any{}, C: database.Nin}})
	if err != nil || where != "1 = 1" {
		t.Fatalf("where = %q, %v", where, err)
	}
	if _, _, err = s.where(database.C{{Key: "id", Value: "x", C: database.In}}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("In with a non-slice: %v", err)
	}
}

// Page and stream queries build their conditions like the CRUD methods: an
// empty In is "false", a non-slice In is InvalidArgument (it panicked), and
// the identifier grammar applies.
func TestPageAndStreamConditions(t *testing.T) {
	ctx := context.Background()
	s := &SQL{scheme: schemeMysql, project: "p"}
	for name, cond := range map[string]database.C{
		"In with a non-slice":  {{Key: "id", Value: "x", C: database.In}},
		"Nin with a non-slice": {{Key: "id", Value: 7, C: database.Nin}},
		"In with nil":          {{Key: "id", C: database.In}},
		"hostile key":          {{Key: "id` OR `1", Value: "x"}},
	} {
		if _, err := PageQuery[identRow](ctx, s, "t", &database.PageQueryRequest{Filters: cond}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("PageQuery %s: %v", name, err)
		}
		if _, err := StreamQuery[identRow](ctx, s, "t", &database.StreamQueryRequest{Filters: cond}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("StreamQuery %s: %v", name, err)
		}
		// The exported helper has no error result: it fails closed.
		if where, args := ParseWhere(cond, "p"); where != "1 = 0" || len(args) != 0 {
			t.Errorf("ParseWhere %s = %q, %v", name, where, args)
		}
	}
	where, args, err := s.whereMode(database.C{{Key: "id", Value: []string{}, C: database.In}, {Key: "a", Value: 1}}, false)
	if err != nil || where != "`project` = ? AND 1 = 0 AND `a` = ?" || len(args) != 2 {
		t.Fatalf("whereMode = %q, %v, %v", where, args, err)
	}
	if where, args := ParseWhere(database.C{{Key: "id", Value: []string{"a", "b"}, C: database.In}}, ""); where != "`id` IN (?,?)" || len(args) != 2 {
		t.Fatalf("ParseWhere = %q, %v", where, args)
	}
}
