// Package manager owns the live, runtime-mutable state of the gateway: the
// set of devices currently being polled. It is the thing the web dashboard
// talks to when someone adds a PLC, adds a tag, or asks "is this device
// online right now" - unlike a static config-file-driven gateway, changes
// made through the dashboard take effect immediately (no restart) and are
// persisted back to the YAML config file so they survive one.
package manager

import (
	"context"
	"fmt"
	"sync"
	"time"

	"opc-plc-gateway/internal/config"
	"opc-plc-gateway/internal/driver"
	"opc-plc-gateway/internal/tagstore"
)

// TagStatus is one tag's config plus its live value, for the dashboard.
type TagStatus struct {
	Name      string      `json:"name"`
	Address   string      `json:"address"`
	Type      string      `json:"type,omitempty"`
	Value     interface{} `json:"value,omitempty"`
	Quality   string      `json:"quality"`
	Error     string      `json:"error,omitempty"`
	Timestamp time.Time   `json:"timestamp,omitempty"`
}

// DeviceStatus is one device's config plus its live connection state, for
// the dashboard's device list.
type DeviceStatus struct {
	Name      string    `json:"name"`
	Driver    string    `json:"driver"`
	Address   string    `json:"address"`
	Rack      int       `json:"rack,omitempty"`
	Slot      int       `json:"slot,omitempty"`
	UnitID    int       `json:"unit_id,omitempty"`
	PollMs    int       `json:"poll_interval_ms"`
	Connected bool      `json:"connected"`
	LastError string    `json:"last_error,omitempty"`
	LastPoll  time.Time `json:"last_poll,omitempty"`
	TagCount  int       `json:"tag_count"`
}

// runningDevice tracks one live poller: the driver instance, its cancel
// func, and the last-known connection state (read by the dashboard while
// the poll loop keeps writing it).
type runningDevice struct {
	cfg    config.DeviceConfig
	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.RWMutex
	connected bool
	lastErr   string
	lastPoll  time.Time
}

// Hooks lets the northbound side (the OPC UA server) react to devices and
// tags being added at runtime, so both interfaces (dashboard and OPC UA)
// stay in sync without the manager needing to know anything about OPC UA.
type Hooks struct {
	OnDeviceAdded func(config.DeviceConfig)
	OnTagAdded    func(deviceName string, tag config.TagConfig)
}

type Manager struct {
	configPath string
	store      *tagstore.Store
	hooks      Hooks

	ctx context.Context // set by StartAll, used to spawn devices added later

	mu      sync.Mutex // guards cfg and running together
	cfg     *config.Config
	running map[string]*runningDevice
}

func New(configPath string, cfg *config.Config, store *tagstore.Store, hooks Hooks) *Manager {
	return &Manager{
		configPath: configPath,
		cfg:        cfg,
		store:      store,
		hooks:      hooks,
		running:    map[string]*runningDevice{},
	}
}

// StartAll begins polling every device already in the config file. It
// stores ctx so devices added later (through AddDevice) can be started
// the same way; StartAll itself returns immediately, it does not block.
func (m *Manager) StartAll(ctx context.Context) {
	m.mu.Lock()
	m.ctx = ctx
	devices := append([]config.DeviceConfig(nil), m.cfg.Devices...)
	m.mu.Unlock()

	for _, d := range devices {
		m.startDevice(ctx, d)
	}
}

// startDevice launches (or relaunches, after an Add/RemoveTag restart) the
// poll goroutine for one device and registers it in m.running.
func (m *Manager) startDevice(ctx context.Context, cfg config.DeviceConfig) {
	devCtx, cancel := context.WithCancel(ctx)
	rd := &runningDevice{cfg: cfg, cancel: cancel, done: make(chan struct{})}

	m.mu.Lock()
	m.running[cfg.Name] = rd
	m.mu.Unlock()

	go m.pollLoop(devCtx, rd)
}

