// Package webui is the gateway's management dashboard: a small embedded
// HTTP server (no external assets, no build step, no install - the HTML,
// CSS and JS are compiled into the binary via go:embed) that lets someone
// add PLCs, add tags, watch live values, and discover devices on the
// network from an ordinary browser, instead of hand-editing a YAML file
// and reading logs to find out whether something is even working - the
// friction that makes Kepware and RSLinx unpleasant for a first pass.
package webui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"time"

	"opc-plc-gateway/internal/config"
	"opc-plc-gateway/internal/discover"
	"opc-plc-gateway/internal/driver"
	"opc-plc-gateway/internal/manager"
	"opc-plc-gateway/internal/tagstore"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	mgr   *manager.Manager
	store *tagstore.Store
	auth  *auth
	http  *http.Server
}

func New(cfg config.WebUIConfig, mgr *manager.Manager, store *tagstore.Store) *Server {
	mux := http.NewServeMux()
	s := &Server{mgr: mgr, store: store, auth: newAuth(cfg.Password)}
	s.routes(mux)

	addr := fmt.Sprintf("%s:%d", cfg.BindAddr, cfg.Port)
	s.http = &http.Server{Addr: addr, Handler: mux}
	return s
}

// Addr is the address the dashboard listens on, once Run has started it
// (used by the caller to build the URL to auto-open in a browser).
func (s *Server) Addr() string {
	return s.http.Addr
}

func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("web dashboard: listening on %s: %w", s.http.Addr, err)
	}
	log.Printf("webui: dashboard listening on http://%s", ln.Addr())

	errCh := make(chan error, 1)
	go func() { errCh <- s.http.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return s.http.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func (s *Server) routes(mux *http.ServeMux) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // embedded at build time, can only fail if the package itself is broken
	}
	mux.Handle("GET /", http.FileServer(http.FS(sub)))

	// Login/session-check are never behind auth themselves (nothing to
	// gate a login page with); everything that reads or touches a PLC
	// is, via requireAuth - a no-op wrapper when no password is
	// configured.
	mux.HandleFunc("GET /api/session", s.handleSession)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)

	mux.HandleFunc("GET /api/drivers", s.requireAuth(s.handleDrivers))
	mux.HandleFunc("GET /api/devices", s.requireAuth(s.handleListDevices))
	mux.HandleFunc("POST /api/devices", s.requireAuth(s.handleAddDevice))
	mux.HandleFunc("GET /api/devices/{name}", s.requireAuth(s.handleGetDevice))
	mux.HandleFunc("DELETE /api/devices/{name}", s.requireAuth(s.handleRemoveDevice))
	mux.HandleFunc("GET /api/devices/{name}/tags", s.requireAuth(s.handleListTags))
	mux.HandleFunc("POST /api/devices/{name}/tags", s.requireAuth(s.handleAddTag))
	mux.HandleFunc("DELETE /api/devices/{name}/tags/{tag}", s.requireAuth(s.handleRemoveTag))
	mux.HandleFunc("POST /api/devices/{name}/tags/{tag}/write", s.requireAuth(s.handleWriteTag))
	mux.HandleFunc("POST /api/test-connection", s.requireAuth(s.handleTestConnection))
	mux.HandleFunc("POST /api/test-read", s.requireAuth(s.handleTestRead))
	mux.HandleFunc("GET /api/discover", s.requireAuth(s.handleDiscover))
	mux.HandleFunc("GET /events", s.requireAuth(s.handleEvents))
}

// --- auth handlers --------------------------------------------------------

