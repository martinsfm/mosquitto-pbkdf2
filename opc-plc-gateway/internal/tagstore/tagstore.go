// Package tagstore holds the last known value of every tag read from every
// PLC, keyed by "<device>.<tag>". It is the single point of contact between
// the southbound drivers (Rockwell, Siemens, Modbus, ...) writing values in,
// and the northbound OPC UA server reading values out.
package tagstore

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Quality mirrors the OPC notion of data quality for a point.
type Quality int

const (
	QualityGood Quality = iota
	QualityBad
	QualityStale
)

// Value is one sampled tag value with its quality and timestamp.
type Value struct {
	Value     interface{}
	Quality   Quality
	Timestamp time.Time
	Err       string // last read error, if Quality == QualityBad
}

// Key builds the "<device>.<tag>" key used throughout the store.
func Key(device, tag string) string {
	return device + "." + tag
}

// SplitKey reverses Key. Device names are not expected to contain ".", so
// splitting on the first occurrence recovers (device, tag) exactly.
func SplitKey(key string) (device, tag string) {
	device, tag, _ = strings.Cut(key, ".")
	return device, tag
}

// Store is a concurrency-safe map of tag key -> latest Value, with an
// optional change-notification channel so subscribers (e.g. the OPC UA
// server) can react without polling.
type Store struct {
	mu     sync.RWMutex
	values map[string]Value
	subs   []chan string
	subsMu sync.Mutex
}

func New() *Store {
	return &Store{values: make(map[string]Value)}
}

// Set stores a new value for key and notifies subscribers if it changed.
func (s *Store) Set(key string, v Value) {
	s.mu.Lock()
	prev, existed := s.values[key]
	s.values[key] = v
	s.mu.Unlock()

	if !existed || prev.Value != v.Value || prev.Quality != v.Quality {
		s.notify(key)
	}
}

// Get returns the last known value for key.
func (s *Store) Get(key string) (Value, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.values[key]
	return v, ok
}

// Keys returns every key currently in the store.
func (s *Store) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.values))
	for k := range s.values {
		keys = append(keys, k)
	}
	return keys
}

// Subscribe returns a channel that receives the key of every value that
// changes from now on. Callers must keep draining it.
func (s *Store) Subscribe() <-chan string {
	ch := make(chan string, 256)
	s.subsMu.Lock()
	s.subs = append(s.subs, ch)
	s.subsMu.Unlock()
	return ch
}

func (s *Store) notify(key string) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for _, ch := range s.subs {
		select {
		case ch <- key:
		default:
			// subscriber too slow, drop the notification rather than block the poller
		}
	}
}

// MarkStale flags every tag belonging to device as stale, used when a
// device's poll cycle fails (disconnected PLC, timeout, ...).
func (s *Store) MarkStale(device string, tags []string, reason error) {
	now := time.Now()
	for _, t := range tags {
		key := Key(device, t)
		s.mu.RLock()
		prev := s.values[key]
		s.mu.RUnlock()
		prev.Quality = QualityBad
		prev.Timestamp = now
		if reason != nil {
			prev.Err = fmt.Sprintf("%v", reason)
		}
		s.Set(key, prev)
	}
}
