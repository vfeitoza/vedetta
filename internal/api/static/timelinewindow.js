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

if (typeof module !== 'undefined' && module.exports) {
  module.exports = TimelineWindow;
}
