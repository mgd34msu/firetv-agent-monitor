// The whole screen: a header (title, counts, page) and six session cards per
// page on the left two thirds; on the right third, the clock, plan usage per
// provider and context usage per session. Fed by the Poller. The cards are a
// port of the Roku channel's SessionsScene and SessionCard. Reads
// window.LGAGENTS_CONFIG ({url, token}) from config.js, which build.sh writes.
//
// The Magic Remote's pointer highlights a card and a click opens it in the
// read-only session view (detail.js); the arrow keys move the highlight and
// OK opens it.
'use strict';

var PER_PAGE = 6;
// The panel lists this many sessions; the rest are counted.
var TOKEN_ROWS = 9;
// Providers with rate limits; Muse is listed even before it reports any.
var LIMIT_KINDS = ['claude', 'codex', 'muse'];
var KEY = {
  LEFT: 37, UP: 38, RIGHT: 39, DOWN: 40, ENTER: 13, BACK: 461,
  REWIND: 412, FAST_FORWARD: 417, CH_UP: 33, CH_DOWN: 34
};

var el = {
  stage: document.getElementById('stage'),
  root: document.getElementById('root'),
  grid: document.getElementById('grid'),
  summary: document.getElementById('summary'),
  clock: document.getElementById('clock'),
  pageInfo: document.getElementById('pageInfo'),
  message: document.getElementById('message'),
  limits: document.getElementById('limits'),
  tokens: document.getElementById('tokens')
};

var state = {
  frame: null,
  frameAt: 0,      // Date.now() when the last frame arrived
  page: 0,
  lastKey: Date.now(),
  blinkOn: true,
  poller: null,
  focus: -1        // highlighted card on this page, -1 for none
};
var cards = [];

function secondsSince(t) {
  return Math.floor((Date.now() - t) / 1000);
}

// --- layout ------------------------------------------------------------------

function fitStage() {
  // The layout size, not innerWidth: Fire TV's WebView reports the screen's
  // device-independent width there (1920) while laying out at 960.
  var root = document.documentElement;
  var w = root.clientWidth || window.innerWidth;
  var h = root.clientHeight || window.innerHeight;
  var scale = Math.min(w / 1920, h / 1080);
  el.stage.style.transform = 'scale(' + scale + ')';
}

function showMessage(text) {
  el.message.textContent = text;
  el.message.hidden = false;
}

function hideMessage() {
  el.message.hidden = true;
}

// --- data in -----------------------------------------------------------------

function onFrame(frame) {
  state.frame = frame;
  state.frameAt = Date.now();
  render();
}

function onFailure(reason) {
  // Before the first frame, say why we are not connected yet. After that,
  // tick decides when a failure has lasted long enough to show.
  if (state.frame === null && reason !== '') showMessage(reason);
}

// --- clocks ------------------------------------------------------------------

function tick() {
  if (state.frame === null) return;
  if (secondsSince(state.frameAt) >= 6) {
    var reason = state.poller.failure || 'Is your computer asleep?';
    el.grid.style.opacity = 0.2;
    showMessage('Reconnecting... ' + reason);
    return;
  }
  if (state.page > 0 && !detail.open && secondsSince(state.lastKey) >= 30) state.page = 0;
  render();
  updateDetailHeader();
}

function blink() {
  state.blinkOn = !state.blinkOn;
  if (state.frame !== null && secondsSince(state.frameAt) < 6) render();
}

// --- drawing -----------------------------------------------------------------

function pageCount(total) {
  return Math.max(1, Math.ceil(total / PER_PAGE));
}

function render() {
  var frame = state.frame;
  if (frame === null) return;
  var agents = frame.agents;
  var pages = pageCount(agents.length);
  if (state.page >= pages) state.page = pages - 1;
  var elapsed = secondsSince(state.frameAt);

  el.grid.style.opacity = 1;
  if (agents.length === 0) showMessage('No agent sessions open');
  else hideMessage();

  var first = state.page * PER_PAGE;
  var visible = agents.slice(first, first + PER_PAGE);
  ensureCards(visible.length);
  if (state.focus >= visible.length) state.focus = visible.length - 1;
  // Without a pointer (Fire TV), a card is always highlighted.
  if (window.AgentBridge && state.focus < 0 && visible.length > 0) state.focus = 0;
  for (var i = 0; i < visible.length; i++) {
    renderCard(cards[i], visible[i], elapsed);
    if (i === state.focus) cards[i].node.className += ' focused';
  }

  renderHeader(frame, agents, pages, elapsed);
  renderPanel(frame, agents, elapsed);
}

