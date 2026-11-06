import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  formatInt, formatPct, formatDuration, formatMetric, delta, formatDelta, SlotAssigner, heatGrid, funnel, formatCompact,
} from './format.js';
import { fromSearch, toSearch, apiParams } from './state.js';

const nbsp = (s) => s.replace(/ | /g, ' ');

test('number formatting', () => {
  assert.equal(nbsp(formatInt(1234567)), '1 234 567');
  assert.equal(formatPct(0.0234), '2,34 %');
  assert.equal(formatDuration(450), '450 мс');
  assert.equal(formatDuration(4500), '4,5 с');
  assert.equal(formatDuration(125000), '2 мин 5 с');
  assert.equal(formatMetric('ctr', 0.5), '50,00 %');
  assert.equal(formatMetric('avg_hover_ms', 1200), '1,2 с');
  assert.equal(formatCompact(999), '999');
  assert.match(nbsp(formatCompact(1500000)), /1,5/);
});

test('delta', () => {
  assert.equal(delta(10, 0), null);
  assert.equal(delta(110, 100).direction, 'up');
  assert.equal(delta(90, 100).direction, 'down');
  assert.equal(delta(100, 100).direction, 'flat');
  assert.equal(formatDelta(delta(110, 100)), '+10,0 % к прошлому периоду');
  assert.match(formatDelta(null), /нет данных/);
});

test('slot assignment follows the entity', () => {
  const s = new SlotAssigner(3);
  assert.equal(s.slot('a'), 0);
  assert.equal(s.slot('b'), 1);
  assert.equal(s.slot('a'), 0, 'stable on repeat');
  assert.equal(s.slot('c'), 2);
  assert.equal(s.slot('d'), -1, 'overflow folds to other');
});

test('heat grid is dense 7x24', () => {
  const g = heatGrid([{ dow: 1, hour: 0, metrics: { ctr: 0.1 } }, { dow: 7, hour: 23, metrics: { ctr: 0.2 } }], 'ctr');
  assert.equal(g.length, 168);
  assert.deepEqual(g[0], [0, 0, 0.1]);
  assert.deepEqual(g[167], [23, 6, 0.2]);
});

test('funnel shares', () => {
  const f = funnel({ impressions: 200, viewable: 150, hovers: 40, clicks: 4 });
  assert.deepEqual(f.map((x) => x.share), [1, 0.75, 0.2, 0.02]);
  assert.equal(funnel({}).every((x) => x.share === 0), true);
});

test('state round-trips through the URL', () => {
  const s = fromSearch('?period=7d&group_by=banner&country=RU&bogus=1');
  assert.equal(s.period, '7d');
  assert.equal(s.metric, 'impressions');
  assert.equal(toSearch(s), '?period=7d&group_by=banner&country=RU');
  assert.equal(fromSearch('?period=nope').period, '24h');
  assert.equal(toSearch(fromSearch('')), '');
});

test('api params', () => {
  const now = Date.UTC(2026, 10, 6, 12, 0, 0);
  const q = apiParams({ ...fromSearch('?period=1h&banner=3&granularity=minute') }, now);
  assert.equal(q.get('from'), '2026-11-06T11:00:00Z');
  assert.equal(q.get('to'), '2026-11-06T12:00:00Z');
  assert.equal(q.get('banner'), '3');
  assert.equal(q.get('granularity'), 'minute');
  const c = apiParams(fromSearch('?period=custom&from=2026-11-01&to=2026-11-03'), now);
  assert.equal(c.get('from'), '2026-11-01');
});
