package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// ValidateContractQuality refuses a project contract that can be drawn but is
// too thin or incomplete for the owner to review:
//  1. every component must state a non-empty responsibility;
//  2. every dependency connection must say what flows over it (a
//     contract.connections entry with non-empty carries);
//  3. every active goal success criterion / confirmed requirement must be
//     covered by at least one component's covers list.
//
// No minimum component count is imposed: coverage determines whether the
// components address the goal.
func ValidateContractQuality(c projectcontext.Contract, requirements []string) error {
	var problems []string
	known := map[string]bool{}
	depEdges := map[string]bool{}
	for _, v := range c.Components {
		known[v.ID] = true
	}
	for _, v := range c.Components {
		if missingContractText(v.Responsibility) {
			problems = append(problems, fmt.Sprintf("component %q has no responsibility; set responsibility to what this component owns", v.ID))
		}
		for _, dep := range v.Dependencies {
			if known[dep] && dep != v.ID {
				depEdges[v.ID+"\x00"+dep] = true
			}
		}
		for _, item := range v.Covers {
			if strings.TrimSpace(item) == "" {
				problems = append(problems, fmt.Sprintf("component %q has a blank entry in covers; list exact goal success criteria or confirmed requirements", v.ID))
				break
			}
		}
	}

	carriesByEdge := map[string]string{}
	seenConn := map[string]bool{}
	for _, conn := range c.Connections {
		key := conn.From + "\x00" + conn.To
		if seenConn[key] {
			problems = append(problems, fmt.Sprintf("connection %q -> %q is listed more than once in contract.connections", conn.From, conn.To))
			continue
		}
		seenConn[key] = true
		if !depEdges[key] {
			problems = append(problems, fmt.Sprintf("connection %q -> %q is not declared in component %q dependencies; add %q to component %q dependencies or remove the connection", conn.From, conn.To, conn.From, conn.To, conn.From))
			continue
		}
		carriesByEdge[key] = conn.Carries
	}

	checkedEdge := map[string]bool{}
	for _, v := range c.Components {
		for _, dep := range v.Dependencies {
			if !known[dep] || dep == v.ID {
				continue
			}
			key := v.ID + "\x00" + dep
			if checkedEdge[key] {
				continue
			}
			checkedEdge[key] = true
			if missingContractText(carriesByEdge[key]) {
				problems = append(problems, fmt.Sprintf("connection %q -> %q does not say what it carries; add {\"from\":%q,\"to\":%q,\"carries\":\"<what flows over this connection>\"} to contract.connections", v.ID, dep, v.ID, dep))
			}
		}
	}

	covered := map[string]bool{}
	for _, v := range c.Components {
		for _, item := range v.Covers {
			if norm := normalizeCoverageText(item); norm != "" {
				covered[norm] = true
			}
		}
	}
	seenReq := map[string]bool{}
	var uncovered []string
	for _, req := range requirements {
		trimmed := strings.TrimSpace(req)
		if trimmed == "" {
			continue
		}
		norm := normalizeCoverageText(trimmed)
		if seenReq[norm] {
			continue
		}
		seenReq[norm] = true
		if !covered[norm] {
			uncovered = append(uncovered, fmt.Sprintf("%q", trimmed))
		}
	}
	if len(uncovered) > 0 {
		problems = append(problems, fmt.Sprintf("goal criteria or confirmed requirements not covered by any component: %s; add each item's text to the covers list of at least one component", strings.Join(uncovered, ", ")))
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func missingContractText(s string) bool {
	trimmed := strings.TrimSpace(s)
	return trimmed == "" || strings.EqualFold(trimmed, contractNotGiven) || strings.EqualFold(trimmed, contractConnectionNotDescribed)
}

func normalizeCoverageText(s string) string {
	s = strings.Join(strings.Fields(strings.ToLower(s)), " ")
	return strings.TrimRight(s, ".")
}
