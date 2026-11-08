package tui

type marshalRunAuthority struct {
	egress, workerModes, settings, control, goal string
}

// marshalNoKeystrokes is the same in both editions: the Marshal acts only
// through its MCP tools and proposals, never by typing into a terminal.
const marshalNoKeystrokes = "- Never type into MARSHAL's or any other terminal pane (no tmux send-keys, no terminal automation) and never run MARSHAL TUI slash commands yourself. Runtime actions go only through the Marshal MCP tools and proposals; a slash command named in a runtime message describes the owner's equivalent, not something for you to type.\n"
