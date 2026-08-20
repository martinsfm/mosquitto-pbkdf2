//go:build !windows

package main

// runAsServiceOrForeground on non-Windows platforms just runs the gateway
// in the foreground (use systemd/launchd to daemonize there instead - this
// build target exists mainly so the codebase can be developed and tested
// outside Windows; production deployment is Windows + Windows Service, see
// service_windows.go).
func runAsServiceOrForeground(configPath string) error {
	ctx, cancel := signalContext()
	defer cancel()
	return run(ctx, configPath)
}
