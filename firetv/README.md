# Agent Sessions for Fire TV

The Fire TV app: a full-screen Android WebView showing the shared TV screen from `../webos/app`, plus a small native
part (`MainActivity.java`, `Discovery.java`) that:

- keeps the screen on while the app is open;
- maps the remote's buttons to the key codes the page already handles (arrows, Select, Back, Rewind, Fast-forward);
- answers the page's discovery requests with a UDP broadcast ([the exchange](../docs/http-api.md#discovery)), so the
  app finds your computer again when its address changes.

## Install and set up

On the Fire TV, turn on Settings > My Fire TV > Developer Options > **ADB debugging**. Then, from this repository:

```sh
./setup.sh YOUR_FIRE_TV_IP [http://YOUR_COMPUTER_IP:8787] [APK]
```

- **The APK is generic:** it holds no server address or token. `setup.sh` installs one (the APK you pass, else
  `agent-sessions.apk` if you built it, else the latest release's), then starts the app with the address and token
  as intent extras. The app stores them in its private preferences and uses them from then on.
- **The token** comes from `$MANAGENTS_TOKEN`, else `~/.config/managents/token`, the file the helper reads.
- **The address** defaults to this computer's address on the route to the TV, port 8787.
- **Rerun it** whenever the token or your computer changes. No rebuild is needed.

The first connection shows "Allow USB debugging?" on the TV: tick "Always allow from this computer". Until it is
set up, the app shows "Not set up yet" with the command to run.

## Build

You need JDK 17 and the Android SDK command-line tools with platform 35 (`ANDROID_HOME`, default `~/Android/Sdk`).
`./build.sh` writes `agent-sessions.apk`. An APK you build is signed with your own debug key, so it cannot update a
release install in place; `setup.sh` then uninstalls the old app first (and sets it up again).

## Files

| Path | What it does |
|---|---|
| `app/src/main/java/org/agentmonitor/firetv/MainActivity.java` | WebView, keep-screen-on, remote keys, stored settings, the `AgentBridge` the page calls |
| `app/src/main/java/org/agentmonitor/firetv/Discovery.java` | LAN discovery: broadcast, HMAC check |
| `app/build.gradle` | Copies `../webos/app` into the APK's assets at build time |
| `build.sh` | Builds `agent-sessions.apk` |
| `setup.sh` | Installs the app over adb and gives it the server address and token |

To debug the page, build with `./gradlew assembleDebug` and open `chrome://inspect` in Chrome while adb is connected.