function ensureCards(count) {
  while (cards.length < count) {
    var node = document.createElement('div');
    node.innerHTML =
      '<div class="top"><div class="name"></div><div class="kind"></div></div>' +
      '<div class="activity"><span></span></div>' +
      '<div class="row"><span class="status"></span><span class="folder"></span></div>' +
      '<div class="ctxTrack"></div><div class="ctxFill"></div>';
    var card = {
      node: node,
      name: node.querySelector('.name'),
      kind: node.querySelector('.kind'),
      activityBox: node.querySelector('.activity'),
      activity: node.querySelector('.activity span'),
      status: node.querySelector('.status'),
      folder: node.querySelector('.folder'),
      ctxTrack: node.querySelector('.ctxTrack'),
      ctxFill: node.querySelector('.ctxFill')
    };
    node.addEventListener('mouseenter', focusCard.bind(null, cards.length));
    node.addEventListener('click', openCard.bind(null, cards.length));
    el.grid.appendChild(node);
    cards.push(card);
  }
  while (cards.length > count) el.grid.removeChild(cards.pop().node);
}

function setText(node, text) {
  if (node.textContent !== text) node.textContent = text;
}

function renderCard(card, a, elapsed) {
  var age = elapsed + (typeof a.age === 'number' ? a.age : 0);
  var name = typeof a.name === 'string' ? a.name : '';
  var title = typeof a.title === 'string' ? a.title : '';
  var activity = typeof a.activity === 'string' ? a.activity : '';

  var cls = 'card ' + statusClass(a.status);
  // With a title, it is the headline and the folder name moves down beside
  // the status. Without one, the folder name is the headline.
  if (title !== '') cls += ' titled';
  if (a.status === 'error' && age < 60 && !state.blinkOn) cls += ' blinkOff';
  if (card.node.className !== cls) card.node.className = cls;

  setText(card.name, title !== '' ? title : name);
  setText(card.kind, kindLabel(a.kind));
  setText(card.folder, title !== '' ? name : '');
  setText(card.activity, activity);
  card.activityBox.style.visibility = activity === '' ? 'hidden' : 'visible';
  setText(card.status, statusLabel(a.status) + ' ' + formatAge(age));

  // Context bar: only when the limit is known, never numbers.
  var ctx = a.ctx;
  var hasLimit = ctx !== null && typeof ctx === 'object' &&
    typeof ctx.used === 'number' && typeof ctx.limit === 'number' && ctx.limit > 0;
  var fraction = hasLimit ? Math.min(1, ctx.used / ctx.limit) : 0;
  card.ctxTrack.style.display = hasLimit ? 'block' : 'none';
  card.ctxFill.style.display = fraction > 0 ? 'block' : 'none';
  card.ctxFill.style.width = (fraction * 100) + '%';
}

function renderHeader(frame, agents, pages, elapsed) {
  var working = 0, waiting = 0, errors = 0;
  agents.forEach(function (a) {
    if (a.status === 'working') working++;
    if (a.status === 'waiting') waiting++;
    if (a.status === 'error') errors++;
  });
  var parts = [];
  if (working > 0) parts.push(working + ' working');
  if (waiting > 0) parts.push(waiting + ' need you');
  if (errors > 0) parts.push(errors + ' failed');
  if (parts.length === 0 && agents.length > 0) parts.push('All idle');
  setText(el.summary, parts.join(',  '));

  var tz = typeof frame.tz === 'number' ? frame.tz : 0;
  setText(el.clock, formatClock(frame.now + tz + elapsed));

  var info = pages > 1 ? 'Page ' + (state.page + 1) + ' of ' + pages : '';
  var more = typeof frame.more === 'number' ? frame.more : 0;
  if (more > 0) info += (info ? '   ' : '') + '+' + more + ' more';
  setText(el.pageInfo, info);
}

// --- panel -----------------------------------------------------------------

