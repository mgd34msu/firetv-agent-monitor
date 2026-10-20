// Polls {base url}/state for as long as the app runs, like the Roku channel's
// PollTask. The base url starts as the address last discovered for this
// config (or config.js's url) and changes when discovery finds the helper
// somewhere else. Discovery itself runs in the app's service, since a page
// cannot send UDP.
'use strict';

var SERVICE = 'luna://org.lgagents.claudesessions.service/';
var STORAGE_KEY = 'lgagents.server';

// One Luna call. Keep the returned bridge while a subscription is open: a
// collected bridge cancels its call.
function luna(uri, params, onResponse) {
  if (typeof PalmServiceBridge === 'undefined') {
    if (onResponse) onResponse({ returnValue: false, errorText: 'not on webOS' });
    return null;
  }
  var bridge = new PalmServiceBridge();
  bridge.onservicecallback = function (raw) {
    var res;
    try { res = JSON.parse(raw); } catch (e) { res = { returnValue: false, errorText: String(raw) }; }
    if (onResponse) onResponse(res);
  };
  bridge.call(uri, JSON.stringify(params || {}));
  return bridge;
}

function Poller(config, onFrame, onFailure) {
  this.configUrl = config.url;
  this.token = config.token || '';
  this.onFrame = onFrame;
  this.onFailure = onFailure;
  this.intervalMs = 1000;
  this.failure = '';
  this.lastSuccess = Date.now();
  this.lastDiscovery = 0;
  // A new build with another url or token starts from its own config again.
  this.registryKey = this.configUrl + ' ' + fnv1a(this.token);
  this.baseUrl = this.savedUrl() || this.configUrl;
}

Poller.prototype.start = function () {
  this.loop();
};

Poller.prototype.loop = async function () {
  var ok = await this.fetchOnce();
  if (ok) {
    this.lastSuccess = Date.now();
  } else if (this.shouldDiscover()) {
    await this.rediscover();
  }
  var self = this;
  setTimeout(function () { self.loop(); }, this.intervalMs);
};

Poller.prototype.setFailure = function (text) {
  this.failure = text;
  this.onFailure(text);
};

Poller.prototype.fetchOnce = async function () {
  var stateUrl = this.baseUrl + '/state';
  var controller = new AbortController();
  var timer = setTimeout(function () { controller.abort(); }, 3000);
  var headers = {};
  if (this.token) headers.Authorization = 'Bearer ' + this.token;
  var res;
  try {
    res = await fetch(stateUrl, { headers: headers, cache: 'no-store', signal: controller.signal });
  } catch (e) {
    clearTimeout(timer);
    this.setFailure(e.name === 'AbortError' ? 'No answer from ' + stateUrl : 'Cannot reach ' + this.baseUrl);
    return false;
  }
  if (res.status === 200) {
    var frame = null;
    try { frame = await res.json(); } catch (e) { /* handled below */ }
    clearTimeout(timer);
    if (isStateFrame(frame)) {
      this.setFailure('');
      this.onFrame(frame);
      return true;
    }
    this.setFailure('The helper sent something this app cannot read');
    return false;
  }
  clearTimeout(timer);
  if (res.status === 401) {
    this.setFailure('The helper rejected the token. Rebuild the app with the right one.');
  } else if (res.status === 503) {
    this.setFailure('The helper is starting...');
  } else {
    this.setFailure('Unexpected answer ' + res.status + ' from ' + stateUrl);
  }
  return false;
};

function isStateFrame(frame) {
  return frame !== null && typeof frame === 'object' &&
    frame.v === 1 && frame.t === 'state' && Array.isArray(frame.agents);
}

// --- discovery (docs/http-api.md#discovery) ---------------------------------

// After 3 seconds without a good answer, at most every 5 seconds, and only
// with a token of 32 characters or more, as the helper requires.
Poller.prototype.shouldDiscover = function () {
  var now = Date.now();
  return this.token.length >= 32 && now - this.lastSuccess >= 3000 && now - this.lastDiscovery >= 5000;
};

Poller.prototype.rediscover = function () {
  this.lastDiscovery = Date.now();
  var self = this;
  return new Promise(function (resolve) {
    platformDiscover(self.token, urlPort(self.configUrl), function (found) {
      if (found === '') {
        // Nobody answered. Maybe a helper without discovery is back at the
        // configured address, so poll that until discovery finds something.
        found = self.configUrl;
      } else {
        self.saveUrl(found);
      }
      self.baseUrl = found;
      resolve();
    });
  });
};

// Broadcasts a discovery request through the platform: the webOS service, or
// the Fire TV app's AgentBridge, which answers by calling
// window.agentDiscovered(url). done gets the verified url, or "".
function platformDiscover(token, port, done) {
  if (window.AgentBridge) {
    window.agentDiscovered = function (url) {
      window.agentDiscovered = null;
      done(typeof url === 'string' ? url : '');
    };
    window.AgentBridge.discover(token, port);
    return;
  }
  luna(SERVICE + 'discover', { token: token, port: port }, function (res) {
    done(res && res.returnValue && typeof res.url === 'string' ? res.url : '');
  });
}

function urlPort(url) {
  var m = /^http:\/\/[^\/:]+:(\d+)/.exec(url);
  return m ? Number(m[1]) : 80;
}

// --- last discovered address ----------------------------------------------

// Only tells builds apart; a collision costs one rediscovery.
function fnv1a(text) {
  var h = 0x811c9dc5;
  for (var i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h.toString(16);
}

Poller.prototype.savedUrl = function () {
  try {
    var saved = JSON.parse(localStorage.getItem(STORAGE_KEY) || 'null');
    if (saved && saved.key === this.registryKey && typeof saved.url === 'string' &&
        saved.url.indexOf('http://') === 0) {
      return saved.url;
    }
  } catch (e) { /* no storage: start from the config */ }
  return '';
};

Poller.prototype.saveUrl = function (url) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ key: this.registryKey, url: url }));
  } catch (e) { /* the next start rediscovers */ }
};
