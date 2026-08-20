// Package opcuaserver is the gateway's northbound interface: it exposes
// every configured tag as a standard OPC UA variable node, under
// Objects/<DeviceName>/<TagName>, so any OPC UA client (Ignition, KEPServer
// clients, .NET/Python OPC UA clients, historians, SCADA/HMI) can browse
// and subscribe to it exactly like they would against Kepware or any other
// OPC server.
package opcuaserver

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"

	"opc-plc-gateway/internal/config"
	"opc-plc-gateway/internal/tagstore"
)

type Server struct {
	ua    *server.Server
	ns    *server.NodeNameSpace
	store *tagstore.Store

	mu       sync.Mutex
	nodes    map[string]*server.Node // tagstore key -> variable node
	devNodes map[string]*server.Node // device name -> its folder node
}

func New(cfg config.ServerConfig, devices []config.DeviceConfig, store *tagstore.Store) *Server {
	opts := []server.Option{
		server.EnableSecurity("None", ua.MessageSecurityModeNone),
		server.EnableAuthMode(ua.UserTokenTypeAnonymous),
		server.EndPoint(cfg.OPCUABindAddr, cfg.OPCUAPort),
	}

	uaSrv := server.New(opts...)
	root, _ := uaSrv.Namespace(0)
	rootObjects := root.Objects()

	nodeNS := server.NewNodeNameSpace(uaSrv, "PLCGateway")
	rootObjects.AddRef(nodeNS.Objects(), id.HasComponent, true)

	s := &Server{
		ua: uaSrv, ns: nodeNS, store: store,
		nodes: map[string]*server.Node{}, devNodes: map[string]*server.Node{},
	}

	// Build one folder-ish object per device and one variable node per
	// configured tag underneath it, wired to read live from the tag
	// store on every OPC UA read/subscription sample. Devices and tags
	// added later through the dashboard go through the same two helpers
	// (AddDevice/AddTag) - the server's address space is not fixed at
	// startup.
	for _, dev := range devices {
		s.AddDevice(dev)
		for _, tag := range dev.Tags {
			s.AddTag(dev.Name, tag)
		}
	}

	return s
}

// AddDevice publishes a new folder node for a device added through the
// dashboard. Safe to call while the server is already running.
func (s *Server) AddDevice(dev config.DeviceConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.devNodes[dev.Name]; exists {
		return
	}
	devObj := server.NewFolderNode(ua.NewNumericNodeID(s.ns.ID(), s.ns.GetNextNodeID()), dev.Name)
	s.ns.AddNode(devObj)
	s.ns.Objects().AddRef(devObj, id.HasComponent, true)
	s.devNodes[dev.Name] = devObj
}

// AddTag publishes a new variable node for a tag added through the
// dashboard, under its device's folder (calling AddDevice first if the
// device folder doesn't exist yet). Safe to call while the server is
// already running.
func (s *Server) AddTag(deviceName string, tag config.TagConfig) {
	s.mu.Lock()
	devObj, ok := s.devNodes[deviceName]
	s.mu.Unlock()
	if !ok {
		s.AddDevice(config.DeviceConfig{Name: deviceName})
		s.mu.Lock()
		devObj = s.devNodes[deviceName]
		s.mu.Unlock()
	}

	key := tagstore.Key(deviceName, tag.Name)
	node := s.ns.AddNewVariableStringNode(fmt.Sprintf("%s.%s", deviceName, tag.Name), s.readerFor(key))
	devObj.AddRef(node, id.HasComponent, true)

	s.mu.Lock()
	s.nodes[key] = node
	s.mu.Unlock()
}

// readerFor returns the getter gopcua calls whenever a client reads or is
// sent a subscription sample for this node: it always reflects the tag
// store's latest value, so polling and OPC UA delivery are decoupled.
func (s *Server) readerFor(key string) func() *ua.DataValue {
	return func() *ua.DataValue {
		v, ok := s.store.Get(key)
		if !ok {
			return &ua.DataValue{
				Value:           ua.MustVariant(int32(0)),
				Status:          ua.StatusBadWaitingForInitialData,
				SourceTimestamp: time.Now(),
				EncodingMask:    ua.DataValueValue | ua.DataValueStatusCode | ua.DataValueSourceTimestamp,
			}
		}
		status := ua.StatusOK
		if v.Quality != tagstore.QualityGood {
			status = ua.StatusBadDeviceFailure
		}
		return &ua.DataValue{
			Value:           ua.MustVariant(v.Value),
			Status:          status,
			SourceTimestamp: v.Timestamp,
			EncodingMask:    ua.DataValueValue | ua.DataValueStatusCode | ua.DataValueSourceTimestamp,
		}
	}
}

// Run starts the OPC UA endpoint and blocks, forwarding tagstore change
// notifications into the node namespace's change-notification mechanism so
// subscribed clients get pushed updates instead of only poll-on-read.
func (s *Server) Run(ctx context.Context) error {
	if err := s.ua.Start(ctx); err != nil {
		return fmt.Errorf("starting OPC UA server: %w", err)
	}
	defer s.ua.Close()

	log.Printf("opcua: server listening (endpoint(s): %v)", s.ua.URLs())

	changes := s.store.Subscribe()
	for {
		select {
		case <-ctx.Done():
			return nil
		case key := <-changes:
			s.mu.Lock()
			node, ok := s.nodes[key]
			s.mu.Unlock()
			if ok {
				s.ns.ChangeNotification(node.ID())
			}
		}
	}
}