// Rebuilt only when its text changes, so the TV is not relaying out every
// second for nothing.
var panelCache = { limits: '', tokens: '' };

function esc(text) {
  return String(text).replace(/[&<>"]/g, function (c) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c];
  });
}

function barHtml(fraction, cls) {
  var pct = Math.max(0, Math.min(1, fraction)) * 100;
  return '<div class="bar"><div class="fill ' + (cls || '') + '" style="width:' + pct.toFixed(1) + '%"></div></div>';
}

function severity(used) {
  return used >= 90 ? 'high' : used >= 70 ? 'warn' : '';
}

function renderPanel(frame, agents, elapsed) {
  var nowSecs = frame.now + elapsed;
  var usage = Array.isArray(frame.usage) ? frame.usage : [];
  var byKind = {};
  usage.forEach(function (p) { if (p && p.kind) byKind[p.kind] = p; });

  var html = '';
  LIMIT_KINDS.forEach(function (kind) {
    var p = byKind[kind];
    html += '<div class="provider"><div class="head">' + esc(kindLabel(kind));
    if (p && p.plan) html += '<span class="plan">' + esc(p.plan) + '</span>';
    if (p && p.asOf && nowSecs - p.asOf > 900) html += '<span class="asOf">as of ' + esc(formatAge(nowSecs - p.asOf)) + ' ago</span>';
    html += '</div>';
    var windows = p && Array.isArray(p.windows) ? p.windows : [];
    if (windows.length === 0) {
      // Meta leaves out Muse's numbers while its 5-hour window is idle.
      html += '<div class="note">' + (p && kind === 'muse' ? '5-hour window idle' : 'No usage reported yet') + '</div>';
    }
    windows.forEach(function (w) {
      var used = typeof w.used === 'number' ? w.used : 0;
      html += '<div class="window"><span class="label">' + esc(w.label) + '</span>' +
        barHtml(used / 100, severity(used)) +
        '<span class="pct">' + used + '%</span>' +
        '<span class="resets">' + esc(formatReset(w.resetsAt, nowSecs)) + '</span></div>';
    });
    html += '</div>';
  });
  if (html !== panelCache.limits) {
    el.limits.innerHTML = html;
    panelCache.limits = html;
  }

  html = '';
  agents.slice(0, TOKEN_ROWS).forEach(function (a) {
    var title = typeof a.title === 'string' && a.title !== '' ? a.title : a.name;
    var ctx = a.ctx;
    var used = ctx && typeof ctx.used === 'number' ? ctx.used : null;
    var limit = ctx && typeof ctx.limit === 'number' && ctx.limit > 0 ? ctx.limit : null;
    var count = used === null ? 'no data' : formatTokens(used) + (limit ? ' / ' + formatTokens(limit) : '');
    html += '<div class="tokenRow"><div class="line">' +
      '<span class="dot ' + statusClass(a.status) + '"></span>' +
      '<span class="who">' + esc(kindLabel(a.kind)) + ' \u00b7 ' + esc(title) + '</span>' +
      '<span class="count">' + esc(count) + '</span></div>' +
      (limit ? barHtml(used / limit, severity(used / limit * 100)) : '') + '</div>';
  });
  if (agents.length > TOKEN_ROWS) html += '<div class="tokenMore">+' + (agents.length - TOKEN_ROWS) + ' more</div>';
  if (agents.length === 0) html = '<div class="tokenMore">No sessions</div>';
  if (html !== panelCache.tokens) {
    el.tokens.innerHTML = html;
    panelCache.tokens = html;
    fitTokens(agents.length);
  }
}

// Drops the session rows that would run past the bottom of the panel (the
// plan-usage section above grows with each provider's windows) and counts
// them in the "+N more" line instead.
function fitTokens(total) {
  var bottom = document.getElementById('panel').getBoundingClientRect().bottom;
  var rows = el.tokens.querySelectorAll('.tokenRow');
  var more = el.tokens.querySelector('.tokenMore');
  var reserve = more || rows.length < total ? 40 : 0;
  var shown = rows.length;
  for (var i = rows.length - 1; i >= 0 && rows[i].getBoundingClientRect().bottom > bottom - reserve; i--) {
    el.tokens.removeChild(rows[i]);
    shown--;
    reserve = 40;
  }
  if (shown < total) {
    if (!more) {
      more = document.createElement('div');
      more.className = 'tokenMore';
      el.tokens.appendChild(more);
    }
    more.textContent = '+' + (total - shown) + ' more';
  }
}

