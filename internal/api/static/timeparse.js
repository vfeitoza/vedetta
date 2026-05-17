'use strict';
// Interpret the camera.html ?t= query parameter.
//
// Returns a Date for a deliberate recording-playback deep-link, or null to
// mean "no playback target: start the live view". Every Vedetta link and
// notification generator emits the timestamp as an ISO 8601 string, so that
// is the ONLY shape treated as a real playback request. A bare epoch-digit
// value never comes from a deliberate action - it is launch-URL cruft (e.g.
// frozen into an iOS "Add to Home Screen" icon) - and any unparseable value
// is likewise not a valid target. All of those return null so the page falls
// through to the live stream instead of stranding on the snapshot loop.
function resolvePlaybackTime(raw) {
  if (raw === null || raw === undefined) return null;
  var s = String(raw).trim();
  if (s === '') return null;
  // Bare epoch digits are accidental (cache-bust / frozen launch URL), never
  // a deliberate deep-link in any current generator. Treat as "go live".
  if (/^\d+$/.test(s)) return null;
  var d = new Date(s);
  return isNaN(d.getTime()) ? null : d;
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { resolvePlaybackTime: resolvePlaybackTime };
}
