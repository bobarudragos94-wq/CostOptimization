package sampler

import "errors"

// errUnsupported marks a probe that does not exist on this platform; it is
// reported once as a degraded collector, not as an error.
var errUnsupported = errors.New("unsupported on this platform")

// ErrUnsupported exposes the sentinel for the agent's health reporting.
var ErrUnsupported = errUnsupported
