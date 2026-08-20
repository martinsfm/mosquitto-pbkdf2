package siemens

import "testing"

func TestParseAddress(t *testing.T) {
	cases := []struct {
		in      string
		want    address
		wantErr bool
	}{
		{"DB10,REAL0", address{area: areaDB, db: 10, dtype: typeReal, byteOffset: 0}, false},
		{"DB10,INT4", address{area: areaDB, db: 10, dtype: typeInt, byteOffset: 4}, false},
		{"DB10,DINT8", address{area: areaDB, db: 10, dtype: typeDInt, byteOffset: 8}, false},
		{"DB10,X8.3", address{area: areaDB, db: 10, dtype: typeBit, byteOffset: 8, bitOffset: 3}, false},
		{"DB10,BOOL8.3", address{area: areaDB, db: 10, dtype: typeBit, byteOffset: 8, bitOffset: 3}, false},
		{"M,BYTE0", address{area: areaMerker, dtype: typeByte, byteOffset: 0}, false},
		{"DB10,FLOAT0", address{}, true},
		{"garbage", address{}, true},
	}
	for _, c := range cases {
		got, err := parseAddress(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseAddress(%q): expected error, got %+v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseAddress(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseAddress(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestByteSize(t *testing.T) {
	if (address{dtype: typeReal}).byteSize() != 4 {
		t.Error("REAL should be 4 bytes")
	}
	if (address{dtype: typeInt}).byteSize() != 2 {
		t.Error("INT should be 2 bytes")
	}
	if (address{dtype: typeBit}).byteSize() != 1 {
		t.Error("bit should read 1 byte")
	}
}