// --- remote ------------------------------------------------------------------

function changePage(delta) {
  if (state.frame === null) return;
  var pages = pageCount(state.frame.agents.length);
  state.page = (state.page + delta + pages) % pages;
  render();
}

function focusCard(i) {
  state.lastKey = Date.now();
  if (state.focus === i) return;
  state.focus = i;
  render();
}

function openCard(i) {
  if (state.frame === null) return;
  openDetail(state.frame.agents[state.page * PER_PAGE + i]);
}

// Moves the highlight on the 2-column grid; past the first or last card it
// turns the page.
function moveFocus(dx, dy) {
  if (state.frame === null || cards.length === 0) return;
  if (state.focus < 0) {
    focusCard(0);
    return;
  }
  var next = state.focus + dx + dy * 2;
  if (next >= cards.length && dx + dy > 0) {
    changePage(1);
    state.focus = 0;
  } else if (next < 0) {
    changePage(-1);
    state.focus = cards.length - 1;
  } else {
    state.focus = Math.max(0, Math.min(cards.length - 1, next));
  }
  render();
}

function onKeyDown(e) {
  state.lastKey = Date.now();
  if (detailKey(e)) {
    e.preventDefault();
    return;
  }
  switch (e.keyCode) {
    case KEY.RIGHT: moveFocus(1, 0); break;
    case KEY.LEFT: moveFocus(-1, 0); break;
    case KEY.DOWN: moveFocus(0, 1); break;
    case KEY.UP: moveFocus(0, -1); break;
    case KEY.ENTER:
      if (state.focus >= 0) openCard(state.focus);
      break;
    case KEY.FAST_FORWARD: case KEY.CH_DOWN:
      changePage(1);
      break;
    case KEY.REWIND: case KEY.CH_UP:
      changePage(-1);
      break;
    case KEY.BACK:
      if (window.AgentBridge) window.AgentBridge.exit();
      else if (window.webOSSystem) window.webOSSystem.platformBack();
      else if (window.PalmSystem) window.PalmSystem.platformBack();
      break;
    default:
      return;
  }
  e.preventDefault();
}

// --- keeping the TV's screensaver away ---------------------------------------

// The service declines screensaver requests while this subscription is open.
// It closes with the page, and while the app is in the background.
var keepAwakeBridge = null;

function keepAwake(on) {
  if (window.AgentBridge) return; // the Fire TV app keeps the screen on itself
  if (on && keepAwakeBridge === null) {
    keepAwakeBridge = luna(SERVICE + 'keepAwake', { subscribe: true }, function () {});
  } else if (!on && keepAwakeBridge !== null) {
    keepAwakeBridge.cancel();
    keepAwakeBridge = null;
  }
}

// --- start -------------------------------------------------------------------

function loadConfig() {
  var cfg = window.LGAGENTS_CONFIG;
  if (!cfg || typeof cfg.url !== 'string' || cfg.url.indexOf('http://') !== 0) return null;
  return { url: cfg.url.replace(/\/+$/, ''), token: typeof cfg.token === 'string' ? cfg.token : '' };
}

// The Fire TV app forwards the remote's keys with their webOS key codes.
window.remoteKey = function (code) {
  onKeyDown({ keyCode: code, preventDefault: function () {} });
};

function start() {
  if (window.AgentBridge) {
    document.getElementById('detailHint').textContent =
      'Read only \u00b7 Up/Down to scroll \u00b7 \u23ea \u23e9 a page at a time \u00b7 Back to close';
  }
  fitStage();
  window.addEventListener('resize', fitStage);
  document.addEventListener('keydown', onKeyDown);
  document.addEventListener('visibilitychange', function () { keepAwake(!document.hidden); });

  var config = loadConfig();
  if (config === null) {
    showMessage('config.js is missing or has no url. Rebuild the app with build.sh.');
    return;
  }
  keepAwake(true);
  state.poller = new Poller(config, onFrame, onFailure);
  state.poller.start();
  setInterval(tick, 1000);
  setInterval(blink, 500);
  showMessage('Connecting to your computer...');
}

start();