// pollLoop owns one Driver for as long as devCtx is alive: connects, polls
// on the configured interval, writes every value into the shared tag
// store, and records connection state on rd instead of ever bringing the
// whole gateway down - a broken PLC is this device's problem, not
// everyone else's.
func (m *Manager) pollLoop(ctx context.Context, rd *runningDevice) {
	defer close(rd.done)
	cfg := rd.cfg

	d, err := driver.New(cfg)
	if err != nil {
		rd.setError(err)
		return
	}
	defer d.Close()

	if err := d.Connect(ctx); err != nil {
		rd.setError(err)
	} else {
		rd.setConnected()
	}

	ticker := time.NewTicker(cfg.PollInterval())
	defer ticker.Stop()

	tagNames := make([]string, len(cfg.Tags))
	for i, t := range cfg.Tags {
		tagNames[i] = t.Name
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			values, err := d.Poll(ctx)
			now := time.Now()
			for name, val := range values {
				m.store.Set(tagstore.Key(cfg.Name, name), tagstore.Value{
					Value: val, Quality: tagstore.QualityGood, Timestamp: now,
				})
			}
			if err != nil {
				rd.setError(err)
				missing := make([]string, 0, len(tagNames))
				for _, n := range tagNames {
					if _, ok := values[n]; !ok {
						missing = append(missing, n)
					}
				}
				m.store.MarkStale(cfg.Name, missing, err)
			} else {
				rd.setConnected()
			}
		}
	}
}

func (rd *runningDevice) setConnected() {
	rd.mu.Lock()
	rd.connected = true
	rd.lastErr = ""
	rd.lastPoll = time.Now()
	rd.mu.Unlock()
}

func (rd *runningDevice) setError(err error) {
	rd.mu.Lock()
	rd.connected = false
	rd.lastErr = err.Error()
	rd.lastPoll = time.Now()
	rd.mu.Unlock()
}

func (rd *runningDevice) status() (bool, string, time.Time) {
	rd.mu.RLock()
	defer rd.mu.RUnlock()
	return rd.connected, rd.lastErr, rd.lastPoll
}

// stopDevice cancels a running poller and waits for its goroutine to
// actually exit, so callers can safely restart it (AddTag/RemoveTag) or
// remove it (RemoveDevice) without a stray old goroutine racing the new
// one over the same PLC connection.
func (m *Manager) stopDevice(name string) {
	m.mu.Lock()
	rd, ok := m.running[name]
	if ok {
		delete(m.running, name)
	}
	m.mu.Unlock()
	if ok {
		rd.cancel()
		<-rd.done
	}
}

// List returns a status snapshot of every configured device, for the
// dashboard's device list.
func (m *Manager) List() []DeviceStatus {
	m.mu.Lock()
	devices := append([]config.DeviceConfig(nil), m.cfg.Devices...)
	running := make(map[string]*runningDevice, len(m.running))
	for k, v := range m.running {
		running[k] = v
	}
	m.mu.Unlock()

	out := make([]DeviceStatus, 0, len(devices))
	for _, d := range devices {
		ds := DeviceStatus{
			Name: d.Name, Driver: d.Driver, Address: d.Address,
			Rack: d.Rack, Slot: d.Slot, UnitID: d.UnitID,
			PollMs: d.PollIntervalMs, TagCount: len(d.Tags),
		}
		if ds.PollMs == 0 {
			ds.PollMs = int(d.PollInterval() / time.Millisecond)
		}
		if rd, ok := running[d.Name]; ok {
			ds.Connected, ds.LastError, ds.LastPoll = rd.status()
		}
		out = append(out, ds)
	}
	return out
}

// Device returns one device's config, for edit forms and driver-specific
// UI hints.
func (m *Manager) Device(name string) (config.DeviceConfig, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.cfg.Devices {
		if d.Name == name {
			return d, true
		}
	}
	return config.DeviceConfig{}, false
}

