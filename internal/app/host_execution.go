package app

import "errors"

// errHostExecution is the recorded cause when the Marshal build runs a
// required check on the host by design rather than after a confinement
// failure.
var errHostExecution = errors.New("Marshal runs checks on the host by design")
