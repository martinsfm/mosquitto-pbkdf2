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

type Config struct {
	Server  ServerConfig   `yaml:"server"`
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
	Name           string      `yaml:"name"`
	Driver         string      `yaml:"driver"` // "rockwell" | "siemens" | "modbus"
	Address        string      `yaml:"address"`
	Rack           int         `yaml:"rack,omitempty"`    // siemens
	Slot           int         `yaml:"slot,omitempty"`    // siemens
	UnitID         int         `yaml:"unit_id,omitempty"` // modbus
	PollIntervalMs int         `yaml:"poll_interval_ms"`
	TimeoutMs      int         `yaml:"timeout_ms"`
	Tags           []TagConfig `yaml:"tags"`
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
	Name    string `yaml:"name"`
	Address string `yaml:"address"`
	Type    string `yaml:"type,omitempty"` // used by drivers that can't infer type from the device (e.g. modbus)
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
	if cfg.Server.OPCUAPort == 0 {
		cfg.Server.OPCUAPort = 4840
	}
	if cfg.Server.OPCUABindAddr == "" {
		cfg.Server.OPCUABindAddr = "0.0.0.0"
	}
	for i, d := range cfg.Devices {
		if d.Name == "" {
			return nil, fmt.Errorf("devices[%d]: name is required", i)
		}
		if d.Driver == "" {
			return nil, fmt.Errorf("device %q: driver is required", d.Name)
		}
		if d.Address == "" {
			return nil, fmt.Errorf("device %q: address is required", d.Name)
		}
	}
	return &cfg, nil
}
