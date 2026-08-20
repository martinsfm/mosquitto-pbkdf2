"use strict";

// ---- state ---------------------------------------------------------------
let drivers = [];          // driver catalogue from /api/drivers
let devices = [];          // last snapshot from /api/devices
let currentDevice = null;  // name of the device shown in the detail panel
let currentTags = [];      // last tag snapshot for currentDevice

// ---- small helpers ---------------------------------------------------------------
function $(id) { return document.getElementById(id); }
function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}
async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (res.status === 401) {
    location.href = "/login.html?next=" + encodeURIComponent(location.pathname);
    throw new Error("sessão expirada");
  }
  if (res.status === 204) return null;
  const data = await res.json().catch(() => null);
  if (!res.ok) throw new Error((data && data.error) || res.statusText);
  return data;
}
function fmtValue(v) {
  if (v === undefined || v === null) return "—";
  if (typeof v === "number") return Number.isInteger(v) ? String(v) : v.toFixed(3);
  return String(v);
}
function fmtTime(t) {
  if (!t) return "";
  const d = new Date(t);
  if (isNaN(d)) return "";
  return d.toLocaleTimeString("pt-BR");
}
function driverInfo(id) { return drivers.find((d) => d.id === id); }
function driverLabel(id) { const d = driverInfo(id); return d ? d.label.split(" (")[0] : id; }

// ---- device list -----------------------------------------------------------
async function refreshDevices() {
  try {
    devices = await api("GET", "/api/devices");
  } catch (e) {
    console.error("falha ao listar dispositivos", e);
    return;
  }
  renderDeviceList();
  const online = devices.filter((d) => d.connected).length;
  $("summary").textContent = devices.length
    ? `${online}/${devices.length} dispositivo(s) online`
    : "nenhum dispositivo configurado ainda";
  if (currentDevice && !devices.some((d) => d.name === currentDevice)) {
    currentDevice = null;
    renderEmptyDetail();
  }
}

function renderDeviceList() {
  const list = $("deviceList");
  list.innerHTML = "";
  if (!devices.length) {
    list.appendChild(el("p", "empty-hint", "Nenhum dispositivo ainda. Clique em “+ Adicionar dispositivo” para conectar o primeiro PLC."));
    return;
  }
  for (const d of devices) {
    const card = el("div", "device-card" + (d.name === currentDevice ? " active" : ""));
    const head = el("div", "device-card-head");
    head.appendChild(el("span", "device-name", d.name));
    head.appendChild(el("span", "status-dot " + (d.connected ? "status-good" : "status-bad")));
    card.appendChild(head);
    card.appendChild(el("div", "device-meta", `${d.address} · ${d.tag_count} tag(s) · ${d.poll_interval_ms}ms`));
    card.appendChild(el("span", "device-driver-badge", driverLabel(d.driver)));
    if (!d.connected && d.last_error) {
      card.appendChild(el("div", "device-error", d.last_error));
    }
    card.addEventListener("click", () => selectDevice(d.name));
    list.appendChild(card);
  }
}

// ---- device detail -----------------------------------------------------------
function renderEmptyDetail() {
  $("deviceDetail").innerHTML =
    '<div class="empty-state"><p>Selecione um dispositivo à esquerda para ver as tags em tempo real,</p>' +
    '<p>ou clique em <strong>+ Adicionar dispositivo</strong> para conectar um PLC novo.</p></div>';
}

async function selectDevice(name) {
  currentDevice = name;
  renderDeviceList();
  await refreshTags();
}

async function refreshTags() {
  if (!currentDevice) return;
  try {
    currentTags = await api("GET", `/api/devices/${encodeURIComponent(currentDevice)}/tags`);
  } catch (e) {
    return;
  }
  renderDetail();
}

