//go:build governed

package app

func marshalStructuredResultPrompt(_, prompt, _ string) string { return prompt }

func MarshalProviders() []string { return []string{"codex", "claude", "agy"} }

func buildMarshalTurn(provider, prompt, schema, session string) ([]string, bool) {
	return nil, false
}

func buildMarshalOutput(provider string, data []byte) ([]byte, string, bool, error) {
	return nil, "", false, nil
}
