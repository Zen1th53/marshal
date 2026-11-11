//go:build governed

package tui

import (
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/store"
)

type proposalMCPState struct{}

func (w *Workspace) prepareProposalMCP(provider, root string, args, env []string, dir *briefingDir) ([]string, []string, *briefingDir, error) {
	return args, env, dir, nil
}
func (w *Workspace) activateProposalMCP(*activeTmuxAgent) {}
func (w *Workspace) revokeProposalMCP(*activeTmuxAgent)   {}
func (w *Workspace) closeProposalMCP()                    {}
func (w *Workspace) bindProposalMCPRequest(req permission.Request) (permission.Request, error) {
	return req, nil
}
func (w *Workspace) beginProposalMCPApply(req permission.Request) (func(bool, error) error, error) {
	return func(bool, error) error { return nil }, nil
}
func (w *Workspace) staleProposalMCP(id string) bool                  { return false }
func (w *Workspace) settledProposalMCP(permission.Request) bool       { return false }
func (w *Workspace) recoverBoundMCPMemory(store.MarshalProposal) bool { return false }

func (w *Workspace) activateNativeProposalMCP(*briefingDir) {}
func (w *Workspace) revokeNativeProposalMCP(*briefingDir)   {}
