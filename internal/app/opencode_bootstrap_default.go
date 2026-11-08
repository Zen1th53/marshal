//go:build governed

package app

import "github.com/Zen1th53/marshal/internal/model"

func privateProviderBootstrap(string) ([]model.Bind, []string, error) { return nil, nil, nil }

func privateOpenCodeVersion() string { return "" }

func privateProviderStorageDirs(_ string, paths []string) []string { return paths }

func privateOpenCodeLocalConfig(_, _ string) ([]string, error) { return nil, nil }
func privateOpenCodeProbeConfig(_, _ string) ([]string, error) { return nil, nil }
