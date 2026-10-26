"use strict";

// Polls /api/status and renders each section with the renderer of the same key.
// To add a panel: register a dashboard.Section in Go and add a renderer here.
// Sections without a renderer are shown as raw JSON.

const POLL_INTERVAL_MS = 2000;

//
// DOM helpers, all text goes through textContent since user agents and addresses come from clients
//

function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs || {})) {
    if (key === "class") node.className = value;
    else if (key === "title") node.title = value;
    else node.setAttribute(key, value);
  }
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

function card(title, ...children) {
  return el("section", { class: "card" }, el("h2", {}, title), ...children);
}

function tiles(items) {
  return el(
    "div",
    { class: "tiles" },
    items.map(([label, value, cls]) =>
      el("div", { class: "tile" }, el("div", { class: "label" }, label), el("div", { class: "value " + (cls || "") }, value)),
    ),
  );
}

function table(headers, rows, emptyText) {
  if (rows.length === 0) return el("p", { class: "empty" }, emptyText);
  return el(
    "div",
    { class: "table-wrap" },
    el("table", {}, el("thead", {}, el("tr", {}, headers.map((h) => el("th", {}, h)))), el("tbody", {}, rows)),
  );
}

function badge(text, cls) {
  return el("span", { class: "badge " + cls }, text);
}

//
// Formatting
//

function fmtDuration(ms) {
  if (ms === null || ms === undefined) return "never";
  const s = Math.floor(ms / 1000);
  if (s < 60) return s + "s";
  const m = Math.floor(s / 60);
  if (m < 60) return m + "m " + (s % 60) + "s";
  const h = Math.floor(m / 60);
  if (h < 24) return h + "h " + (m % 60) + "m";
  return Math.floor(h / 24) + "d " + (h % 24) + "h";
}

function fmtMbps(mbps) {
  return mbps.toFixed(2) + " Mbps";
}

function fmtBytes(bytes) {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let i = 0;
  while (bytes >= 1024 && i < units.length - 1) {
    bytes /= 1024;
    i++;
  }
  return bytes.toFixed(i === 0 ? 0 : 1) + " " + units[i];
}

function fmtLocation(loc) {
  if (!loc || (!loc.city && !loc.country)) return "unknown";
  return [loc.city, loc.state !== "Unknown" ? loc.state : "", loc.country].filter(Boolean).join(", ");
}

function rttClass(ms) {
  return ms > 400 ? "bad" : ms > 200 ? "warn" : "good";
}

function lossClass(percent) {
  return percent > 5 ? "bad" : percent > 1 ? "warn" : "good";
}

//
// Section renderers, called with (data, status)
//

function srtDetails(srt) {
  return el(
    "dl",
    {},
    [
      ["Connected for", fmtDuration(srt.connected_for_ms)],
      ["RTT", el("span", { class: rttClass(srt.rtt_ms) }, srt.rtt_ms.toFixed(1) + " ms")],
      ["Receive rate", fmtMbps(srt.recv_mbps)],
      ["Send rate", fmtMbps(srt.send_mbps)],
      ["Link capacity", fmtMbps(srt.link_capacity_mbps)],
      ["Receive loss", el("span", { class: lossClass(srt.recv_loss_percent) }, srt.recv_loss_percent.toFixed(2) + " %")],
      ["Lost / retransmitted", srt.pkt_recv_loss + " / " + srt.pkt_recv_retrans],
      ["Dropped", el("span", { class: srt.pkt_recv_drop > 0 ? "warn" : "" }, String(srt.pkt_recv_drop))],
      ["Receive buffer", srt.recv_buf_ms + " ms"],
      ["Latency", srt.latency_ms + " ms"],
      ["Received", fmtBytes(srt.byte_recv)],
    ].map(([k, v]) => el("div", {}, el("dt", {}, k), el("dd", {}, v))),
  );
}

function renderIngest(peer, status) {
  const title = status.role ? "Publisher ingest" : "Upstream flagship";
  if (!peer) return card(title, el("p", { class: "empty" }, status.role ? "No publisher connected." : "Not connected to the flagship."));
  return card(
    title,
    el("p", {}, peer.addr, " ", el("span", { class: "sub" }, fmtLocation(peer.location))),
    srtDetails(peer.srt),
  );
}

function renderEscorts(escorts, status) {
  const redirects = sectionData(status, "redirects");
  const redirectsByAddr = new Map((redirects ? redirects.escorts : []).map((r) => [r.hls_addr, r.count]));

  const rows = escorts.map((e) => {
    const s = e.srt;
    return el(
      "tr",
      {},
      el("td", {}, e.hls_addr || "-", el("div", { class: "sub" }, "SRT " + e.addr)),
      el("td", {}, fmtLocation(e.location)),
      el("td", {}, fmtDuration(s.connected_for_ms)),
      el("td", { class: rttClass(s.rtt_ms) }, s.rtt_ms.toFixed(1) + " ms"),
      el("td", {}, fmtMbps(s.send_mbps)),
      el("td", {}, fmtMbps(s.link_capacity_mbps)),
      el("td", { class: lossClass(s.send_loss_percent) }, s.send_loss_percent.toFixed(2) + " %"),
      el("td", {}, s.pkt_retrans),
      el("td", { class: s.pkt_send_drop > 0 ? "warn" : "" }, s.pkt_send_drop),
      el("td", {}, s.send_buf_ms + " ms"),
      el("td", { class: "num" }, redirectsByAddr.get(e.hls_addr) || 0),
    );
  });

  return card(
    "Escorts (" + escorts.length + ")",
    table(
      ["Escort", "Location", "Connected", "RTT", "Send rate", "Link capacity", "Send loss", "Retransmitted", "Dropped", "Send buffer", "Redirected"],
      rows,
      "No escorts connected.",
    ),
  );
}

