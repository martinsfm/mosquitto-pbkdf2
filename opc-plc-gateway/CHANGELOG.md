# Changelog — OPC PLC Gateway

Registro cronológico de cada modificação feita no projeto, para você
conseguir retomar de onde parou mesmo trocando/formatando de máquina —
tudo aqui está commitado no branch `claude/opc-server-plc-factory-g41856`
do repositório `martinsfm/mosquitto-pbkdf2` no GitHub, então o histórico
sobrevive independente do que aconteça com o PC local.

Cada entrada abaixo é uma sessão de trabalho: o que foi pedido, o que foi
feito, e em quais arquivos.

---

## 2026-08-20 — Estrutura inicial: arquitetura + Rockwell + Siemens + Modbus

**Pedido:** um servidor OPC que rode em segundo plano no Windows sem
precisar instalar nada, capaz de enxergar PLCs de várias marcas (Rockwell,
Siemens, "demais grandes empresas"), no estilo Kepware, construído do zero.

**Decisões tomadas:**
- Linguagem: **Go**. Motivo: compila para um único `.exe` estático
  (`CGO_ENABLED=0`), sem depender de .NET/Java/Python instalado na máquina
  de destino — só o `.exe` + um arquivo de config. Validei compilando de
  verdade neste ambiente (não fiz suposições sem testar).
- Arquitetura plugin-based: cada marca de PLC é um "driver" (pacote Go)
  que implementa uma interface comum (`Connect`, `Poll`, `Close`) e se
  registra sozinho — adicionar uma marca nova não exige tocar no núcleo.
- Servidor OPC UA real (biblioteca `gopcua/opcua`, pacote `server`), não
  um mock — qualquer cliente OPC UA padrão consegue navegar/assinar as
  tags.

**O que foi criado:**
- `internal/tagstore/` — cache em memória thread-safe (ponte entre os
  drivers e o servidor OPC UA), com notificação de mudança.
- `internal/config/` — carregamento do `gateway.yaml` (lista de
  dispositivos, endereços, tags).
- `internal/driver/` — interface `Driver` + registro de plugins.
- `internal/driver/rockwell/` — driver nativo Allen-Bradley/Rockwell
  (EtherNet/IP CIP) via biblioteca `danomagnum/gologix`. Endereço = nome
  da tag no controlador (ex: `Program:MainProgram.Velocidade`).
- `internal/driver/siemens/` — driver nativo Siemens S7-300/400/1200/1500
  via biblioteca `robinson/gos7`. Endereço estilo `DB10,REAL0`.
- `internal/driver/modbus/` — driver Modbus TCP via `goburrow/modbus`,
  usado como fallback universal pra qualquer marca que fale Modbus TCP
  (cobre a maioria do "demais grandes empresas"). Endereço estilo
  `HR:100`.
- `internal/opcuaserver/` — publica cada tag configurada como nó OPC UA
  navegável, sob `Objects/<Dispositivo>/<Tag>`.
- `cmd/gateway/` — ponto de entrada; detecta se foi iniciado pelo Service
  Control Manager do Windows e roda como **Windows Service nativo**
  (instalável com `sc create`, sem instalador) ou em primeiro plano
  (pra teste, Ctrl+C pra parar).
- `configs/gateway.example.yaml` — exemplo completo de configuração.
- `build/build-windows.sh` — script de cross-compile pra gerar o `.exe`.
- `README.md` — arquitetura, escopo honesto (isto não é um Kepware
  completo ainda), como buildar, como instalar como serviço, como
  adicionar uma marca nova.

**Validação feita:** `go build`, `go vet` e `go test` passando limpo tanto
pra Linux (dev) quanto cross-compilado pra Windows amd64 (`.exe` de
~10MB, zero dependência).

---

## 2026-08-20 — Driver nativo Mitsubishi (MC Protocol)

**Pedido:** adicionar suporte a mais marcas: Mitsubishi, Danfoss, ABB,
Schneider, Omron, Emerson, Honeywell, Yokogawa, Yaskawa, SEW.

