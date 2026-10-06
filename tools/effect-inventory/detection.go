package main

import (
	"go/ast"
	"strconv"
	"strings"
)

// Support candidates remain in the reviewed table, but the detector prevents
// them from contributing to either numerator or the material denominator.
func supportReason(call *ast.CallExpr, imports map[string]string, file, function string, body ast.Node) string {
	selector, _ := call.Fun.(*ast.SelectorExpr)
	if selector != nil {
		if receiver, ok := selector.X.(*ast.Ident); ok && imports[receiver.Name] == "github.com/Zen1th53/marshal/internal/hostgit" && selector.Sel.Name == "Root" {
			return "Pure filesystem repository-path resolver; no Git or program execution."
		}
	}
	effect := classify(call, imports, file)
	var args []ast.Expr
	switch effect {
	case "git-program", "git-wrapper", "git-or-restore-wrapper":
		if selector == nil {
			id := call.Fun.(*ast.Ident)
			if strings.Contains(strings.ToLower(id.Name), "restore") {
				return ""
			}
		}
		start := 2
		if selector == nil {
			id := call.Fun.(*ast.Ident)
			if id.Name == "diffGit" {
				start = 3
			}
			if id.Name == "gitOutput" {
				start = 1
			}
			if len(call.Args) > 0 {
				if _, ok := call.Args[0].(*ast.BasicLit); ok && id.Name == "git" {
					start = 0
				}
			}
		} else if effect == "git-wrapper" && file == "internal/worker/honeypot.go" {
			start = 1
		}
		var words []string
		if len(call.Args) > start {
			for _, arg := range call.Args[start:] {
				words = append(words, argv(arg, body, map[*ast.Object]bool{})...)
			}
		}
		if readOnlyGit(words) {
			return "Read-only Git query; no refs, index or worktree mutation."
		}

	case "program", "program-wrapper":
		if selector != nil && selector.Sel.Name == "Run" {
			for _, arg := range call.Args {
				if literal, ok := arg.(*ast.CompositeLit); ok {
					for _, elt := range literal.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Args" {
								args = []ast.Expr{kv.Value}
							}
						}
					}
				}
			}
		} else if selector != nil {
			start := 1
			if selector.Sel.Name == "CommandContext" {
				start = 2
			}
			if len(call.Args) >= start {
				args = call.Args[start:]
			}
		}
		var words []string
		for _, arg := range args {
			words = append(words, argv(arg, body, map[*ast.Object]bool{})...)
		}
		if len(words) > 0 && (words[0] == "--version" || words[0] == "--help" || words[0] == "doctor" || (words[0] == "exec" && len(words) == 2 && words[1] == "--help")) {
			return "Version, help or doctor diagnostic probe; no material task execution."
		}
		if len(words) > 0 && (words[0] == "has-session" || (words[0] == "session" && len(words) > 1 && words[1] == "list")) {
			return "Read-only session status/history query."
		}
		if selector != nil && len(call.Args) > 1 {
			if binary, ok := call.Args[1].(*ast.BasicLit); ok && binary.Value == `"go"` && len(words) > 0 && words[0] == "list" {
				return "Read-only Go module metadata query."
			}
		}
		if selector != nil {
			binaryIndex := 0
			if selector.Sel.Name == "CommandContext" {
				binaryIndex = 1
			}
			if len(call.Args) > binaryIndex {
				if lit, ok := call.Args[binaryIndex].(*ast.BasicLit); ok {
					binary, _ := strconv.Unquote(lit.Value)
					if (binary == "git" || binary == "/usr/bin/git" || binary == "/bin/git") && readOnlyGit(words) {
						return "Read-only Git query; no refs, index or worktree mutation."
					}
				}
			}
		}
		// These private probe dispatchers have reviewed diagnostic-only callers.
		// Their containing-body hashes still require re-review on any source change.
		if (file == "internal/doctor/doctor.go" || file == "internal/resources/resources.go") && function == "commandOutput" {
			return "Private diagnostic dispatcher: tool/namespace probes or nvidia-smi telemetry only."
		}
		if file == "internal/sandbox/bwrap.go" && function == "*Bwrap.Probe" {
			return "Bubblewrap capability probe runs /usr/bin/true in temporary namespaces."
		}
		if file == "internal/hostgit/command.go" && len(call.Args) > 2 && explicitConfigQuery(call.Args[2]) {
			return "Read-only Git helper configuration inspection."
		}
	}
	return ""
}

