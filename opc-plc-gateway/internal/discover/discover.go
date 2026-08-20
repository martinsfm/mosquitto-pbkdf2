// Package discover implements the dashboard's "Procurar PLCs na rede"
// button: a bounded TCP port scan of the local subnet(s) against the
// well-known ports each supported PLC brand's protocol listens on. It is
// a hint, not an identification - a listening port strongly suggests a
// brand but does not prove it (e.g. Omron's NJ/NX series also answers on
// EtherNet/IP's port 44818, same as Rockwell).
package discover

import (
	"context"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

// Found is one open port seen during a scan.
type Found struct {
	IP     string `json:"ip"`
	Port   int    `json:"port"`
	Driver string `json:"driver"`
	Label  string `json:"label"`
}

type portGuess struct {
	port   int
	driver string
	label  string
}

var wellKnownPorts = []portGuess{
	{44818, "rockwell", "Rockwell / Allen-Bradley (EtherNet/IP) — também pode ser outro dispositivo CIP, ex: Omron NJ/NX"},
	{102, "siemens", "Siemens (S7 / ISO-on-TCP)"},
	{502, "modbus", "Modbus TCP — Schneider, ABB, Omron, Danfoss, Emerson, Honeywell, Yokogawa, Yaskawa, SEW ou outro"},
	{5007, "mitsubishi", "Mitsubishi (MC Protocol) — confirme a porta configurada no GX Works"},
}

// maxHostsPerNetwork caps how many addresses a single local network gets
// expanded to, so a machine sitting on a very large VLAN doesn't turn one
// dashboard click into a scan of tens of thousands of hosts.
const maxHostsPerNetwork = 1024

// LocalIPv4Networks returns the IPv4 networks this machine has an
// interface on (skipping loopback and link-local), as the default scan
// target for the dashboard's discovery button.
func LocalIPv4Networks() []*net.IPNet {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var nets []*net.IPNet
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || ip4.IsLinkLocalUnicast() {
				continue
			}
			ones, _ := ipnet.Mask.Size()
			if ones < 22 { // skip anything bigger than a /22 (~1000 hosts)
				continue
			}
			nets = append(nets, &net.IPNet{IP: ip4, Mask: ipnet.Mask})
		}
	}
	return nets
}

// Scan probes every well-known PLC port against every host in nets,
// bounded by concurrency and perHostTimeout, and returns every port that
// answered. It respects ctx for early cancellation (e.g. an HTTP request
// timeout or the caller navigating away).
func Scan(ctx context.Context, nets []*net.IPNet, perHostTimeout time.Duration) []Found {
	if perHostTimeout <= 0 {
		perHostTimeout = 400 * time.Millisecond
	}

	hosts := expandHosts(nets)

	type target struct {
		ip string
		pg portGuess
	}
	targets := make(chan target, 256)
	results := make(chan Found, 64)

	var wg sync.WaitGroup
	const workers = 200
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := net.Dialer{Timeout: perHostTimeout}
			for t := range targets {
				select {
				case <-ctx.Done():
					continue
				default:
				}
				addr := fmt.Sprintf("%s:%d", t.ip, t.pg.port)
				conn, err := d.DialContext(ctx, "tcp", addr)
				if err != nil {
					continue
				}
				conn.Close()
				results <- Found{IP: t.ip, Port: t.pg.port, Driver: t.pg.driver, Label: t.pg.label}
			}
		}()
	}

	go func() {
		defer close(targets)
		for _, ip := range hosts {
			for _, pg := range wellKnownPorts {
				select {
				case <-ctx.Done():
					return
				case targets <- target{ip: ip, pg: pg}:
				}
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	found := make([]Found, 0)
	for f := range results {
		found = append(found, f)
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].IP != found[j].IP {
			return found[i].IP < found[j].IP
		}
		return found[i].Port < found[j].Port
	})
	return found
}

// expandHosts lists every usable host address in each network (excluding
// network/broadcast addresses for netmasks smaller than /31), capped at
// maxHostsPerNetwork per network.
func expandHosts(nets []*net.IPNet) []string {
	var hosts []string
	for _, n := range nets {
		ones, bits := n.Mask.Size()
		if bits != 32 {
			continue
		}
		count := 1 << uint(bits-ones)
		base := n.IP.Mask(n.Mask)

		start, end := 0, count
		if count > 2 {
			start, end = 1, count-1 // skip network and broadcast addresses
		}
		if end-start > maxHostsPerNetwork {
			end = start + maxHostsPerNetwork
		}
		for i := start; i < end; i++ {
			ip := make(net.IP, 4)
			copy(ip, base)
			addUint32(ip, uint32(i))
			hosts = append(hosts, ip.String())
		}
	}
	return hosts
}

func addUint32(ip net.IP, n uint32) {
	v := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
	v += n
	ip[0] = byte(v >> 24)
	ip[1] = byte(v >> 16)
	ip[2] = byte(v >> 8)
	ip[3] = byte(v)
}
