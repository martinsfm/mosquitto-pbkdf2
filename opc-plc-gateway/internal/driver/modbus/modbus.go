// Package modbus talks Modbus TCP, which is the closest thing the
// automation world has to a universal protocol: most non-Rockwell,
// non-Siemens PLCs and PACs (Schneider Electric, ABB, Omron, Delta, WEG,
// most VFDs and remote I/O) expose it either natively or via a gateway
// module, which makes this driver the gateway's "any other big brand"
// fallback described in the README.
//
// Tag address in config selects the Modbus table and register:
//
//	"HR:100"   -> holding register 100 (function code 3)
//	"IR:100"   -> input register 100   (function code 4)
//	"COIL:5"   -> coil 5               (function code 1, bool)
//	"DI:5"     -> discrete input 5     (function code 2, bool)
//
// The Type field selects how HR/IR register bytes are decoded (default
// "uint16" if omitted): uint16, int16, uint32, int32, float32,
// float32_swapped (word-swapped float32, common on Schneider/ABB PLCs).
// uint32/int32/float32 span two consecutive registers starting at the
// given address.
package modbus

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/goburrow/modbus"

	"opc-plc-gateway/internal/config"
	"opc-plc-gateway/internal/driver"
)

func init() {
	driver.Register("modbus", New)
}

type Driver struct {
	cfg     config.DeviceConfig
	handler *modbus.TCPClientHandler
	client  modbus.Client
	addrs   map[string]address
}

func New(cfg config.DeviceConfig) (driver.Driver, error) {
	addrs := make(map[string]address, len(cfg.Tags))
	for _, t := range cfg.Tags {
		a, err := parseAddress(t.Address, t.Type)
		if err != nil {
			return nil, fmt.Errorf("modbus %s: tag %q: %w", cfg.Name, t.Name, err)
		}
		addrs[t.Name] = a
	}
	return &Driver{cfg: cfg, addrs: addrs}, nil
}

func (d *Driver) Connect(ctx context.Context) error {
	if d.handler != nil {
		d.handler.Close()
	}
	h := modbus.NewTCPClientHandler(d.cfg.Address)
	h.Timeout = d.cfg.Timeout()
	if d.cfg.UnitID > 0 {
		h.SlaveId = byte(d.cfg.UnitID)
	} else {
		h.SlaveId = 1
	}
	if err := h.Connect(); err != nil {
		return fmt.Errorf("modbus %s: connect %s: %w", d.cfg.Name, d.cfg.Address, err)
	}
	d.handler = h
	d.client = modbus.NewClient(h)
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
				firstErr = fmt.Errorf("modbus %s: read %s (%s): %w", d.cfg.Name, tag.Name, tag.Address, err)
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
	switch a.table {
	case tableCoil:
		bits, err := d.client.ReadCoils(a.reg, 1)
		if err != nil {
			return nil, err
		}
		return bits[0]&0x01 == 1, nil
	case tableDiscreteInput:
		bits, err := d.client.ReadDiscreteInputs(a.reg, 1)
		if err != nil {
			return nil, err
		}
		return bits[0]&0x01 == 1, nil
	case tableHolding, tableInput:
		qty := uint16(1)
		if a.regCount() == 2 {
			qty = 2
		}
		var raw []byte
		var err error
		if a.table == tableHolding {
			raw, err = d.client.ReadHoldingRegisters(a.reg, qty)
		} else {
			raw, err = d.client.ReadInputRegisters(a.reg, qty)
		}
		if err != nil {
			return nil, err
		}
		return decode(a.dtype, raw), nil
	}
	return nil, fmt.Errorf("unsupported modbus table")
}

func decode(dtype string, raw []byte) interface{} {
	switch dtype {
	case typeUint16:
		return binary.BigEndian.Uint16(raw)
	case typeInt16:
		return int16(binary.BigEndian.Uint16(raw))
	case typeUint32:
		return binary.BigEndian.Uint32(raw)
	case typeInt32:
		return int32(binary.BigEndian.Uint32(raw))
	case typeFloat32:
		return math.Float32frombits(binary.BigEndian.Uint32(raw))
	case typeFloat32Swapped:
		swapped := []byte{raw[2], raw[3], raw[0], raw[1]}
		return math.Float32frombits(binary.BigEndian.Uint32(swapped))
	}
	return binary.BigEndian.Uint16(raw)
}

