package tui

// Control outcome helpers shared by the goal-cancel binding and the slash
// commands: a canonical authority either refuses, or returns without proof.
// Split mechanically from controlbind.go; see that file's header for the
// binding contract.

import "fmt"

// refusal builds the outcome for a canonical authority declining a request.
func refusal(what string, err error, target Target, source string) (Outcome, error) {
	reason := fmt.Sprintf("%s: %s", what, err)
	return Outcome{
		Verdict: VerdictFail,
		Detail:  reason,
		Target:  target,
		Proof:   Errored(reason, source),
	}, err
}

// unproven builds the outcome for a call that returned but could not be proved.
//
// This is the case the contract cares most about: the mutation may or may not
// have applied, and the only honest report is that MARSHAL does not know.
func unproven(target Target, id string, err error) (Outcome, error) {
	reason := fmt.Sprintf(
		"the request was accepted but %s could not be re-read to prove it: %s", id, err)
	return Outcome{
		Verdict: VerdictUnknown,
		Detail:  reason,
		Target:  target,
		Proof:   Unknown(reason, "durable state"),
	}, nil
}
