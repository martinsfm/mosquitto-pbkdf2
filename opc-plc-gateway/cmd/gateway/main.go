// Command gateway is the OPC UA <-> multi-brand PLC server: it polls every
// device listed in the config file through the matching driver (Rockwell
// EtherNet/IP, Siemens S7, Modbus TCP, ...) and republishes every tag as an
// OPC UA variable node that any OPC UA client can browse and subscribe to.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"opc-plc-gateway/internal/config"
	"opc-plc-gateway/internal/driver"
	"opc-plc-gateway/internal/opcuaserver"
	"opc-plc-gateway/internal/tagstore"

	// Side-effect imports: each driver package registers itself with the
	// driver registry on init(). Adding support for a new PLC brand means
	// writing a new package under internal/driver/<brand> and adding one
	// import line here - nothing else in the gateway changes.
	_ "opc-plc-gateway/internal/driver/modbus"
	_ "opc-plc-gateway/internal/driver/rockwell"
	_ "opc-plc-gateway/internal/driver/siemens"
)

func main() {
	configPath := flag.String("config", "gateway.yaml", "path to the gateway YAML config file")
	flag.Parse()

	if err := runAsServiceOrForeground(*configPath); err != nil {
		log.Fatal(err)
	}
}

// run is the actual gateway logic, shared by the console-mode entry point
// and the Windows service entry point (see service_windows.go /
// service_other.go). It blocks until ctx is cancelled.
func run(ctx context.Context, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	log.Printf("gateway: loaded %d device(s) from %s", len(cfg.Devices), configPath)

	store := tagstore.New()

	for _, devCfg := range cfg.Devices {
		devCfg := devCfg
		d, err := driver.New(devCfg)
		if err != nil {
			return err
		}
		go pollDevice(ctx, devCfg, d, store)
	}

	srv := opcuaserver.New(cfg.Server, cfg.Devices, store)
	return srv.Run(ctx)
}

// pollDevice owns one Driver for the lifetime of the process: it connects,
// polls on the configured interval, writes every value into the shared tag
// store, and marks the device's tags stale (rather than crashing the
// gateway) whenever a poll cycle fails - the next tick simply tries again,
// reconnecting first if needed.
func pollDevice(ctx context.Context, cfg config.DeviceConfig, d driver.Driver, store *tagstore.Store) {
	log.Printf("driver[%s/%s]: connecting to %s", cfg.Driver, cfg.Name, cfg.Address)
	if err := d.Connect(ctx); err != nil {
		log.Printf("driver[%s/%s]: initial connect failed, will retry on next poll: %v", cfg.Driver, cfg.Name, err)
	}
	defer d.Close()

	ticker := time.NewTicker(cfg.PollInterval())
	defer ticker.Stop()

	tagNames := make([]string, len(cfg.Tags))
	for i, t := range cfg.Tags {
		tagNames[i] = t.Name
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			values, err := d.Poll(ctx)
			now := time.Now()
			for name, val := range values {
				store.Set(tagstore.Key(cfg.Name, name), tagstore.Value{
					Value: val, Quality: tagstore.QualityGood, Timestamp: now,
				})
			}
			if err != nil {
				log.Printf("driver[%s/%s]: poll error: %v", cfg.Driver, cfg.Name, err)
				missing := make([]string, 0, len(tagNames))
				for _, n := range tagNames {
					if _, ok := values[n]; !ok {
						missing = append(missing, n)
					}
				}
				store.MarkStale(cfg.Name, missing, err)
			}
		}
	}
}

// signalContext returns a context cancelled on SIGINT/SIGTERM, used by the
// non-Windows-service (console/foreground) run path.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
