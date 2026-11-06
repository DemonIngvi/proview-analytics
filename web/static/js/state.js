// Dashboard state <-> URL query string, so every view is a shareable link.

export const PERIODS = {
  '1h': { label: 'Последний час', ms: 3600e3 },
  '24h': { label: 'Последние 24 часа', ms: 24 * 3600e3 },
  '7d': { label: 'Последние 7 дней', ms: 7 * 24 * 3600e3 },
  '30d': { label: 'Последние 30 дней', ms: 30 * 24 * 3600e3 },
  '90d': { label: 'Последние 90 дней', ms: 90 * 24 * 3600e3 },
  custom: { label: 'Свой интервал…', ms: 0 },
};

export const DEFAULTS = {
  period: '24h',
  from: '',
  to: '',
  granularity: '',
  group_by: 'none',
  metric: 'impressions',
  dimension: 'banner',
  tz: 'UTC',
  banner: '',
  placement: '',
  country: '',
  device: '',
  refresh: '0',
};

const KEYS = Object.keys(DEFAULTS);

export function fromSearch(search, defaults = DEFAULTS) {
  const q = new URLSearchParams(search);
  const s = { ...defaults };
  for (const k of KEYS) {
    if (q.has(k)) s[k] = q.get(k);
  }
  if (!PERIODS[s.period]) s.period = defaults.period;
  return s;
}

export function toSearch(state) {
  const q = new URLSearchParams();
  for (const k of KEYS) {
    if (state[k] !== DEFAULTS[k] && state[k] !== '') q.set(k, state[k]);
  }
  const str = q.toString();
  return str ? `?${str}` : '';
}

// apiParams builds the analytics API query for the current state.
export function apiParams(state, now = Date.now()) {
  const q = new URLSearchParams();
  if (state.period === 'custom' && state.from && state.to) {
    q.set('from', state.from);
    q.set('to', state.to);
  } else {
    const p = PERIODS[state.period] ?? PERIODS['24h'];
    q.set('from', new Date(now - p.ms).toISOString().replace(/\.\d{3}Z$/, 'Z'));
    q.set('to', new Date(now).toISOString().replace(/\.\d{3}Z$/, 'Z'));
  }
  q.set('tz', state.tz || 'UTC');
  if (state.granularity) q.set('granularity', state.granularity);
  for (const k of ['banner', 'placement', 'country', 'device']) {
    if (state[k]) q.set(k, state[k]);
  }
  return q;
}
