//go:build !windows

package app

import "net"

func boundDialer(int) *net.Dialer { return nil }
