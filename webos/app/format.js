// Shared formatting helpers: the same shapes as the Roku channel and the desk
// display. Plain ES2017 for webOS 5's Chromium 68.
'use strict';

var STATUS_LABELS = {
  working: 'Working',
  waiting: 'Needs you',
  error: 'Error',
  idle: 'Idle',
  open: 'Open'
};

var KIND_LABELS = {
  claude: 'Claude',
  codex: 'Codex',
  muse: 'Muse',
  goodvibes: 'GoodVibes',
  opencode: 'OpenCode'
};

function kindLabel(kind) {
  return KIND_LABELS.hasOwnProperty(kind) ? KIND_LABELS[kind] : String(kind || 'Agent');
}

// 238K, 1.2M: token counts for the panel.
function formatTokens(n) {
  if (n >= 1e6) return (Math.round(n / 1e5) / 10) + 'M';
  if (n >= 1e3) return Math.round(n / 1e3) + 'K';
  return String(n);
}

// "in 3h 20m" or "Tue 2:00 PM" for a reset time, from UNIX seconds.
function formatReset(resetsAt, nowSecs) {
  if (!resetsAt) return '';
  var left = resetsAt - nowSecs;
  if (left <= 0) return 'resetting';
  if (left < 86400) return 'resets in ' + formatAge(left);
  var d = new Date(resetsAt * 1000);
  var days = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
  var h = d.getHours();
  var ampm = h >= 12 ? 'PM' : 'AM';
  h = h % 12 || 12;
  return 'resets ' + days[d.getDay()] + ' ' + h + ':' + pad2(d.getMinutes()) + ' ' + ampm;
}

function statusClass(status) {
  return STATUS_LABELS.hasOwnProperty(status) ? status : 'unknown';
}

function statusLabel(status) {
  return STATUS_LABELS.hasOwnProperty(status) ? STATUS_LABELS[status] : 'Unknown';
}

// 42s, 5m, 1h 12m, 2h, 1d 2h, 3d.
function formatAge(seconds) {
  var s = Math.max(0, Math.floor(seconds));
  if (s < 60) return s + 's';
  if (s < 3600) return Math.floor(s / 60) + 'm';
  if (s < 86400) {
    var h = Math.floor(s / 3600);
    var mins = Math.floor((s % 3600) / 60);
    if (mins === 0 || h >= 10) return h + 'h';
    return h + 'h ' + mins + 'm';
  }
  var d = Math.floor(s / 86400);
  var hd = Math.floor((s % 86400) / 3600);
  if (hd === 0 || d >= 3) return d + 'd';
  return d + 'd ' + hd + 'h';
}

// HH:MM from a local UNIX time (host time plus its UTC offset).
function formatClock(localEpoch) {
  var daySecs = Math.floor(localEpoch) % 86400;
  if (daySecs < 0) daySecs += 86400;
  return pad2(Math.floor(daySecs / 3600)) + ':' + pad2(Math.floor((daySecs % 3600) / 60));
}

function pad2(n) {
  return n < 10 ? '0' + n : String(n);
}

if (typeof module !== 'undefined') {
  module.exports = {
    statusClass: statusClass, statusLabel: statusLabel, kindLabel: kindLabel, formatAge: formatAge,
    formatClock: formatClock, formatTokens: formatTokens, formatReset: formatReset
  };
}
