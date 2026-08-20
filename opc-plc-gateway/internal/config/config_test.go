package config

import (
	"os"
	"testing"
)

func TestLoadExample(t *testing.T) {
	cfg, err := Load("../../configs/gateway.example.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.OPCUAPort != 4840 {
		t.Errorf("OPCUAPort = %d, want 4840", cfg.Server.OPCUAPort)
	}
	if len(cfg.Devices) != 4 {
		t.Fatalf("len(Devices) = %d, want 4", len(cfg.Devices))
	}
	if cfg.Devices[0].Driver != "rockwell" {
		t.Errorf("Devices[0].Driver = %q, want rockwell", cfg.Devices[0].Driver)
	}
	if cfg.Devices[1].Driver != "siemens" || cfg.Devices[1].Rack != 0 || cfg.Devices[1].Slot != 1 {
		t.Errorf("Devices[1] = %+v, want siemens rack=0 slot=1", cfg.Devices[1])
	}
	if cfg.Devices[2].Driver != "mitsubishi" {
		t.Errorf("Devices[2] = %+v, want mitsubishi", cfg.Devices[2])
	}
	if cfg.Devices[3].Driver != "modbus" || cfg.Devices[3].UnitID != 1 {
		t.Errorf("Devices[3] = %+v, want modbus unit_id=1", cfg.Devices[3])
	}
}

func TestLoadMissingRequiredFields(t *testing.T) {
	tmp := t.TempDir() + "/bad.yaml"
	if err := os.WriteFile(tmp, []byte("devices:\n  - driver: rockwell\n    address: 1.2.3.4\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(tmp); err == nil {
		t.Fatal("expected error for missing device name")
	}
}
