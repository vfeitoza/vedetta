// Watchpost — Control Room Noir
// Vanilla JS for WebRTC, timeline, keyboard shortcuts, and UI interactions

'use strict';

// ─── State ───
let peerConnection = null;
let currentStream = null; // 'webrtc' | 'mjpeg' | null
let playbackMode = false; // true when playing back a recording
let timelineDate = new Date();
let calendarDate = new Date();

// ─── Helpers ───
function getCameraName() {
  return new URLSearchParams(location.search).get('name');
}

function el(id) {
  return document.getElementById(id);
}

function formatTimeAgo(dateStr) {
  const d = new Date(dateStr);
  const s = Math.floor((Date.now() - d) / 1000);
  if (s < 60) return s + 's ago';
  if (s < 3600) return Math.floor(s / 60) + 'm ago';
  if (s < 86400) return Math.floor(s / 3600) + 'h ago';
  return Math.floor(s / 86400) + 'd ago';
}

function formatBytes(bytes) {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

function toast(message, type) {
  const container = el('toasts');
  if (!container) return;
  const div = document.createElement('div');
  div.className = 'toast';
  if (type === 'error') div.style.borderColor = 'var(--red)';
  div.textContent = message;
  container.appendChild(div);
  setTimeout(() => div.remove(), 4000);
}

// ─── WebRTC ───
async function startWebRTC() {
  const name = getCameraName();
  if (!name) return;
  stopStream();

  try {
    peerConnection = new RTCPeerConnection({
      iceServers: [{ urls: 'stun:stun.l.google.com:19302' }]
    });

    peerConnection.addTransceiver('video', { direction: 'recvonly' });

    peerConnection.ontrack = function(event) {
      const video = el('live-video');
      if (!video) return;
      video.srcObject = event.streams[0];
      video.classList.remove('hidden');
      hide('live-snapshot');
      hide('live-mjpeg');
    };

    peerConnection.oniceconnectionstatechange = function() {
      const state = peerConnection.iceConnectionState;
      if (state === 'failed' || state === 'disconnected') {
        toast('WebRTC connection lost', 'error');
        stopStream();
        webrtcAutoReconnect();
      } else if (state === 'connected') {
        webrtcReconnectAttempts = 0;
      }
    };

    const offer = await peerConnection.createOffer();
    await peerConnection.setLocalDescription(offer);

    const resp = await fetch('/api/cameras/' + encodeURIComponent(name) + '/webrtc/offer', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(peerConnection.localDescription)
    });

    if (!resp.ok) throw new Error('Server returned ' + resp.status);

    const answer = await resp.json();
    await peerConnection.setRemoteDescription(new RTCSessionDescription(answer));

    currentStream = 'webrtc';
    updateStreamButtons();
    toast('WebRTC connected');
  } catch (err) {
    console.error('WebRTC error:', err);
    toast('WebRTC failed: ' + err.message, 'error');
    stopStream();
  }
}

function startMJPEG() {
  const name = getCameraName();
  if (!name) return;
  stopStream();

  const mjpeg = el('live-mjpeg');
  if (!mjpeg) return;
  mjpeg.src = '/api/cameras/' + encodeURIComponent(name) + '/mjpeg';
  mjpeg.classList.remove('hidden');
  hide('live-snapshot');
  hide('live-video');

  currentStream = 'mjpeg';
  updateStreamButtons();
  toast('MJPEG stream started');
}

function stopStream() {
  if (peerConnection) {
    peerConnection.close();
    peerConnection = null;
  }

  const video = el('live-video');
  if (video) { video.srcObject = null; video.classList.add('hidden'); }

  const mjpeg = el('live-mjpeg');
  if (mjpeg) { mjpeg.src = ''; mjpeg.classList.add('hidden'); }

  const snap = el('live-snapshot');
  if (snap) snap.classList.remove('hidden');

  currentStream = null;
  updateStreamButtons();
}

function updateStreamButtons() {
  const btnWebrtc = el('btn-webrtc');
  const btnMjpeg = el('btn-mjpeg');
  const btnStop = el('btn-stop');

  if (btnWebrtc) {
    btnWebrtc.classList.toggle('active', currentStream === 'webrtc');
    btnWebrtc.disabled = currentStream !== null;
  }
  if (btnMjpeg) {
    btnMjpeg.classList.toggle('active', currentStream === 'mjpeg');
    btnMjpeg.disabled = currentStream !== null;
  }
  if (btnStop) {
    btnStop.classList.toggle('hidden', currentStream === null);
  }
}

function hide(id) {
  const e = el(id);
  if (e) e.classList.add('hidden');
}

// ─── Picture-in-Picture ───
async function togglePiP() {
  const video = el('live-video');
  if (!video || !document.pictureInPictureEnabled) {
    toast('Picture-in-Picture not available', 'error');
    return;
  }

  try {
    if (document.pictureInPictureElement) {
      await document.exitPictureInPicture();
    } else {
      await video.requestPictureInPicture();
    }
  } catch (err) {
    toast('PiP failed: ' + err.message, 'error');
  }
}

