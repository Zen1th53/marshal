package app

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func goodQualityContract() projectcontext.Contract {
	return projectcontext.Contract{
		ID:       "review-fixes",
		Revision: 1,
		Purpose:  "Fix the architecture review page and its submit checks",
		Components: []projectcontext.Component{
			{
				ID:             "architecturereview",
				Responsibility: "Serve the browser review page and render components and labeled edges",
				Paths:          []string{"internal/architecturereview"},
				Dependencies:   []string{"architecturecodec"},
				Covers:         []string{"Review page shows real connection labels", "All 18 review fixes pass tests"},
			},
			{
				ID:             "architecturecodec",
				Responsibility: "Validate and encode stored architecture state and generic layouts",
				Paths:          []string{"internal/architecturecodec"},
				Covers:         []string{"Stored review state round-trips without loss"},
			},
		},
		Connections: []projectcontext.Connection{
			{From: "architecturereview", To: "architecturecodec", Carries: "stored review state and layout encoding"},
		},
		Invariants: []string{"never invent text the contract did not supply"},
	}
}

func TestValidateContractQualityPassesGoodContract(t *testing.T) {
	criteria := []string{
		"Review page shows real connection labels",
		"All 18 review fixes pass tests",
		"Stored review state round-trips without loss",
	}
	good := goodQualityContract()
	if err := ValidateContractDrawable(good); err != nil {
		t.Fatalf("good contract failed drawable check: %v", err)
	}
	if err := ValidateContractQuality(good, criteria); err != nil {
		t.Fatalf("good contract failed quality check: %v", err)
	}

	// Single component covering all criteria passes: no invented minimum count.
	single := projectcontext.Contract{
		ID:       "tiny-cli",
		Revision: 1,
		Purpose:  "Print greeting",
		Components: []projectcontext.Component{
			{
				ID:             "cli",
				Responsibility: "Parse args and print greeting",
				Paths:          []string{"main.go"},
				Covers:         criteria,
			},
		},
	}
	if err := ValidateContractQuality(single, criteria); err != nil {
		t.Fatalf("single-component contract covering all criteria was refused: %v", err)
	}

	// Normalization tolerates minor case, whitespace, and trailing-period differences.
	normalized := goodQualityContract()
	normalized.Components[0].Covers = []string{
		"  review page   shows real connection labels. ",
		"all 18 review fixes pass tests.",
	}
	if err := ValidateContractQuality(normalized, criteria); err != nil {
		t.Fatalf("normalized criterion text was refused: %v", err)
	}
}

func TestValidateContractQualityRefusesConnectionWithoutCarries(t *testing.T) {
	criteria := []string{
		"Review page shows real connection labels",
		"All 18 review fixes pass tests",
		"Stored review state round-trips without loss",
	}
	for name, mutate := range map[string]func(*projectcontext.Contract){
		"missing_connection":    func(c *projectcontext.Contract) { c.Connections = nil },
		"empty_carries":         func(c *projectcontext.Contract) { c.Connections[0].Carries = "" },
		"whitespace_carries":    func(c *projectcontext.Contract) { c.Connections[0].Carries = "   \t " },
		"not_given_carries":     func(c *projectcontext.Contract) { c.Connections[0].Carries = "not given" },
		"not_described_carries": func(c *projectcontext.Contract) { c.Connections[0].Carries = "not described" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := goodQualityContract()
			mutate(&bad)
			err := ValidateContractQuality(bad, criteria)
			if err == nil {
				t.Fatalf("%s: unlabeled connection was accepted", name)
			}
			for _, want := range []string{`"architecturereview" -> "architecturecodec"`, "does not say what it carries", "contract.connections"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s: refusal %q lacks %q", name, err.Error(), want)
				}
			}
		})
	}

	// Undeclared or duplicate connection entries are also refused with actionable text.
	undeclared := goodQualityContract()
	undeclared.Connections = append(undeclared.Connections, projectcontext.Connection{
		From: "architecturecodec", To: "architecturereview", Carries: "back edge",
	})
	if err := ValidateContractQuality(undeclared, criteria); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("undeclared connection not refused: %v", err)
	}
	dup := goodQualityContract()
	dup.Connections = append(dup.Connections, dup.Connections[0])
	if err := ValidateContractQuality(dup, criteria); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate connection not refused: %v", err)
	}
}

func TestValidateContractQualityRefusesUncoveredGoalCriteria(t *testing.T) {
	criteria := []string{
		"Review page shows real connection labels",
		"All 18 review fixes pass tests",
		"Stored review state round-trips without loss",
	}

	// Real-world regression: two components, one unlabeled edge, three uncovered criteria.
	thin := projectcontext.Contract{
		ID:       "thin-contract",
		Revision: 1,
		Purpose:  "Fix review page",
		Components: []projectcontext.Component{
			{ID: "architecturereview", Responsibility: "Review page", Paths: []string{"internal/architecturereview"}, Dependencies: []string{"architecturecodec"}},
			{ID: "architecturecodec", Responsibility: "Codec", Paths: []string{"internal/architecturecodec"}},
		},
	}
	err := ValidateContractQuality(thin, criteria)
	if err == nil {
		t.Fatal("thin contract with unlabeled edge and uncovered criteria was accepted")
	}
	for _, want := range []string{
		`"architecturereview" -> "architecturecodec"`,
		"does not say what it carries",
		"Review page shows real connection labels",
		"All 18 review fixes pass tests",
		"Stored review state round-trips without loss",
		"covers",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err.Error(), want)
		}
	}

	// Vague placeholder text in covers does not satisfy coverage.
	vague := goodQualityContract()
	vague.Components[0].Covers = []string{"all"}
	vague.Components[1].Covers = nil
	if err := ValidateContractQuality(vague, criteria); err == nil || !strings.Contains(err.Error(), "All 18 review fixes pass tests") {
		t.Fatalf("vague covers satisfied criteria: %v", err)
	}

	// Blank entry in covers is refused.
	blankCover := goodQualityContract()
	blankCover.Components[0].Covers = append(blankCover.Components[0].Covers, "  ")
	if err := ValidateContractQuality(blankCover, criteria); err == nil || !strings.Contains(err.Error(), "blank entry in covers") {
		t.Fatalf("blank covers entry was accepted: %v", err)
	}
}

func TestValidateContractQualityRefusesMissingComponentResponsibility(t *testing.T) {
	criteria := []string{
		"Review page shows real connection labels",
		"All 18 review fixes pass tests",
		"Stored review state round-trips without loss",
	}
	for name, value := range map[string]string{
		"empty":      "",
		"whitespace": "   \n ",
		"not_given":  "not given",
	} {
		t.Run(name, func(t *testing.T) {
			bad := goodQualityContract()
			bad.Components[1].Responsibility = value
			err := ValidateContractQuality(bad, criteria)
			if err == nil {
				t.Fatalf("%s: component with missing responsibility was accepted", name)
			}
			for _, want := range []string{`"architecturecodec"`, "has no responsibility", "responsibility"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s: refusal %q lacks %q", name, err.Error(), want)
				}
			}
		})
	}
}
