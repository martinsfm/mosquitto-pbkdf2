# OPC PLC Gateway

A from-scratch OPC UA server that polls PLCs from multiple brands and
republishes their tags as a standard OPC UA address space — the same role
KEPServerEX, Ignition, or FactoryTalk Linx Gateway play in a factory, built
here as a single dependency-free Windows executable instead of a licensed
product. It ships with a **local web dashboard** so day-to-day use (add a
PLC, add a tag, watch live values, find PLCs on the network) never requires
opening the YAML config file — the thing people usually complain is
Kepware's biggest weak point.

## Honest scope

Kepware/Ignition are the product of many years of engineering across dozens
of certified protocol drivers, redundancy, security hardening, and vendor
support contracts. This project is **not** a drop-in replacement for that —
it's a real, working foundation with:

- A plugin-style driver architecture, so adding a new PLC brand is "write
  one package, add one import line," not a rewrite.
- Three native drivers today: **Rockwell/Allen-Bradley** (EtherNet/IP CIP —
  ControlLogix, CompactLogix, Micro8xx), **Siemens** (S7 protocol —
  S7-300/400/1200/1500), and **Mitsubishi Electric** (MC Protocol/SLMP —
  Q/L/iQ-R series).
- One universal fallback driver: **Modbus TCP**, which covers most other
  big brands (Schneider, ABB, Omron, Danfoss, Emerson, Honeywell,
  Yokogawa, Yaskawa, SEW, Delta, WEG, most VFDs and remote I/O) since
  virtually all of them expose it natively or via a gateway module. See
  `CHANGELOG.md` for the full brand-by-brand coverage table.
- A real OPC UA server (`gopcua/server`) exposing every tag live, so any
  standard OPC UA client — SCADA, historian, MES, another Ignition/Kepware
  instance — can browse and subscribe to it.
- A **web dashboard** (`internal/webui`), embedded in the same `.exe`, with
  no separate install: add a device with a wizard that tests the connection
  before saving, add a tag with a one-click "test read" before committing
  to it, watch every tag's live value update in real time, write a value
  back to a tag right from its row, and a "find PLCs on the network"
  button that scans for the well-known ports each brand listens on.
  Changes made here take effect immediately (no restart) and are written
  back to `gateway.yaml`, so the file and the dashboard are always the
  same source of truth. An optional password (`webui.password`) gates the
  whole dashboard behind a login when it's reachable from more than just
  this machine.
- **Tag write-back from the dashboard**: every driver (Rockwell, Siemens,
  Mitsubishi, Modbus) implements `driver.Writer` — type a value into a
  tag's row and click "Escrever" to push it straight to the PLC, not just
  read it. A few narrow gaps are noted per-driver in the source (e.g.
  Mitsubishi bit devices can't be written — the underlying library has no
  bit-write primitive). This does **not** yet extend to OPC UA clients: an
  OPC UA Write request against a node only updates that node's local
  cached value, it does not reach the PLC (see `CHANGELOG.md`).

What it does **not** have yet, and would need before it's a serious Kepware
competitor: OPC UA security (certificates/encryption — it currently runs
`MessageSecurityModeNone`; the dashboard's own password is separate from
this and doesn't cover the OPC UA endpoint itself), redundancy/failover,
and a native driver for Omron's NJ/NX EtherNet/IP (CIP) family or
Beckhoff's ADS protocol (they currently fall back to Modbus TCP if the
device supports it — see `CHANGELOG.md` for the full brand coverage
table). Treat this as the architecture, the first three brands, the
dashboard, and tag writes done properly — extend from here.

## Architecture

```
   ┌───────────┐ ┌───────────┐ ┌─────────────┐ ┌───────────┐
   │ rockwell  │ │ siemens   │ │ mitsubishi  │ │  modbus   │  ← southbound drivers
   │(gologix)  │ │ (gos7)    │ │(go-mcprotocol)│ (goburrow)│    (add more here)
   └─────┬─────┘ └─────┬─────┘ └──────┬──────┘ └─────┬─────┘
         │  poll loop, one goroutine per device (internal/manager)
         ▼             ▼              ▼              ▼
        ┌─────────────────────────────────────────────────┐
        │                   tagstore                        │  in-memory,
        │      "<device>.<tag>" -> {value, quality, ts}      │  thread-safe
        └───────────────────────┬─────────────────────────┘
                    ▼                                ▼
            ┌───────────────┐               ┌──────────────────┐
            │  opcuaserver   │               │      webui        │
            │ (gopcua/server)│               │ REST API + SSE +  │
            │ opc.tcp://:4840│               │ embedded dashboard │
            └───────┬────────┘               │  http://:8080      │
                    ▲                        └─────────┬──────────┘
                    │                                   ▲
       any OPC UA client (SCADA, historian, MES, ...)    │ your browser
```