// ─── Fullscreen ───
function toggleFullscreen() {
  const viewport = el('live-viewport');
  if (!viewport) return;

  if (document.fullscreenElement) {
    document.exitFullscreen();
  } else {
    viewport.requestFullscreen().catch(() => {});
  }
}

// ─── Timeline ───
function initTimeline() {
  timelineDate = new Date();
  updateTimelineDate();
  updatePlayheadToNow();
  fetchTimelineData();
  startPlayheadAnimation();

  const track = el('timeline-track');
  if (!track) return;

  // Add hover cursor element
  var cursor = document.createElement('div');
  cursor.className = 'timeline-cursor';
  var cursorTime = document.createElement('span');
  cursorTime.className = 'timeline-cursor-time';
  cursor.appendChild(cursorTime);
  track.appendChild(cursor);

  let dragging = false;

  track.addEventListener('mousedown', function(e) {
    dragging = true;
    scrubTimeline(e);
  });

  track.addEventListener('mousemove', function(e) {
    if (dragging) scrubTimeline(e);
    // Update hover cursor position and time
    var rect = track.getBoundingClientRect();
    var pct = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
    cursor.style.left = (pct * 100) + '%';
    var totalMin = pct * 24 * 60;
    var h = Math.floor(totalMin / 60);
    var m = Math.floor(totalMin % 60);
    cursorTime.textContent = String(h).padStart(2, '0') + ':' + String(m).padStart(2, '0');
  });

  track.addEventListener('mouseleave', function() {
    cursor.style.display = 'none';
  });

  track.addEventListener('mouseenter', function() {
    cursor.style.display = '';
  });

  document.addEventListener('mouseup', function() { dragging = false; });

  // Touch support
  track.addEventListener('touchstart', function(e) {
    dragging = true;
    scrubTimeline(e.touches[0]);
    e.preventDefault();
  }, { passive: false });

  track.addEventListener('touchmove', function(e) {
    if (dragging) scrubTimeline(e.touches[0]);
    e.preventDefault();
  }, { passive: false });

  track.addEventListener('touchend', function() { dragging = false; });
}

function scrubTimeline(e) {
  const track = el('timeline-track');
  const playhead = el('timeline-playhead');
  if (!track || !playhead) return;

  const rect = track.getBoundingClientRect();
  let pct = (e.clientX - rect.left) / rect.width;
  pct = Math.max(0, Math.min(1, pct));

  playhead.style.left = (pct * 100) + '%';
  playhead.style.display = '';

  // Calculate the timestamp from position
  const totalMinutes = pct * 24 * 60;
  const hours = Math.floor(totalMinutes / 60);
  const mins = Math.floor(totalMinutes % 60);
  const secs = Math.floor((totalMinutes % 1) * 60);

  // Build the full timestamp
  var d = new Date(timelineDate);
  d.setUTCHours(hours, mins, secs, 0);

  startPlayback(d);
}

function timelineNav(delta) {
  timelineDate.setDate(timelineDate.getDate() + delta);
  updateTimelineDate();
  updatePlayheadToNow();
  fetchTimelineData();
}

function timelineToday() {
  timelineDate = new Date();
  updateTimelineDate();
  updatePlayheadToNow();
  fetchTimelineData();
}

function updateTimelineDate() {
  const label = el('timeline-date');
  if (!label) return;

  const today = new Date();
  const isToday = timelineDate.toDateString() === today.toDateString();

  if (isToday) {
    label.textContent = 'Today';
  } else {
    label.textContent = timelineDate.toLocaleDateString('en-US', {
      weekday: 'short', month: 'short', day: 'numeric'
    });
  }

  // Update hidden date input
  var dateInput = el('timeline-date-input');
  if (dateInput) {
    dateInput.value = timelineDate.getFullYear() + '-' +
      String(timelineDate.getMonth() + 1).padStart(2, '0') + '-' +
      String(timelineDate.getDate()).padStart(2, '0');
  }
}

function updatePlayheadToNow() {
  const playhead = el('timeline-playhead');
  if (!playhead) return;

  const now = new Date();
  const today = new Date();
  const isToday = timelineDate.toDateString() === today.toDateString();

  if (isToday) {
    var pct = (now.getHours() * 60 + now.getMinutes()) / (24 * 60) * 100;
    playhead.style.left = pct + '%';
    playhead.style.display = '';
  } else {
    playhead.style.left = '0%';
    playhead.style.display = 'none';
  }
}

function fetchTimelineData() {
  var name = getCameraName();
  if (!name) return;

  var dateStr = timelineDate.getFullYear() + '-' +
    String(timelineDate.getMonth() + 1).padStart(2, '0') + '-' +
    String(timelineDate.getDate()).padStart(2, '0');

  fetch('/api/cameras/' + encodeURIComponent(name) + '/timeline?date=' + dateStr)
    .then(function(resp) {
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      return resp.json();
    })
    .then(function(data) {
      renderTimelineSegments(data.segments || []);
      renderTimelineEvents(data.events || []);
    })
    .catch(function(err) {
      console.error('Timeline fetch error:', err);
    });
}