// Tags returns every tag on a device with its live value from the store.
func (m *Manager) Tags(deviceName string) ([]TagStatus, bool) {
	dev, ok := m.Device(deviceName)
	if !ok {
		return nil, false
	}
	out := make([]TagStatus, 0, len(dev.Tags))
	for _, t := range dev.Tags {
		ts := TagStatus{Name: t.Name, Address: t.Address, Type: t.Type, Quality: "uncertain"}
		if v, ok := m.store.Get(tagstore.Key(deviceName, t.Name)); ok {
			ts.Value = v.Value
			ts.Timestamp = v.Timestamp
			ts.Error = v.Err
			switch v.Quality {
			case tagstore.QualityGood:
				ts.Quality = "good"
			case tagstore.QualityBad:
				ts.Quality = "bad"
			default:
				ts.Quality = "stale"
			}
		}
		out = append(out, ts)
	}
	return out, true
}

// AddDevice validates the device, proves it's actually reachable (same
// spirit as Kepware/RSLinx's "test channel" before you commit to it),
// then persists it and starts polling immediately - no restart needed.
func (m *Manager) AddDevice(ctx context.Context, dev config.DeviceConfig) error {
	if err := config.ValidateDevice(dev); err != nil {
		return err
	}
	if dev.PollIntervalMs == 0 {
		dev.PollIntervalMs = 1000
	}

	m.mu.Lock()
	for _, d := range m.cfg.Devices {
		if d.Name == dev.Name {
			m.mu.Unlock()
			return fmt.Errorf("já existe um dispositivo chamado %q", dev.Name)
		}
	}
	m.mu.Unlock()

	if err := testConnection(ctx, dev); err != nil {
		return fmt.Errorf("não consegui conectar em %s (%s): %w", dev.Address, dev.Driver, err)
	}

	m.mu.Lock()
	m.cfg.Devices = append(m.cfg.Devices, dev)
	err := config.Save(m.configPath, m.cfg)
	runCtx := m.ctx
	m.mu.Unlock()
	if err != nil {
		return err
	}

	if m.hooks.OnDeviceAdded != nil {
		m.hooks.OnDeviceAdded(dev)
	}
	if runCtx != nil {
		m.startDevice(runCtx, dev)
	}
	return nil
}

// RemoveDevice stops polling a device, drops it from the config, and
// marks its tags bad in the store (any OPC UA node already published for
// it will show bad quality rather than a frozen last-good value; the node
// itself is only actually removed on the next gateway restart - see
// CHANGELOG.md).
func (m *Manager) RemoveDevice(name string) error {
	dev, ok := m.Device(name)
	if !ok {
		return fmt.Errorf("dispositivo %q não encontrado", name)
	}
	m.stopDevice(name)

	tagNames := make([]string, len(dev.Tags))
	for i, t := range dev.Tags {
		tagNames[i] = t.Name
	}
	m.store.MarkStale(name, tagNames, fmt.Errorf("dispositivo removido"))

	m.mu.Lock()
	defer m.mu.Unlock()
	kept := make([]config.DeviceConfig, 0, len(m.cfg.Devices))
	for _, d := range m.cfg.Devices {
		if d.Name != name {
			kept = append(kept, d)
		}
	}
	m.cfg.Devices = kept
	return config.Save(m.configPath, m.cfg)
}

// AddTag adds a tag to an existing device, persists it, and restarts that
// device's poller so the new tag is read starting on the next tick - the
// PLC connection briefly drops and reconnects, nothing else is affected.
func (m *Manager) AddTag(deviceName string, tag config.TagConfig) error {
	if tag.Name == "" || tag.Address == "" {
		return fmt.Errorf("nome e endereço da tag são obrigatórios")
	}

	m.mu.Lock()
	idx := -1
	for i, d := range m.cfg.Devices {
		if d.Name == deviceName {
			idx = i
			break
		}
	}
	if idx == -1 {
		m.mu.Unlock()
		return fmt.Errorf("dispositivo %q não encontrado", deviceName)
	}
	for _, t := range m.cfg.Devices[idx].Tags {
		if t.Name == tag.Name {
			m.mu.Unlock()
			return fmt.Errorf("já existe uma tag chamada %q neste dispositivo", tag.Name)
		}
	}
	m.cfg.Devices[idx].Tags = append(m.cfg.Devices[idx].Tags, tag)
	updated := m.cfg.Devices[idx]
	err := config.Save(m.configPath, m.cfg)
	runCtx := m.ctx
	m.mu.Unlock()
	if err != nil {
		return err
	}

	if m.hooks.OnTagAdded != nil {
		m.hooks.OnTagAdded(deviceName, tag)
	}
	m.stopDevice(deviceName)
	if runCtx != nil {
		m.startDevice(runCtx, updated)
	}
	return nil
}

