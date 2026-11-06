// Pure formatting and metric helpers (unit-tested with `node --test`).

export const METRICS = {
  impressions:   { label: 'Показы',               kind: 'int' },
  viewable:      { label: 'Видимые показы',       kind: 'int' },
  clicks:        { label: 'Клики',                kind: 'int' },
  ctr:           { label: 'CTR',                  kind: 'pct' },
  viewability:   { label: 'Видимость',            kind: 'pct' },
  hover_rate:    { label: 'Доля наведений',       kind: 'pct' },
  avg_hover_ms:  { label: 'Ср. время наведения',  kind: 'dur' },
  avg_active_ms: { label: 'Ср. активное время',   kind: 'dur' },
  visitors:      { label: 'Уникальные посетители', kind: 'int' },
};

export const KPI_ORDER = ['impressions', 'viewability', 'clicks', 'ctr', 'avg_hover_ms', 'avg_active_ms', 'visitors'];

const intFmt = new Intl.NumberFormat('ru-RU');
const compactFmt = new Intl.NumberFormat('ru-RU', { notation: 'compact', maximumFractionDigits: 1 });

export function formatInt(v) {
  return intFmt.format(Math.round(v || 0));
}

export function formatCompact(v) {
  return Math.abs(v) < 10000 ? formatInt(v) : compactFmt.format(v);
}

export function formatPct(v, digits = 2) {
  return `${((v || 0) * 100).toFixed(digits).replace('.', ',')} %`;
}

export function formatDuration(ms) {
  const v = ms || 0;
  if (v < 1000) return `${Math.round(v)} мс`;
  if (v < 60000) return `${(v / 1000).toFixed(1).replace('.', ',')} с`;
  const m = Math.floor(v / 60000);
  const s = Math.round((v % 60000) / 1000);
  return `${m} мин ${s} с`;
}

export function formatMetric(key, v, { compact = false } = {}) {
  const kind = METRICS[key]?.kind ?? 'int';
  if (kind === 'pct') return formatPct(v);
  if (kind === 'dur') return formatDuration(v);
  return compact ? formatCompact(v) : formatInt(v);
}

// delta compares current with previous. Returns null when there is no base.
export function delta(cur, prev) {
  if (!prev) return null;
  const change = (cur - prev) / prev;
  return { change, direction: change > 0.0005 ? 'up' : change < -0.0005 ? 'down' : 'flat' };
}

export function formatDelta(d) {
  if (!d) return 'нет данных за прошлый период';
  const sign = d.change > 0 ? '+' : '';
  return `${sign}${(d.change * 100).toFixed(1).replace('.', ',')} % к прошлому периоду`;
}

// Color follows the entity, never its rank: a key keeps its slot for the
// whole session, even when filters change which series are visible.
export class SlotAssigner {
  constructor(size = 8) {
    this.size = size;
    this.slots = new Map();
  }
  slot(key) {
    if (!this.slots.has(key)) {
      const used = new Set(this.slots.values());
      let s = 0;
      while (used.has(s) && s < this.size) s++;
      this.slots.set(key, s < this.size ? s : -1); // -1 = "other" (neutral)
    }
    return this.slots.get(key);
  }
}

// Labels for bucket timestamps by granularity.
export function bucketLabel(unix, granularity, timeZone) {
  const d = new Date(unix * 1000);
  const opts = granularity === 'day'
    ? { day: '2-digit', month: 'short' }
    : granularity === 'hour'
      ? { day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit' }
      : { hour: '2-digit', minute: '2-digit' };
  return new Intl.DateTimeFormat('ru-RU', { ...opts, timeZone }).format(d);
}

export const WEEKDAYS = ['Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб', 'Вс'];

// heatGrid turns API cells into a dense 7x24 matrix [hour, dowIndex, value].
export function heatGrid(cells, metric) {
  const byKey = new Map(cells.map((c) => [`${c.dow}:${c.hour}`, c.metrics[metric] ?? 0]));
  const out = [];
  for (let dow = 1; dow <= 7; dow++) {
    for (let h = 0; h < 24; h++) out.push([h, dow - 1, byKey.get(`${dow}:${h}`) ?? 0]);
  }
  return out;
}

// funnel builds the impression -> viewable -> hover -> click stages with
// step conversion relative to the first stage.
export function funnel(m) {
  const stages = [
    ['Показы', m.impressions],
    ['Видимые', m.viewable],
    ['С наведением', m.hovers],
    ['Клики', m.clicks],
  ];
  const base = m.impressions || 0;
  return stages.map(([label, value]) => ({ label, value: value || 0, share: base ? (value || 0) / base : 0 }));
}