function renderTimelineSegments(segments) {
  var track = el('timeline-track');
  if (!track) return;

  // Remove existing segments
  track.querySelectorAll('.timeline-segment').forEach(function(s) { s.remove(); });

  segments.forEach(function(seg) {
    var start = new Date(seg.start_time);
    var end = new Date(seg.end_time);
    var startPct = (start.getUTCHours() * 60 + start.getUTCMinutes()) / (24 * 60) * 100;
    var endPct = (end.getUTCHours() * 60 + end.getUTCMinutes()) / (24 * 60) * 100;
    var widthPct = endPct - startPct;
    if (widthPct < 0.1) widthPct = 0.1;

    var div = document.createElement('div');
    div.className = 'timeline-segment';
    div.style.left = startPct + '%';
    div.style.width = widthPct + '%';
    track.insertBefore(div, track.querySelector('.timeline-playhead'));
  });
}

function renderTimelineEvents(events) {
  var track = el('timeline-track');
  if (!track) return;

  // Remove existing event markers and tooltips
  track.querySelectorAll('.timeline-event').forEach(function(e) { e.remove(); });
  document.querySelectorAll('.timeline-tooltip').forEach(function(t) { t.remove(); });

  events.forEach(function(evt) {
    var ts = new Date(evt.timestamp);
    var pct = (ts.getUTCHours() * 60 + ts.getUTCMinutes()) / (24 * 60) * 100;
    var timeStr = String(ts.getUTCHours()).padStart(2, '0') + ':' +
      String(ts.getUTCMinutes()).padStart(2, '0') + ':' +
      String(ts.getUTCSeconds()).padStart(2, '0');
    var scoreStr = Math.round(evt.score * 100) + '%';

    var dot = document.createElement('div');
    dot.className = 'timeline-event ' + evt.label;
    dot.style.left = pct + '%';
    dot.title = evt.label + ' at ' + timeStr + ' (' + scoreStr + ')';

    dot.addEventListener('mouseenter', function(e) {
      showTimelineTooltip(dot, evt.label, timeStr, scoreStr);
    });

    dot.addEventListener('mouseleave', function() {
      hideTimelineTooltip();
    });

    dot.addEventListener('click', function(e) {
      e.stopPropagation();
      location.href = '/event.html?id=' + encodeURIComponent(evt.id);
    });

    track.insertBefore(dot, track.querySelector('.timeline-playhead'));
  });
}

function showTimelineTooltip(anchor, label, time, score) {
  hideTimelineTooltip();

  var tooltip = document.createElement('div');
  tooltip.className = 'timeline-tooltip';
  tooltip.innerHTML = '<strong>' + label + '</strong><br>' + time + '<br>' + score;

  var container = el('timeline-container');
  if (!container) return;
  container.appendChild(tooltip);

  var anchorRect = anchor.getBoundingClientRect();
  var containerRect = container.getBoundingClientRect();
  var tooltipWidth = tooltip.offsetWidth;

  var left = anchorRect.left + anchorRect.width / 2 - containerRect.left - tooltipWidth / 2;
  left = Math.max(4, Math.min(left, containerRect.width - tooltipWidth - 4));

  tooltip.style.left = left + 'px';
}

function hideTimelineTooltip() {
  document.querySelectorAll('.timeline-tooltip').forEach(function(t) { t.remove(); });
}

// ─── Birdseye View ───
let birdseyeInterval = null;

function setView(mode) {
  var btnGrid = el('btn-grid-view');
  var btnBirdseye = el('btn-birdseye-view');
  var cameraGrid = el('camera-grid');
  var birdseyeGrid = el('birdseye-grid');

  if (!cameraGrid || !birdseyeGrid) return;

  if (mode === 'birdseye') {
    cameraGrid.style.display = 'none';
    birdseyeGrid.style.display = '';
    if (btnGrid) btnGrid.classList.remove('active');
    if (btnBirdseye) btnBirdseye.classList.add('active');
    startBirdseye();
  } else {
    cameraGrid.style.display = '';
    birdseyeGrid.style.display = 'none';
    if (btnGrid) btnGrid.classList.add('active');
    if (btnBirdseye) btnBirdseye.classList.remove('active');
    stopBirdseye();
  }

  localStorage.setItem('watchpost-view', mode);
}

function startBirdseye() {
  stopBirdseye();
  refreshBirdseye();
  birdseyeInterval = setInterval(refreshBirdseye, 2000);
}

function stopBirdseye() {
  if (birdseyeInterval) {
    clearInterval(birdseyeInterval);
    birdseyeInterval = null;
  }
}

