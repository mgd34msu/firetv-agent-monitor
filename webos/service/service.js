// The app's helper service on the TV. A web page cannot send UDP or keep the
// TV's screensaver away, so the app asks this service for two things:
//
//   discover    broadcasts a discovery request (docs/http-api.md#discovery)
//               and answers with the first base url signed with the token.
//   keepAwake   a subscription; while it is open, the service declines every
//               screensaver request from the TV. Closing the app closes it.
//
// Runs on the TV's Node 8, so plain ES2015 and no npm dependencies.
'use strict';

var crypto = require('crypto');
var dgram = require('dgram');
var os = require('os');
var Service = require('webos-service');

var SERVICE_ID = 'org.lgagents.claudesessions.service';
var service = new Service(SERVICE_ID);

// --- discovery -------------------------------------------------------------

function hex(hash) {
  return hash.digest('hex');
}

function tokenId(token) {
  return hex(crypto.createHash('sha256').update(token, 'ascii')).slice(0, 16);
}

function sign(token, message) {
  return hex(crypto.createHmac('sha256', token).update(message, 'ascii')).slice(0, 32);
}

// The limited broadcast address plus each interface's own broadcast address,
// for routers that drop the limited one.
function broadcastHosts() {
  var hosts = ['255.255.255.255'];
  var ifaces = os.networkInterfaces();
  Object.keys(ifaces).forEach(function (name) {
    ifaces[name].forEach(function (a) {
      if (a.family !== 'IPv4' || a.internal) return;
      var ip = a.address.split('.').map(Number);
      var mask = a.netmask.split('.').map(Number);
      var bcast = ip.map(function (octet, i) { return (octet | (~mask[i] & 255)); }).join('.');
      if (hosts.indexOf(bcast) < 0) hosts.push(bcast);
    });
  });
  return hosts;
}

// Returns the url in "rokuagents v1 <url> <mac>" when mac proves the sender
// holds the token and saw this nonce, or "".
function verifiedUrl(reply, token, nonce) {
  var parts = reply.trim().split(' ');
  if (parts.length !== 4 || parts[0] !== 'rokuagents' || parts[1] !== 'v1') return '';
  var url = parts[2];
  if (url.indexOf('http://') !== 0) return '';
  if (parts[3].toLowerCase() !== sign(token, nonce + ' ' + url)) return '';
  return url;
}

function discover(token, port, timeoutMs, done) {
  var nonce = crypto.randomBytes(8).toString('hex');
  var request = Buffer.from('rokuagents-discover v1 ' + tokenId(token) + ' ' + nonce, 'ascii');
  var sock = dgram.createSocket('udp4');
  var finished = false;
  var timer;
  function finish(url) {
    if (finished) return;
    finished = true;
    clearTimeout(timer);
    try { sock.close(); } catch (e) { /* already closed */ }
    done(url);
  }
  sock.on('error', function () { finish(''); });
  sock.on('message', function (msg) {
    var url = verifiedUrl(msg.toString('ascii'), token, nonce);
    if (url) finish(url);
  });
  sock.bind(0, function () {
    sock.setBroadcast(true);
    broadcastHosts().forEach(function (host) {
      sock.send(request, 0, request.length, port, host);
    });
    timer = setTimeout(function () { finish(''); }, timeoutMs);
  });
}

service.register('discover', function (message) {
  var p = message.payload || {};
  var token = typeof p.token === 'string' ? p.token : '';
  var port = Number(p.port);
  // The helper ignores tokens under 32 characters; so do we (see the doc).
  if (token.length < 32 || !(port > 0 && port < 65536)) {
    message.respond({ returnValue: false, errorText: 'need a token of 32+ characters and a port' });
    return;
  }
  discover(token, port, 1500, function (url) {
    message.respond({ returnValue: true, url: url });
  });
});

// --- screensaver -------------------------------------------------------------

var awakeClients = {};
var screensaverSub = null;

function awakeCount() {
  return Object.keys(awakeClients).length;
}

// The TV asks registered clients before starting its screensaver, with a
// timestamp to quote back; ack false declines.
function startBlocking() {
  if (screensaverSub) return;
  screensaverSub = service.subscribe('luna://com.webos.service.tvpower/power/registerScreenSaverRequest',
    { subscribe: true, clientName: SERVICE_ID });
  screensaverSub.on('response', function (msg) {
    var ts = msg.payload && msg.payload.timestamp;
    if (ts === undefined) return;
    service.call('luna://com.webos.service.tvpower/power/responseScreenSaverRequest',
      { clientName: SERVICE_ID, ack: false, timestamp: ts }, function () {});
  });
  screensaverSub.on('cancel', function () { screensaverSub = null; });
}

function stopBlocking() {
  if (!screensaverSub) return;
  screensaverSub.cancel();
  screensaverSub = null;
}

service.register('keepAwake', function (message) {
  if (!message.isSubscription) {
    message.respond({ returnValue: false, errorText: 'subscribe: true is required' });
    return;
  }
  awakeClients[message.uniqueToken] = true;
  startBlocking();
  message.respond({ returnValue: true, subscribed: true });
}, function (message) {
  delete awakeClients[message.uniqueToken];
  if (awakeCount() === 0) stopBlocking();
});
