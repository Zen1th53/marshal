//go:build !governed

package app

func editionOutputSchemas() map[string]string {
	return map[string]string{
		"requiredArchitectureSchema": requiredArchitectureSchema,
		"requiredVerifierSchema":     requiredVerifierSchema,
	}
}
