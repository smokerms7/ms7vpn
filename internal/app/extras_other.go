//go:build !windows && !ms7android

package app

const AutostartArg = "--autostart"

func AutostartEnabled() bool               { return false }
func SetAutostart(bool) error              { return nil }
func RegisterURLScheme() error             { return nil }
func runningPrograms() []string            { return []string{} }
func appIconPNG(string) []byte             { return nil }
func EnableConsoleProxy(string, int) error { return nil }
func RestoreConsoleProxy(string) error     { return nil }
