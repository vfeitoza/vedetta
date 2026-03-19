// Watchpost — Control Room Noir
// Vanilla JS for WebRTC, timeline, keyboard shortcuts, and UI interactions

'use strict';

// ─── State ───
let peerConnection = null;
let currentStream = null; // 'webrtc' | 'mjpeg' | null
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

  // Calculate time from position
  const hours = Math.floor(pct * 24);
  const mins = Math.floor((pct * 24 - hours) * 60);
  const timeStr = String(hours).padStart(2, '0') + ':' + String(mins).padStart(2, '0');

  // Could show tooltip here
}

function timelineNav(delta) {
  timelineDate.setDate(timelineDate.getDate() + delta);
  updateTimelineDate();
  updatePlayheadToNow();
}

function timelineToday() {
  timelineDate = new Date();
  updateTimelineDate();
  updatePlayheadToNow();
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

    const cellDate = new Date(year, month, d);
    if (cellDate.toDateString() === today.toDateString()) {
      cell.classList.add('today');
    }

    // Has data check would come from the API
    if (cellDate <= today) {
      cell.classList.add('has-data');
    }

    cell.onclick = function() {
      grid.querySelectorAll('.calendar-day.selected').forEach(s => s.classList.remove('selected'));
      cell.classList.add('selected');
      loadRecordingsForDate(cellDate);
    };

    grid.appendChild(cell);
  }
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
  const selected = document.querySelector('.calendar-day.selected');
  if (selected) {
    selected.click();
  }
}

// ─── Keyboard Shortcuts ───
document.addEventListener('keydown', function(e) {
  // Don't capture when typing in inputs
  if (e.target.tagName === 'INPUT' || e.target.tagName === 'SELECT' || e.target.tagName === 'TEXTAREA') return;

  switch (e.key) {
    case '?':
      toast('Shortcuts: W=WebRTC, M=MJPEG, S=Stop, P=PiP, F=Fullscreen, Esc=Back');
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
    case 'f':
    case 'F':
      if (el('live-viewport')) toggleFullscreen();
      break;
    case 'Escape':
      if (document.fullscreenElement) {
        document.exitFullscreen();
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

// ─── HTMX error handling ───
document.addEventListener('htmx:responseError', function(e) {
  console.error('HTMX error:', e.detail);
});

// ─── Page visibility: pause updates when hidden ───
document.addEventListener('visibilitychange', function() {
  // htmx will handle this via polling triggers
});
