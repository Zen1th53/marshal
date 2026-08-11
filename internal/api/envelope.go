package api

import "encoding/json"

type Version struct {
	APIVersion string `json:"api_version"`
}

type Health struct {
	Status string `json:"status"`
}

type Envelope struct {
	RequestID string          `json:"request_id"`
	Data      json.RawMessage `json:"data,omitempty"`
	Error     *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
