package archconform

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func shop() *Map {
	return NewMap(projectcontext.Contract{Components: []projectcontext.Component{
		{ID: "web", Paths: []string{"web"}, Dependencies: []string{"api"}},
		{ID: "login", Paths: []string{"web/login"}, Parent: "web"},
		{ID: "signup", Paths: []string{"web/signup.ts"}, Parent: "web"},
		{ID: "api", Paths: []string{"internal/api"}, Dependencies: []string{"store"}},
		{ID: "store", Paths: []string{"internal/store"}},
		{ID: "billing", Paths: []string{"billing"}},
	}})
}

func source(files map[string]string) Source {
	return func(p string) (string, bool) { t, ok := files[p]; return t, ok }
}

func kinds(fs []Finding) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Kind+":"+f.File+":"+f.Uses)
	}
	return strings.Join(out, " ")
}

func TestOwnerIsTheMostSpecificPart(t *testing.T) {
	m := shop()
	for p, want := range map[string]string{"web/index.ts": "web", "web/login/form.ts": "login", "./web/login": "login", "web/signup.ts": "signup", "internal/api/x.go": "api", "docs/a.md": "", "webby/x": ""} {
		if got := m.Owner(p); got != want {
			t.Errorf("Owner(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestDependenciesFollowTheHierarchy(t *testing.T) {
	m := shop()
	cases := map[[2]string]bool{
		{"login", "api"}:    true, // web, which contains login, depends on api
		{"login", "web"}:    true, // inside its own container
		{"web", "login"}:    true,
		{"login", "signup"}: false, // siblings with no declared dependency
		{"api", "store"}:    true,
		{"store", "api"}:    false,
		{"web", "store"}:    false, // dependencies are not transitive
	}
	for pair, want := range cases {
		if got := m.Allowed(pair[0], pair[1]); got != want {
			t.Errorf("Allowed(%s, %s) = %v, want %v", pair[0], pair[1], got, want)
		}
	}
}

func TestCheckFindsUndeclaredUseByLanguage(t *testing.T) {
	m := shop()
	files := map[string]string{
		"internal/store/db.go": "package store\nimport (\n\t\"fmt\"\n\tapi \"example.com/shop/internal/api\"\n)\n",
		"internal/api/h.go":    "package api\nimport \"example.com/shop/internal/store\"\n",
		"web/login/form.ts":    "import { save } from '../signup'\nimport x from 'react'\nconst y = require('../../internal/api/client')\n",
		"billing/pay.py":       "from internal.store import db\nimport os, internal.api.client\n",
	}
	var changed []string
	for f := range files {
		changed = append(changed, f)
	}
	got := kinds(Check(m, changed, nil, "example.com/shop", source(files)))
	for _, want := range []string{
		"undeclared_dependency:billing/pay.py:store", "undeclared_dependency:billing/pay.py:api",
		"undeclared_dependency:internal/store/db.go:api", "undeclared_dependency:web/login/form.ts:signup",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	for _, unwanted := range []string{"internal/api/h.go", "form.ts:api", "react"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("unexpected %s in %s", unwanted, got)
		}
	}
}

func TestCheckFindsUnownedAndOutsideTask(t *testing.T) {
	m := shop()
	files := map[string]string{"web/login/a.ts": "", "billing/b.py": "", "README.md": ""}
	got := Check(m, []string{"web/login/a.ts", "billing/b.py", "README.md", "web/gone.ts"}, []string{"web"}, "", source(files))
	text := kinds(got)
	if !strings.Contains(text, "unowned:README.md") || !strings.Contains(text, "outside_task:billing/b.py") || strings.Contains(text, "web/login") {
		t.Fatalf("findings = %s", text)
	}
	for _, f := range got {
		if f.String() == "" {
			t.Fatal("every finding explains itself")
		}
	}
}

func TestGoModule(t *testing.T) {
	if GoModule("// c\nmodule example.com/shop\n\ngo 1.26\n") != "example.com/shop" {
		t.Fatal("module path not read")
	}
}
