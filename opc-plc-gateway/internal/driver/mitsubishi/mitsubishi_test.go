package mitsubishi

import "testing"

func TestParseAddress(t *testing.T) {
	cases := []struct {
		in      string
		typ     string
		want    address
		wantErr bool
	}{
		{"D100", "", address{device: "D", offset: 100, dtype: typeInt16}, false},
		{"D100", "float32", address{device: "D", offset: 100, dtype: typeFloat32}, false},
		{"W26", "uint16", address{device: "W", offset: 26, dtype: typeUint16}, false},
		{"M20", "", address{device: "M", offset: 20, isBit: true}, false},
		{"X10", "", address{device: "X", offset: 10, isBit: true}, false},
		{"Y10", "", address{device: "Y", offset: 10, isBit: true}, false},
		{"Z10", "", address{}, true}, // unsupported device code
		{"D100", "bogus", address{}, true},
		{"garbage", "", address{}, true},
	}
	for _, c := range cases {
		got, err := parseAddress(c.in, c.typ)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseAddress(%q,%q): expected error, got %+v", c.in, c.typ, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseAddress(%q,%q): unexpected error: %v", c.in, c.typ, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseAddress(%q,%q) = %+v, want %+v", c.in, c.typ, got, c.want)
		}
	}
}

func TestSplitHostPort(t *testing.T) {
	host, port, err := splitHostPort("192.168.1.50:5007")
	if err != nil || host != "192.168.1.50" || port != 5007 {
		t.Errorf("splitHostPort = %q, %d, %v", host, port, err)
	}
	if _, _, err := splitHostPort("192.168.1.50"); err == nil {
		t.Error("expected error for missing port")
	}
}
