//go:build governed

package tui

import "context"

type operatorMemoryReadState struct{}

func (*CommandHandler) handleOperatorMemoryReview(context.Context, []string) (string, bool, error) {
	return "", false, nil
}
func operatorMemoryReviewSubcommands(existing []string) []string   { return existing }
func operatorMemoryReviewHelp() string                             { return "" }
func (*CommandHandler) closeOperatorMemoryReads()                  {}
func (*CommandHandler) prepareOperatorMemoryRuntimeAttach() func() { return func() {} }
