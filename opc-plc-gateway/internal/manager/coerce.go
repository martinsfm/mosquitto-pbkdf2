package manager

import "fmt"

// coerceValue converts value - as it arrives over JSON, so always float64,
// bool, or string - into the concrete Go type a driver's WriteTag call
// expects for this tag. typeHint (the tag's configured "type" field, e.g.
// "int16"/"float32"/"bool") wins when set; otherwise the Go type of
// existing (the tag's last successfully read value, if any) is used as
// the best available guess - by the time someone writes to a tag from the
// dashboard, it has almost always already been read at least once.
//
// Returns an error if neither a hint nor a previous read is available (the
// gateway genuinely doesn't know what type to send yet), or if the
// incoming JSON value can't be represented as the target type (e.g. text
// where a number is expected).
func coerceValue(value interface{}, typeHint string, existing interface{}) (interface{}, error) {
	target := typeHint
	if target == "" {
		target = goTypeName(existing)
	}
	if target == "" {
		return nil, fmt.Errorf("tipo desta tag ainda é desconhecido (nunca foi lida com sucesso) - defina \"type\" na tag ou espere uma leitura boa antes de escrever")
	}

	switch target {
	case "bool":
		b, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("valor precisa ser true/false pra esta tag")
		}
		return b, nil
	case "string":
		s, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("valor precisa ser texto pra esta tag")
		}
		return s, nil
	}

	f, ok := numberValue(value)
	if !ok {
		return nil, fmt.Errorf("valor precisa ser numérico pra esta tag")
	}
	switch target {
	case "int8":
		return int8(f), nil
	case "uint8", "byte":
		return uint8(f), nil
	case "int16":
		return int16(f), nil
	case "uint16":
		return uint16(f), nil
	case "int32", "dint":
		return int32(f), nil
	case "uint32":
		return uint32(f), nil
	case "int64":
		return int64(f), nil
	case "uint64":
		return uint64(f), nil
	case "int":
		return int(f), nil
	case "float32", "real":
		return float32(f), nil
	case "float32_swapped":
		return float32(f), nil
	case "float64":
		return f, nil
	}
	return nil, fmt.Errorf("tipo de tag %q não suportado pra escrita", target)
}

// numberValue extracts a float64 out of whatever concrete numeric type v
// holds - JSON numbers always decode to float64, but existing tag values
// (used to infer the target type) hold whatever Go type the driver that
// read them last produced.
func numberValue(v interface{}) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int8:
		return float64(t), true
	case int16:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint8:
		return float64(t), true
	case uint16:
		return float64(t), true
	case uint32:
		return float64(t), true
	case uint64:
		return float64(t), true
	}
	return 0, false
}

// goTypeName names the coerceValue target that reproduces v's own Go
// type, so a tag with no explicit "type" configured can still be written
// by matching whatever type its last successful read produced.
func goTypeName(v interface{}) string {
	switch v.(type) {
	case bool:
		return "bool"
	case string:
		return "string"
	case int8:
		return "int8"
	case uint8:
		return "uint8"
	case int16:
		return "int16"
	case uint16:
		return "uint16"
	case int32:
		return "int32"
	case uint32:
		return "uint32"
	case int64:
		return "int64"
	case uint64:
		return "uint64"
	case int:
		return "int"
	case float32:
		return "float32"
	case float64:
		return "float64"
	}
	return ""
}
