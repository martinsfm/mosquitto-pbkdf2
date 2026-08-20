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
	nodes map[string]*server.Node // tagstore key -> variable node
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

	s := &Server{ua: uaSrv, ns: nodeNS, store: store, nodes: map[string]*server.Node{}}

	// Build one folder-ish object per device and one variable node per
	// configured tag underneath it, wired to read live from the tag
	// store on every OPC UA read/subscription sample.
	for _, dev := range devices {
		devObj := server.NewFolderNode(ua.NewNumericNodeID(nodeNS.ID(), nodeNS.GetNextNodeID()), dev.Name)
		nodeNS.AddNode(devObj)
		nodeNS.Objects().AddRef(devObj, id.HasComponent, true)

		for _, tag := range dev.Tags {
			key := tagstore.Key(dev.Name, tag.Name)
			node := nodeNS.AddNewVariableStringNode(fmt.Sprintf("%s.%s", dev.Name, tag.Name), s.readerFor(key))
			devObj.AddRef(node, id.HasComponent, true)
			s.nodes[key] = node
		}
	}

	return s
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
			if node, ok := s.nodes[key]; ok {
				s.ns.ChangeNotification(node.ID())
			}
		}
	}
}