function refreshBirdseye() {
  fetch('/api/cameras')
    .then(function(resp) {
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      return resp.json();
    })
    .then(function(cameras) {
      var grid = el('birdseye-grid');
      if (!grid) return;

      var cameraList = cameras || [];
      if (cameraList.length === 0) {
        grid.innerHTML = '<div class="empty-state"><p>No cameras configured</p></div>';
        return;
      }

      // Build or update cells
      cameraList.forEach(function(cam) {
        var cellId = 'birdseye-' + cam.name;
        var cell = document.getElementById(cellId);
        if (!cell) {
          cell = document.createElement('div');
          cell.id = cellId;
          cell.className = 'birdseye-cell';
          cell.setAttribute('role', 'listitem');
          cell.onclick = function() {
            location.href = '/camera.html?name=' + encodeURIComponent(cam.name);
          };

          var img = document.createElement('img');
          img.alt = cam.name + ' camera feed';
          cell.appendChild(img);

          var label = document.createElement('div');
          label.className = 'birdseye-label';
          label.textContent = cam.name;
          cell.appendChild(label);

          grid.appendChild(cell);
        }

        // Update snapshot with cache-busting timestamp
        var img = cell.querySelector('img');
        if (img) {
          var newSrc = '/api/cameras/' + encodeURIComponent(cam.name) + '/snapshot?t=' + Date.now();
          img.src = newSrc;
        }
      });

      // Remove cells for cameras that no longer exist
      var validIds = new Set(cameraList.map(function(c) { return 'birdseye-' + c.name; }));
      Array.from(grid.querySelectorAll('.birdseye-cell')).forEach(function(cell) {
        if (!validIds.has(cell.id)) {
          cell.remove();
        }
      });
    })
    .catch(function(err) {
      console.error('Birdseye refresh error:', err);
    });
}

// Restore saved view preference on page load
(function() {
  var saved = localStorage.getItem('watchpost-view');
  if (saved === 'birdseye') {
    // Defer to after DOM is ready
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', function() { setView('birdseye'); });
    } else {
      setView('birdseye');
    }
  }
})();

// ─── Playback ───
function startPlayback(timestamp) {
  var name = getCameraName();
  if (!name) return;

  var isoStr = timestamp.toISOString();
  var url = '/api/cameras/' + encodeURIComponent(name) + '/playback?start=' + encodeURIComponent(isoStr);

  // Stop any live stream first
  if (currentStream) {
    stopStream();
  }

  var video = el('live-video');
  if (!video) return;

  // Fetch with HEAD first to check if segment exists and get offset
  fetch(url, { method: 'HEAD' })
    .then(function(resp) {
      if (!resp.ok) {
        if (resp.status === 404) {
          toast('No recording found for this timestamp', 'error');
        } else {
          toast('Playback error: ' + resp.status, 'error');
        }
        return;
      }

      var offset = parseFloat(resp.headers.get('X-Playback-Offset') || '0');

      // Set video source to playback endpoint
      video.srcObject = null;
      video.src = url;
      video.muted = false;
      video.autoplay = true;
      video.classList.remove('hidden');
      hide('live-snapshot');
      hide('live-mjpeg');

      video.onloadedmetadata = function() {
        if (offset > 0 && offset < video.duration) {
          video.currentTime = offset;
        }
        video.play().catch(function() {});
      };

      video.onended = function() {
        // When segment finishes, return to live
        returnToLive();
      };

      playbackMode = true;
      updatePlaybackUI();
      toast('Playing recording from ' + timestamp.toLocaleTimeString());
    })
    .catch(function(err) {
      toast('Playback failed: ' + err.message, 'error');
    });
}

function returnToLive() {
  var video = el('live-video');
  if (video) {
    video.pause();
    video.src = '';
    video.srcObject = null;
    video.onloadedmetadata = null;
    video.onended = null;
    video.classList.add('hidden');
  }

  var snap = el('live-snapshot');
  if (snap) snap.classList.remove('hidden');

  playbackMode = false;
  currentStream = null;
  updatePlaybackUI();
  updateStreamButtons();

  // Reset playhead to current time if viewing today
  updatePlayheadToNow();

  toast('Returned to live view');
}

function updatePlaybackUI() {
  var badge = el('playback-badge');
  var liveBadge = el('live-badge');
  var btnLive = el('btn-live');

  if (badge) badge.classList.toggle('hidden', !playbackMode);
  if (liveBadge) liveBadge.classList.toggle('hidden', playbackMode);
  if (btnLive) btnLive.classList.toggle('hidden', !playbackMode);

  // Disable stream buttons during playback
  var btnWebrtc = el('btn-webrtc');
  var btnMjpeg = el('btn-mjpeg');
  if (btnWebrtc) btnWebrtc.disabled = playbackMode;
  if (btnMjpeg) btnMjpeg.disabled = playbackMode;
}

// ─── Filter Chips ───
function toggleChip(chipEl, filterType) {
  // Deactivate siblings of same filter type
  const siblings = chipEl.parentElement.querySelectorAll('.chip[data-filter="' + filterType + '"]');
  siblings.forEach(s => s.classList.remove('active'));
  chipEl.classList.add('active');

  // Reload events with current filters
  reloadEvents();
}

function reloadEvents() {
  const gallery = el('events-gallery');
  if (!gallery) return;

  const labelChip = document.querySelector('.chip[data-filter="label"].active');
  const cameraChip = document.querySelector('.chip[data-filter="camera"].active');

  let url = '/partials/events-gallery?limit=50';
  if (labelChip && labelChip.dataset.value) {
    url += '&label=' + encodeURIComponent(labelChip.dataset.value);
  }
  if (cameraChip && cameraChip.dataset.value) {
    url += '&camera=' + encodeURIComponent(cameraChip.dataset.value);
  }

  gallery.setAttribute('hx-get', url);
  htmx.trigger(gallery, 'htmx:abort');
  htmx.ajax('GET', url, { target: '#events-gallery', swap: 'innerHTML' });
}