// WriteTag writes value to the tag named by tagName. Only holding
// registers (HR) and coils (COIL) are writable per the Modbus spec -
// input registers (IR) and discrete inputs (DI) are read-only on the
// device side, so WriteTag returns an error for those. value must
// already be the Go type the tag's Type expects (bool for COIL;
// uint16/int16/uint32/int32/float32/float32_swapped for HR) - the caller
// (internal/manager) is responsible for coercing whatever came in over
// the dashboard/API to match.
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
		return fmt.Errorf("modbus %s: write %s: %w", d.cfg.Name, tagName, err)
	}
	return nil
}

func (d *Driver) writeOne(a address, value interface{}) error {
	switch a.table {
	case tableCoil:
		b, ok := value.(bool)
		if !ok {
			return fmt.Errorf("valor %v não é booleano", value)
		}
		v := uint16(0x0000)
		if b {
			v = 0xFF00
		}
		_, err := d.client.WriteSingleCoil(a.reg, v)
		return err
	case tableHolding:
		buf, err := encode(a.dtype, value)
		if err != nil {
			return err
		}
		if len(buf) == 2 {
			_, err = d.client.WriteSingleRegister(a.reg, binary.BigEndian.Uint16(buf))
		} else {
			_, err = d.client.WriteMultipleRegisters(a.reg, uint16(len(buf)/2), buf)
		}
		return err
	default:
		return fmt.Errorf("registrador somente leitura (input register / discrete input)")
	}
}

func encode(dtype string, value interface{}) ([]byte, error) {
	switch dtype {
	case typeUint16:
		v, ok := value.(uint16)
		if !ok {
			return nil, fmt.Errorf("valor %v não é uint16", value)
		}
		buf := make([]byte, 2)
		binary.BigEndian.PutUint16(buf, v)
		return buf, nil
	case typeInt16:
		v, ok := value.(int16)
		if !ok {
			return nil, fmt.Errorf("valor %v não é int16", value)
		}
		buf := make([]byte, 2)
		binary.BigEndian.PutUint16(buf, uint16(v))
		return buf, nil
	case typeUint32:
		v, ok := value.(uint32)
		if !ok {
			return nil, fmt.Errorf("valor %v não é uint32", value)
		}
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, v)
		return buf, nil
	case typeInt32:
		v, ok := value.(int32)
		if !ok {
			return nil, fmt.Errorf("valor %v não é int32", value)
		}
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, uint32(v))
		return buf, nil
	case typeFloat32:
		v, ok := value.(float32)
		if !ok {
			return nil, fmt.Errorf("valor %v não é float32", value)
		}
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, math.Float32bits(v))
		return buf, nil
	case typeFloat32Swapped:
		v, ok := value.(float32)
		if !ok {
			return nil, fmt.Errorf("valor %v não é float32", value)
		}
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, math.Float32bits(v))
		return []byte{buf[2], buf[3], buf[0], buf[1]}, nil
	}
	return nil, fmt.Errorf("tipo %q desconhecido", dtype)
}

func (d *Driver) Close() error {
	if d.handler == nil {
		return nil
	}
	return d.handler.Close()
}

// --- address parsing ---------------------------------------------------

type table int

const (
	tableHolding table = iota
	tableInput
	tableCoil
	tableDiscreteInput
)

const (
	typeUint16         = "uint16"
	typeInt16          = "int16"
	typeUint32         = "uint32"
	typeInt32          = "int32"
	typeFloat32        = "float32"
	typeFloat32Swapped = "float32_swapped"
)

type address struct {
	table table
	reg   uint16
	dtype string
}

func (a address) regCount() int {
	switch a.dtype {
	case typeUint32, typeInt32, typeFloat32, typeFloat32Swapped:
		return 2
	}
	return 1
}

var addrPattern = regexp.MustCompile(`^(HR|IR|COIL|DI):(\d+)$`)

func parseAddress(s, dtype string) (address, error) {
	m := addrPattern.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(s)))
	if m == nil {
		return address{}, fmt.Errorf("invalid modbus address %q, expected e.g. \"HR:100\", \"IR:40\", \"COIL:5\", \"DI:5\"", s)
	}
	reg, _ := strconv.Atoi(m[2])
	a := address{reg: uint16(reg)}
	switch m[1] {
	case "HR":
		a.table = tableHolding
	case "IR":
		a.table = tableInput
	case "COIL":
		a.table = tableCoil
	case "DI":
		a.table = tableDiscreteInput
	}
	if a.table == tableHolding || a.table == tableInput {
		if dtype == "" {
			dtype = typeUint16
		}
		switch dtype {
		case typeUint16, typeInt16, typeUint32, typeInt32, typeFloat32, typeFloat32Swapped:
			a.dtype = dtype
		default:
			return address{}, fmt.Errorf("invalid modbus type %q (use uint16/int16/uint32/int32/float32/float32_swapped)", dtype)
		}
	}
	return a, nil
}
