//go:build !governed

package store

func init() {
	knownMemoryTables["extreme_memory_snapshots"] = "canonical-private-archive: immutable selected full-record snapshot, Marshal v92"
	knownMemoryTables["extreme_memory_restore_previews"] = "canonical-private-receipt: exact authenticated restore preview and consumption, Marshal v92"
	knownMemoryTables["extreme_memory_restore_history"] = "canonical-private-history: immutable full pre-restore and restored revisions, Marshal v92"
	knownMemoryTables["extreme_approved_cells"] = "canonical-private-cell: exact approved one-shot cell descriptor and state, Marshal v93"
	knownMemoryTables["extreme_approved_cell_history"] = "canonical-private-history: immutable approved cell revisions, Marshal v93"
	knownMemoryTables["extreme_cell_command_phases"] = "canonical-private-receipt: immutable one-shot cell command phases, Marshal v93"
}
