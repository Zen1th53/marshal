//go:build !governed

package app

func privateLocalControlActions() []string {
	return []string{"cell.prepare", "cell.exec", "cell.destroy"}
}
