import {
  METRICS, KPI_ORDER, formatMetric, delta, formatDelta, SlotAssigner, bucketLabel, WEEKDAYS, heatGrid, funnel,
} from './format.js';
import { PERIODS, fromSearch, toSearch, apiParams } from './state.js';

const MAX_SERIES = 8; // categorical palette size; more series fold into the top-N limit
const $ = (sel) => document.querySelector(sel);
const form = $('#filters');
const slots = new SlotAssigner(MAX_SERIES);
const charts = {};
let state = fromSearch(location.search);
let refreshTimer = null;
let lastData = null;

// ---------- theme ----------
function css(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}
function seriesColor(key) {
  const s = slots.slot(key);
  return s < 0 ? css('--other') : css(`--s${s + 1}`);
}
function initTheme() {
  try {
    const t = localStorage.getItem('proview-theme');
    if (t) document.documentElement.dataset.theme = t;
  } catch { /* storage unavailable */ }
  $('#theme').addEventListener('click', () => {
    const dark = document.documentElement.dataset.theme
      ? document.documentElement.dataset.theme === 'dark'
      : matchMedia('(prefers-color-scheme: dark)').matches;
    document.documentElement.dataset.theme = dark ? 'light' : 'dark';
    try { localStorage.setItem('proview-theme', document.documentElement.dataset.theme); } catch { /* ignore */ }
    if (lastData) render(lastData);
  });
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => lastData && render(lastData));
}

// ---------- filters ----------
function initFilters() {
  const period = $('#f-period');
  for (const [k, p] of Object.entries(PERIODS)) period.add(new Option(p.label, k));
  const metric = $('#f-metric');
  for (const [k, m] of Object.entries(METRICS)) metric.add(new Option(m.label, k));
  writeForm();
  form.addEventListener('change', () => {
    readForm();
    history.replaceState(null, '', toSearch(state) || location.pathname);
    load();
  });
  document.querySelectorAll('#dim-tabs button').forEach((b) => b.addEventListener('click', () => {
    state.dimension = b.dataset.dim;
    history.replaceState(null, '', toSearch(state) || location.pathname);
    load();
  }));
  document.querySelector('[data-table-toggle="ts"]').addEventListener('click', (e) => {
    const t = $('#table-ts');
    t.hidden = !t.hidden;
    e.target.textContent = t.hidden ? 'Таблица' : 'График';
    $('#chart-ts').hidden = !t.hidden;
    if (t.hidden) charts.ts?.resize();
  });
}

function writeForm() {
  for (const el of form.elements) {
    if (el.name && state[el.name] !== undefined) {
      if (el.tagName === 'SELECT' && el.dataset.dim && state[el.name] && ![...el.options].some((o) => o.value === state[el.name])) {
        el.add(new Option(state[el.name], state[el.name]));
      }
      el.value = state[el.name];
    }
  }
  form.classList.toggle('custom', state.period === 'custom');
}

function readForm() {
  for (const el of form.elements) if (el.name) state[el.name] = el.value;
  form.classList.toggle('custom', state.period === 'custom');
}

function fillDimension(name, options) {
  const sel = form.querySelector(`select[data-dim="${name}"]`);
  const current = sel.value;
  sel.length = 1;
  for (const o of options) sel.add(new Option(o.label, o.value));
  if (current && ![...sel.options].some((o) => o.value === current)) sel.add(new Option(current, current));
  sel.value = current;
}

// ---------- data ----------
async function api(path, params) {
  const res = await fetch(`api/v1/${path}?${params}`, { headers: { Accept: 'application/json' } });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `${res.status} ${res.statusText}`);
  return body;
}