// handleSession tells the frontend, on load, whether a login is required
// at all and whether the current cookie (if any) already satisfies it -
// so the dashboard can decide in one round trip whether to show itself or
// redirect to the login page.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	authenticated := true
	if s.auth.enabled() {
		c, err := r.Cookie(sessionCookieName)
		authenticated = err == nil && s.auth.validate(c.Value)
	}
	writeJSON(w, http.StatusOK, map[string]bool{
		"auth_required": s.auth.enabled(),
		"authenticated": authenticated,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.auth.enabled() {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	token, ok := s.auth.login(body.Password)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": "senha incorreta"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.auth.logout(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- JSON helpers -------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, v interface{}) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// --- driver catalogue (form hints for the dashboard) --------------------

type driverInfo struct {
	ID                    string   `json:"id"`
	Label                 string   `json:"label"`
	AddressHelp           string   `json:"address_help"`
	AddressPlaceholder    string   `json:"address_placeholder"`
	TagAddressHelp        string   `json:"tag_address_help"`
	TagAddressPlaceholder string   `json:"tag_address_placeholder"`
	ExtraFields           []string `json:"extra_fields"` // config keys this driver needs besides name/address/tags
}

var driverCatalogue = []driverInfo{
	{
		ID: "rockwell", Label: "Rockwell / Allen-Bradley (EtherNet/IP)",
		AddressHelp:           "IP do controlador, sem porta (ex: 192.168.1.10)",
		AddressPlaceholder:    "192.168.1.10",
		TagAddressHelp:        "Nome exato da tag no controlador (Studio 5000)",
		TagAddressPlaceholder: "Program:MainProgram.Velocidade",
	},
	{
		ID: "siemens", Label: "Siemens (S7-300/400/1200/1500)",
		AddressHelp:           "IP do CLP, sem porta (ex: 192.168.1.20)",
		AddressPlaceholder:    "192.168.1.20",
		TagAddressHelp:        "DBx,tipoOffset — ex: DB10,REAL0 / DB10,INT4 / DB10,X8.0 (bit)",
		TagAddressPlaceholder: "DB10,REAL0",
		ExtraFields:           []string{"rack", "slot"},
	},
	{
		ID: "mitsubishi", Label: "Mitsubishi Electric (MC Protocol / Q,L,iQ-R)",
		AddressHelp:           "IP:porta MC Protocol configurada no GX Works (ex: 192.168.1.40:5007)",
		AddressPlaceholder:    "192.168.1.40:5007",
		TagAddressHelp:        "Dispositivo — ex: D100 (palavra), M20/X10/Y10 (bit)",
		TagAddressPlaceholder: "D100",
	},
	{
		ID: "modbus", Label: "Modbus TCP (Schneider, ABB, Omron, Danfoss, Emerson, Honeywell, Yokogawa, Yaskawa, SEW e outros)",
		AddressHelp:           "IP:porta (porta padrão 502) — ex: 192.168.1.50:502",
		AddressPlaceholder:    "192.168.1.50:502",
		TagAddressHelp:        "TIPO:endereço — ex: HR:100 (holding register), IR:10, COIL:5, DI:3. Use \"type\" pra int32/float32/uint16",
		TagAddressPlaceholder: "HR:100",
		ExtraFields:           []string{"unit_id"},
	},
}

func (s *Server) handleDrivers(w http.ResponseWriter, r *http.Request) {
	registered := map[string]bool{}
	for _, n := range driver.Names() {
		registered[n] = true
	}
	out := make([]driverInfo, 0, len(driverCatalogue))
	for _, di := range driverCatalogue {
		if registered[di.ID] {
			out = append(out, di)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// --- devices --------------------------------------------------------------

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.mgr.List())
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	dev, ok := s.mgr.Device(name)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("dispositivo %q não encontrado", name))
		return
	}
	writeJSON(w, http.StatusOK, dev)
}

func (s *Server) handleAddDevice(w http.ResponseWriter, r *http.Request) {
	var dev config.DeviceConfig
	if err := readJSON(r, &dev); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.mgr.AddDevice(r.Context(), dev); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, dev)
}

func (s *Server) handleRemoveDevice(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.mgr.RemoveDevice(name); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- tags -------------------------------------------------------------

func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	tags, ok := s.mgr.Tags(name)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("dispositivo %q não encontrado", name))
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

func (s *Server) handleAddTag(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var tag config.TagConfig
	if err := readJSON(r, &tag); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.mgr.AddTag(name, tag); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, tag)
}

func (s *Server) handleRemoveTag(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	tag := r.PathValue("tag")
	if err := s.mgr.RemoveTag(name, tag); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleWriteTag writes a value back to a tag's PLC address - the
// dashboard's inline "escrever" control on a tag row. The body is a bare
// JSON value ({"value": ...}) so numbers/booleans/strings round-trip
// exactly as the browser's <input> produced them; internal/manager takes
// care of turning that into the concrete Go type the driver expects.
func (s *Server) handleWriteTag(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	tag := r.PathValue("tag")

	var body struct {
		Value interface{} `json:"value"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.mgr.WriteTag(ctx, name, tag, body.Value); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// --- testing (wizard "testar conexão" / "testar leitura") --------------

func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	var dev config.DeviceConfig
	if err := readJSON(r, &dev); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.mgr.TestConnection(ctx, dev); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) handleTestRead(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Device config.DeviceConfig `json:"device"`
		Tag    config.TagConfig    `json:"tag"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	val, err := s.mgr.TestRead(ctx, body.Device, body.Tag)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "value": val})
}

// --- discovery ----------------------------------------------------------

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	nets := discover.LocalIPv4Networks()
	if len(nets) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{"networks": []string{}, "found": []discover.Found{}})
		return
	}
	found := discover.Scan(ctx, nets, 400*time.Millisecond)

	netNames := make([]string, len(nets))
	for i, n := range nets {
		netNames[i] = n.String()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"networks": netNames, "found": found})
}

// --- live updates (SSE) --------------------------------------------------

// handleEvents streams every tag value change as it happens, so the
// dashboard's tag tables update live without polling the REST API.
// Each event's data is JSON: {"device":"...","tag":"...","value":...,
// "quality":"good|bad|stale","error":"...","timestamp":"..."}.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	changes := s.store.Subscribe()
	ctx := r.Context()

	fmt.Fprintf(w, "retry: 2000\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case key := <-changes:
			device, tag := tagstore.SplitKey(key)
			v, ok := s.store.Get(key)
			if !ok {
				continue
			}
			payload := map[string]interface{}{
				"device":    device,
				"tag":       tag,
				"value":     v.Value,
				"quality":   qualityString(v.Quality),
				"error":     v.Err,
				"timestamp": v.Timestamp,
			}
			data, _ := json.Marshal(payload)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func qualityString(q tagstore.Quality) string {
	switch q {
	case tagstore.QualityGood:
		return "good"
	case tagstore.QualityBad:
		return "bad"
	default:
		return "stale"
	}
}
