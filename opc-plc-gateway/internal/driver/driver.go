// Package driver defines the contract every PLC brand connector must
// implement, plus a small registry so new brands can be added without
// touching the gateway's core: a driver package just needs to call
// Register() from an init() function and be imported (for its side effect)
// from cmd/gateway/main.go.
package driver

import (
	"context"
	"fmt"

	"opc-plc-gateway/internal/config"
)

// Driver polls one physical PLC and returns the current value of every
// configured tag. Implementations are expected to keep their own
// connection alive across calls to Poll and reconnect transparently when
// needed; a Poll call that returns an error is treated by the gateway as
// "this device is currently unreachable" and its tags are marked stale.
type Driver interface {
	// Connect opens the connection to the device. Called once at startup
	// and again after a Poll error if the driver reports itself
	// disconnected.
	Connect(ctx context.Context) error

	// Poll reads every tag in cfg.Tags and returns its value keyed by tag
	// name. A partial result (some tags read, others missing) is valid;
	// the gateway will mark the missing ones stale.
	Poll(ctx context.Context) (map[string]interface{}, error)

	// Close releases the underlying connection.
	Close() error
}

// Writer is an optional capability: drivers that support writing a value
// back to the PLC implement it in addition to Driver. Not every driver (or
// every tag on a driver that does support it - e.g. Mitsubishi bit
// devices) can write; WriteTag returns an error for those, the same way
// Poll would fail to read an invalid tag.
type Writer interface {
	WriteTag(ctx context.Context, tagName string, value interface{}) error
}

// Factory builds a Driver for the given device configuration.
type Factory func(cfg config.DeviceConfig) (Driver, error)

var registry = map[string]Factory{}

// Register makes a driver factory available under name (e.g. "rockwell").
// Call it from the driver package's init().
func Register(name string, f Factory) {
	registry[name] = f
}

// New builds the driver configured for cfg.Driver.
func New(cfg config.DeviceConfig) (Driver, error) {
	f, ok := registry[cfg.Driver]
	if !ok {
		return nil, fmt.Errorf("unknown driver %q (device %q) - registered drivers: %v", cfg.Driver, cfg.Name, Names())
	}
	return f(cfg)
}

// Names lists every registered driver, for error messages and diagnostics.
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	return names
}
