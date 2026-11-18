(() => {
  'use strict';

  const API = window.FLEET_API || '/api/v1';
  const STATUS = {
    alert:   { label: 'Em alerta',    color: '#f87171', order: 0 },
    moving:  { label: 'Em movimento', color: '#34d399', order: 1 },
    idle:    { label: 'Parado',       color: '#fbbf24', order: 2 },
    offline: { label: 'Sem sinal',    color: '#64748b', order: 3 },
  };
  const SEVERITY = { critical: '#f87171', warning: '#fbbf24', info: '#38bdf8' };
  const ALERT_TYPES = {
    SPEEDING: 'Excesso de velocidade', LOW_FUEL: 'Combustível crítico',
    GEOFENCE_ENTER: 'Entrada em cerca', GEOFENCE_EXIT: 'Saída de cerca',
  };
  const SPEED_LIMIT = 100, FUEL_CRITICAL = 15;

  const $ = (id) => document.getElementById(id);
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const ago = (iso) => {
    const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
    if (s < 60) return `há ${Math.round(s)} s`;
    if (s < 3600) return `há ${Math.round(s / 60)} min`;
    return `há ${Math.round(s / 3600)} h`;
  };
  const fuelColor = (f) => (f <= FUEL_CRITICAL ? '#f87171' : f <= 40 ? '#fbbf24' : '#34d399');

  async function api(path, opts) {
    const res = await fetch(API + path, opts);
    if (!res.ok) throw new Error(`${res.status} ${path}`);
    return res.json();
  }

  // ---- Estado ---------------------------------------------------------------
  const fleet = new Map();
  const markers = new Map();
  let selected = null;
  let routeLayer = null;
  let firstFit = true;

  // ---- Mapa -----------------------------------------------------------------
  const map = L.map('map', { zoomControl: true }).setView([-8.09, -34.91], 12);
  L.tileLayer('https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png', {
    attribution: '&copy; OpenStreetMap &copy; CARTO', maxZoom: 19, subdomains: 'abcd',
  }).addTo(map);

  $('legend').innerHTML = Object.values(STATUS).map((s) =>
    `<li class="flex items-center gap-1.5"><span class="inline-block h-2.5 w-2.5 rounded-full" style="background:${s.color}"></span>${s.label}</li>`).join('');

  function statusOf(v) {
    if (!v.online) return 'offline';
    if (v.fuel_level <= FUEL_CRITICAL || v.speed_kmh > SPEED_LIMIT) return 'alert';
    return v.speed_kmh >= 2 ? 'moving' : 'idle';
  }

  // ---- Frota ----------------------------------------------------------------
  async function refreshFleet() {
    const { data } = await api('/fleet');
    const seen = new Set();
    for (const v of data) {
      seen.add(v.vehicle_id);
      fleet.set(v.vehicle_id, v);
      const color = STATUS[statusOf(v)].color;
      let m = markers.get(v.vehicle_id);
      if (!m) {
        m = L.circleMarker([v.latitude, v.longitude], { radius: 7, weight: 2, fillOpacity: 0.85 }).addTo(map);
        m.bindTooltip(v.vehicle_id);
        m.on('click', () => select(v.vehicle_id));
        markers.set(v.vehicle_id, m);
      }
      m.setLatLng([v.latitude, v.longitude]);
      m.setStyle({ color: v.vehicle_id === selected ? '#ffffff' : color, fillColor: color, radius: v.vehicle_id === selected ? 10 : 7 });
    }
    for (const id of [...markers.keys()]) {
      if (!seen.has(id)) { markers.get(id).remove(); markers.delete(id); fleet.delete(id); }
    }
    if (firstFit && data.length) {
      map.fitBounds(data.map((v) => [v.latitude, v.longitude]), { padding: [40, 40], maxZoom: 13 });
      firstFit = false;
    }
    renderList();
    renderDetail();
  }

  function renderList() {
    const q = $('filter').value.trim().toLowerCase();
    const items = [...fleet.values()]
      .filter((v) => v.vehicle_id.toLowerCase().includes(q))
      .sort((a, b) => STATUS[statusOf(a)].order - STATUS[statusOf(b)].order || a.vehicle_id.localeCompare(b.vehicle_id));
    $('vehicle-count').textContent = `${items.length} de ${fleet.size}`;
    $('vehicle-list').innerHTML = items.map((v) => {
      const st = STATUS[statusOf(v)];
      const active = v.vehicle_id === selected ? 'bg-ink-800' : 'hover:bg-ink-800/60';
      return `<li><button type="button" data-id="${esc(v.vehicle_id)}" class="w-full text-left px-3 py-2 ${active}">
        <div class="flex items-center justify-between">
          <span class="flex items-center gap-2 text-sm font-medium"><span class="h-2 w-2 rounded-full" style="background:${st.color}"></span>${esc(v.vehicle_id)}</span>
          <span class="text-sm text-slate-300">${v.online ? Math.round(v.speed_kmh) + ' km/h' : st.label}</span>
        </div>
        <div class="mt-1.5 h-1 rounded bg-ink-700"><div class="h-1 rounded" style="width:${v.fuel_level}%;background:${fuelColor(v.fuel_level)}"></div></div>
      </button></li>`;
    }).join('') || '<li class="p-4 text-sm text-slate-400">Nenhum veículo encontrado. Inicie o simulador com <code>make sim</code>.</li>';
  }

  // ---- Detalhe e rota ---------------------------------------------------------
  function select(id) {
    selected = id;
    clearRoute();
    const v = fleet.get(id);
    if (v) map.flyTo([v.latitude, v.longitude], Math.max(map.getZoom(), 14), { duration: 0.6 });
    refreshFleet().catch(() => {});
  }

  function clearRoute() {
    if (routeLayer) { routeLayer.remove(); routeLayer = null; }
  }

  async function toggleRoute() {
    if (routeLayer) { clearRoute(); renderDetail(); return; }
    const from = new Date(Date.now() - 30 * 60 * 1000).toISOString();
    const { data } = await api(`/vehicles/${encodeURIComponent(selected)}/history?from=${encodeURIComponent(from)}&limit=2000`);
    if (!data.length) return;
    routeLayer = L.polyline(data.map((p) => [p.latitude, p.longitude]), { color: '#38bdf8', weight: 3, opacity: 0.8 }).addTo(map);
    map.fitBounds(routeLayer.getBounds(), { padding: [50, 50] });
    renderDetail();
  }

  function renderDetail() {
    const el = $('detail');
    const v = selected && fleet.get(selected);
    if (!v) {
      el.innerHTML = '<h2 class="text-sm font-semibold mb-1">Detalhes do veículo</h2><p class="text-sm text-slate-400">Selecione um veículo na lista ou no mapa para ver a telemetria.</p>';
      return;
    }
    const st = STATUS[statusOf(v)];
    const row = (k, val) => `<div class="flex justify-between py-1"><dt class="text-slate-400">${k}</dt><dd>${val}</dd></div>`;
    el.innerHTML = `
      <div class="flex items-center justify-between mb-2">
        <h2 class="text-sm font-semibold">${esc(v.vehicle_id)}</h2>
        <span class="flex items-center gap-1.5 text-xs"><span class="h-2 w-2 rounded-full" style="background:${st.color}"></span>${st.label}</span>
      </div>
      <dl class="text-sm divide-y divide-ink-800">
        ${row('Velocidade', `${v.speed_kmh.toFixed(0)} km/h`)}
        ${row('Combustível', `<span style="color:${fuelColor(v.fuel_level)}">${v.fuel_level.toFixed(0)}%</span>`)}
        ${row('Motor', { on: 'Ligado', off: 'Desligado', idle: 'Ocioso' }[v.engine_status] || esc(v.engine_status))}
        ${row('Posição', `${v.latitude.toFixed(5)}, ${v.longitude.toFixed(5)}`)}
        ${row('Última leitura', ago(v.updated_at))}
      </dl>
      <button type="button" id="route-btn" class="mt-3 w-full rounded-md border border-ink-600 bg-ink-800 hover:bg-ink-700 px-3 py-1.5 text-sm">
        ${routeLayer ? 'Ocultar rota' : 'Mostrar rota dos últimos 30 min'}</button>`;
    $('route-btn').addEventListener('click', () => toggleRoute().catch(() => {}));
  }

  // ---- Indicadores ------------------------------------------------------------
  async function refreshSummary() {
    const s = await api('/metrics/summary');
    $('kpi-online').textContent = `${s.vehicles_online} / ${s.vehicles_total}`;
    $('kpi-moving').textContent = s.vehicles_moving;
    $('kpi-speed').textContent = `${s.avg_speed_kmh.toFixed(0)} km/h`;
    $('kpi-fuel').textContent = s.low_fuel_count;
    $('kpi-alerts').textContent = s.active_alerts;
    $('kpi-msgs').textContent = s.messages_last_minute;
  }

  // ---- Alertas ----------------------------------------------------------------
  async function refreshAlerts() {
    const { data } = await api('/alerts?status=active&limit=40');
    $('alert-count').textContent = data.length ? `${data.length} recentes` : '';
    $('alert-list').innerHTML = data.map((a) => `
      <li class="p-3 border-l-4" style="border-left-color:${SEVERITY[a.severity] || '#64748b'}">
        <div class="flex items-center justify-between text-sm">
          <span class="font-medium">${ALERT_TYPES[a.type] || esc(a.type)}</span>
          <span class="text-xs text-slate-400">${ago(a.created_at)}</span>
        </div>
        <p class="text-sm text-slate-300 mt-0.5">${esc(a.message)}</p>
        <div class="mt-2 flex gap-2 text-xs">
          <button type="button" data-vehicle="${esc(a.vehicle_id)}" class="rounded border border-ink-600 px-2 py-1 hover:bg-ink-800">Ver ${esc(a.vehicle_id)} no mapa</button>
          <button type="button" data-ack="${a.id}" class="rounded border border-ink-600 px-2 py-1 hover:bg-ink-800">Reconhecer</button>
        </div>
      </li>`).join('') || '<li class="p-4 text-sm text-slate-400">Nenhum alerta ativo. Os alertas aparecem aqui assim que uma regra for acionada.</li>';
  }

  // ---- Gráfico ----------------------------------------------------------------
  Chart.defaults.color = '#94a3b8';
  Chart.defaults.font.family = '"IBM Plex Sans", system-ui, sans-serif';
  const chart = new Chart($('chart'), {
    type: 'line',
    data: { labels: [], datasets: [
      { label: 'Velocidade média (km/h)', data: [], borderColor: '#38bdf8', backgroundColor: '#38bdf822', fill: true, tension: 0.3, pointRadius: 0, yAxisID: 'y' },
      { label: 'Mensagens por minuto', data: [], borderColor: '#a78bfa', tension: 0.3, pointRadius: 0, yAxisID: 'y1' },
    ] },
    options: {
      responsive: true, maintainAspectRatio: false, animation: false, interaction: { mode: 'index', intersect: false },
      plugins: { legend: { position: 'bottom', labels: { boxWidth: 10, usePointStyle: true } } },
      scales: {
        x: { grid: { color: '#1e2d4a' } },
        y: { position: 'left', beginAtZero: true, grid: { color: '#1e2d4a' } },
        y1: { position: 'right', beginAtZero: true, grid: { drawOnChartArea: false } },
      },
    },
  });

  async function refreshChart() {
    const { data } = await api('/metrics/speed-series?minutes=30');
    chart.data.labels = data.map((p) => new Date(p.bucket).toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit' }));
    chart.data.datasets[0].data = data.map((p) => +p.avg_speed_kmh.toFixed(1));
    chart.data.datasets[1].data = data.map((p) => p.messages);
    chart.update();
  }

  // ---- Cercas virtuais (carregadas uma vez) -------------------------------------
  async function loadGeofences() {
    const { data } = await api('/geofences');
    for (const g of data) {
      L.circle([g.latitude, g.longitude], { radius: g.radius_m, color: '#38bdf8', weight: 1.5, dashArray: '6 6', fillOpacity: 0.06 })
        .bindTooltip(g.name, { sticky: true }).addTo(map);
    }
  }

  // ---- Eventos ----------------------------------------------------------------
  $('filter').addEventListener('input', renderList);
  $('vehicle-list').addEventListener('click', (e) => {
    const btn = e.target.closest('[data-id]');
    if (btn) select(btn.dataset.id);
  });
  $('alert-list').addEventListener('click', async (e) => {
    const ack = e.target.closest('[data-ack]');
    if (ack) {
      ack.disabled = true;
      try { await api(`/alerts/${ack.dataset.ack}/ack`, { method: 'POST' }); } catch (_) { /* já reconhecido */ }
      refreshAlerts().catch(() => {}); refreshSummary().catch(() => {});
      return;
    }
    const veh = e.target.closest('[data-vehicle]');
    if (veh) select(veh.dataset.vehicle);
  });

  // ---- Polling sequencial (sem sobreposição) e indicador de conexão ---------------
  const health = {};
  function setLive() {
    const ok = Object.values(health).every(Boolean);
    $('live-dot').className = `inline-block h-2.5 w-2.5 rounded-full ${ok ? 'bg-emerald-400' : 'bg-red-400'}`;
    $('live-label').textContent = ok ? 'Ao vivo' : 'Sem conexão com a API';
  }
  function poll(name, fn, ms) {
    const tick = async () => {
      try { await fn(); health[name] = true; } catch (_) { health[name] = false; }
      setLive();
      setTimeout(tick, ms);
    };
    tick();
  }

  loadGeofences().catch(() => {});
  poll('fleet', refreshFleet, 2000);
  poll('summary', refreshSummary, 3000);
  poll('alerts', refreshAlerts, 3000);
  poll('chart', refreshChart, 15000);
  renderDetail();
})();