// ─── Calendar (Recordings page) ───
function initCalendar() {
  calendarDate = new Date();
  renderCalendar();
}

function calendarNav(delta) {
  calendarDate.setMonth(calendarDate.getMonth() + delta);
  renderCalendar();
}

function renderCalendar() {
  const grid = el('calendar-grid');
  const monthLabel = el('calendar-month');
  if (!grid || !monthLabel) return;

  const year = calendarDate.getFullYear();
  const month = calendarDate.getMonth();

  monthLabel.textContent = calendarDate.toLocaleDateString('en-US', { month: 'long', year: 'numeric' });

  // Clear existing day cells (keep the 7 day-label headers)
  const labels = grid.querySelectorAll('.calendar-day-label');
  grid.innerHTML = '';
  labels.forEach(l => grid.appendChild(l));

  const firstDay = new Date(year, month, 1).getDay();
  const daysInMonth = new Date(year, month + 1, 0).getDate();
  const today = new Date();

  // Empty cells before first day
  for (let i = 0; i < firstDay; i++) {
    const cell = document.createElement('span');
    cell.className = 'calendar-day empty';
    grid.appendChild(cell);
  }

  for (let d = 1; d <= daysInMonth; d++) {
    const cell = document.createElement('button');
    cell.className = 'calendar-day';
    cell.textContent = d;
    cell.dataset.day = d;

    const cellDate = new Date(year, month, d);
    if (cellDate.toDateString() === today.toDateString()) {
      cell.classList.add('today');
    }

    cell.onclick = function() {
      grid.querySelectorAll('.calendar-day.selected').forEach(s => s.classList.remove('selected'));
      cell.classList.add('selected');
      loadRecordingsForDate(cellDate);
    };

    grid.appendChild(cell);
  }

  // Fetch real recording days from API
  const monthStr = year + '-' + String(month + 1).padStart(2, '0');
  const camera = el('rec-camera')?.value || '';
  let url = '/api/recordings/calendar?month=' + monthStr;
  if (camera) url += '&camera=' + encodeURIComponent(camera);

  fetch(url)
    .then(function(resp) { return resp.json(); })
    .then(function(data) {
      const daysWithData = new Set(data.days || []);
      grid.querySelectorAll('.calendar-day[data-day]').forEach(function(cell) {
        const day = parseInt(cell.dataset.day, 10);
        if (daysWithData.has(day)) {
          cell.classList.add('has-data');
        }
      });

      // Auto-select: today if it has data, otherwise most recent day with data
      const todayDay = (today.getFullYear() === year && today.getMonth() === month) ? today.getDate() : null;
      let selectDay = null;
      if (todayDay && daysWithData.has(todayDay)) {
        selectDay = todayDay;
      } else if (daysWithData.size > 0) {
        selectDay = Math.max.apply(null, Array.from(daysWithData));
      }

      if (selectDay) {
        const btn = grid.querySelector('.calendar-day[data-day="' + selectDay + '"]');
        if (btn) btn.click();
      }
    })
    .catch(function(err) { console.error('Failed to load calendar data:', err); });
}

function loadRecordingsForDate(date) {
  const camera = el('rec-camera')?.value || '';
  const dateStr = date.toISOString().split('T')[0];
  const detail = el('recordings-detail');
  if (!detail) return;

  let url = '/partials/recordings?date=' + dateStr;
  if (camera) url += '&camera=' + encodeURIComponent(camera);

  htmx.ajax('GET', url, { target: '#recordings-detail', swap: 'innerHTML' });
}

function loadRecordings() {
  renderCalendar();
}

// ─── Keyboard Shortcuts ───
document.addEventListener('keydown', function(e) {
  // Don't capture when typing in inputs
  if (e.target.tagName === 'INPUT' || e.target.tagName === 'SELECT' || e.target.tagName === 'TEXTAREA') return;

  switch (e.key) {
    case '?':
      toggleShortcutModal();
      break;
    case 'w':
    case 'W':
      if (el('btn-webrtc')) startWebRTC();
      break;
    case 'm':
    case 'M':
      if (el('btn-mjpeg')) startMJPEG();
      break;
    case 's':
    case 'S':
      if (el('btn-stop') && currentStream) stopStream();
      break;
    case 'p':
    case 'P':
      if (el('btn-pip')) togglePiP();
      break;
    case 'l':
    case 'L':
      if (playbackMode) returnToLive();
      break;
    case 'f':
    case 'F':
      if (el('live-viewport')) toggleFullscreen();
      break;
    case 'ArrowLeft': {
      var prev = document.querySelector('[data-prev-id]');
      if (prev) { location.href = prev.href; e.preventDefault(); }
      break;
    }
    case 'ArrowRight': {
      var next = document.querySelector('[data-next-id]');
      if (next) { location.href = next.href; e.preventDefault(); }
      break;
    }
    case 'd':
    case 'D': {
      var clipLink = document.querySelector('.download-row[href*="/clip"]');
      if (clipLink) { clipLink.click(); }
      break;
    }
    case 'Escape':
      if (el('shortcut-modal') && el('shortcut-modal').classList.contains('open')) {
        closeShortcutModal();
      } else if (document.fullscreenElement) {
        document.exitFullscreen();
      } else if (playbackMode) {
        returnToLive();
      } else if (currentStream) {
        stopStream();
      }
      break;
    case '1':
      if (e.ctrlKey || e.metaKey) { e.preventDefault(); location.href = '/'; }
      break;
    case '2':
      if (e.ctrlKey || e.metaKey) { e.preventDefault(); location.href = '/events.html'; }
      break;
    case '3':
      if (e.ctrlKey || e.metaKey) { e.preventDefault(); location.href = '/recordings.html'; }
      break;
    case '4':
      if (e.ctrlKey || e.metaKey) { e.preventDefault(); location.href = '/system.html'; }
      break;
  }
});

