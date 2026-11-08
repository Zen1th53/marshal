//go:build !governed && !linux

package store

import "github.com/Zen1th53/marshal/internal/model"

func legacyPublishNamespace(stage, target string) error { return model.ErrUnavailable }