async function load() {
  if (state.period === 'custom' && (!state.from || !state.to)) {
    setStatus('Укажите начало и конец интервала');
    return;
  }
  setStatus('Загрузка…');
  const base = apiParams(state);
  const ts = new URLSearchParams(base);
  ts.set('group_by', state.group_by);
  ts.set('top', String(MAX_SERIES));
  const bd = new URLSearchParams(base);
  bd.set('dimension', state.dimension);
  bd.set('limit', '20');
  const dims = new URLSearchParams(base);
  for (const k of ['banner', 'placement', 'country', 'device']) dims.delete(k);
  try {
    const [summary, timeseries, breakdown, heatmap, dimensions] = await Promise.all([
      api('summary', base), api('timeseries', ts), api('breakdown', bd), api('heatmap', base), api('dimensions', dims),
    ]);
    for (const [k, v] of Object.entries(dimensions)) fillDimension(k, v);
    lastData = { summary, timeseries, breakdown, heatmap };
    render(lastData);
    setStatus(`Обновлено ${new Date().toLocaleTimeString('ru-RU')}`);
  } catch (err) {
    setStatus(`Ошибка: ${err.message}`, true);
  }
  scheduleRefresh();
}

function scheduleRefresh() {
  clearTimeout(refreshTimer);
  const sec = Number(state.refresh);
  if (sec > 0) refreshTimer = setTimeout(load, sec * 1000);
}

function setStatus(text, error = false) {
  const s = $('#status');
  s.textContent = text;
  s.classList.toggle('error', error);
}

// ---------- rendering ----------
function chart(id) {
  if (!charts[id]) {
    charts[id] = window.echarts.init(document.getElementById(`chart-${id}`), null, { renderer: 'svg' });
  }
  return charts[id];
}