// ─── Theme Toggle ───
function initTheme() {
  var saved = localStorage.getItem('watchpost-theme');
  if (saved === 'light') {
    document.documentElement.setAttribute('data-theme', 'light');
  }
  updateThemeUI();
}

function toggleTheme() {
  var current = document.documentElement.getAttribute('data-theme');
  var next = current === 'light' ? 'dark' : 'light';

  if (next === 'light') {
    document.documentElement.setAttribute('data-theme', 'light');
  } else {
    document.documentElement.removeAttribute('data-theme');
  }

  localStorage.setItem('watchpost-theme', next);
  updateThemeUI();
}

function updateThemeUI() {
  var isLight = document.documentElement.getAttribute('data-theme') === 'light';
  var iconDark = document.getElementById('theme-icon-dark');
  var iconLight = document.getElementById('theme-icon-light');
  var meta = document.querySelector('meta[name="theme-color"]');

  if (iconDark) iconDark.style.display = isLight ? 'none' : '';
  if (iconLight) iconLight.style.display = isLight ? '' : 'none';
  if (meta) meta.setAttribute('content', isLight ? '#ffffff' : '#0a0e14');
}

initTheme();

// ─── Connection Status ───
var connDebounceTimer = null;

function setConnStatus(ok) {
  var dot = document.getElementById('conn-dot');
  var label = document.getElementById('conn-label');
  if (!dot || !label) return;

  if (ok) {
    dot.className = 'conn-dot ok';
    label.textContent = 'Connected';
  } else {
    dot.className = 'conn-dot error';
    label.textContent = 'Reconnecting...';
  }
}

document.addEventListener('htmx:sendError', function() {
  clearTimeout(connDebounceTimer);
  setConnStatus(false);
});

document.addEventListener('htmx:responseError', function(e) {
  console.error('HTMX error:', e.detail);
  clearTimeout(connDebounceTimer);
  setConnStatus(false);
});

document.addEventListener('htmx:afterRequest', function(e) {
  if (!e.detail.failed) {
    clearTimeout(connDebounceTimer);
    connDebounceTimer = setTimeout(function() {
      setConnStatus(true);
    }, 300);
  }
});

// ─── Page visibility: pause updates when hidden ───
document.addEventListener('visibilitychange', function() {
  if (document.hidden) {
    stopBirdseye();
    stopGridSnapshotRefresh();
    stopStatsRefresh();
  } else {
    if (localStorage.getItem('watchpost-view') === 'birdseye') {
      var birdseyeGrid = el('birdseye-grid');
      if (birdseyeGrid && birdseyeGrid.style.display !== 'none') {
        startBirdseye();
      }
    } else if (el('camera-grid')) {
      startGridSnapshotRefresh();
    }
    if (el('stats-row')) {
      startStatsRefresh();
    }
  }
});

// ─── Grid Snapshot Refresh ───
// Instead of htmx replacing the entire grid DOM every 2s (causes flash),
// update only the <img> src attributes with a cache-busting timestamp.
let gridSnapshotInterval = null;

function startGridSnapshotRefresh() {
  stopGridSnapshotRefresh();
  gridSnapshotInterval = setInterval(refreshGridSnapshots, 2000);
}

function stopGridSnapshotRefresh() {
  if (gridSnapshotInterval) {
    clearInterval(gridSnapshotInterval);
    gridSnapshotInterval = null;
  }
}

function refreshGridSnapshots() {
  var grid = el('camera-grid');
  if (!grid || grid.style.display === 'none') return;
  var cards = grid.querySelectorAll('.cam-card');
  var t = Date.now();
  cards.forEach(function(card) {
    // Skip offline cameras — their snapshot endpoint returns 503
    if (card.querySelector('.cam-live-dot.offline')) return;
    var img = card.querySelector('.cam-preview img');
    if (!img) return;
    var base = img.src.split('?')[0];
    img.src = base + '?t=' + t;
  });
}

// Start refresh after htmx loads the grid initially
document.addEventListener('htmx:afterSwap', function(e) {
  if (e.detail.target && e.detail.target.id === 'camera-grid') {
    startGridSnapshotRefresh();
  }
});

