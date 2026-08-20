package manager

import "testing"

func TestCoerceValueWithTypeHint(t *testing.T) {
	v, err := coerceValue(float64(42), "int16", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, ok := v.(int16); !ok || got != 42 {
		t.Errorf("got %#v, want int16(42)", v)
	}

	v, err = coerceValue(true, "bool", nil)
	if err != nil || v != true {
		t.Errorf("bool passthrough failed: %v, %v", v, err)
	}
}

func TestCoerceValueFallsBackToExistingType(t *testing.T) {
	v, err := coerceValue(float64(3), "", float32(0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, ok := v.(float32); !ok || got != 3 {
		t.Errorf("got %#v, want float32(3)", v)
	}
}

func TestCoerceValueNoTypeInfoErrors(t *testing.T) {
	if _, err := coerceValue(float64(1), "", nil); err == nil {
		t.Error("expected error when neither type hint nor existing value is available")
	}
}

func TestCoerceValueTypeMismatchErrors(t *testing.T) {
	if _, err := coerceValue("not a number", "int16", nil); err == nil {
		t.Error("expected error writing a string into a numeric tag")
	}
	if _, err := coerceValue(float64(1), "bool", nil); err == nil {
		t.Error("expected error writing a number into a bool tag")
	}
}
