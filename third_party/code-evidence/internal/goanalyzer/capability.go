package goanalyzer

// PredicateCapability declares one supported relation.
type PredicateCapability struct {
	Relation string   `json:"relation"`
	Subjects []string `json:"subjects"`
	Method   string   `json:"method"`
	Bound    string   `json:"bound"`
	Notes    string   `json:"notes"`
}

// Capability is the analyzer capability contract (SPEC §2): supported
// predicates, resolution method, approximation direction, semantic
// dependencies and configuration handling. A relation not listed is
// "unsupported"; incomplete coverage makes results "unknown".
type Capability struct {
	Analyzer         string                `json:"analyzer"`
	Language         string                `json:"language"`
	Method           string                `json:"method"`
	ExecutesRepoCode bool                  `json:"executes_repo_code"`
	Configurations   string                `json:"configurations"`
	Predicates       []PredicateCapability `json:"predicates"`
	Dependencies     string                `json:"dependencies"`
	KnownLimitations []string              `json:"known_limitations"`
}

// Capabilities returns the tier-1 Go analyzer capability declaration.
func Capabilities() Capability {
	return Capability{
		Analyzer:         Version,
		Language:         "go",
		Method:           "syntactic",
		ExecutesRepoCode: false,
		Configurations: "file membership per named configuration from file-name GOOS/GOARCH suffixes, " +
			"//go:build and // +build constraints, cgo (files importing \"C\"), test files and release tags; " +
			"requested configuration == effective configuration (no go command is run)",
		Predicates: []PredicateCapability{
			{RelDeclaredIn, []string{"func", "method", "type", "var", "const"}, MethodDecl, BoundExact,
				"declaration locations per configuration"},
			{RelCallers, []string{"func"}, MethodScope, BoundLower,
				"direct call sites resolved by package scope and imports; function values, reflection, linkname, assembly and dot-imports are not followed"},
			{RelCallers, []string{"method"}, MethodNameMatch, BoundNone,
				"member calls x.M() matched by method name only; receiver types are not resolved (tier 2), so unrelated methods with the same name are included and promoted-method rebinding is invisible"},
			{RelCallees, []string{"func", "method"}, MethodScope, BoundLower,
				"direct calls in the body; member calls are reported unresolved as ?.Name"},
			{RelReferences, []string{"func", "type", "var", "const"}, MethodScope, BoundLower,
				"scope-resolved identifier and qualified-identifier occurrences outside the declaration"},
			{RelTestsOf, []string{"func", "type", "var", "const"}, MethodScope, BoundLower,
				"Test/Benchmark/Fuzz/Example functions that directly reference the subject"},
			{RelTestsOf, []string{"method"}, MethodNameMatch, BoundNone,
				"test functions calling a member with the method's name"},
		},
		Dependencies: "results depend only on the bytes of analyzable visible .go files and go.mod module lines; " +
			"name-match results additionally depend on the set of methods sharing the name (deps digest)",
		KnownLimitations: []string{
			"monotonicity holds for occurrences, not bindings: adding a method can rebind an untouched t.M() (detected only as a deps change → unknown)",
			"import names are inferred from the import path's last element when no explicit name is given",
			"composite-literal keys are treated as field names, never as references",
			"method values (x.M without a call) are not recorded",
			"files that fail to parse make coverage incomplete; negative results are then withheld",
		},
	}
}
