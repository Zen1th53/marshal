//go:build !governed

package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func formatFailoverSetting(s marshal.Settings) string {
	order := app.GetEffectiveFailoverOrder(s)
	return "\nfailover-order " + strings.Join(order, " -> ")
}

func parseFailoverOrder(raw string) ([]string, error) {
	replacer := strings.NewReplacer("->", ",", " ", ",", "\t", ",")
	normalized := replacer.Replace(raw)
	parts := strings.Split(normalized, ",")
	var result []string
	validProviders := map[string]bool{
		"codex":    true,
		"agy":      true,
		"claude":   true,
		"opencode": true,
	}
	for _, p := range parts {
		trimmed := strings.ToLower(strings.TrimSpace(p))
		if trimmed == "" {
			continue
		}
		if !validProviders[trimmed] {
			return nil, fmt.Errorf("invalid provider %q in failover order (supported: codex, agy, claude, opencode)", trimmed)
		}
		if slices.Contains(result, trimmed) {
			return nil, fmt.Errorf("duplicate provider %q in failover order", trimmed)
		}
		result = append(result, trimmed)
	}
	if len(result) == 0 {
		return nil, errors.New("failover order cannot be empty")
	}
	return result, nil
}

func handleFailoverSetting(args []string, s *marshal.Settings) (string, bool, error) {
	if args[0] != "failover-order" {
		return "", false, nil
	}
	if len(args) < 2 {
		return "", true, errors.New("usage: /marshal settings failover-order <providers>")
	}
	order, err := parseFailoverOrder(strings.Join(args[1:], " "))
	if err != nil {
		return "", true, err
	}
	s.FailoverOrder = strings.Join(order, ",")
	return fmt.Sprintf("failover-order set to %s. It applies to the next Marshal run.", strings.Join(order, " -> ")), true, nil
}