// ─── Stats Refresh ───
// Fetch dashboard stats as HTML fragment and diff-update only changed values.
let statsInterval = null;

function startStatsRefresh() {
  stopStatsRefresh();
  statsInterval = setInterval(refreshStats, 5000);
}

function stopStatsRefresh() {
  if (statsInterval) {
    clearInterval(statsInterval);
    statsInterval = null;
  }
}

function refreshStats() {
  var row = el('stats-row');
  if (!row) return;
  fetch('/partials/dashboard-stats')
    .then(function(r) { return r.ok ? r.text() : null; })
    .then(function(html) {
      if (!html) return;
      var tmp = document.createElement('div');
      tmp.innerHTML = html;
      var newValues = tmp.querySelectorAll('.stat-value');
      var oldValues = row.querySelectorAll('.stat-value');
      newValues.forEach(function(nv, i) {
        if (i < oldValues.length && oldValues[i].innerHTML !== nv.innerHTML) {
          oldValues[i].innerHTML = nv.innerHTML;
          oldValues[i].className = nv.className;
        }
      });
    })
    .catch(function() {});
}

document.addEventListener('htmx:afterSwap', function(e) {
  if (e.detail.target && e.detail.target.id === 'stats-row') {
    startStatsRefresh();
  }
});

// ─── Staggered Fade-In ───
// Apply fade-in classes to cards after htmx swaps
document.addEventListener('htmx:afterSwap', function(e) {
  var target = e.detail.target;
  if (!target) return;

  // Stagger stat cards
  var statCards = target.querySelectorAll('.stat-card');
  statCards.forEach(function(card, i) {
    card.classList.add('fade-in', 'stagger-' + Math.min(i + 1, 4));
  });

  // Stagger camera cards
  var camCards = target.querySelectorAll('.cam-card');
  camCards.forEach(function(card, i) {
    card.classList.add('fade-in', 'stagger-' + Math.min(i + 1, 4));
  });

  // Stagger event cards
  var eventCards = target.querySelectorAll('.event-card');
  eventCards.forEach(function(card, i) {
    card.classList.add('fade-in', 'stagger-' + Math.min(i + 1, 4));
  });
});

// ─── Keyboard Shortcut Modal ───
function toggleShortcutModal() {
  var backdrop = el('shortcut-backdrop');
  var modal = el('shortcut-modal');
  if (!backdrop || !modal) return;

  var isOpen = modal.classList.contains('open');
  if (isOpen) {
    closeShortcutModal();
  } else {
    openShortcutModal();
  }
}

function openShortcutModal() {
  var backdrop = el('shortcut-backdrop');
  var modal = el('shortcut-modal');
  if (!backdrop || !modal) return;

  backdrop.classList.add('open');
  modal.classList.add('open');
  // Trap focus
  modal.querySelector('button')?.focus();
}

function closeShortcutModal() {
  var backdrop = el('shortcut-backdrop');
  var modal = el('shortcut-modal');
  if (!backdrop || !modal) return;

  backdrop.classList.remove('open');
  modal.classList.remove('open');
}

// Close modal on Escape (handled in keydown handler via existing Escape case)
// Close modal on backdrop click
document.addEventListener('click', function(e) {
  if (e.target && e.target.id === 'shortcut-backdrop') {
    closeShortcutModal();
  }
});

// ─── Real-time Playhead Animation ───
var playheadRAF = null;

function startPlayheadAnimation() {
  if (playheadRAF) cancelAnimationFrame(playheadRAF);

  function tick() {
    if (!playbackMode) {
      var playhead = el('timeline-playhead');
      if (playhead) {
        var now = new Date();
        var today = new Date();
        if (timelineDate.toDateString() === today.toDateString()) {
          var pct = (now.getHours() * 60 + now.getMinutes() + now.getSeconds() / 60) / (24 * 60) * 100;
          playhead.style.left = pct + '%';
          playhead.style.display = '';
        }
      }
    }
    playheadRAF = requestAnimationFrame(tick);
  }
  tick();
}

// ─── Infinite Scroll for Events ───
var infiniteScrollObserver = null;
var eventsOffset = 0;
var eventsLoading = false;
var eventsExhausted = false;

function initInfiniteScroll() {
  var gallery = el('events-gallery');
  if (!gallery) return;

  eventsOffset = 0;
  eventsExhausted = false;
  eventsLoading = false;

  // Watch for initial load completion, then set up sentinel
  var checkReady = setInterval(function() {
    var cards = gallery.querySelectorAll('.event-card');
    if (cards.length > 0 || gallery.querySelector('.empty-state')) {
      clearInterval(checkReady);
      eventsOffset = cards.length;
      if (cards.length >= 50) {
        addScrollSentinel();
      }
    }
  }, 500);
}

