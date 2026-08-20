package tagstore

import (
	"testing"
	"time"
)

func TestSetGet(t *testing.T) {
	s := New()
	key := Key("dev1", "tag1")
	s.Set(key, Value{Value: int32(42), Quality: QualityGood, Timestamp: time.Now()})

	got, ok := s.Get(key)
	if !ok {
		t.Fatal("expected value to be present")
	}
	if got.Value != int32(42) {
		t.Errorf("Value = %v, want 42", got.Value)
	}
}

func TestSubscribeNotifiesOnChange(t *testing.T) {
	s := New()
	ch := s.Subscribe()
	key := Key("dev1", "tag1")

	s.Set(key, Value{Value: int32(1)})
	select {
	case got := <-ch:
		if got != key {
			t.Errorf("notified key = %q, want %q", got, key)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for change notification")
	}
}

func TestMarkStale(t *testing.T) {
	s := New()
	device := "dev1"
	s.Set(Key(device, "t1"), Value{Value: int32(1), Quality: QualityGood})
	s.MarkStale(device, []string{"t1"}, nil)

	got, ok := s.Get(Key(device, "t1"))
	if !ok {
		t.Fatal("expected value present")
	}
	if got.Quality != QualityBad {
		t.Errorf("Quality = %v, want QualityBad", got.Quality)
	}
}
