//go:build !windows

package tunnet

import (
	"context"
	"errors"
	"time"
)

var errWindowsOnly = errors.New("TUN-режим Xray настраивается только в Windows")

func DefaultRoute() (string, string, bool)                   { return "", "", false }
func ResolveIPv4All(context.Context, string) []string        { return nil }
func AdapterReady(string) bool                               { return false }
func AdapterExists(string) bool                              { return false }
func WaitAdapterGone(string, time.Duration) bool             { return true }
func PrepareForConnect(string, []string) (string, error)     { return "", errWindowsOnly }
func ConfigureAdapter(context.Context, string, string) error { return errWindowsOnly }
func FlushCaches()                                           {}
func Cleanup([]string)                                       {}
