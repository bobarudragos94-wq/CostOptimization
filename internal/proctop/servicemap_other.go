//go:build !linux && !windows

package proctop

// NewPlatformMapper: no service mapping on unsupported platforms.
func NewPlatformMapper() ServiceMapper { return nullMapper{} }

type nullMapper struct{}

func (nullMapper) ServiceFor(int32) string { return "" }