function renderDetail() {
  const dev = devices.find((d) => d.name === currentDevice);
  const root = $("deviceDetail");
  root.innerHTML = "";
  if (!dev) { renderEmptyDetail(); return; }

  const header = el("div", "detail-header");
  header.appendChild(el("h2", null, dev.name));
  const delBtn = el("button", "btn btn-danger btn-small", "Remover dispositivo");
  delBtn.addEventListener("click", () => removeDevice(dev.name));
  header.appendChild(delBtn);
  root.appendChild(header);

  root.appendChild(el("div", "detail-sub",
    `${driverLabel(dev.driver)} · ${dev.address} · leitura a cada ${dev.poll_interval_ms}ms` +
    (dev.connected ? "" : dev.last_error ? ` · ${dev.last_error}` : " · aguardando conexão")));

  const table = el("table", "tag-table");
  const thead = el("thead");
  thead.innerHTML = "<tr><th>Tag</th><th>Endereço</th><th>Valor</th><th>Qualidade</th><th>Atualizado</th><th>Escrever</th><th></th></tr>";
  table.appendChild(thead);
  const tbody = el("tbody");
  tbody.id = "tagTableBody";
  for (const t of currentTags) {
    tbody.appendChild(renderTagRow(t));
  }
  table.appendChild(tbody);
  root.appendChild(table);

  const actions = el("div", "section-actions");
  const addTagBtn = el("button", "btn btn-primary btn-small", "+ Adicionar tag");
  addTagBtn.addEventListener("click", () => openTagModal(dev));
  actions.appendChild(addTagBtn);
  root.appendChild(actions);
}

function renderTagRow(t) {
  const tr = el("tr");
  tr.dataset.tag = t.name;
  tr.appendChild(el("td", null, t.name));
  tr.appendChild(el("td", "tag-addr", t.address + (t.type ? ` (${t.type})` : "")));
  const valTd = el("td", "tag-value", fmtValue(t.value));
  valTd.dataset.role = "value";
  tr.appendChild(valTd);
  const qTd = el("td");
  const qBadge = el("span", "quality-badge quality-" + t.quality, qualityLabel(t.quality));
  qTd.dataset.role = "quality";
  qTd.appendChild(qBadge);
  tr.appendChild(qTd);
  const tsTd = el("td", null, fmtTime(t.timestamp));
  tsTd.dataset.role = "timestamp";
  tr.appendChild(tsTd);

  const writeTd = el("td");
  const writeRow = el("div", "write-row");
  const writeInput = el("input");
  writeInput.type = "text";
  writeInput.className = "write-input";
  writeInput.placeholder = fmtValue(t.value);
  const writeBtn = el("button", "btn btn-small btn-ghost", "Escrever");
  const writeMsg = el("span", "write-msg");
  writeBtn.addEventListener("click", () => writeTag(currentDevice, t.name, writeInput, writeMsg));
  writeInput.addEventListener("keydown", (ev) => { if (ev.key === "Enter") writeTag(currentDevice, t.name, writeInput, writeMsg); });
  writeRow.appendChild(writeInput);
  writeRow.appendChild(writeBtn);
  writeTd.appendChild(writeRow);
  writeTd.appendChild(writeMsg);
  tr.appendChild(writeTd);

  const rmTd = el("td");
  const rmBtn = el("button", "remove-x", "✕");
  rmBtn.title = "Remover tag";
  rmBtn.addEventListener("click", () => removeTag(currentDevice, t.name));
  rmTd.appendChild(rmBtn);
  tr.appendChild(rmTd);
  return tr;
}

function qualityLabel(q) {
  if (q === "good") return "boa";
  if (q === "bad") return "ruim";
  return "obsoleta";
}

function parseInputValue(raw) {
  const trimmed = raw.trim();
  if (trimmed === "true") return true;
  if (trimmed === "false") return false;
  if (trimmed !== "" && !isNaN(trimmed)) return Number(trimmed);
  return trimmed;
}

