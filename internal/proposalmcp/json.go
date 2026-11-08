package proposalmcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// DecodeStrict rejects duplicate object keys at every nesting depth and trailing
// data, then decodes into dst with unknown fields disallowed. It is the single
// entry point for all wire and config decoding so a normalized map can never be
// substituted for the original bytes.
func DecodeStrict(data []byte, dst any) error {
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("proposalmcp: unexpected trailing JSON data")
	}
	return nil
}

func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := walkJSONValue(dec); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("proposalmcp: unexpected trailing JSON data")
	}
	return nil
}

func walkJSONValue(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	switch d := token.(type) {
	case json.Delim:
		switch d {
		case '{':
			return walkJSONObject(dec)
		case '[':
			return walkJSONArray(dec)
		}
	}
	return nil
}

func walkJSONObject(dec *json.Decoder) error {
	seen := map[string]bool{}
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("proposalmcp: invalid JSON object key")
		}
		if seen[key] {
			return fmt.Errorf("proposalmcp: duplicate JSON object key %q", key)
		}
		seen[key] = true
		if err := walkJSONValue(dec); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	return nil
}

func walkJSONArray(dec *json.Decoder) error {
	for dec.More() {
		if err := walkJSONValue(dec); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	return nil
}
