// Package rockwell talks EtherNet/IP (CIP) to Allen-Bradley / Rockwell
// controllers (ControlLogix, CompactLogix, Micro8xx) using the pure-Go
// gologix library - no vendor software (RSLinx, Studio 5000) required on
// the machine running the gateway.
//
// Tag address in config: the exact controller tag name, e.g.
//
//	"Local:1:I.Data" or "Program:MainProgram.Velocidade" or "MyUDT.Member[3]"
package rockwell

import (
	"context"
	"fmt"

	"github.com/danomagnum/gologix"

	"opc-plc-gateway/internal/config"
	"opc-plc-gateway/internal/driver"
)

func init() {
	driver.Register("rockwell", New)
}

type Driver struct {
	cfg    config.DeviceConfig
	client *gologix.Client
}

func New(cfg config.DeviceConfig) (driver.Driver, error) {
	return &Driver{cfg: cfg}, nil
}

func (d *Driver) Connect(ctx context.Context) error {
	d.client = gologix.NewClient(d.cfg.Address)
	if err := d.client.Connect(); err != nil {
		return fmt.Errorf("rockwell %s: connect %s: %w", d.cfg.Name, d.cfg.Address, err)
	}
	return nil
}

func (d *Driver) Poll(ctx context.Context) (map[string]interface{}, error) {
	if d.client == nil || !d.client.Connected() {
		if err := d.Connect(ctx); err != nil {
			return nil, err
		}
	}

	results := make(map[string]interface{}, len(d.cfg.Tags))
	var firstErr error
	for _, tag := range d.cfg.Tags {
		// CIPTypeUnknown lets gologix infer the tag's data type from the
		// controller's own tag database, so the config doesn't need to
		// know it up front.
		val, err := d.client.Read_single(tag.Address, gologix.CIPTypeUnknown, 1)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("rockwell %s: read %s: %w", d.cfg.Name, tag.Address, err)
			}
			continue
		}
		results[tag.Name] = val
	}
	// A device is considered reachable if at least one tag was read; a
	// fully empty result (with tags configured) means the connection
	// itself is the problem, so surface the error to trigger a reconnect.
	if len(results) == 0 && len(d.cfg.Tags) > 0 {
		return results, firstErr
	}
	return results, nil
}

func (d *Driver) Close() error {
	if d.client == nil {
		return nil
	}
	return d.client.Disconnect()
}
