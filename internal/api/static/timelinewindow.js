'use strict';
// Pure timeline-window math for the camera page. NO DOM, no Date/Math.random in
// outputs. A "window" is { start, end } in seconds-of-day. Every helper takes a
// window and returns a NEW window; nothing mutates its input. Tested with
// `node --test` (see timelinewindow.test.js); attached to the global
// `TimelineWindow` for the browser, mirroring timeparse.js.

var TimelineWindow = {};

TimelineWindow.SECONDS_PER_DAY = 86400;
TimelineWindow.MIN_SPAN = 1800; // 30 min: tightest zoom
TimelineWindow.DEFAULT_NARROW_SPAN = 10800; // 3 h: mobile default window
TimelineWindow.LIVE_MARGIN = 300; // 5 min empty lead kept right of the playhead
TimelineWindow.MIN_EVENT_SEC = 1; // floor width for a zero-length event
TimelineWindow.WIDE_MIN_PX = 640; // track width at/above which we default full-day

function clamp(v, lo, hi) { return v < lo ? lo : (v > hi ? hi : v); }

TimelineWindow.clampSpan = function (span) {
  return clamp(span, TimelineWindow.MIN_SPAN, TimelineWindow.SECONDS_PER_DAY);
};

TimelineWindow.makeWindow = function (start, end) {
  return { start: start, end: end };
};

TimelineWindow.secToPctRaw = function (win, sec) {
  return (sec - win.start) / (win.end - win.start);
};

TimelineWindow.isSecInView = function (win, sec) {
  return sec >= win.start && sec <= win.end;
};

TimelineWindow.pctToSec = function (win, pct) {
  return win.start + pct * (win.end - win.start);
};

TimelineWindow.setWindow = function (start, span) {
  span = TimelineWindow.clampSpan(span);
  start = clamp(start, 0, TimelineWindow.SECONDS_PER_DAY - span);
  return TimelineWindow.makeWindow(start, start + span);
};

TimelineWindow.panBy = function (win, deltaSec) {
  return TimelineWindow.setWindow(win.start + deltaSec, win.end - win.start);
};

TimelineWindow.zoomAt = function (win, anchorPct, factor) {
  var span = win.end - win.start;
  var anchorSec = win.start + anchorPct * span;
  var newSpan = TimelineWindow.clampSpan(span * factor);
  // Keep anchorSec under anchorPct; setWindow shifts (not squashes) at edges so
  // the span is always preserved even when the anchor is clamped against 0/day.
  return TimelineWindow.setWindow(anchorSec - anchorPct * newSpan, newSpan);
};

TimelineWindow.followLiveWindow = function (nowSec, span) {
  span = TimelineWindow.clampSpan(span);
  var end = Math.min(TimelineWindow.SECONDS_PER_DAY, nowSec + TimelineWindow.LIVE_MARGIN);
  var start = Math.max(0, end - span);
  end = Math.min(TimelineWindow.SECONDS_PER_DAY, start + span);
  return TimelineWindow.makeWindow(start, end);
};

TimelineWindow.isWideTimeline = function (trackWidthPx, pointerFine) {
  return trackWidthPx >= TimelineWindow.WIDE_MIN_PX && !!pointerFine;
};

// opts: { wide, isToday, nowSec, latestActivitySec }
// Returns { start, end, followLive }. Pure: caller supplies wide/now/activity.
TimelineWindow.defaultWindow = function (opts) {
  if (opts.wide) {
    return { start: 0, end: TimelineWindow.SECONDS_PER_DAY, followLive: false };
  }
  var span = TimelineWindow.DEFAULT_NARROW_SPAN;
  if (opts.isToday) {
    var live = TimelineWindow.followLiveWindow(opts.nowSec, span);
    return { start: live.start, end: live.end, followLive: true };
  }
  var center = (opts.latestActivitySec != null)
    ? opts.latestActivitySec
    : TimelineWindow.SECONDS_PER_DAY / 2;
  var w = TimelineWindow.setWindow(center - span / 2, span);
  return { start: w.start, end: w.end, followLive: false };
};

TimelineWindow.shouldResetView = function (trigger) {
  return trigger === 'init' || trigger === 'daychange'
    || trigger === 'return-live' || trigger === 'viewport-cross';
};

if (typeof module !== 'undefined' && module.exports) {
  module.exports = TimelineWindow;
}
