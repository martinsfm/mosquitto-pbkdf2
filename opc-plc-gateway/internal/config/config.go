// Package config loads the gateway's YAML configuration file: the OPC UA
// server settings and the list of PLC devices (any supported driver) with
// their tags.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// WebUIConfig controls the built-in local management dashboard.
type WebUIConfig struct {
	BindAddr string `yaml:"bind_addr"`
	Port     int    `yaml:"port"`
	// OpenBrowser controls whether the gateway launches the OS default
	// browser pointed at the dashboard on startup. Ignored (never opens)
	// when running as a Windows service, since services have no desktop
	// to show a browser window on.
	OpenBrowser *bool `yaml:"open_browser,omitempty"`
}

func (w WebUIConfig) ShouldOpenBrowser() bool {
	return w.OpenBrowser == nil || *w.OpenBrowser
}

type Config struct {
	Server  ServerConfig   `yaml:"server"`
	WebUI   WebUIConfig    `yaml:"webui"`
	Devices []DeviceConfig `yaml:"devices"`
}

type ServerConfig struct {
	OPCUABindAddr string `yaml:"opcua_bind_addr"`
	OPCUAPort     int    `yaml:"opcua_port"`
}

// DeviceConfig describes one PLC: which driver talks to it, how to reach
// it, and which tags to poll. Driver-specific connection fields (Rack/Slot
// for Siemens, UnitID for Modbus, ...) are simply left empty when unused.
type DeviceConfig struct {
	Name           string      `yaml:"name" json:"name"`
	Driver         string      `yaml:"driver" json:"driver"` // "rockwell" | "siemens" | "mitsubishi" | "modbus"
	Address        string      `yaml:"address" json:"address"`
	Rack           int         `yaml:"rack,omitempty" json:"rack,omitempty"`       // siemens
	Slot           int         `yaml:"slot,omitempty" json:"slot,omitempty"`       // siemens
	UnitID         int         `yaml:"unit_id,omitempty" json:"unit_id,omitempty"` // modbus
	PollIntervalMs int         `yaml:"poll_interval_ms" json:"poll_interval_ms"`
	TimeoutMs      int         `yaml:"timeout_ms" json:"timeout_ms,omitempty"`
	Tags           []TagConfig `yaml:"tags" json:"tags,omitempty"`
}

func (d DeviceConfig) PollInterval() time.Duration {
	if d.PollIntervalMs <= 0 {
		return time.Second
	}
	return time.Duration(d.PollIntervalMs) * time.Millisecond
}

func (d DeviceConfig) Timeout() time.Duration {
	if d.TimeoutMs <= 0 {
		return 2 * time.Second
	}
	return time.Duration(d.TimeoutMs) * time.Millisecond
}

// TagConfig is one point to poll on a device. Address syntax is
// driver-specific, see configs/gateway.example.yaml.
type TagConfig struct {
	Name    string `yaml:"name" json:"name"`
	Address string `yaml:"address" json:"address"`
	Type    string `yaml:"type,omitempty" json:"type,omitempty"` // used by drivers that can't infer type from the device (e.g. modbus)
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %q: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %q: %w", path, err)
	}
	applyDefaults(&cfg)
	for i, d := range cfg.Devices {
		if err := ValidateDevice(d); err != nil {
			return nil, fmt.Errorf("devices[%d]: %w", i, err)
		}
	}
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Server.OPCUAPort == 0 {
		cfg.Server.OPCUAPort = 4840
	}
	if cfg.Server.OPCUABindAddr == "" {
		cfg.Server.OPCUABindAddr = "0.0.0.0"
	}
	if cfg.WebUI.Port == 0 {
		cfg.WebUI.Port = 8080
	}
	if cfg.WebUI.BindAddr == "" {
		cfg.WebUI.BindAddr = "127.0.0.1"
	}
}

// ValidateDevice checks the fields every device needs regardless of driver.
// Driver-specific address syntax is validated by the driver itself when the
// connection is actually attempted.
func ValidateDevice(d DeviceConfig) error {
	if d.Name == "" {
		return fmt.Errorf("name is required")
	}
	if d.Driver == "" {
		return fmt.Errorf("device %q: driver is required", d.Name)
	}
	if d.Address == "" {
		return fmt.Errorf("device %q: address is required", d.Name)
	}
	return nil
}

// Save writes cfg back to path as YAML, preserving it as the single source
// of truth after changes made through the web dashboard (add/remove
// device or tag). It writes to a temp file first and renames over the
// original so a crash mid-write never corrupts the config the gateway
// will try to load on next start.
func Save(path string, cfg *Config) error {
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	return nil
}
