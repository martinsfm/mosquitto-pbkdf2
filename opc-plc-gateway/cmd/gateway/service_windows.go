//go:build windows

package main

import (
	"context"
	"log"
	"time"

	"golang.org/x/sys/windows/svc"
)

// serviceName must match whatever name the operator passes to
// `sc create <name> binPath= "..."` when installing the gateway as a
// Windows Service (see README for the exact command).
const serviceName = "OpcPlcGateway"

// runAsServiceOrForeground is the Windows entry point: if the process was
// started by the Service Control Manager it registers as a proper Windows
// service (Start/Stop from services.msc, auto-start on boot, no console
// window needed); otherwise (double-clicked or run from a terminal for
// testing) it just runs the gateway in the foreground until Ctrl+C.
func runAsServiceOrForeground(configPath string, openBrowser bool) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isService {
		ctx, cancel := signalContext()
		defer cancel()
		return run(ctx, configPath, openBrowser)
	}
	// Running under the Service Control Manager: Session 0 has no
	// desktop, so there is no browser to open regardless of the flag.
	return svc.Run(serviceName, &winService{configPath: configPath})
}

type winService struct {
	configPath string
}

func (s *winService) Execute(args []string, r <-chan svc.ChangeRequest, statusCh chan<- svc.Status) (bool, uint32) {
	statusCh <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- run(ctx, s.configPath, false) }()

	statusCh <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-errCh:
			if err != nil {
				log.Printf("gateway: exited with error: %v", err)
				statusCh <- svc.Status{State: svc.Stopped}
				return true, 1
			}
			statusCh <- svc.Status{State: svc.Stopped}
			return false, 0
		case req := <-r:
			switch req.Cmd {
			case svc.Interrogate:
				statusCh <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				statusCh <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-errCh:
				case <-time.After(10 * time.Second):
				}
				statusCh <- svc.Status{State: svc.Stopped}
				return false, 0
			}
		}
	}
}
