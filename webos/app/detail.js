// The read-only session view: one session's recent conversation, full screen,
// refreshed every two seconds from GET /session (serve --transcripts). Opened
// by clicking a card with the Magic Remote's pointer or pressing OK on the
// focused card; Back or the close button returns to the cards. Nothing here
// sends anything to the session.
'use strict';

var DETAIL_REFRESH_MS = 2000;
var SCROLL_STEP = 240;

var detail = {
  open: false,
  id: '',
  kind: '',
  timer: 0,
  lastBody: '',
  node: document.getElementById('detail'),
  kindTag: document.getElementById('detailKind'),
  title: document.getElementById('detailTitle'),
  sub: document.getElementById('detailSub'),
  list: document.getElementById('detailMessages'),
  close: document.getElementById('detailClose')
};

function openDetail(agent) {
  if (!agent || !state.poller) return;
  detail.open = true;
  detail.id = agent.id;
  detail.kind = agent.kind;
  detail.lastBody = '';
  detail.kindTag.textContent = kindLabel(agent.kind);
  detail.title.textContent = agent.title || agent.name || '';
  detail.list.innerHTML = '<div class="detailNote">Loading the session...</div>';
  detail.node.hidden = false;
  updateDetailHeader();
  fetchDetail();
  detail.timer = setInterval(fetchDetail, DETAIL_REFRESH_MS);
}

function closeDetail() {
  detail.open = false;
  clearInterval(detail.timer);
  detail.node.hidden = true;
  detail.list.innerHTML = '';
  state.lastKey = Date.now();
}

// The status line follows the cards' data, so it moves every second.
function updateDetailHeader() {
  if (!detail.open || state.frame === null) return;
  var a = null;
  state.frame.agents.forEach(function (x) { if (x.id === detail.id) a = x; });
  if (a === null) {
    detail.sub.textContent = 'This session has closed';
    return;
  }
  var age = secondsSince(state.frameAt) + (typeof a.age === 'number' ? a.age : 0);
  var parts = [statusLabel(a.status) + ' ' + formatAge(age), a.name];
  if (a.ctx && a.ctx.limit > 0) parts.push(formatTokens(a.ctx.used) + ' / ' + formatTokens(a.ctx.limit) + ' context');
  detail.sub.textContent = parts.join('   ·   ');
  detail.node.className = 'status-' + statusClass(a.status);
}

async function fetchDetail() {
  var id = detail.id;
  var poller = state.poller;
  var headers = {};
  if (poller.token) headers.Authorization = 'Bearer ' + poller.token;
  var res;
  try {
    res = await fetch(poller.baseUrl + '/session?id=' + encodeURIComponent(id), { headers: headers, cache: 'no-store' });
  } catch (e) {
    return; // the cards say when the computer is unreachable
  }
  if (!detail.open || detail.id !== id) return;
  if (res.status === 404) {
    var why = await res.text();
    showDetailNote(why.indexOf('--transcripts') >= 0
      ? 'The session view is off. Start the helper with --transcripts.'
      : 'No conversation to show for this session yet.');
    return;
  }
  if (!res.ok) {
    showDetailNote('The helper could not read this session (' + res.status + ').');
    return;
  }
  var body = await res.text();
  if (body === detail.lastBody) return;
  detail.lastBody = body;
  var view;
  try { view = JSON.parse(body); } catch (e) { return; }
  renderDetail(view);
}

function showDetailNote(text) {
  detail.lastBody = '';
  detail.list.innerHTML = '<div class="detailNote">' + esc(text) + '</div>';
}

function renderDetail(view) {
  var messages = Array.isArray(view.messages) ? view.messages : [];
  if (view.title) detail.title.textContent = view.title;
  // Stay at the bottom while new messages arrive, unless scrolled up to read.
  var list = detail.list;
  var atBottom = list.scrollTop + list.clientHeight >= list.scrollHeight - 40 || list.querySelector('.detailNote');
  var html = '';
  var prevRole = '';
  messages.forEach(function (m) {
    var text = esc(m.text || '');
    if (m.role === 'user') {
      html += '<div class="msg user"><div class="who">You</div><div class="text">' + text + '</div></div>';
    } else if (m.role === 'assistant' && m.tool) {
      html += '<div class="msg call"><span class="tool">' + esc(m.tool) + '</span><code>' + text + '</code></div>';
    } else if (m.role === 'assistant') {
      var who = prevRole === 'assistant' ? '' : '<div class="who">' + esc(kindLabel(detail.kind)) + '</div>';
      html += '<div class="msg assistant">' + who + '<div class="text">' + text + '</div></div>';
    } else if (m.role === 'tool') {
      html += '<div class="msg output' + (m.error ? ' error' : '') + '"><pre>' + text + '</pre></div>';
    }
    prevRole = m.role === 'assistant' && !m.tool ? 'assistant' : m.role + (m.tool ? 'call' : '');
  });
  if (html === '') html = '<div class="detailNote">No conversation to show for this session yet.</div>';
  list.innerHTML = html;
  if (atBottom) list.scrollTop = list.scrollHeight;
}

// Keys while the view is open; returns whether it took the key.
function detailKey(e) {
  if (!detail.open) return false;
  var list = detail.list;
  switch (e.keyCode) {
    case KEY.BACK:
      closeDetail();
      return true;
    case KEY.UP:
      list.scrollTop -= SCROLL_STEP;
      return true;
    case KEY.DOWN:
      list.scrollTop += SCROLL_STEP;
      return true;
    case KEY.CH_UP: case KEY.REWIND: case KEY.LEFT:
      list.scrollTop -= list.clientHeight * 0.9;
      return true;
    case KEY.CH_DOWN: case KEY.FAST_FORWARD: case KEY.RIGHT:
      list.scrollTop += list.clientHeight * 0.9;
      return true;
    case KEY.ENTER:
      return true;
  }
  return false;
}

detail.close.addEventListener('click', closeDetail);
