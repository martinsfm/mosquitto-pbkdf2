// Command gateway is the OPC UA <-> multi-brand PLC server: it polls every
// device listed in the config file through the matching driver (Rockwell
// EtherNet/IP, Siemens S7, Mitsubishi MC Protocol, Modbus TCP, ...) and
// republishes every tag as an OPC UA variable node that any OPC UA client
// can browse and subscribe to. A local web dashboard (see internal/webui)
// lets you add PLCs, add tags, and watch live values from a browser
// instead of hand-editing the config file - it opens automatically on
// startup unless disabled or running as a Windows service.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"opc-plc-gateway/internal/config"
	"opc-plc-gateway/internal/manager"
	"opc-plc-gateway/internal/opcuaserver"
	"opc-plc-gateway/internal/tagstore"
	"opc-plc-gateway/internal/webui"

	// Side-effect imports: each driver package registers itself with the
	// driver registry on init(). Adding support for a new PLC brand means
	// writing a new package under internal/driver/<brand> and adding one
	// import line here - nothing else in the gateway changes.
	_ "opc-plc-gateway/internal/driver/mitsubishi"
	_ "opc-plc-gateway/internal/driver/modbus"
	_ "opc-plc-gateway/internal/driver/rockwell"
	_ "opc-plc-gateway/internal/driver/siemens"
)

func main() {
	configPath := flag.String("config", "gateway.yaml", "path to the gateway YAML config file")
	noBrowser := flag.Bool("no-browser", false, "don't automatically open the dashboard in a browser on startup")
	flag.Parse()

	if err := runAsServiceOrForeground(*configPath, !*noBrowser); err != nil {
		log.Fatal(err)
	}
}

// run is the actual gateway logic, shared by the console-mode entry point
// and the Windows service entry point (see service_windows.go /
// service_other.go). It blocks until ctx is cancelled.
func run(ctx context.Context, configPath string, openBrowser bool) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	log.Printf("gateway: loaded %d device(s) from %s", len(cfg.Devices), configPath)
	if !cfg.WebUI.IsLoopback() && cfg.WebUI.Password == "" {
		log.Printf("gateway: WARNING - dashboard is bound to %s (reachable from the network) with no password set. Set webui.password in %s.", cfg.WebUI.BindAddr, configPath)
	}

	store := tagstore.New()
	uaSrv := opcuaserver.New(cfg.Server, cfg.Devices, store)

	// The manager owns the live, mutable device list (what the dashboard
	// adds/removes at runtime); the hooks keep the OPC UA address space
	// in sync with it without the manager needing to know OPC UA exists.
	mgr := manager.New(configPath, cfg, store, manager.Hooks{
		OnDeviceAdded: uaSrv.AddDevice,
		OnTagAdded:    uaSrv.AddTag,
	})

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	mgr.StartAll(runCtx)
	defer mgr.Shutdown()

	web := webui.New(cfg.WebUI, mgr, store)
	dashboardURL := fmt.Sprintf("http://%s/", web.Addr())
	if openBrowser {
		go openBrowserWhenReady(runCtx, web.Addr(), dashboardURL)
	} else {
		log.Printf("gateway: dashboard available at %s", dashboardURL)
	}

	errCh := make(chan error, 2)
	go func() { errCh <- uaSrv.Run(runCtx) }()
	go func() { errCh <- web.Run(runCtx) }()

	var firstErr error
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
			cancel() // one side failed - stop the other instead of leaving it dangling
		}
	}
	return firstErr
}

// openBrowserWhenReady waits for the dashboard's HTTP listener to actually
// accept connections (startup is near-instant but not synchronous with
// this goroutine) and then launches the OS default browser at url. It
// gives up quietly after a few seconds - the gateway keeps running either
// way, the dashboard is just one manual browser tab away.
func openBrowserWhenReady(ctx context.Context, addr, url string) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err := openBrowser(url); err != nil {
		log.Printf("gateway: could not auto-open the dashboard (%v) - open %s manually", err, url)
	}
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// signalContext returns a context cancelled on SIGINT/SIGTERM, used by the
// non-Windows-service (console/foreground) run path.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
