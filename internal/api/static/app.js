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

  const track = el('timeline-track');
  if (!track) return;

  let dragging = false;

  track.addEventListener('mousedown', function(e) {
    dragging = true;
    scrubTimeline(e);
  });

  track.addEventListener('mousemove', function(e) {
    if (dragging) scrubTimeline(e);
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
      toast('Shortcuts: W=WebRTC, M=MJPEG, S=Stop, L=Live, P=PiP, F=Fullscreen, D=Download, Esc=Back');
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
      if (document.fullscreenElement) {
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
  } else if (localStorage.getItem('watchpost-view') === 'birdseye') {
    var birdseyeGrid = el('birdseye-grid');
    if (birdseyeGrid && birdseyeGrid.style.display !== 'none') {
      startBirdseye();
    }
  }
});

// ─── Snapshot Crossfade ───
// Capture current image sources before camera grid swap to prevent flash
let cachedSnapshotSrcs = {};

document.addEventListener('htmx:beforeSwap', function(e) {
  if (e.detail.target && e.detail.target.id === 'camera-grid') {
    cachedSnapshotSrcs = {};
    var imgs = e.detail.target.querySelectorAll('.cam-preview img');
    imgs.forEach(function(img) {
      if (img.src && img.naturalWidth > 0) {
        cachedSnapshotSrcs[img.src] = true;
      }
    });
  }
});

document.addEventListener('htmx:afterSwap', function(e) {
  if (e.detail.target && e.detail.target.id === 'camera-grid') {
    var imgs = e.detail.target.querySelectorAll('.cam-preview img');
    imgs.forEach(function(img) {
      if (cachedSnapshotSrcs[img.src]) {
        // Same image, skip the fade — hold at full opacity
        img.classList.add('crossfade-hold');
        setTimeout(function() { img.classList.remove('crossfade-hold'); }, 50);
      }
    });
    cachedSnapshotSrcs = {};
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