**O que foi analisado:** das 10 marcas pedidas, 9 já são cobertas pelo
driver Modbus TCP que já existia (Danfoss, ABB, Schneider, Omron parcial,
Emerson, Honeywell, Yokogawa, Yaskawa, SEW — ver tabela de cobertura no
README). A exceção é a **Mitsubishi**: os PLCs da linha Q/L/iQ-R usam um
protocolo próprio (MC Protocol / MELSEC Communication Protocol, também
chamado SLMP) que não é Modbus, então precisava de um driver nativo.

**O que foi criado:**
- `internal/driver/mitsubishi/` — driver nativo via biblioteca
  `future-architect/go-mcprotocol` (frame binário 3E). Endereços estilo
  `D100` (registro de palavra), `M20`/`X10`/`Y10` (bit).
- Testes unitários do parser de endereço
  (`internal/driver/mitsubishi/mitsubishi_test.go`).
- Registrado em `cmd/gateway/main.go` (uma linha de import, como
  documentado na arquitetura).
- Exemplo adicionado em `configs/gateway.example.yaml`.
- Tabela de cobertura das 10 marcas pedidas adicionada ao `README.md`.

**Limitação conhecida:** a biblioteca MC protocol usada está marcada
como "work in progress" pelo autor e abre uma conexão TCP nova a cada
leitura (funciona, mas não é o mais performático — ok pra intervalos de
poll de 500ms-1s, atenção se precisar de polling muito rápido em muitas
tags Mitsubishi).

**Validação feita:** `go build`, `go vet`, `go test` (Linux) e
cross-compile Windows, todos limpos.

---

## Cobertura de marcas — estado atual

| Marca | Como é coberta hoje | Observação |
|---|---|---|
| Rockwell / Allen-Bradley | Driver nativo (`rockwell`, EtherNet/IP CIP) | ControlLogix, CompactLogix, Micro8xx |
| Siemens | Driver nativo (`siemens`, protocolo S7) | S7-300/400/1200/1500; S7-1200/1500 exigem "optimized block access" desligado no DB |
| Mitsubishi | Driver nativo (`mitsubishi`, MC Protocol) | Q/L/iQ-R com porta Ethernet |
| Schneider Electric | Driver `modbus` | Modicon fala Modbus nativamente (protocolo foi criado pela própria Schneider/Modicon) |
| ABB | Driver `modbus` | AC500 e a maioria dos drives ACS falam Modbus TCP |
| Omron | Driver `modbus` (parcial) | CJ/CS/CP com módulo Modbus TCP: ok. Série NJ/NX usa EtherNet/IP nativo (CIP) — **não coberto ainda**, ver "próximos passos" |
| Danfoss | Driver `modbus` | Drives VLT falam Modbus TCP/RTU nativamente |
| Emerson | Driver `modbus` | PACSystems (ex-GE) RX3i suporta Modbus TCP Server nativamente. DeltaV (DCS) já vem com servidor OPC próprio — nem precisa deste gateway |
| Honeywell | Driver `modbus` | ControlEdge/Trend geralmente suportam Modbus TCP. Experion (DCS) já vem com OPC próprio |
| Yokogawa | Driver `modbus` | FA-M3 suporta Modbus TCP. Centum (DCS) já vem com OPC próprio |
| Yaskawa | Driver `modbus` | MP2000/MP3000 suportam Modbus TCP |
| SEW-EURODRIVE | Driver `modbus` | MOVITRAC/MOVIDRIVE suportam Modbus TCP (também têm opção Profinet/EtherCAT, não implementada) |

## Próximos passos sugeridos (não implementados ainda)

Em ordem de impacto prático:
1. **Segurança OPC UA** — hoje roda sem autenticação/criptografia
   (`MessageSecurityModeNone`); ok atrás de firewall de fábrica, mas vale
   endurecer antes de expor mais amplamente.
2. **Escrita de tags** — hoje o gateway só lê (monitoramento). Escrever
   valores de volta no PLC via OPC UA ainda não existe.
3. **Driver EtherNet/IP nativo pra Omron NJ/NX** — mesma família de
   protocolo do Rockwell (CIP), mas com particularidades próprias da Omron.
4. **Redundância/failover** e **UI de gestão** (hoje é YAML + logs).
