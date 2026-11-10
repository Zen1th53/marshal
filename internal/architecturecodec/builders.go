package architecturecodec

import "errors"

// BuilderProviders are the providers an owner may choose to build a part,
// in the order the browser lists them.
var BuilderProviders = []string{"claude", "codex", "agy", "opencode"}

// ValidateBuilders checks a builders object against the architecture's
// component IDs: every key is a component, every value a provider.
func ValidateBuilders(builders map[string]any, architecture map[string]any) error {
	ids := map[string]bool{}
	if components, ok := architecture["components"].([]any); ok {
		for _, value := range components {
			if component, ok := value.(map[string]any); ok {
				if id, ok := component["id"].(string); ok {
					ids[id] = true
				}
			}
		}
	}
	known := map[string]bool{}
	for _, provider := range BuilderProviders {
		known[provider] = true
	}
	for key, value := range builders {
		if !ids[key] {
			return errors.New("architecture builders name an unknown part")
		}
		provider, ok := value.(string)
		if !ok || !known[provider] {
			return errors.New("architecture builder is not a known provider")
		}
	}
	return nil
}

// BuildersDigest is the canonical digest of a builders object.
func BuildersDigest(builders map[string]any) (string, error) {
	return digestValue(builders)
}
