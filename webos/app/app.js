// The whole screen: a header (title, counts, clock, page) and nine cards per
// page, fed by the Poller. Port of the Roku channel's SessionsScene and
// SessionCard. Reads window.LGAGENTS_CONFIG ({url, token}) from config.js,
// which build.sh writes.
'use strict';

var PER_PAGE = 9;
var KEY = {
  LEFT: 37, UP: 38, RIGHT: 39, DOWN: 40, BACK: 461,
  REWIND: 412, FAST_FORWARD: 417, CH_UP: 33, CH_DOWN: 34
};

var el = {
  stage: document.getElementById('stage'),
  root: document.getElementById('root'),
  grid: document.getElementById('grid'),
  summary: document.getElementById('summary'),
  clock: document.getElementById('clock'),
  pageInfo: document.getElementById('pageInfo'),
  message: document.getElementById('message')
};

var state = {
  frame: null,
  frameAt: 0,      // Date.now() when the last frame arrived
  page: 0,
  lastKey: Date.now(),
  blinkOn: true,
  poller: null
};
var cards = [];

function secondsSince(t) {
  return Math.floor((Date.now() - t) / 1000);
}

// --- layout ------------------------------------------------------------------

function fitStage() {
  var scale = Math.min(window.innerWidth / 1920, window.innerHeight / 1080);
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
  if (state.page > 0 && secondsSince(state.lastKey) >= 30) state.page = 0;
  render();
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
  if (agents.length === 0) showMessage('No Claude sessions open');
  else hideMessage();

  var first = state.page * PER_PAGE;
  var visible = agents.slice(first, first + PER_PAGE);
  ensureCards(visible.length);
  for (var i = 0; i < visible.length; i++) renderCard(cards[i], visible[i], elapsed);

  renderHeader(frame, agents, pages, elapsed);
}

function ensureCards(count) {
  while (cards.length < count) {
    var node = document.createElement('div');
    node.innerHTML =
      '<div class="name"></div>' +
      '<div class="activity"><span></span></div>' +
      '<div class="row"><span class="status"></span><span class="folder"></span></div>' +
      '<div class="ctxTrack"></div><div class="ctxFill"></div>';
    var card = {
      node: node,
      name: node.querySelector('.name'),
      activityBox: node.querySelector('.activity'),
      activity: node.querySelector('.activity span'),
      status: node.querySelector('.status'),
      folder: node.querySelector('.folder'),
      ctxTrack: node.querySelector('.ctxTrack'),
      ctxFill: node.querySelector('.ctxFill')
    };
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

// --- remote ------------------------------------------------------------------

function changePage(delta) {
  if (state.frame === null) return;
  var pages = pageCount(state.frame.agents.length);
  state.page = (state.page + delta + pages) % pages;
  render();
}

function onKeyDown(e) {
  state.lastKey = Date.now();
  switch (e.keyCode) {
    case KEY.RIGHT: case KEY.DOWN: case KEY.FAST_FORWARD: case KEY.CH_DOWN:
      changePage(1);
      break;
    case KEY.LEFT: case KEY.UP: case KEY.REWIND: case KEY.CH_UP:
      changePage(-1);
      break;
    case KEY.BACK:
      if (window.webOSSystem) window.webOSSystem.platformBack();
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

function start() {
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
