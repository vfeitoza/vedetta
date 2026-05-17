'use strict';
// Decide what startNativeHLS should do when its native-iOS-HLS <video>
// element fires an `error`.
//
// iOS Safari suspends a backgrounded tab for tens of seconds (lock screen,
// app switch). On resume AVPlayer requests the media segment it had queued;
// if the live window already evicted that id the request 404s and AVPlayer
// fires `error`. Before this decision existed, a post-start error went
// straight to the escalate/snapshot cascade and the page stranded on ~2s
// snapshots forever. A post-start error must instead first attempt one
// live-HLS restart (reload the playlist, resync to the live edge) so a
// recoverable suspend/resume stall recovers to live video.
//
// Returns one of:
//   'warmup-retry' - not playing yet, still inside the cold-start budget
//   'restart'      - was playing, spend one live-HLS restart to resync
//   'escalate'     - give up this attempt (step quality tier, then snapshots)
function nextHlsErrorAction(state) {
  if (!state.started) {
    return state.warmupAttempts < state.maxWarmupRetries ? 'warmup-retry' : 'escalate';
  }
  return state.restartsUsed < state.maxRestarts ? 'restart' : 'escalate';
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { nextHlsErrorAction: nextHlsErrorAction };
}
