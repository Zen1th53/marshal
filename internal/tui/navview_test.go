package tui

// key and runeKey build the KeyEvents the whole package's tests feed to the
// workspace input loop. They lived with the navigation view's tests; the view
// is gone, these shared constructors are not.
func key(k KeyType) KeyEvent  { return KeyEvent{Type: k} }
func runeKey(r rune) KeyEvent { return KeyEvent{Type: KeyRune, Rune: r} }
