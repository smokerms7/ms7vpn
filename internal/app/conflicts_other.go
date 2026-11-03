//go:build !windows

package app

import "context"

func stopConflictingVPNs(context.Context, []string) ([]string, error) { return nil, nil }
