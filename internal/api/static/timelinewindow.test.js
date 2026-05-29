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
