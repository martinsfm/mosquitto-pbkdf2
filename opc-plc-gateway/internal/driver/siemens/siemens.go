// Package siemens talks the native S7 protocol (ISO-on-TCP, port 102) to
// Siemens S7-300/400/1200/1500 controllers using the pure-Go gos7 library -
// no vendor software (Step7, TIA Portal runtime) required on the machine
// running the gateway.
//
// Tag address in config uses classic S7 addressing:
//
//	"DB10,REAL0"   -> data block 10, REAL (float32) at byte offset 0
//	"DB10,INT4"    -> data block 10, INT (int16) at byte offset 4
//	"DB10,DINT8"   -> data block 10, DINT (int32) at byte offset 8
//	"DB10,BYTE2"   -> data block 10, BYTE (uint8) at byte offset 2
//	"DB10,WORD2"   -> data block 10, WORD (uint16) at byte offset 2
//	"DB10,DWORD2"  -> data block 10, DWORD (uint32) at byte offset 2
//	"DB10,X0.3"    -> data block 10, single bit: byte 0, bit 3 (bool)
//	"M,BYTE0"      -> Merker (bit memory) byte 0; same type syntax as DB, no DB number
//
// S7-1200/1500 require "optimized block access" turned OFF for the DBs you
// want to poll (Block properties -> uncheck "Optimized block access" in
// TIA Portal), otherwise byte offsets are not stable and reads will return
// wrong data.
package siemens

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/robinson/gos7"

	"opc-plc-gateway/internal/config"
	"opc-plc-gateway/internal/driver"
)

func init() {
	driver.Register("siemens", New)
}

type Driver struct {
	cfg     config.DeviceConfig
	handler *gos7.TCPClientHandler
	client  gos7.Client
	helper  gos7.Helper
	addrs   map[string]address // tag name -> parsed address, computed once
}

func New(cfg config.DeviceConfig) (driver.Driver, error) {
	addrs := make(map[string]address, len(cfg.Tags))
	for _, t := range cfg.Tags {
		a, err := parseAddress(t.Address)
		if err != nil {
			return nil, fmt.Errorf("siemens %s: tag %q: %w", cfg.Name, t.Name, err)
		}
		addrs[t.Name] = a
	}
	return &Driver{cfg: cfg, addrs: addrs}, nil
}

func (d *Driver) Connect(ctx context.Context) error {
	if d.handler != nil {
		d.handler.Close()
	}
	h := gos7.NewTCPClientHandler(d.cfg.Address, d.cfg.Rack, d.cfg.Slot)
	h.Timeout = d.cfg.Timeout()
	h.IdleTimeout = 60 * time.Second
	if err := h.Connect(); err != nil {
		return fmt.Errorf("siemens %s: connect %s (rack %d slot %d): %w", d.cfg.Name, d.cfg.Address, d.cfg.Rack, d.cfg.Slot, err)
	}
	d.handler = h
	d.client = gos7.NewClient(h)
	return nil
}

func (d *Driver) Poll(ctx context.Context) (map[string]interface{}, error) {
	if d.client == nil {
		if err := d.Connect(ctx); err != nil {
			return nil, err
		}
	}

	results := make(map[string]interface{}, len(d.cfg.Tags))
	var firstErr error
	for _, tag := range d.cfg.Tags {
		a := d.addrs[tag.Name]
		val, err := d.readOne(a)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("siemens %s: read %s (%s): %w", d.cfg.Name, tag.Name, tag.Address, err)
			}
			continue
		}
		results[tag.Name] = val
	}
	if len(results) == 0 && len(d.cfg.Tags) > 0 {
		// nothing readable at all: treat as a connection failure so the
		// gateway reconnects on the next poll cycle.
		d.client = nil
		return results, firstErr
	}
	return results, nil
}

func (d *Driver) readOne(a address) (interface{}, error) {
	size := a.byteSize()
	buf := make([]byte, size)

	var err error
	switch a.area {
	case areaDB:
		err = d.client.AGReadDB(a.db, a.byteOffset, size, buf)
	case areaMerker:
		err = d.client.AGReadMB(a.byteOffset, size, buf)
	}
	if err != nil {
		return nil, err
	}

	switch a.dtype {
	case typeReal:
		return d.helper.GetRealAt(buf, 0), nil
	case typeInt:
		var v int16
		d.helper.GetValueAt(buf, 0, &v)
		return v, nil
	case typeDInt:
		var v int32
		d.helper.GetValueAt(buf, 0, &v)
		return v, nil
	case typeWord:
		var v uint16
		d.helper.GetValueAt(buf, 0, &v)
		return v, nil
	case typeDWord:
		var v uint32
		d.helper.GetValueAt(buf, 0, &v)
		return v, nil
	case typeByte:
		return buf[0], nil
	case typeBit:
		return (buf[0]>>uint(a.bitOffset))&0x01 == 1, nil
	default:
		return nil, fmt.Errorf("unsupported s7 type %q", a.dtype)
	}
}