async function writeTag(deviceName, tagName, inputEl, msgEl) {
  const raw = inputEl.value;
  if (raw.trim() === "") { setInlineResult(msgEl, false, "Digite um valor."); return; }
  setInlineResult(msgEl, null, "Escrevendo…");
  try {
    const res = await api("POST", `/api/devices/${encodeURIComponent(deviceName)}/tags/${encodeURIComponent(tagName)}/write`, { value: parseInputValue(raw) });
    if (res.ok) {
      setInlineResult(msgEl, true, "OK");
      inputEl.value = "";
    } else {
      setInlineResult(msgEl, false, res.error);
    }
  } catch (e) {
    setInlineResult(msgEl, false, e.message);
  }
}

function setInlineResult(el, ok, msg) {
  el.textContent = msg;
  el.className = "write-msg" + (ok === true ? " test-ok" : ok === false ? " test-fail" : "");
}

async function removeDevice(name) {
  if (!confirm(`Remover o dispositivo "${name}"? Isso para a leitura imediatamente.`)) return;
  try {
    await api("DELETE", `/api/devices/${encodeURIComponent(name)}`);
  } catch (e) {
    alert("Erro ao remover: " + e.message);
    return;
  }
  currentDevice = null;
  await refreshDevices();
  renderEmptyDetail();
}

async function removeTag(deviceName, tagName) {
  try {
    await api("DELETE", `/api/devices/${encodeURIComponent(deviceName)}/tags/${encodeURIComponent(tagName)}`);
  } catch (e) {
    alert("Erro ao remover tag: " + e.message);
    return;
  }
  await refreshTags();
}

// ---- add device modal -----------------------------------------------------------
function openDeviceModal() {
  $("fNome").value = "";
  $("fAddress").value = "";
  $("fPollMs").value = 1000;
  $("testConnResult").textContent = "";
  renderDriverFields();
  $("deviceModalBackdrop").classList.add("open");
}
function closeDeviceModal() { $("deviceModalBackdrop").classList.remove("open"); }

function populateDriverSelect() {
  const sel = $("fDriver");
  sel.innerHTML = "";
  for (const d of drivers) {
    const opt = el("option", null, d.label);
    opt.value = d.id;
    sel.appendChild(opt);
  }
}

function renderDriverFields() {
  const id = $("fDriver").value || (drivers[0] && drivers[0].id);
  const info = driverInfo(id);
  if (!info) return;
  $("fDriverHelp").textContent = info.address_help;
  $("fAddress").placeholder = info.address_placeholder;

  const extra = $("fExtraFields");
  extra.innerHTML = "";
  for (const field of info.extra_fields || []) {
    const label = el("label");
    if (field === "rack") { label.textContent = "Rack "; label.appendChild(numberInput("fRack", 0)); }
    else if (field === "slot") { label.textContent = "Slot "; label.appendChild(numberInput("fSlot", 1)); }
    else if (field === "unit_id") { label.textContent = "Unit ID "; label.appendChild(numberInput("fUnitId", 1)); }
    extra.appendChild(label);
  }
}
function numberInput(id, def) {
  const i = el("input");
  i.type = "number";
  i.id = id;
  i.value = def;
  return i;
}

function collectDeviceForm() {
  const id = $("fDriver").value;
  const dev = {
    name: $("fNome").value.trim(),
    driver: id,
    address: $("fAddress").value.trim(),
    poll_interval_ms: parseInt($("fPollMs").value, 10) || 1000,
  };
  const rack = $("fRack"); if (rack) dev.rack = parseInt(rack.value, 10) || 0;
  const slot = $("fSlot"); if (slot) dev.slot = parseInt(slot.value, 10) || 0;
  const unit = $("fUnitId"); if (unit) dev.unit_id = parseInt(unit.value, 10) || 1;
  return dev;
}

