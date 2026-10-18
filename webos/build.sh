#!/bin/sh
# Build the Claude Sessions webOS app, and optionally install it on a rooted
# LG TV (Homebrew Channel with SSH enabled).
#
#   ./build.sh http://192.168.1.20:8787 "$MANAGENTS_TOKEN"
#
# Writes app/config.js and dist/<app id>_<version>_all.ipk. If TV_IP is set,
# it also copies the package to the TV over SSH as root, installs it, gives
# its service root rights through the Homebrew Channel (so it may keep the
# screensaver away) and opens the app. Set TV_PASS to log in with a password
# (needs sshpass); otherwise SSH uses your keys.
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

app_id=$(sed -n 's/.*"id": *"\([^"]*\)".*/\1/p' app/appinfo.json)
version=$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' app/appinfo.json)
service_id="$app_id.service"
ipk="dist/${app_id}_${version}_all.ipk"

printf 'window.LGAGENTS_CONFIG = {"url": "%s", "token": "%s"};\n' "$url" "$token" > app/config.js

# Package with LG's ares-package (from @webos-tools/cli, fetched by npx on
# first use). It bundles the service with the app and writes packageinfo.json.
mkdir -p dist
rm -f "$ipk"
npx --yes -p @webos-tools/cli@3 ares-package --no-minify app service -o dist >/dev/null
echo "built $ipk"

[ -n "${TV_IP:-}" ] || exit 0

ssh_opts="-o ConnectTimeout=10 -o LogLevel=ERROR"
if [ -n "${TV_PASS:-}" ]; then
  export SSHPASS="$TV_PASS"
  tv() { sshpass -e ssh $ssh_opts "root@$TV_IP" "$@"; }
  tv_copy() { sshpass -e ssh $ssh_opts "root@$TV_IP" "cat > $2" < "$1"; }
else
  tv() { ssh $ssh_opts "root@$TV_IP" "$@"; }
  tv_copy() { ssh $ssh_opts "root@$TV_IP" "cat > $2" < "$1"; }
fi

# luna-send on the TV keeps subscriptions open forever and loses its output
# when killed, so each call runs in the background on the TV with output in a
# file, which is printed once it holds a line matching $3 (or after $4 s).
luna() {
  tv "luna-send -i $1 '$2' > /tmp/lgagents-luna.log 2>&1 & pid=\$!
    i=0; while [ \$i -lt $4 ] && ! grep -Eq '$3' /tmp/lgagents-luna.log; do sleep 1; i=\$((i + 1)); done
    kill \$pid 2>/dev/null; cat /tmp/lgagents-luna.log; rm -f /tmp/lgagents-luna.log"
}

echo "installing on $TV_IP..."
remote=/tmp/lgagents.ipk
tv_copy "$ipk" "$remote"
result=$(luna luna://com.webos.appInstallService/dev/install \
  "{\"id\":\"$app_id\",\"ipkUrl\":\"$remote\",\"subscribe\":true}" '"installed"|install failed' 300 |
  grep -Eo '"state": *"(installed|install failed)"|"reason": *"[^"]*"' | tr '\n' ' ' || true)
tv "rm -f $remote"
case "$result" in
  *'"installed"'*) ;;
  *) echo "install failed: ${result:-no answer from appInstallService}" >&2; exit 1 ;;
esac
case "$(luna luna://org.webosbrew.hbchannel.service/elevateService "{\"id\":\"$service_id\"}" returnValue 15)" in
  *'"returnValue":true'*|*'"returnValue": true'*) ;;
  *) echo "could not elevate the service; the TV's screensaver may cover the app" >&2 ;;
esac
luna luna://com.webos.applicationManager/launch "{\"id\":\"$app_id\"}" returnValue 10 >/dev/null
echo "installed. It should be open on your TV now."
