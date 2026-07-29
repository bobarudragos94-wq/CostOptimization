//go:build !linux && !windows

package sched

import "github.com/bobarudragos94-wq/costoptimization/internal/model"

func platformEntries(func(string, string)) []model.SchedEntry { return nil }