function baseText() {
  return { color: css('--text-secondary'), fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif', fontSize: 12 };
}

function tooltipStyle() {
  return {
    backgroundColor: css('--surface'), borderColor: css('--border'), borderWidth: 1,
    textStyle: { color: css('--text-primary'), fontSize: 12 }, extraCssText: 'box-shadow:0 4px 16px rgba(0,0,0,.12);border-radius:8px;',
  };
}

function axisStyle() {
  return {
    axisLine: { lineStyle: { color: css('--axis') } },
    axisTick: { show: false },
    axisLabel: { color: css('--text-muted'), fontSize: 11 },
    splitLine: { lineStyle: { color: css('--grid'), width: 1 } },
  };
}

function render({ summary, timeseries, breakdown, heatmap }) {
  document.querySelectorAll('#dim-tabs button').forEach((b) => b.setAttribute('aria-selected', String(b.dataset.dim === state.dimension)));
  renderKPIs(summary);
  renderTimeseries(timeseries);
  renderFunnel(summary.current);
  renderHeatmap(heatmap);
  renderBreakdown(breakdown);
}

function renderKPIs({ current, previous }) {
  const root = $('#kpis');
  root.replaceChildren(...KPI_ORDER.map((key) => {
    const d = delta(current[key], previous[key]);
    const el = document.createElement('article');
    el.className = 'kpi';
    const arrow = !d ? '' : d.direction === 'up' ? '▲' : d.direction === 'down' ? '▼' : '■';
    el.innerHTML = `<div class="label"></div><div class="value"></div><div class="delta ${d ? d.direction : ''}"><span aria-hidden="true">${arrow}</span><span></span></div>`;
    el.querySelector('.label').textContent = METRICS[key].label;
    el.querySelector('.value').textContent = formatMetric(key, current[key]);
    el.querySelector('.delta span:last-child').textContent = formatDelta(d);
    return el;
  }));
}

function renderTimeseries(ts) {
  const metric = state.metric;
  const label = METRICS[metric].label;
  $('#ts-title').textContent = `${label} — динамика${state.group_by !== 'none' ? ', по разрезу' : ''}`;
  const c = chart('ts');
  const cats = ts.buckets.map((b) => bucketLabel(b, ts.granularity, state.tz));
  const many = ts.series.length > 1;
  const series = ts.series.map((s) => {
    const color = many ? seriesColor(s.key) : css('--s1');
    const data = s.points.map((p) => p[metric]);
    return {
      name: s.label, type: 'line', data, showSymbol: false, symbolSize: 8, smooth: false,
      lineStyle: { width: 2, color }, itemStyle: { color, borderColor: css('--surface'), borderWidth: 2 },
      emphasis: { focus: 'series' },
      areaStyle: many ? undefined : { color: new window.echarts.graphic.LinearGradient(0, 0, 0, 1, [
        { offset: 0, color: `${color}33` }, { offset: 1, color: `${color}00` }]) },
      endLabel: many && ts.series.length <= 4 ? { show: true, formatter: '{a}', color: css('--text-secondary'), fontSize: 11 } : undefined,
    };
  });
  c.setOption({
    animationDuration: 300,
    textStyle: baseText(),
    grid: { left: 8, right: many && ts.series.length <= 4 ? 120 : 16, top: many ? 36 : 16, bottom: 8, containLabel: true },
    legend: many ? { top: 0, left: 0, icon: 'roundRect', itemWidth: 10, itemHeight: 10, textStyle: { color: css('--text-secondary') } } : { show: false },
    tooltip: {
      trigger: 'axis', ...tooltipStyle(),
      axisPointer: { type: 'line', lineStyle: { color: css('--axis') } },
      valueFormatter: (v) => formatMetric(metric, v),
    },
    xAxis: { type: 'category', data: cats, boundaryGap: false, ...axisStyle(), splitLine: { show: false } },
    yAxis: { type: 'value', ...axisStyle(), axisLine: { show: false },
      axisLabel: { ...axisStyle().axisLabel, formatter: (v) => formatMetric(metric, v, { compact: true }) } },
    series,
  }, true);
  if (!series.length) c.setOption({ graphic: emptyGraphic() });

  // Table view (accessible alternative to the chart).
  const table = document.createElement('table');
  table.innerHTML = '<thead><tr><th>Время</th></tr></thead><tbody></tbody>';
  const head = table.querySelector('thead tr');
  for (const s of ts.series) head.append(Object.assign(document.createElement('th'), { textContent: s.label }));
  const body = table.querySelector('tbody');
  cats.forEach((cat, i) => {
    const tr = document.createElement('tr');
    tr.insertAdjacentHTML('beforeend', '<td></td>');
    tr.lastChild.textContent = cat;
    for (const s of ts.series) {
      tr.insertAdjacentHTML('beforeend', '<td></td>');
      tr.lastChild.textContent = formatMetric(metric, s.points[i][metric]);
    }
    body.append(tr);
  });
  $('#table-ts').replaceChildren(table);
}

function emptyGraphic() {
  return [{ type: 'text', left: 'center', top: 'middle', style: { text: 'Нет данных за выбранный период', fill: css('--text-muted'), fontSize: 13 } }];
}

function renderFunnel(m) {
  const stages = funnel(m);
  const colors = ['--funnel-1', '--funnel-2', '--funnel-3', '--funnel-4'].map(css);
  chart('funnel').setOption({
    textStyle: baseText(),
    grid: { left: 8, right: 150, top: 8, bottom: 8, containLabel: true },
    tooltip: { trigger: 'item', ...tooltipStyle(),
      formatter: (p) => `${p.name}<br><b>${formatMetric('impressions', p.value)}</b> · ${formatMetric('ctr', stages[p.dataIndex].share)} от показов` },
    xAxis: { type: 'value', show: false, max: Math.max(1, stages[0].value) },
    yAxis: { type: 'category', inverse: true, data: stages.map((s) => s.label), ...axisStyle(), axisLine: { show: false },
      axisLabel: { color: css('--text-secondary'), fontSize: 12 } },
    series: [{
      type: 'bar', barWidth: '58%', data: stages.map((s, i) => ({ value: s.value, itemStyle: { color: colors[i], borderRadius: [0, 4, 4, 0] } })),
      label: { show: true, position: 'right', color: css('--text-secondary'), fontSize: 12,
        formatter: (p) => `${formatMetric('impressions', p.value, { compact: true })} · ${formatMetric('ctr', stages[p.dataIndex].share)}` },
    }],
  }, true);
}

function renderHeatmap({ cells, tz }) {
  const metric = state.metric;
  $('#heat-title').textContent = `${METRICS[metric].label} по дням недели и часам (${tz})`;
  const data = heatGrid(cells, metric);
  const max = Math.max(0, ...data.map((d) => d[2]));
  chart('heat').setOption({
    textStyle: baseText(),
    grid: { left: 8, right: 8, top: 8, bottom: 40, containLabel: true },
    tooltip: { ...tooltipStyle(), formatter: (p) => `${WEEKDAYS[p.value[1]]}, ${String(p.value[0]).padStart(2, '0')}:00<br><b>${formatMetric(metric, p.value[2])}</b>` },
    xAxis: { type: 'category', data: [...Array(24).keys()].map((h) => String(h).padStart(2, '0')), ...axisStyle(), splitLine: { show: false }, axisLine: { show: false } },
    yAxis: { type: 'category', inverse: true, data: WEEKDAYS, ...axisStyle(), splitLine: { show: false }, axisLine: { show: false } },
    visualMap: { min: 0, max: max || 1, calculable: false, text: [formatMetric(metric, max, { compact: true }), '0'], orient: 'horizontal', left: 'center', bottom: 0, itemHeight: 140, itemWidth: 10,
      textStyle: { color: css('--text-muted'), fontSize: 11 }, formatter: (v) => formatMetric(metric, v, { compact: true }),
      inRange: { color: ['--seq-0', '--seq-1', '--seq-2', '--seq-3', '--seq-4', '--seq-5'].map(css) } },
    series: [{ type: 'heatmap', data, itemStyle: { borderColor: css('--surface'), borderWidth: 2, borderRadius: 3 },
      emphasis: { itemStyle: { borderColor: css('--text-primary'), borderWidth: 1 } } }],
  }, true);
}

function renderBreakdown({ rows }) {
  const metric = state.metric;
  const sorted = [...rows].sort((a, b) => b.metrics[metric] - a.metrics[metric]).slice(0, 12);
  const c = chart('breakdown');
  c.setOption({
    textStyle: baseText(),
    grid: { left: 8, right: 70, top: 8, bottom: 8, containLabel: true },
    tooltip: { trigger: 'item', ...tooltipStyle(), formatter: (p) => `${p.name}<br><b>${formatMetric(metric, p.value)}</b>` },
    xAxis: { type: 'value', show: false },
    yAxis: { type: 'category', inverse: true, data: sorted.map((r) => r.label), ...axisStyle(), axisLine: { show: false },
      axisLabel: { color: css('--text-secondary'), fontSize: 12, width: 160, overflow: 'truncate' } },
    series: [{ type: 'bar', barMaxWidth: 18, data: sorted.map((r) => r.metrics[metric]),
      itemStyle: { color: css('--s1'), borderRadius: [0, 4, 4, 0] },
      label: { show: true, position: 'right', color: css('--text-secondary'), fontSize: 12, formatter: (p) => formatMetric(metric, p.value, { compact: true }) } }],
    graphic: sorted.length ? [] : emptyGraphic(),
  }, true);

  const cols = ['impressions', 'clicks', 'ctr', 'viewability'];
  const table = document.createElement('table');
  table.innerHTML = `<thead><tr><th>Значение</th>${cols.map(() => '<th></th>').join('')}</tr></thead><tbody></tbody>`;
  table.querySelectorAll('thead th').forEach((th, i) => { if (i) th.textContent = METRICS[cols[i - 1]].label; });
  const body = table.querySelector('tbody');
  for (const r of rows) {
    const tr = document.createElement('tr');
    tr.innerHTML = `<td></td>${cols.map(() => '<td></td>').join('')}`;
    tr.children[0].textContent = r.label;
    cols.forEach((k, i) => { tr.children[i + 1].textContent = formatMetric(k, r.metrics[k]); });
    body.append(tr);
  }
  $('#table-breakdown').replaceChildren(rows.length ? table : Object.assign(document.createElement('div'), { className: 'empty', textContent: 'Нет данных' }));
}

window.addEventListener('resize', () => Object.values(charts).forEach((c) => c.resize()));
initTheme();
initFilters();
load();
