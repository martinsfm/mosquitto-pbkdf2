// Package mitsubishi talks the MC protocol (MELSEC Communication Protocol,
// 3E binary frame) to Mitsubishi Electric controllers (Q/L/iQ-R series with
// a built-in or add-on Ethernet port) using the pure-Go go-mcprotocol
// library - no vendor software (GX Works, MX Component) required on the
// machine running the gateway.
//
// The PLC's Ethernet module must have an "MC protocol" (SLMP) communication
// port open (set in GX Works "Network parameters" / "External device
// configuration"), using the binary communication format, which is the
// common default.
//
// Tag address in config:
//
//	"D100"   -> word device D, offset 100 (16-bit by default)
//	"W1A"    -> word device W, offset 0x1A - offsets for W/X/Y are commonly
//	            given in hex in Mitsubishi documentation; this driver always
//	            parses the numeric part as decimal, so translate hex
//	            addresses from GX Works to decimal first.
//	"M100"   -> bit device M, offset 100 (bool)
//	"X20"    -> bit device X, offset 20 (bool)
//	"Y20"    -> bit device Y, offset 20 (bool)
//
// Supported device codes (matching the underlying library): D, W (word),
// X, Y, M, L, F, V, B (bit).
//
// The Type field selects how a word device's registers are decoded
// (default "int16"): int16, uint16, int32, float32. int32/float32 span two
// consecutive registers (D100 and D101) starting at the given address, in
// Mitsubishi's little-endian ("binary code") word order.
package mitsubishi

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/future-architect/go-mcprotocol/mcp"

	"opc-plc-gateway/internal/config"
	"opc-plc-gateway/internal/driver"
)

func init() {
	driver.Register("mitsubishi", New)
}

var wordDevices = map[string]bool{"D": true, "W": true}
var bitDevices = map[string]bool{"X": true, "Y": true, "M": true, "L": true, "F": true, "V": true, "B": true}

type Driver struct {
	cfg    config.DeviceConfig
	client mcp.Client
	addrs  map[string]address
}

func New(cfg config.DeviceConfig) (driver.Driver, error) {
	addrs := make(map[string]address, len(cfg.Tags))
	for _, t := range cfg.Tags {
		a, err := parseAddress(t.Address, t.Type)
		if err != nil {
			return nil, fmt.Errorf("mitsubishi %s: tag %q: %w", cfg.Name, t.Name, err)
		}
		addrs[t.Name] = a
	}
	return &Driver{cfg: cfg, addrs: addrs}, nil
}

func (d *Driver) Connect(ctx context.Context) error {
	host, port, err := splitHostPort(d.cfg.Address)
	if err != nil {
		return fmt.Errorf("mitsubishi %s: %w", d.cfg.Name, err)
	}
	client, err := mcp.New3EClient(host, port, mcp.NewLocalStation())
	if err != nil {
		return fmt.Errorf("mitsubishi %s: creating client for %s: %w", d.cfg.Name, d.cfg.Address, err)
	}
	if err := client.HealthCheck(); err != nil {
		return fmt.Errorf("mitsubishi %s: health check %s: %w", d.cfg.Name, d.cfg.Address, err)
	}
	d.client = client
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
				firstErr = fmt.Errorf("mitsubishi %s: read %s (%s): %w", d.cfg.Name, tag.Name, tag.Address, err)
			}
			continue
		}
		results[tag.Name] = val
	}
	if len(results) == 0 && len(d.cfg.Tags) > 0 {
		d.client = nil
		return results, firstErr
	}
	return results, nil
}

func (d *Driver) readOne(a address) (interface{}, error) {
	if a.isBit {
		raw, err := d.client.BitRead(a.device, a.offset, 1)
		if err != nil {
			return nil, err
		}
		return decodeBit(raw)
	}

	points := int64(1)
	if a.regCount() == 2 {
		points = 2
	}
	raw, err := d.client.Read(a.device, a.offset, points)
	if err != nil {
		return nil, err
	}
	resp, err := mcp.NewParser().Do(raw)
	if err != nil {
		return nil, err
	}
	if resp.EndCode != "0000" {
		return nil, fmt.Errorf("plc returned end code %s", resp.EndCode)
	}
	return decodeWord(a.dtype, resp.Payload)
}

func decodeBit(raw []byte) (interface{}, error) {
	resp, err := mcp.NewParser().Do(raw)
	if err != nil {
		return nil, err
	}
	if resp.EndCode != "0000" {
		return nil, fmt.Errorf("plc returned end code %s", resp.EndCode)
	}
	if len(resp.Payload) == 0 {
		return nil, fmt.Errorf("empty bit response")
	}
	// payload is one ASCII-ish nibble per point: 0/1 (or 16/17 packed, per
	// library docs) - the low bit of the first byte tells on/off for a
	// single-point read.
	return resp.Payload[0]&0x0F != 0, nil
}