async function testConnection() {
  const dev = collectDeviceForm();
  if (!dev.name || !dev.address) { setTestResult("testConnResult", false, "Preencha nome e endereço primeiro."); return; }
  setTestResult("testConnResult", null, "Testando…");
  try {
    const res = await api("POST", "/api/test-connection", dev);
    setTestResult("testConnResult", res.ok, res.ok ? "Conectou com sucesso!" : res.error);
  } catch (e) {
    setTestResult("testConnResult", false, e.message);
  }
}
function setTestResult(id, ok, msg) {
  const span = $(id);
  span.textContent = msg;
  span.className = "test-result" + (ok === true ? " test-ok" : ok === false ? " test-fail" : "");
}

async function saveDevice() {
  const dev = collectDeviceForm();
  if (!dev.name || !dev.address) { alert("Nome e endereço são obrigatórios."); return; }
  try {
    await api("POST", "/api/devices", dev);
  } catch (e) {
    alert("Erro ao salvar: " + e.message);
    return;
  }
  closeDeviceModal();
  await refreshDevices();
  selectDevice(dev.name);
}

// ---- add tag modal -----------------------------------------------------------
let tagModalDevice = null;

function openTagModal(dev) {
  tagModalDevice = dev;
  $("tName").value = "";
  $("tAddress").value = "";
  $("tType").value = "";
  $("testReadResult").textContent = "";
  const info = driverInfo(dev.driver);
  $("tAddressHelp").textContent = info ? info.tag_address_help : "";
  $("tAddress").placeholder = info ? info.tag_address_placeholder : "";
  $("tagModalBackdrop").classList.add("open");
}
function closeTagModal() { $("tagModalBackdrop").classList.remove("open"); }

function collectTagForm() {
  return {
    name: $("tName").value.trim(),
    address: $("tAddress").value.trim(),
    type: $("tType").value || undefined,
  };
}

async function testRead() {
  const tag = collectTagForm();
  if (!tag.address) { setTestResult("testReadResult", false, "Preencha o endereço primeiro."); return; }
  setTestResult("testReadResult", null, "Lendo…");
  try {
    const res = await api("POST", "/api/test-read", { device: deviceConfigFor(tagModalDevice), tag });
    setTestResult("testReadResult", res.ok, res.ok ? `Valor lido: ${fmtValue(res.value)}` : res.error);
  } catch (e) {
    setTestResult("testReadResult", false, e.message);
  }
}

function deviceConfigFor(dev) {
  // dev here is a manager.DeviceStatus (from /api/devices); it already
  // carries every field the driver needs (address, rack, slot, unit_id).
  return {
    name: dev.name, driver: dev.driver, address: dev.address,
    rack: dev.rack, slot: dev.slot, unit_id: dev.unit_id,
    poll_interval_ms: dev.poll_interval_ms,
  };
}

async function saveTag() {
  const tag = collectTagForm();
  if (!tag.name || !tag.address) { alert("Nome e endereço da tag são obrigatórios."); return; }
  try {
    await api("POST", `/api/devices/${encodeURIComponent(tagModalDevice.name)}/tags`, tag);
  } catch (e) {
    alert("Erro ao adicionar tag: " + e.message);
    return;
  }
  closeTagModal();
  await refreshTags();
}

// ---- discovery modal -----------------------------------------------------------
async function openDiscoverModal() {
  $("discoverModalBackdrop").classList.add("open");
  $("discoverTable").style.display = "none";
  $("discoverStatus").textContent = "Procurando na rede local… isso pode levar alguns segundos.";
  $("discoverStatus").className = "test-result";
  try {
    const res = await api("GET", "/api/discover");
    renderDiscoverResults(res);
  } catch (e) {
    $("discoverStatus").textContent = "Erro na varredura: " + e.message;
    $("discoverStatus").className = "test-result test-fail";
  }
}
function closeDiscoverModal() { $("discoverModalBackdrop").classList.remove("open"); }

