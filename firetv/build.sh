#!/bin/sh
# Build the Agent Sessions Fire TV app, and optionally install it.
#
#   ./build.sh http://192.168.1.20:8787 "$MANAGENTS_TOKEN"
#
# Writes config.js (server address and token) and app-release.apk. If
# FIRETV_IP is set, it also installs the APK over adb and opens the app. Turn
# on Settings > My Fire TV > Developer Options > ADB debugging first, and
# accept the prompt on the TV the first time.
#
# Needs JDK 17 and the Android SDK (ANDROID_HOME, default ~/Android/Sdk).
set -eu
cd "$(dirname "$0")"

url="${1:-}"
token="${2:-}"
case "$url" in
  http://*) ;;
  *) echo "usage: $0 http://YOUR_COMPUTER_IP:8787 [TOKEN]" >&2; exit 2 ;;
esac
url="${url%/}"
if printf '%s' "$url$token" | grep -q '["\\[:space:]]'; then
  echo "url and token must not contain quotes, backslashes or spaces" >&2
  exit 2
fi

export ANDROID_HOME="${ANDROID_HOME:-$HOME/Android/Sdk}"
if [ -z "${JAVA_HOME:-}" ] && [ -d /usr/lib/jvm/java-17-openjdk ]; then
  export JAVA_HOME=/usr/lib/jvm/java-17-openjdk
fi
[ -f local.properties ] || echo "sdk.dir=$ANDROID_HOME" > local.properties

printf 'window.LGAGENTS_CONFIG = {"url": "%s", "token": "%s"};\n' "$url" "$token" > config.js
./gradlew -q --console=plain assembleRelease
apk=app/build/outputs/apk/release/app-release.apk
cp "$apk" agent-sessions.apk
echo "built firetv/agent-sessions.apk"

[ -n "${FIRETV_IP:-}" ] || exit 0

device="$FIRETV_IP:5555"
adb connect "$device" >/dev/null
if ! adb -s "$device" get-state >/dev/null 2>&1; then
  echo "cannot reach $device: is ADB debugging on, and did you accept the prompt on the TV?" >&2
  exit 1
fi
echo "installing on $FIRETV_IP..."
adb -s "$device" install -r agent-sessions.apk >/dev/null
adb -s "$device" shell am start -S -n org.agentmonitor.firetv/.MainActivity >/dev/null
echo "installed. It should be open on your TV now."