function renderRedirects(data) {
  const max = Math.max(1, ...data.escorts.map((e) => e.count));
  const rows = data.escorts.map((e) => {
    const fill = el("span");
    fill.style.width = (100 * e.count) / max + "%";
    return el("div", { class: "bar-row" }, el("span", {}, e.hls_addr), el("div", { class: "bar" }, fill), el("span", { class: "num" }, e.count));
  });

  return card(
    "Readers redirected to escorts",
    tiles([["Total", data.total]]),
    rows.length ? rows : el("p", { class: "empty" }, "No readers redirected yet."),
  );
}

const viewerStatusClass = { playing: "good", starting: "info", stuck: "bad", idle: "muted" };

function renderHLS(data) {
  const counts = { playing: 0, starting: 0, stuck: 0, idle: 0 };
  for (const v of data.viewers) counts[v.status] = (counts[v.status] || 0) + 1;

  let stream = ["Inactive", "muted"];
  if (data.ended) stream = ["Ended", "muted"];
  else if (data.primed && !data.active) stream = ["Waiting for publisher", "info"];
  else if (data.primed) stream = ["Live", "good"];
  else if (data.active) stream = ["Priming", "info"];
  const lastSegment = data.since_last_segment_ms;
  // Same threshold as stallTimeout in the HLS diagnostics
  const stalled = data.active && data.primed && lastSegment !== null && lastSegment > Math.max(5000, 3 * data.target_duration_s * 1000);
  if (stalled) stream = ["Stalled", "bad"];

  const rows = data.viewers.map((v) =>
    el(
      "tr",
      {},
      el("td", {}, badge(v.status, viewerStatusClass[v.status] || "")),
      el("td", {}, v.ip, el("div", { class: "sub" }, v.id)),
      el("td", {}, fmtDuration(v.watched_ms)),
      el("td", {}, fmtDuration(v.since_last_segment_ms)),
      el("td", { class: v.segments_behind_live > 2 ? "warn" : "" }, v.segments_behind_live ?? "-"),
      el("td", {}, v.playlist_requests),
      el("td", {}, v.segment_requests),
      el("td", { class: v.segments_not_found > 0 ? "warn" : "" }, v.segments_not_found),
      el("td", { class: "wrap sub" }, v.user_agent),
    ),
  );

  return card(
    "HLS clients",
    tiles([
      ["Stream", stream[0], stream[1]],
      ["Connected", data.viewers.length],
      ["Playing", counts.playing, "good"],
      ["Starting", counts.starting, "info"],
      ["Stuck", counts.stuck, counts.stuck ? "bad" : ""],
      ["Idle", counts.idle, "muted"],
      ["Last segment", data.active || data.primed ? fmtDuration(lastSegment) + (lastSegment === null ? "" : " ago") : "-"],
    ]),
    table(
      ["Status", "Client", "Watched", "Last segment", "Behind live", "Playlists", "Segments", "Not found", "User agent"],
      rows,
      "No HLS clients connected.",
    ),
  );
}

function renderRaw(data, _status, key) {
  return card(key, el("pre", {}, JSON.stringify(data, null, 2)));
}

const renderers = {
  ingest: renderIngest,
  escorts: renderEscorts,
  redirects: renderRedirects,
  hls: renderHLS,
};

//
// Polling
//

function sectionData(status, key) {
  const section = status.sections.find((s) => s.key === key);
  return section ? section.data : undefined;
}

const root = document.getElementById("sections");
const stateEl = document.getElementById("state");
const pauseBtn = document.getElementById("pause");
let paused = false;
let inFlight = false;
let timer = null;

function render(status) {
  const role = status.role ? "flagship" : "escort";
  document.getElementById("role").textContent = role;
  document.body.className = "role-" + role;
  document.getElementById("uptime").textContent = "up " + fmtDuration(status.uptime_ms);

  root.replaceChildren(
    ...status.sections.map((section) => {
      const renderer = renderers[section.key] || renderRaw;
      try {
        return renderer(section.data, status, section.key);
      } catch (err) {
        return card(section.key, el("p", { class: "bad" }, "Failed to render: " + err.message));
      }
    }),
  );
}

function setState(text, cls) {
  stateEl.textContent = text;
  stateEl.className = "state " + cls;
}

async function poll() {
  timer = null;
  inFlight = true;
  try {
    const res = await fetch("api/status", { cache: "no-store" });
    if (!res.ok) throw new Error("HTTP " + res.status);
    render(await res.json());
    setState("live", "good");
  } catch (err) {
    setState("disconnected (" + err.message + ")", "bad");
  }
  inFlight = false;
  if (!paused) timer = setTimeout(poll, POLL_INTERVAL_MS);
  else setState("paused", "muted");
}

pauseBtn.addEventListener("click", () => {
  paused = !paused;
  pauseBtn.textContent = paused ? "Resume" : "Pause";
  if (paused) {
    clearTimeout(timer);
    timer = null;
    setState("paused", "muted");
  } else if (timer === null && !inFlight) {
    poll();
  }
});

poll();