function renderDiscoverResults(res) {
  const found = res.found || [];
  if (!found.length) {
    $("discoverStatus").textContent = `Nenhum PLC encontrado nas redes ${res.networks.join(", ") || "locais"}. Verifique se a máquina está na mesma rede da fábrica.`;
    return;
  }
  $("discoverStatus").textContent = `${found.length} porta(s) aberta(s) encontrada(s):`;
  const body = $("discoverBody");
  body.innerHTML = "";
  for (const f of found) {
    const tr = el("tr");
    tr.appendChild(el("td", null, f.ip));
    tr.appendChild(el("td", null, String(f.port)));
    tr.appendChild(el("td", null, f.label));
    const useTd = el("td");
    const useBtn = el("button", "btn btn-small btn-ghost", "Usar");
    useBtn.addEventListener("click", () => {
      closeDiscoverModal();
      openDeviceModal();
      $("fDriver").value = f.driver;
      renderDriverFields();
      $("fAddress").value = (f.driver === "modbus" || f.driver === "mitsubishi") ? `${f.ip}:${f.port}` : f.ip;
    });
    useTd.appendChild(useBtn);
    tr.appendChild(useTd);
    body.appendChild(tr);
  }
  $("discoverTable").style.display = "";
}

// ---- live updates (SSE) -----------------------------------------------------------
function connectEvents() {
  const es = new EventSource("/events");
  es.onmessage = (msg) => {
    let payload;
    try { payload = JSON.parse(msg.data); } catch { return; }
    if (payload.device === currentDevice) {
      const t = currentTags.find((x) => x.name === payload.tag);
      if (t) {
        t.value = payload.value; t.quality = payload.quality; t.timestamp = payload.timestamp; t.error = payload.error;
        patchTagRow(t);
      }
    }
    const dev = devices.find((d) => d.name === payload.device);
    if (dev) dev.connected = payload.quality === "good";
  };
  es.onerror = () => { /* browser auto-reconnects EventSource */ };
}

function patchTagRow(t) {
  const body = $("tagTableBody");
  if (!body) return;
  const row = body.querySelector(`tr[data-tag="${CSS.escape(t.name)}"]`);
  if (!row) return;
  row.querySelector('[data-role="value"]').textContent = fmtValue(t.value);
  const qCell = row.querySelector('[data-role="quality"]');
  qCell.innerHTML = "";
  qCell.appendChild(el("span", "quality-badge quality-" + t.quality, qualityLabel(t.quality)));
  row.querySelector('[data-role="timestamp"]').textContent = fmtTime(t.timestamp);
}

// ---- wiring -----------------------------------------------------------
async function init() {
  const session = await fetch("/api/session").then((r) => r.json());
  if (session.auth_required && !session.authenticated) {
    location.href = "/login.html?next=" + encodeURIComponent(location.pathname);
    return;
  }
  if (session.auth_required) {
    const logoutBtn = el("button", "btn btn-ghost", "Sair");
    logoutBtn.addEventListener("click", async () => {
      await fetch("/api/logout", { method: "POST" });
      location.href = "/login.html";
    });
    $("summary").insertAdjacentElement("afterend", logoutBtn);
  }

  drivers = await api("GET", "/api/drivers");
  populateDriverSelect();
  $("fDriver").addEventListener("change", renderDriverFields);

  $("btnAddDevice").addEventListener("click", openDeviceModal);
  $("closeDeviceModal").addEventListener("click", closeDeviceModal);
  $("cancelDeviceModal").addEventListener("click", closeDeviceModal);
  $("btnTestConn").addEventListener("click", testConnection);
  $("saveDeviceBtn").addEventListener("click", saveDevice);

  $("closeTagModal").addEventListener("click", closeTagModal);
  $("cancelTagModal").addEventListener("click", closeTagModal);
  $("btnTestRead").addEventListener("click", testRead);
  $("saveTagBtn").addEventListener("click", saveTag);

  $("btnDiscover").addEventListener("click", openDiscoverModal);
  $("closeDiscoverModal").addEventListener("click", closeDiscoverModal);
  $("closeDiscoverModal2").addEventListener("click", closeDiscoverModal);

  await refreshDevices();
  connectEvents();
  setInterval(refreshDevices, 3000);
}

init();