func decodeWord(dtype string, payload []byte) (interface{}, error) {
	need := 2
	if dtype == typeInt32 || dtype == typeFloat32 {
		need = 4
	}
	if len(payload) < need {
		return nil, fmt.Errorf("short response: got %d bytes, need %d", len(payload), need)
	}
	switch dtype {
	case typeUint16:
		return binary.LittleEndian.Uint16(payload), nil
	case typeInt16:
		return int16(binary.LittleEndian.Uint16(payload)), nil
	case typeInt32:
		return int32(binary.LittleEndian.Uint32(payload)), nil
	case typeFloat32:
		return math.Float32frombits(binary.LittleEndian.Uint32(payload)), nil
	}
	return int16(binary.LittleEndian.Uint16(payload)), nil
}

// WriteTag writes value to the word device named by tagName (D or W only -
// the underlying go-mcprotocol library has no bit-write primitive, so
// M/X/Y/L/F/V/B tags can't be written; WriteTag returns an error for
// those). value must already be the Go type the tag's Type expects
// (int16/uint16/int32/float32) - the caller (internal/manager) is
// responsible for coercing whatever came in over the dashboard/API.
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
	if a.isBit {
		return fmt.Errorf("escrita em dispositivo de bit (%s) não é suportada por este driver ainda", a.device)
	}

	buf, err := encodeWord(a.dtype, value)
	if err != nil {
		return fmt.Errorf("mitsubishi %s: write %s: %w", d.cfg.Name, tagName, err)
	}
	points := int64(1)
	if a.regCount() == 2 {
		points = 2
	}
	if _, err := d.client.Write(a.device, a.offset, points, buf); err != nil {
		return fmt.Errorf("mitsubishi %s: write %s: %w", d.cfg.Name, tagName, err)
	}
	return nil
}

func encodeWord(dtype string, value interface{}) ([]byte, error) {
	switch dtype {
	case typeUint16:
		v, ok := value.(uint16)
		if !ok {
			return nil, fmt.Errorf("valor %v não é uint16", value)
		}
		buf := make([]byte, 2)
		binary.LittleEndian.PutUint16(buf, v)
		return buf, nil
	case typeInt32:
		v, ok := value.(int32)
		if !ok {
			return nil, fmt.Errorf("valor %v não é int32", value)
		}
		buf := make([]byte, 4)
		binary.LittleEndian.PutUint32(buf, uint32(v))
		return buf, nil
	case typeFloat32:
		v, ok := value.(float32)
		if !ok {
			return nil, fmt.Errorf("valor %v não é float32", value)
		}
		buf := make([]byte, 4)
		binary.LittleEndian.PutUint32(buf, math.Float32bits(v))
		return buf, nil
	default: // typeInt16
		v, ok := value.(int16)
		if !ok {
			return nil, fmt.Errorf("valor %v não é int16", value)
		}
		buf := make([]byte, 2)
		binary.LittleEndian.PutUint16(buf, uint16(v))
		return buf, nil
	}
}

func (d *Driver) Close() error {
	d.client = nil
	return nil
}

func splitHostPort(addr string) (string, int, error) {
	host, portStr, found := strings.Cut(addr, ":")
	if !found {
		return "", 0, fmt.Errorf("address %q must be host:port (MC protocol port configured in GX Works, commonly 5007/5000/1281)", addr)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port in address %q: %w", addr, err)
	}
	return host, port, nil
}

// --- address parsing ---------------------------------------------------

const (
	typeInt16   = "int16"
	typeUint16  = "uint16"
	typeInt32   = "int32"
	typeFloat32 = "float32"
)

type address struct {
	device string
	offset int64
	isBit  bool
	dtype  string
}

func (a address) regCount() int {
	if a.dtype == typeInt32 || a.dtype == typeFloat32 {
		return 2
	}
	return 1
}

var addrPattern = regexp.MustCompile(`^([A-Za-z]+)(\d+)$`)

func parseAddress(s, dtype string) (address, error) {
	m := addrPattern.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(s)))
	if m == nil {
		return address{}, fmt.Errorf("invalid mitsubishi address %q, expected e.g. \"D100\", \"M20\", \"X10\"", s)
	}
	device := m[1]
	offset, _ := strconv.ParseInt(m[2], 10, 64)

	if wordDevices[device] {
		if dtype == "" {
			dtype = typeInt16
		}
		switch dtype {
		case typeInt16, typeUint16, typeInt32, typeFloat32:
		default:
			return address{}, fmt.Errorf("invalid mitsubishi type %q for word device %q (use int16/uint16/int32/float32)", dtype, device)
		}
		return address{device: device, offset: offset, dtype: dtype}, nil
	}
	if bitDevices[device] {
		return address{device: device, offset: offset, isBit: true}, nil
	}
	return address{}, fmt.Errorf("invalid mitsubishi address %q: unknown device code %q (supported: D,W word; X,Y,M,L,F,V,B bit)", s, device)
}
