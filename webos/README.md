# Agent Sessions for LG webOS

Your Claude Code, Codex, Muse Code, GoodVibes and OpenCode sessions on an LG TV: session
cards on the left two thirds, plan usage (Claude, Codex, Muse) and per-session context on the
right. Built for a rooted 86NANO90UNA (webOS 5.6, Homebrew Channel with SSH).

## Run the helper on your computer

```sh
cd helper && go build -o ~/bin/managents ./cmd/managents
MANAGENTS_TOKEN=$(cat ~/.config/managents/token) ~/bin/managents serve --addr :8787
```

Open TCP and UDP 8787 to your LAN if a firewall is on (`ufw allow from 192.168.0.0/24 to any port 8787`).

## Build and install

```sh
cd webos
TV_IP=192.168.0.170 TV_PASS=alpine ./build.sh http://YOUR_PC_IP:8787 "$(cat ~/.config/managents/token)"
```

Bump `version` in `app/appinfo.json` before reinstalling. If the scripted install reports
"ipk verified failed", install the built package by hand:

```sh
sshpass -p alpine ssh root@$TV_IP 'cat > /tmp/a.ipk' < dist/*.ipk
sshpass -p alpine ssh root@$TV_IP '(luna-send -i luna://com.webos.appInstallService/dev/install \
  "{\"id\":\"org.lgagents.claudesessions\",\"ipkUrl\":\"/tmp/a.ipk\",\"subscribe\":true}" > /tmp/i.log 2>&1 & P=$!; \
  sleep 20; kill $P); grep -o "\"state\":\"[a-z ]*\"" /tmp/i.log | tail -1'
```

## What the panel shows

| Provider | Source |
|---|---|
| Claude | `api.anthropic.com/api/oauth/usage` with Claude Code's local login (`~/.claude/.credentials.json`), every 60 s |
| Codex | `rate_limits` in the newest `~/.codex/sessions` log, no network |
| Muse | `POST api.meta.ai/muse-code/key` with the Muse CLI's `dca:` token from `~/.config/muse/auth.json`, every 5 min (method from [CodexBar](https://github.com/steipete/CodexBar/blob/main/docs/muse.md)); the call mints an inference key, which is discarded |

GoodVibes terminal sessions show as "Open" unless they wait on an approval: the daemon
does not see their turns.
