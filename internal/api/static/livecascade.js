'use strict';
// Pure decisions for the non-iOS live-video cascade. No DOM, no timers, no
// globals - so app.js can call these and node --test can verify the exact
// same code path (same pattern as hlsrecovery.js / livehls.js).

// nextWebrtcAction bounds WebRTC reconnect. `attempts` is the count of
// reconnect attempts already started; `maxAttempts` is the cap. The cap is
// strict (attempts >= maxAttempts -> 'fallback'). The caller must derive
// `attempts` from a counter reset only by a genuine ICE 'connected' event,
// never by SDP signaling success, so a STUN-only camera that always answers
// SDP still reaches the cap instead of reconnecting forever.
function nextWebrtcAction(state) {
  return state.attempts >= state.maxAttempts ? 'fallback' : 'reconnect';
}

// liveOverlayState maps server-reported camera status to the overlay shown
// when the cascade has exhausted live transports. apiOnline === true means
// /api/cameras/{name} reports the camera up, so the failure is a transport
// hiccup -> 'reconnecting'. Anything else (false, or null/undefined when the
// status could not be read) -> 'offline'.
function liveOverlayState(state) {
  return state.apiOnline === true ? 'reconnecting' : 'offline';
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { nextWebrtcAction: nextWebrtcAction, liveOverlayState: liveOverlayState };
}