`internal/manager` owns the live device list: each device gets its own
polling goroutine talking to one driver instance; a failed poll marks that
device's tags stale (quality `Bad`) instead of crashing the gateway, and
the next tick reconnects automatically. Adding/removing a device or tag
through the dashboard goes through the manager too, so both the OPC UA
address space and the tag store stay in sync immediately — no restart.

### Adding a new PLC brand

1. Create `internal/driver/<brand>/<brand>.go`.
2. Implement the three-method `driver.Driver` interface (`Connect`,
   `Poll`, `Close`) — see `internal/driver/modbus/modbus.go` for the
   simplest example.
3. Register it: `driver.Register("<brand>", New)` in an `init()`.
4. Add `_ "opc-plc-gateway/internal/driver/<brand>"` to
   `cmd/gateway/main.go`'s import block.

Nothing else in the gateway needs to change — the config loader, tag store,
and OPC UA server are all driver-agnostic.

## Build (single, dependency-free .exe)

Requires the Go toolchain on the **build** machine only — the resulting
`.exe` needs nothing installed on the **target** factory PC beyond a
stock Windows install:

```bash
./build/build-windows.sh
# -> build/gateway.exe  (~10 MB, no DLL/runtime dependencies)
```

(This is a static Go binary, `CGO_ENABLED=0` — it does not touch .NET,
Python, Java, or Visual C++ redistributables on the target machine.)

## Run

```
gateway.exe -config gateway.yaml
```

Copy `configs/gateway.example.yaml` to `gateway.yaml` next to the `.exe`
first (it can start with an empty `devices: []` — you can add everything
from the dashboard instead of editing YAML).

Double-clicking the `.exe` (or running it from a terminal) opens your
default browser at **http://127.0.0.1:8080** automatically — that's the
dashboard: click **+ Adicionar dispositivo**, pick the brand, type the
PLC's IP, hit **Testar conexão** to confirm it actually answers before
saving, then **+ Adicionar tag** the same way (with a **Testar leitura**
button so a wrong address shows up immediately, not after you've walked
away). Don't know a PLC's IP? **Procurar PLCs na rede** scans the local
subnet for the ports each supported brand listens on. Pass `-no-browser`
to skip the auto-open (e.g. running under a task scheduler); the dashboard
keeps running either way. See the comments in `gateway.example.yaml` if
you'd rather script the device list directly — same address syntax the
dashboard's help text shows per brand (Rockwell tag names, Siemens
`DB10,REAL0`, Mitsubishi `D100`, Modbus `HR:100`).

Point any OPC UA client at `opc.tcp://<this-machine>:4840` (no auth, no
encryption by default — put it behind your network's normal firewalling
until OPC UA security is added). The dashboard itself binds to
`127.0.0.1` only by default; set `webui.bind_addr: "0.0.0.0"` in the
config to reach it from other machines on the factory LAN.

## Install as a Windows Service (background, auto-start, no installer)

The same `.exe` detects whether the Service Control Manager started it; run
these from an elevated (Administrator) `cmd.exe`/PowerShell — `sc.exe` ships
with every Windows install, nothing extra needed:

```cmd
sc create OpcPlcGateway binPath= "C:\path\to\gateway.exe -config C:\path\to\gateway.yaml" start= auto
sc description OpcPlcGateway "OPC UA gateway for factory PLCs"
sc start OpcPlcGateway
```

Check status in `services.msc` or with `sc query OpcPlcGateway`. Logs go to
the Windows Application Event Log via the standard service logging path
during startup/shutdown transitions, and to stdout when run in the
foreground for testing (`gateway.exe -config gateway.yaml`, Ctrl+C to stop).

To remove: `sc stop OpcPlcGateway && sc delete OpcPlcGateway`.

## Repo layout

```
cmd/gateway/            entrypoint, Windows service wiring, browser auto-open
internal/config/        YAML config loading + saving
internal/manager/       live device/tag lifecycle (add/remove, polling, test connect/read)
internal/tagstore/      thread-safe shared tag value store
internal/driver/        driver.Driver interface + registry, one subpackage per brand
internal/opcuaserver/   OPC UA server wiring tagstore -> address space
internal/webui/         embedded dashboard: REST API, SSE live updates, static HTML/CSS/JS
internal/discover/      network port scanner behind the dashboard's "find PLCs" button
configs/                example gateway.yaml
build/                  cross-compile script, build output (gitignored)
```
