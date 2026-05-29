'use strict';
// Run: node --test internal/api/static/timelinewindow.test.js
const { test } = require('node:test');
const assert = require('node:assert/strict');
const TLW = require('./timelinewindow.js');

test('constants have the spec values', () => {
  assert.equal(TLW.SECONDS_PER_DAY, 86400);
  assert.equal(TLW.MIN_SPAN, 1800);
  assert.equal(TLW.DEFAULT_NARROW_SPAN, 10800);
  assert.equal(TLW.LIVE_MARGIN, 300);
  assert.equal(TLW.MIN_EVENT_SEC, 1);
  assert.equal(TLW.WIDE_MIN_PX, 640);
});

test('clampSpan bounds span to [MIN_SPAN, day]', () => {
  assert.equal(TLW.clampSpan(0), 1800);
  assert.equal(TLW.clampSpan(1000), 1800);
  assert.equal(TLW.clampSpan(10800), 10800);
  assert.equal(TLW.clampSpan(999999), 86400);
});

test('secToPctRaw is NOT clamped (returns <0 and >1 off-window)', () => {
  const w = TLW.makeWindow(50400, 61200); // 14:00-17:00, span 10800
  assert.equal(TLW.secToPctRaw(w, 50400), 0);
  assert.equal(TLW.secToPctRaw(w, 61200), 1);
  assert.equal(TLW.secToPctRaw(w, 55800), 0.5);
  assert.ok(TLW.secToPctRaw(w, 0) < 0);       // before window
  assert.ok(TLW.secToPctRaw(w, 86400) > 1);   // after window
});

test('isSecInView agrees at boundaries', () => {
  const w = TLW.makeWindow(50400, 61200);
  assert.equal(TLW.isSecInView(w, 50400), true);
  assert.equal(TLW.isSecInView(w, 61200), true);
  assert.equal(TLW.isSecInView(w, 50399), false);
  assert.equal(TLW.isSecInView(w, 61201), false);
});

test('pctToSec <-> secToPctRaw round-trip', () => {
  const w = TLW.makeWindow(50400, 61200);
  for (const pct of [0, 0.25, 0.5, 0.75, 1]) {
    const sec = TLW.pctToSec(w, pct);
    assert.ok(Math.abs(TLW.secToPctRaw(w, sec) - pct) < 1e-9);
  }
});

test('setWindow clamps span and keeps window inside the day', () => {
  // start before 0 -> pinned to 0
  let w = TLW.setWindow(-1000, 10800);
  assert.equal(w.start, 0);
  assert.equal(w.end, 10800);
  // start past the end -> pinned so end == day, span preserved
  w = TLW.setWindow(90000, 10800);
  assert.equal(w.end, 86400);
  assert.equal(w.end - w.start, 10800);
  // span clamped up to MIN_SPAN
  w = TLW.setWindow(0, 10);
  assert.equal(w.end - w.start, 1800);
});

test('panBy shifts the window, preserves span, stays in day', () => {
  const w0 = TLW.makeWindow(50400, 61200); // span 10800
  const w1 = TLW.panBy(w0, 3600);
  assert.equal(w1.start, 54000);
  assert.equal(w1.end - w1.start, 10800);
  // pan past midnight clamps, span preserved
  const w2 = TLW.panBy(w0, 999999);
  assert.equal(w2.end, 86400);
  assert.equal(w2.end - w2.start, 10800);
});

test('zoomAt keeps the anchored time invariant in the interior', () => {
  const w0 = TLW.makeWindow(0, 86400); // full day
  const anchorPct = 0.5; // 43200s
  const w1 = TLW.zoomAt(w0, anchorPct, 0.25); // zoom in 4x -> span 21600
  assert.equal(w1.end - w1.start, 21600);
  // 43200 still sits at pct 0.5
  assert.ok(Math.abs(TLW.secToPctRaw(w1, 43200) - 0.5) < 1e-9);
});

test('zoomAt clamps span and preserves span at edges (no squash)', () => {
  const w0 = TLW.makeWindow(0, 3600); // span 3600
  // zoom OUT hugely -> clamps to full day
  const wOut = TLW.zoomAt(w0, 0.5, 1000);
  assert.equal(wOut.start, 0);
  assert.equal(wOut.end, 86400);
  // zoom IN below MIN_SPAN -> clamps to MIN_SPAN
  const wIn = TLW.zoomAt(w0, 0.5, 0.0001);
  assert.equal(wIn.end - wIn.start, 1800);
  // anchor near the left edge: span still preserved after edge clamp
  const wEdge = TLW.zoomAt(TLW.makeWindow(0, 86400), 0.0, 0.25);
  assert.equal(wEdge.start, 0);
  assert.equal(wEdge.end - wEdge.start, 21600);
});

test('followLiveWindow pins end at now+LIVE_MARGIN, preserves span', () => {
  const w = TLW.followLiveWindow(50400, 10800); // now 14:00
  assert.equal(w.end, 50700); // 14:00 + 5min
  assert.equal(w.end - w.start, 10800);
});

test('followLiveWindow clamps at midnight', () => {
  const w = TLW.followLiveWindow(86300, 10800); // ~23:58:20 + margin > day
  assert.equal(w.end, 86400);
  assert.equal(w.end - w.start, 10800);
});

test('followLiveWindow early in the day pins start to 0', () => {
  const w = TLW.followLiveWindow(600, 10800); // 00:10
  assert.equal(w.start, 0);
  assert.equal(w.end, 10800);
});

test('isWideTimeline: 640px boundary AND pointer fine', () => {
  assert.equal(TLW.isWideTimeline(639, true), false);
  assert.equal(TLW.isWideTimeline(640, true), true);
  assert.equal(TLW.isWideTimeline(641, true), true);
  assert.equal(TLW.isWideTimeline(1200, false), false); // wide but coarse pointer
  assert.equal(TLW.isWideTimeline(300, true), false);
});

test('defaultWindow wide -> full day, no follow', () => {
  const d = TLW.defaultWindow({ wide: true, isToday: true, nowSec: 50400, latestActivitySec: null });
  assert.equal(d.start, 0);
  assert.equal(d.end, 86400);
  assert.equal(d.followLive, false);
});

test('defaultWindow narrow + today -> followLiveWindow + follow', () => {
  const d = TLW.defaultWindow({ wide: false, isToday: true, nowSec: 50400, latestActivitySec: null });
  assert.equal(d.end - d.start, 10800);
  assert.equal(d.end, 50700); // matches followLiveWindow(50400, 10800)
  assert.equal(d.followLive, true);
});

test('defaultWindow narrow + past + activity -> centered on activity', () => {
  const d = TLW.defaultWindow({ wide: false, isToday: false, nowSec: 0, latestActivitySec: 43200 });
  assert.equal(d.end - d.start, 10800);
  assert.equal(d.start, 43200 - 5400); // centered: 37800
  assert.equal(d.followLive, false);
});

test('defaultWindow narrow + past + no activity -> centered on midday', () => {
  const d = TLW.defaultWindow({ wide: false, isToday: false, nowSec: 0, latestActivitySec: null });
  assert.equal(d.start, 43200 - 5400);
  assert.equal(d.followLive, false);
});

test('shouldResetView gates triggers (refresh never resets)', () => {
  for (const t of ['init', 'daychange', 'return-live', 'viewport-cross']) {
    assert.equal(TLW.shouldResetView(t), true, t);
  }
  assert.equal(TLW.shouldResetView('refresh'), false);
  assert.equal(TLW.shouldResetView('whatever'), false);
});
