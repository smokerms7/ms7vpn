//go:build !windows

package app

import (
	"context"
	"fmt"
)

func tunEnvironment(context.Context) (string, error) {
	return "", fmt.Errorf("эта сборка TUN предназначена для Windows")
}
func verifyTunRoutes(context.Context) error {
	return fmt.Errorf("эта сборка TUN предназначена для Windows")
}
func physicalInterface(context.Context) (string, error) {
	return "", fmt.Errorf("только Windows")
}