// RemoveTag drops a tag from a device, persists it, and restarts the
// device's poller. The tag's last value in the store is marked bad rather
// than deleted, so a client watching it sees the drop instead of a
// frozen value.
func (m *Manager) RemoveTag(deviceName, tagName string) error {
	m.mu.Lock()
	idx := -1
	for i, d := range m.cfg.Devices {
		if d.Name == deviceName {
			idx = i
			break
		}
	}
	if idx == -1 {
		m.mu.Unlock()
		return fmt.Errorf("dispositivo %q não encontrado", deviceName)
	}
	kept := make([]config.TagConfig, 0, len(m.cfg.Devices[idx].Tags))
	for _, t := range m.cfg.Devices[idx].Tags {
		if t.Name != tagName {
			kept = append(kept, t)
		}
	}
	m.cfg.Devices[idx].Tags = kept
	updated := m.cfg.Devices[idx]
	err := config.Save(m.configPath, m.cfg)
	runCtx := m.ctx
	m.mu.Unlock()
	if err != nil {
		return err
	}

	m.store.MarkStale(deviceName, []string{tagName}, fmt.Errorf("tag removida"))
	m.stopDevice(deviceName)
	if runCtx != nil {
		m.startDevice(runCtx, updated)
	}
	return nil
}

// TestConnection tries to connect to a device without saving or polling
// it - the dashboard's "Testar conexão" button on the add-device wizard.
func (m *Manager) TestConnection(ctx context.Context, dev config.DeviceConfig) error {
	if err := config.ValidateDevice(dev); err != nil {
		return err
	}
	return testConnection(ctx, dev)
}

func testConnection(ctx context.Context, dev config.DeviceConfig) error {
	d, err := driver.New(dev)
	if err != nil {
		return err
	}
	defer d.Close()

	timeout := dev.Timeout()
	cctx, cancel := context.WithTimeout(ctx, timeout+2*time.Second)
	defer cancel()
	return d.Connect(cctx)
}

// TestRead connects to a device, reads exactly one tag, and disconnects -
// the dashboard's "Testar leitura" button, so a tag address can be
// verified before it's added for good.
func (m *Manager) TestRead(ctx context.Context, dev config.DeviceConfig, tag config.TagConfig) (interface{}, error) {
	if tag.Address == "" {
		return nil, fmt.Errorf("endereço da tag é obrigatório")
	}
	probe := dev
	probe.Tags = []config.TagConfig{tag}

	d, err := driver.New(probe)
	if err != nil {
		return nil, err
	}
	defer d.Close()

	timeout := dev.Timeout()
	cctx, cancel := context.WithTimeout(ctx, timeout+2*time.Second)
	defer cancel()

	if err := d.Connect(cctx); err != nil {
		return nil, err
	}
	values, err := d.Poll(cctx)
	if err != nil && len(values) == 0 {
		return nil, err
	}
	val, ok := values[tag.Name]
	if !ok {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("PLC não retornou valor para esta tag")
	}
	return val, nil
}

// Shutdown stops every running poller. Called on gateway exit.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	names := make([]string, 0, len(m.running))
	for n := range m.running {
		names = append(names, n)
	}
	m.mu.Unlock()
	for _, n := range names {
		m.stopDevice(n)
	}
}
