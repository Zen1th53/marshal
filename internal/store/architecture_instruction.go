package store

// BrowserAcceptanceInstruction is the instruction text recorded when the
// owner presses Accept architecture in the browser.
const BrowserAcceptanceInstruction = "accept architecture"

func ArchitectureAcceptanceInstruction(text string) bool { return text == BrowserAcceptanceInstruction }
