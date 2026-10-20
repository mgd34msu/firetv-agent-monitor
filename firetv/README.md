# Agent Sessions for Fire TV

The Fire TV app: a full-screen Android WebView showing the shared TV screen from `../webos/app`, plus a small native
part (`MainActivity.java`, `Discovery.java`) that:

- keeps the screen on while the app is open;
- maps the remote's buttons to the key codes the page already handles (arrows, Select, Back, Rewind, Fast-forward);
- answers the page's discovery requests with a UDP broadcast ([the exchange](../docs/http-api.md#discovery)), so the
  app finds your computer again when its address changes.

## Build and install

You need JDK 17 and the Android SDK command-line tools with platform 35 (`ANDROID_HOME`, default `~/Android/Sdk`).
On the Fire TV, turn on Settings > My Fire TV > Developer Options > **ADB debugging**.

```sh
FIRETV_IP=YOUR_FIRE_TV_IP ./build.sh http://YOUR_COMPUTER_IP:8787 "$(cat ~/.config/managents/token)"
```

The first install shows "Allow USB debugging?" on the TV: tick "Always allow from this computer". Without
`FIRETV_IP` the script only builds `agent-sessions.apk`, which you can install with `adb install -r`.

The server address and token are written into `config.js` and packaged into the app, so rebuild when the token
changes. An address change is handled by discovery.

## Files

| Path | What it does |
|---|---|
| `app/src/main/java/org/agentmonitor/firetv/MainActivity.java` | WebView, keep-screen-on, remote keys, the `AgentBridge` the page calls |
| `app/src/main/java/org/agentmonitor/firetv/Discovery.java` | LAN discovery: broadcast, HMAC check |
| `app/build.gradle` | Copies `../webos/app` into the APK's assets at build time |
| `build.sh` | Writes `config.js`, builds, installs over adb |

To debug the page, open `chrome://inspect` in Chrome on the computer while adb is connected.
