package discover

import (
	"net"
	"testing"
)

func TestExpandHostsSlash24(t *testing.T) {
	_, ipnet, _ := net.ParseCIDR("192.168.1.10/24")
	hosts := expandHosts([]*net.IPNet{ipnet})
	if len(hosts) != 254 {
		t.Fatalf("len(hosts) = %d, want 254 (a /24 minus network+broadcast)", len(hosts))
	}
	if hosts[0] != "192.168.1.1" {
		t.Errorf("hosts[0] = %q, want 192.168.1.1", hosts[0])
	}
	if hosts[len(hosts)-1] != "192.168.1.254" {
		t.Errorf("last host = %q, want 192.168.1.254", hosts[len(hosts)-1])
	}
}

func TestExpandHostsCapsLargeNetwork(t *testing.T) {
	_, ipnet, _ := net.ParseCIDR("10.0.0.1/22") // 1022 usable hosts
	hosts := expandHosts([]*net.IPNet{ipnet})
	if len(hosts) > maxHostsPerNetwork {
		t.Errorf("len(hosts) = %d, exceeds cap %d", len(hosts), maxHostsPerNetwork)
	}
}

func TestAddUint32(t *testing.T) {
	ip := net.IPv4(192, 168, 1, 0).To4()
	addUint32(ip, 5)
	if ip.String() != "192.168.1.5" {
		t.Errorf("addUint32 = %s, want 192.168.1.5", ip.String())
	}

	ip2 := net.IPv4(192, 168, 1, 250).To4()
	addUint32(ip2, 10)
	if ip2.String() != "192.168.2.4" {
		t.Errorf("addUint32 carry = %s, want 192.168.2.4", ip2.String())
	}
}
