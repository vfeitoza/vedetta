const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

function fmtBytes(n) {
  if (n < 1024) return n + " B";
  const units = ["KB","MB","GB","TB"];
  let i = -1;
  do { n /= 1024; i++; } while (n >= 1024 && i < units.length - 1);
  return n.toFixed(n >= 10 ? 0 : 1) + " " + units[i];
}

function parseBytesInput(s) {
  const m = /^(\d+(?:\.\d+)?)\s*(B|KB|MB|GB|TB)?$/i.exec(s.trim());
  if (!m) return null;
  const n = parseFloat(m[1]);
  const u = (m[2] || "B").toUpperCase();
  return Math.round(n * ({B:1, KB:1024, MB:1024**2, GB:1024**3, TB:1024**4})[u]);
}

async function loadSummary() {
  const r = await fetch("/api/storage");
  if (!r.ok) return;
  const data = await r.json();

  $("#recording-paused-banner").hidden = !data.recording_paused;

  renderSummaryCards(data);
  renderCameraTable(data.cameras || []);
}

function renderSummaryCards({ recording, snapshots }) {
  const sameFS = snapshots.same_filesystem_as_recording;
  const cards = $("#summary");
  cards.innerHTML = `
    <div class="card">
      <h3>Recording</h3>
      <p><strong>${fmtBytes(recording.used_bytes)}</strong> used</p>
      <p>${fmtBytes(recording.disk_available)} free on disk</p>
      <p class="muted">${recording.root}</p>
    </div>
    ${sameFS ? "" : `
    <div class="card">
      <h3>Snapshots</h3>
      <p><strong>${fmtBytes(snapshots.used_bytes)}</strong> used</p>
      <p>${fmtBytes(snapshots.disk_available)} free on disk</p>
      <p class="muted">${snapshots.root}</p>
    </div>`}
  `;
}

function renderCameraTable(cameras) {
  const body = $("#camera-table-body");
  body.innerHTML = "";
  for (const c of cameras) {
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>${c.name}</td>
      <td>${fmtBytes(c.segment_bytes)}</td>
      <td>${fmtBytes(c.clip_bytes)}</td>
      <td>${c.oldest_segment ? c.oldest_segment.slice(0,10) : "—"}</td>
      <td>${fmtBytes(c.last_7d_bytes)}</td>
      <td>
        <button data-action="older" data-camera="${c.name}" data-days="${c.effective_retain_days}">Older than ${c.effective_retain_days}d</button>
        <button data-action="range" data-camera="${c.name}">Range…</button>
        <button data-action="clips" data-camera="${c.name}">All clips</button>
      </td>
    `;
    body.appendChild(tr);

    const chartRow = document.createElement("tr");
    chartRow.innerHTML = `<td colspan="6">${renderPerDayBars(c.per_day)}</td>`;
    body.appendChild(chartRow);
  }
}

function renderPerDayBars(days) {
  if (!days?.length) return "<em>no data</em>";
  const max = days.reduce((m, d) => Math.max(m, d.bytes), 0) || 1;
  return `
    <svg width="${days.length * 12}" height="40" role="img" aria-label="daily bytes">
      ${days.map((d, i) =>
        `<rect x="${i*12}" y="${40 - 38*(d.bytes/max)}" width="10" height="${38*(d.bytes/max)}" fill="#4a8"></rect>`
      ).join("")}
    </svg>
  `;
}

document.addEventListener("click", async (e) => {
  const btn = e.target.closest("button[data-action]");
  if (!btn) return;
  const camera = btn.dataset.camera;
  const action = btn.dataset.action;
  let body;
  if (action === "older") {
    const days = parseInt(prompt(`Delete segments older than how many days?`, btn.dataset.days), 10);
    if (!days) return;
    body = { target: "segments", camera, older_than_days: days };
  } else if (action === "range") {
    const from = prompt("From (YYYY-MM-DD, UTC):");
    const to   = prompt("To (YYYY-MM-DD, UTC):");
    if (!from || !to) return;
    body = { target: "segments", camera, from, to };
  } else if (action === "clips") {
    body = { target: "clips", camera };
  } else return;
  await confirmAndDelete(body);
});

async function confirmAndDelete(body) {
  const preview = await fetch("/api/storage/delete?dry_run=true", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!preview.ok) {
    alert("Preview failed: " + (await preview.text()));
    return;
  }
  const p = await preview.json();
  $("#delete-modal-summary").textContent =
    `Will delete ${p.segments} segments, ${p.clips} event clips, ${p.snapshots} snapshots — ${fmtBytes(p.bytes)} across ${(p.cameras || []).join(", ") || "(none)"}.`;
  const dlg = $("#delete-modal");
  $("#delete-confirm").onclick = async () => {
    dlg.close();
    const r = await fetch("/api/storage/delete", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (r.status === 409) { alert("Storage busy — try again in a few seconds."); return; }
    if (r.status === 422) {
      const j = await r.json();
      alert("Cannot delete — request targets currently-open segment(s):\n" + (j.protected_paths || []).join("\n"));
      return;
    }
    if (!r.ok) { alert("Delete failed: " + (await r.text())); return; }
    const out = await r.json();
    alert(`Freed ${fmtBytes(out.bytes)} (${out.segments + out.clips} files).`);
    loadSummary();
    loadAudit();
  };
  $("#delete-cancel").onclick = () => dlg.close();
  dlg.showModal();
}

$("#run-cleanup-btn")?.addEventListener("click", async () => {
  const r = await fetch("/api/storage/cleanup", { method: "POST" });
  if (r.status === 409) { alert("Cleanup already running."); return; }
  if (!r.ok) { alert("Cleanup failed: " + (await r.text())); return; }
  alert("Cleanup started.");
});

$("#free-space-btn")?.addEventListener("click", async () => {
  const target = parseBytesInput($("#free-target").value);
  if (!target) { alert("Enter a target like '50 GB'."); return; }
  await confirmAndDelete({ target: "free_bytes", free_bytes_target: target });
});

async function loadAudit() {
  const r = await fetch("/api/storage/audit?limit=20");
  if (!r.ok) return;
  const rows = await r.json();
  $("#audit-list").innerHTML = rows.map(row =>
    `<li>${row.ts} — ${row.actor} — ${row.files} files / ${fmtBytes(row.bytes)} — <code>${JSON.stringify(row.scope)}</code></li>`
  ).join("");
}

loadSummary();
loadAudit();
setInterval(loadSummary, 30_000);
