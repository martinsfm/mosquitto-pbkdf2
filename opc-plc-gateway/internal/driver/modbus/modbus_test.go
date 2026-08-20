package modbus

import (
	"math"
	"testing"
)

func TestParseAddress(t *testing.T) {
	cases := []struct {
		in      string
		typ     string
		want    address
		wantErr bool
	}{
		{"HR:100", "", address{table: tableHolding, reg: 100, dtype: typeUint16}, false},
		{"HR:100", "float32", address{table: tableHolding, reg: 100, dtype: typeFloat32}, false},
		{"IR:40", "int16", address{table: tableInput, reg: 40, dtype: typeInt16}, false},
		{"COIL:5", "", address{table: tableCoil, reg: 5}, false},
		{"DI:5", "", address{table: tableDiscreteInput, reg: 5}, false},
		{"HR:1", "bogus", address{}, true},
		{"XX:1", "", address{}, true},
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

func TestDecode(t *testing.T) {
	if got := decode(typeUint16, []byte{0x01, 0x02}); got != uint16(0x0102) {
		t.Errorf("uint16 decode = %v", got)
	}
	if got := decode(typeInt16, []byte{0xFF, 0xFF}); got != int16(-1) {
		t.Errorf("int16 decode = %v", got)
	}
	raw := make([]byte, 4)
	bits := math.Float32bits(3.14)
	raw[0], raw[1], raw[2], raw[3] = byte(bits>>24), byte(bits>>16), byte(bits>>8), byte(bits)
	if got := decode(typeFloat32, raw); got.(float32) != float32(3.14) {
		t.Errorf("float32 decode = %v", got)
	}
}

func TestRegCount(t *testing.T) {
	if (address{dtype: typeFloat32}).regCount() != 2 {
		t.Error("float32 should span 2 registers")
	}
	if (address{dtype: typeUint16}).regCount() != 1 {
		t.Error("uint16 should span 1 register")
	}
}
