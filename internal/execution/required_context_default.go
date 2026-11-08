//go:build governed

package execution

func requiredConstraintDigest(pkg ConstraintPackage) (string, bool) { return "", false }
func verifyRequiredConstraintBinding(pkg ConstraintPackage) error {
	if pkg.RequiredContext != nil {
		return ErrConstraintViolation
	}
	return nil
}