function addScrollSentinel() {
  var gallery = el('events-gallery');
  if (!gallery || gallery.querySelector('.scroll-sentinel')) return;

  var sentinel = document.createElement('div');
  sentinel.className = 'scroll-sentinel';
  sentinel.innerHTML = '<div class="loading-spinner"></div>';
  gallery.appendChild(sentinel);

  if (infiniteScrollObserver) infiniteScrollObserver.disconnect();
  infiniteScrollObserver = new IntersectionObserver(function(entries) {
    if (entries[0].isIntersecting && !eventsLoading && !eventsExhausted) {
      loadMoreEvents();
    }
  }, { rootMargin: '200px' });

  infiniteScrollObserver.observe(sentinel);
}

function loadMoreEvents() {
  if (eventsLoading || eventsExhausted) return;
  eventsLoading = true;

  var labelChip = document.querySelector('.chip[data-filter="label"].active');
  var cameraChip = document.querySelector('.chip[data-filter="camera"].active');

  var url = '/partials/events-gallery?limit=50&offset=' + eventsOffset;
  if (labelChip && labelChip.dataset.value) {
    url += '&label=' + encodeURIComponent(labelChip.dataset.value);
  }
  if (cameraChip && cameraChip.dataset.value) {
    url += '&camera=' + encodeURIComponent(cameraChip.dataset.value);
  }

  fetch(url)
    .then(function(resp) { return resp.text(); })
    .then(function(html) {
      var gallery = el('events-gallery');
      if (!gallery) return;

      // Remove old sentinel
      var oldSentinel = gallery.querySelector('.scroll-sentinel');
      if (oldSentinel) oldSentinel.remove();

      // Parse and count new cards
      var tmp = document.createElement('div');
      tmp.innerHTML = html;
      var newCards = tmp.querySelectorAll('.event-card');

      if (newCards.length === 0) {
        eventsExhausted = true;
        eventsLoading = false;
        return;
      }

      // Append new cards with stagger animation
      newCards.forEach(function(card, i) {
        card.classList.add('fade-in', 'stagger-' + Math.min(i + 1, 4));
        gallery.appendChild(card);
      });

      eventsOffset += newCards.length;
      eventsLoading = false;

      // Add new sentinel if we got a full page
      if (newCards.length >= 50) {
        addScrollSentinel();
      }
    })
    .catch(function(err) {
      console.error('Failed to load more events:', err);
      eventsLoading = false;
    });
}

// ─── Event Search ───
var searchDebounceTimer = null;

function initEventSearch() {
  var input = el('event-search');
  if (!input) return;

  input.addEventListener('input', function() {
    clearTimeout(searchDebounceTimer);
    searchDebounceTimer = setTimeout(function() {
      reloadEvents();
    }, 300);
  });
}

// Patch reloadEvents to include search term
var _origReloadEvents = typeof reloadEvents === 'function' ? reloadEvents : null;

function reloadEventsWithSearch() {
  var gallery = el('events-gallery');
  if (!gallery) return;

  var labelChip = document.querySelector('.chip[data-filter="label"].active');
  var cameraChip = document.querySelector('.chip[data-filter="camera"].active');
  var searchInput = el('event-search');

  var url = '/partials/events-gallery?limit=50';
  if (labelChip && labelChip.dataset.value) {
    url += '&label=' + encodeURIComponent(labelChip.dataset.value);
  }
  if (cameraChip && cameraChip.dataset.value) {
    url += '&camera=' + encodeURIComponent(cameraChip.dataset.value);
  }
  if (searchInput && searchInput.value.trim()) {
    url += '&q=' + encodeURIComponent(searchInput.value.trim());
  }

  // Reset infinite scroll state
  eventsOffset = 0;
  eventsExhausted = false;

  htmx.ajax('GET', url, { target: '#events-gallery', swap: 'innerHTML' });
}

// Override reloadEvents to include search
reloadEvents = function() {
  reloadEventsWithSearch();
};

// ─── Timeline Date Picker ───
function openTimelineDatePicker() {
  var input = el('timeline-date-input');
  if (input) {
    input.showPicker();
  }
}

function onTimelineDatePick(input) {
  if (!input.value) return;
  var parts = input.value.split('-');
  timelineDate = new Date(parseInt(parts[0]), parseInt(parts[1]) - 1, parseInt(parts[2]));
  updateTimelineDate();
  updatePlayheadToNow();
  fetchTimelineData();
}

// ─── WebRTC Auto-Reconnect ───
var webrtcReconnectAttempts = 0;
var webrtcMaxReconnect = 3;
var webrtcReconnectTimer = null;

function webrtcAutoReconnect() {
  if (webrtcReconnectAttempts >= webrtcMaxReconnect) {
    toast('WebRTC reconnect failed after ' + webrtcMaxReconnect + ' attempts', 'error');
    webrtcReconnectAttempts = 0;
    return;
  }

  webrtcReconnectAttempts++;
  var delay = Math.min(1000 * Math.pow(2, webrtcReconnectAttempts - 1), 8000);
  toast('Reconnecting WebRTC (' + webrtcReconnectAttempts + '/' + webrtcMaxReconnect + ')...');

  webrtcReconnectTimer = setTimeout(function() {
    startWebRTC().then(function() {
      webrtcReconnectAttempts = 0;
    }).catch(function() {
      webrtcAutoReconnect();
    });
  }, delay);
}
