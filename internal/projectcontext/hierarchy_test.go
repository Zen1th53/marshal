package projectcontext

import (
	"strings"
	"testing"
)

func TestHierarchyProblem(t *testing.T) {
	site := Component{ID: "site", Responsibility: "r", Paths: []string{"web"}}
	login := Component{ID: "login", Responsibility: "r", Paths: []string{"web/login"}, Parent: "site"}
	db := Component{ID: "db", Responsibility: "r", Paths: []string{"db"}}
	if p := HierarchyProblem([]Component{site, login, db}); p != "" {
		t.Fatalf("valid nesting refused: %s", p)
	}
	cases := map[string][]Component{
		"not inside any path":  {site, {ID: "login", Responsibility: "r", Paths: []string{"auth"}, Parent: "site"}},
		"not another declared": {site, {ID: "login", Responsibility: "r", Paths: []string{"web/login"}, Parent: "ghost"}},
		"nested inside itself": {{ID: "a", Responsibility: "r", Paths: []string{"x"}, Parent: "b"}, {ID: "b", Responsibility: "r", Paths: []string{"x"}, Parent: "a"}},
		"contains it":          {site, {ID: "login", Responsibility: "r", Paths: []string{"web/login"}, Parent: "site", Dependencies: []string{"site"}}},
	}
	for want, components := range cases {
		if p := HierarchyProblem(components); !strings.Contains(p, want) {
			t.Errorf("want problem with %q, got %q", want, p)
		}
	}
	if HierarchyProblem([]Component{site, {ID: "x", Responsibility: "r", Paths: []string{"webby"}, Parent: "site"}}) == "" {
		t.Error("a sibling path with the same prefix is not inside the parent")
	}
}