// WriteTag writes value to the tag named by tagName. value must already be
// the Go type the tag's declared S7 type expects (int16 for INT, int32
// for DINT, float32 for REAL, uint16/uint32 for WORD/DWORD, byte for
// BYTE, bool for X.n) - the caller (internal/manager) is responsible for
// coercing whatever came in over the dashboard/API to match.
//
// Writing a single bit (X.n) is a read-modify-write: S7 has no "write one
// bit" primitive, so this reads the containing byte, flips the bit, and
// writes the byte back - a write to another bit in that same byte
// happening concurrently on the PLC from ladder logic could in principle
// race with this, same as it would with any other S7 tool.
func (d *Driver) WriteTag(ctx context.Context, tagName string, value interface{}) error {
	if d.client == nil {
		if err := d.Connect(ctx); err != nil {
			return err
		}
	}
	a, ok := d.addrs[tagName]
	if !ok {
		return fmt.Errorf("tag %q não está configurada neste dispositivo", tagName)
	}
	if err := d.writeOne(a, value); err != nil {
		return fmt.Errorf("siemens %s: write %s (%s): %w", d.cfg.Name, tagName, tagName, err)
	}
	return nil
}

func (d *Driver) writeOne(a address, value interface{}) error {
	buf := make([]byte, a.byteSize())

	if a.dtype == typeBit {
		if err := d.read(a, buf); err != nil {
			return err
		}
		b, ok := value.(bool)
		if !ok {
			return fmt.Errorf("valor %v não é booleano", value)
		}
		buf[0] = d.helper.SetBoolAt(buf[0], uint(a.bitOffset), b)
	} else if a.dtype == typeReal {
		f, ok := value.(float32)
		if !ok {
			return fmt.Errorf("valor %v não é float32", value)
		}
		d.helper.SetRealAt(buf, 0, f)
	} else {
		d.helper.SetValueAt(buf, 0, value)
	}

	switch a.area {
	case areaDB:
		return d.client.AGWriteDB(a.db, a.byteOffset, len(buf), buf)
	case areaMerker:
		return d.client.AGWriteMB(a.byteOffset, len(buf), buf)
	}
	return fmt.Errorf("unsupported s7 area")
}

func (d *Driver) read(a address, buf []byte) error {
	switch a.area {
	case areaDB:
		return d.client.AGReadDB(a.db, a.byteOffset, len(buf), buf)
	case areaMerker:
		return d.client.AGReadMB(a.byteOffset, len(buf), buf)
	}
	return fmt.Errorf("unsupported s7 area")
}

func (d *Driver) Close() error {
	if d.handler == nil {
		return nil
	}
	d.handler.Close()
	return nil
}

// --- address parsing ---------------------------------------------------

type area int

const (
	areaDB area = iota
	areaMerker
)

const (
	typeReal  = "REAL"
	typeInt   = "INT"
	typeDInt  = "DINT"
	typeWord  = "WORD"
	typeDWord = "DWORD"
	typeByte  = "BYTE"
	typeBit   = "X"
)

type address struct {
	area       area
	db         int
	dtype      string
	byteOffset int
	bitOffset  int
}

func (a address) byteSize() int {
	switch a.dtype {
	case typeReal, typeDInt, typeDWord:
		return 4
	case typeInt, typeWord:
		return 2
	case typeByte, typeBit:
		return 1
	}
	return 1
}

var (
	dbPattern = regexp.MustCompile(`^DB(\d+),([A-Za-z]+)(\d+)(?:\.(\d+))?$`)
	mPattern  = regexp.MustCompile(`^M,([A-Za-z]+)(\d+)(?:\.(\d+))?$`)
)

func parseAddress(s string) (address, error) {
	if m := dbPattern.FindStringSubmatch(s); m != nil {
		db, _ := strconv.Atoi(m[1])
		off, _ := strconv.Atoi(m[3])
		a := address{area: areaDB, db: db, dtype: normalizeType(m[2]), byteOffset: off}
		if m[4] != "" {
			bit, _ := strconv.Atoi(m[4])
			a.bitOffset = bit
		}
		return a, validate(a, s)
	}
	if m := mPattern.FindStringSubmatch(s); m != nil {
		off, _ := strconv.Atoi(m[2])
		a := address{area: areaMerker, dtype: normalizeType(m[1]), byteOffset: off}
		if m[3] != "" {
			bit, _ := strconv.Atoi(m[3])
			a.bitOffset = bit
		}
		return a, validate(a, s)
	}
	return address{}, fmt.Errorf("invalid siemens address %q, expected e.g. \"DB10,REAL0\" or \"M,BYTE0\"", s)
}

func normalizeType(t string) string {
	if t == "BOOL" {
		return typeBit
	}
	return t
}

func validate(a address, raw string) error {
	switch a.dtype {
	case typeReal, typeInt, typeDInt, typeWord, typeDWord, typeByte, typeBit:
		return nil
	default:
		return fmt.Errorf("invalid siemens address %q: unknown type %q (use REAL/INT/DINT/WORD/DWORD/BYTE/X)", raw, a.dtype)
	}
}
