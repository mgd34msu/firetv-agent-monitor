// Shared formatting helpers: the same shapes as the Roku channel and the desk
// display. Plain ES2017 for webOS 5's Chromium 68.
'use strict';

var STATUS_LABELS = {
  working: 'Working',
  waiting: 'Needs you',
  error: 'Error',
  idle: 'Idle'
};

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
  module.exports = { statusClass: statusClass, statusLabel: statusLabel, formatAge: formatAge, formatClock: formatClock };
}