// argv resolves literal slices, append prefixes and all local assignments.
// Unknown values stay explicit: a dynamic command must remain material.
func argv(expr ast.Expr, body ast.Node, visiting map[*ast.Object]bool) []string {
	switch expr := expr.(type) {
	case *ast.BasicLit:
		if value, err := strconv.Unquote(expr.Value); err == nil {
			return []string{value}
		}
	case *ast.CompositeLit:
		var words []string
		for _, elt := range expr.Elts {
			words = append(words, argv(elt, body, visiting)...)
		}
		return words
	case *ast.CallExpr:
		if id, ok := expr.Fun.(*ast.Ident); ok && id.Name == "append" {
			var words []string
			for _, arg := range expr.Args {
				words = append(words, argv(arg, body, visiting)...)
			}
			return words
		}
	case *ast.Ident:
		if expr.Name == "nil" {
			return nil
		}
		if expr.Obj == nil || visiting[expr.Obj] {
			return []string{"?"}
		}
		visiting[expr.Obj] = true
		defer delete(visiting, expr.Obj)
		var variants [][]string
		ast.Inspect(body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range n.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Obj == expr.Obj && i < len(n.Rhs) {
						// Appending to the same slice preserves its command prefix.
						if c, ok := n.Rhs[i].(*ast.CallExpr); ok && len(c.Args) > 0 {
							if f, ok := c.Fun.(*ast.Ident); ok && f.Name == "append" {
								if base, ok := c.Args[0].(*ast.Ident); ok && base.Obj == expr.Obj {
									continue
								}
							}
						}
						variants = append(variants, argv(n.Rhs[i], body, visiting))
					}
				}
			case *ast.RangeStmt:
				if id, ok := n.Value.(*ast.Ident); ok && id.Obj == expr.Obj {
					if list, ok := n.X.(*ast.CompositeLit); ok {
						for _, elt := range list.Elts {
							variants = append(variants, argv(elt, body, visiting))
						}
					}
				}
			}
			return true
		})
		if len(variants) == 1 {
			return variants[0]
		}
		if len(variants) > 1 {
			for _, v := range variants {
				if !readOnlyGit(v) {
					return []string{"?"}
				}
			}
			return variants[0]
		}
	}
	return []string{"?"}
}

func readOnlyGit(words []string) bool {
	if len(words) == 0 {
		return false
	}
	// Skip fixed Git global options; an unknown command prefix stays material.
	for len(words) > 0 && (strings.HasPrefix(words[0], "--") || words[0] == "?" || words[0] == "-c" || words[0] == "-C" || strings.Contains(words[0], "=")) {
		// Unknown argv is not a known command.
		if words[0] == "?" {
			return false
		}
		if (words[0] == "-c" || words[0] == "-C") && len(words) > 1 {
			words = words[2:]
			continue
		}
		words = words[1:]
	}
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "rev-parse", "status", "diff", "log", "show", "cat-file", "merge-base", "rev-list", "ls-files", "show-ref":
		return true
	case "worktree":
		return len(words) > 1 && words[1] == "list"
	case "remote":
		return len(words) > 1 && words[1] == "get-url"
	case "config":
		for _, word := range words[1:] {
			switch word {
			case "--get", "--get-all", "--get-regexp", "--list", "-l":
				return true
			}
		}
	case "symbolic-ref":
		// Only the current query form: assigning a ref takes an extra operand.
		operands := 0
		for _, word := range words[1:] {
			if word == "--delete" || word == "-d" || word == "?" {
				return false
			}
			if !strings.HasPrefix(word, "-") {
				operands++
			}
		}
		return operands == 1
	case "hash-object":
		for _, word := range words[1:] {
			if word == "--" {
				return true
			}
			if word == "-w" || word == "?" {
				return false
			}
		}
		return true
	}
	return false
}

// Only the explicit tail of append(..., "config", query flags...) is a query;
// append(flags,args...) stays a general material execution boundary.
func explicitConfigQuery(expr ast.Expr) bool {
	c, ok := expr.(*ast.CallExpr)
	if !ok || len(c.Args) < 3 {
		return false
	}
	id, ok := c.Fun.(*ast.Ident)
	if !ok || id.Name != "append" {
		return false
	}
	var words []string
	for _, arg := range c.Args[1:] {
		lit, ok := arg.(*ast.BasicLit)
		if !ok {
			return false
		}
		word, err := strconv.Unquote(lit.Value)
		if err != nil {
			return false
		}
		words = append(words, word)
	}
	return len(words) > 0 && words[0] == "config" && readOnlyGit(words)
}
