#!/bin/sh
# Install the Agent Sessions app on a Fire TV and point it at your computer.
#
#   ./setup.sh FIRE_TV_IP [http://YOUR_COMPUTER_IP:8787] [APK]
#
# The token comes from $MANAGENTS_TOKEN, else ~/.config/managents/token (the
# file the helper reads). The server address defaults to this computer's
# address on the way to the TV, port 8787. The APK is the given file, else
# firetv/agent-sessions.apk if you built one, else the latest release; it is
# skipped when the app is already installed and none was given or built.
#
# Turn on Settings > My Fire TV > Developer Options > ADB debugging first, and
# accept the prompt on the TV the first time. Rerun this whenever the token or
# your computer changes: the app keeps the settings, not the APK.
set -eu
cd "$(dirname "$0")"

tv="${1:-}"
if [ -z "$tv" ]; then
  echo "usage: $0 FIRE_TV_IP [http://YOUR_COMPUTER_IP:8787] [APK]" >&2
  exit 2
fi
url="${2:-}"
apk="${3:-}"
pkg=org.agentmonitor.firetv
repo=mgd34msu/firetv-agent-monitor

token="${MANAGENTS_TOKEN:-}"
if [ -z "$token" ] && [ -f "$HOME/.config/managents/token" ]; then
  token=$(cat "$HOME/.config/managents/token")
fi
if [ -z "$token" ]; then
  echo "no token: set MANAGENTS_TOKEN or create ~/.config/managents/token" >&2
  exit 2
fi
if [ -z "$url" ]; then
  me=$(ip -4 route get "$tv" 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p')
  if [ -z "$me" ]; then
    echo "cannot tell this computer's address; pass http://YOUR_COMPUTER_IP:8787" >&2
    exit 2
  fi
  url="http://$me:8787"
fi
case "$url" in http://*) ;; *) echo "the server address must start with http://" >&2; exit 2 ;; esac
url="${url%/}"
if printf '%s' "$url$token" | grep -q "[^A-Za-z0-9:/._-]"; then
  echo "url and token may contain only letters, digits and : / . _ -" >&2
  exit 2
fi

device="$tv:5555"
adb connect "$device" >/dev/null 2>&1 || true
if ! adb -s "$device" get-state >/dev/null 2>&1; then
  echo "cannot reach $device: is ADB debugging on, and did you accept the prompt on the TV?" >&2
  exit 1
fi

if [ -z "$apk" ] && [ -f agent-sessions.apk ]; then
  apk=agent-sessions.apk
fi
if [ -z "$apk" ] && ! adb -s "$device" shell pm path "$pkg" 2>/dev/null | grep -q package:; then
  echo "downloading the latest release..."
  apk=$(mktemp --suffix=.apk)
  curl -fsSL -o "$apk" "https://github.com/$repo/releases/latest/download/agent-sessions.apk"
fi
if [ -n "$apk" ]; then
  echo "installing $apk..."
  if ! adb -s "$device" install -r "$apk" >/dev/null 2>&1; then
    # An APK signed with another key (your own build after a release, or the
    # reverse) cannot replace the installed one.
    echo "replacing an app signed with another key..."
    adb -s "$device" uninstall "$pkg" >/dev/null
    adb -s "$device" install "$apk" >/dev/null
  fi
fi

adb -s "$device" shell am start -n "$pkg/.MainActivity" --es url "$url" --es token "$token" >/dev/null
echo "set up: the app on $tv now polls $url"
