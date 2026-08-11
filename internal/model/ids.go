package model

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func NewID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate %s ID: %w", prefix, err)
	}
	return prefix + hex.EncodeToString(value[:]), nil
}
