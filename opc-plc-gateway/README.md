# OPC PLC Gateway

A from-scratch OPC UA server that polls PLCs from multiple brands and
republishes their tags as a standard OPC UA address space — the same role
KEPServerEX, Ignition, or FactoryTalk Linx Gateway play in a factory, built
here as a single dependency-free Windows executable instead of a licensed
product.

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

What it does **not** have yet, and would need before it's a serious Kepware
competitor: OPC UA security (certificates/encryption — it currently runs
`MessageSecurityModeNone`), tag-level write support back to the PLCs,
redundancy/failover, a management UI (today it's YAML + logs), and a native
driver for Omron's NJ/NX EtherNet/IP (CIP) family or Beckhoff's ADS
protocol (they currently fall back to Modbus TCP if the device supports
it — see `CHANGELOG.md` for the full brand coverage table). Treat this as
the architecture and the first three brands done properly — extend from
here.

## Architecture

```
   ┌───────────┐ ┌───────────┐ ┌─────────────┐ ┌───────────┐
   │ rockwell  │ │ siemens   │ │ mitsubishi  │ │  modbus   │  ← southbound drivers
   │(gologix)  │ │ (gos7)    │ │(go-mcprotocol)│ (goburrow)│    (add more here)
   └─────┬─────┘ └─────┬─────┘ └──────┬──────┘ └─────┬─────┘
         │  poll loop, one goroutine per configured device
         ▼             ▼              ▼              ▼
        ┌─────────────────────────────────────────────────┐
        │                   tagstore                        │  in-memory,
        │      "<device>.<tag>" -> {value, quality, ts}      │  thread-safe
        └───────────────────────┬─────────────────────────┘
                                 ▼
                         ┌───────────────┐
                         │  opcuaserver   │   OPC UA server (gopcua/server)
                         │ (gopcua/server)│   opc.tcp://<host>:4840
                         └───────────────┘
                                 ▲
                                 │
                    any OPC UA client (SCADA, historian, MES, ...)
```

Each device in `gateway.yaml` gets its own polling goroutine talking to one
driver instance; a failed poll marks that device's tags stale (quality
`Bad`) instead of crashing the gateway, and the next tick reconnects
automatically.

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
and edit the `devices:` list — see the comments in that file for the
address syntax of each driver (Rockwell tag names, Siemens `DB10,REAL0`
style addresses, Modbus `HR:100` style addresses).

Point any OPC UA client at `opc.tcp://<this-machine>:4840` (no auth, no
encryption by default — put it behind your network's normal firewalling
until OPC UA security is added).

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
cmd/gateway/            entrypoint, Windows service wiring, poll orchestration
internal/config/        YAML config loading
internal/tagstore/      thread-safe shared tag value store
internal/driver/        driver.Driver interface + registry, one subpackage per brand
internal/opcuaserver/   OPC UA server wiring tagstore -> address space
configs/                example gateway.yaml
build/                  cross-compile script, build output (gitignored)
```
