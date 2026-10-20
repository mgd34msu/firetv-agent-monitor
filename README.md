<h1 align="center">Fire TV Agent Monitor</h1>

<p align="center">
  <img src="docs/images/lg-tv-demo.png" alt="The TV app: six session cards for Claude, Codex, Muse and GoodVibes on the left, plan usage bars and context per session on the right" width="820">
</p>

<p align="center">
  <b>Every AI coding agent you have open, on your Fire TV: which one is working, which one needs you, and how much of your plan is left.</b>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT license"></a>
</p>

Each open session is a card: green while it works, yellow when it waits for you, red when it failed, dimmed once it
has been idle for hours. A card names the tool, the session's title and folder, what the agent is doing right now (or
what it asked you), how long it has been in that state, and a bar for context-window usage. The right third of the
screen shows how much of each subscription's 5-hour and weekly limits is used, and the context of every session.
Select a card to read that session's conversation, read only.

This is the Fire TV version of [lg-agent-monitor](https://github.com/mgd34msu/lg-agent-monitor), which forks
[rokuagents](https://github.com/jonsykes/rokuagents) by Jon Sykes, itself a fork of
[managents](https://github.com/tonylook/managents) by Antonio Alvino. See [Credits](#credits).

## Supported agents

| Agent | Status | What it is doing | Context | Plan limits |
|---|---|---|---|---|
| [Claude Code](https://claude.com/claude-code) | yes | yes | yes | 5-hour and weekly |
| Codex | yes | yes | yes | 5-hour and weekly |
| Muse Code | yes | yes | yes | 5-hour and weekly |
| GoodVibes | yes | yes | yes | none (no subscription) |
| OpenCode | yes | no | no | none |

Where each number comes from, all read on your computer:

| Agent | Sessions and status | Plan limits |
|---|---|---|
| Claude Code | `~/.claude/sessions` and transcripts | `api.anthropic.com/api/oauth/usage` with Claude Code's own login, every minute |
| Codex | running `codex` processes, `~/.codex` state database and rollouts | `rate_limits` in the newest rollout, no network |
| Muse Code | running `muse-bin-*` processes, `~/.local/share/muse` session index and logs | `api.meta.ai/muse-code/key` with the Muse CLI's login, every 5 minutes |
| GoodVibes | the GoodVibes daemon (`127.0.0.1:3421`), each session's transcript journal and recovery file | none |

## How it works

```
Claude Code, Codex,   managents serve ──── HTTP, your LAN ────> Fire TV app
Muse, GoodVibes  ───> reads session files    GET /state, once      draws the cards,
(your computer)       and plan usage         a second; /session    the panel and the
                                             for the session view  session view
```

- **`managents serve`** runs on your computer. It detects the sessions, works out each one's status and activity,
  polls plan usage, and answers `GET /state` with JSON, plus `GET /session` for the session view with
  `--transcripts`. Requests need a bearer token. See [docs/http-api.md](docs/http-api.md).
- **The Fire TV app** in [`firetv/`](firetv/README.md) is a small Android app: a full-screen WebView showing the same
  TV screen as the LG version (the shared code in `webos/app`), with a native part that keeps the screen on, finds
  your computer again with a LAN broadcast when its address changes, and passes the remote's buttons to the page.

## What you need

- **A computer** with the agents you use. Developed on Linux; the upstream helper also supports macOS.
- **Go 1.26 or newer** to build the helper, or the Linux binary from the [latest release](../../releases/latest).
- **A Fire TV** with **ADB debugging** on (Settings > My Fire TV > Developer Options). Built on a Fire TV Cube (3rd
  generation); any Fire TV or Android TV device should work.
- **`adb`** on your computer (`android-tools` on Arch, `adb` on Debian/Ubuntu, `android-platform-tools` on Homebrew).
  Building the app yourself also needs JDK 17 and the Android SDK; the release APK does not.

## Get started

1. **Build the helper:**

   ```sh
   cd helper && go build -o ~/bin/managents ./cmd/managents
   ```

2. **Create a token and start the server:**

   ```sh
   mkdir -p ~/.config/managents
   (umask 077; openssl rand -hex 16 > ~/.config/managents/token)
   MANAGENTS_TOKEN=$(cat ~/.config/managents/token) ~/bin/managents serve --addr :8787 --transcripts
   ```

   Leave out `--transcripts` if you do not want the TV to be able to show conversations. If a firewall is on, open
   TCP and UDP 8787 to your LAN only, for example `sudo ufw allow from 192.168.0.0/24 to any port 8787`.

3. **Install the app and point it at your computer**, from this repository:

   ```sh
   firetv/setup.sh YOUR_FIRE_TV_IP
   ```

   It installs the latest release's APK over adb, then gives the app your computer's address and the token from
   `~/.config/managents/token`. The APK itself holds neither, so the same file works for everyone; the app keeps
   the settings it is given. Rerun `setup.sh` when the token or your computer changes. The first time, accept the
   "Allow USB debugging?" prompt on the TV. The app is **Agent Sessions** in Your Apps. To build the APK yourself,
   see [firetv/README.md](firetv/README.md).

## Using it

| Remote | On the cards | In the session view |
|---|---|---|
| Arrows | Move the highlight; past the last card, turn the page | Up/Down scroll |
| Select | Open the highlighted session | |
| Rewind / Fast-forward | Turn the page | Scroll a page at a time |
| Back | Leave the app | Back to the cards |

- Six cards per page; it returns to the first page after 30 seconds.
- The session view shows your prompts, the agent's replies, its tool calls and the start of their output, refreshed
  every two seconds. It never sends anything to the session.

## Privacy

The server sends each session's folder name, title, status, age, context usage and a short activity line, plus your
plans' usage percentages. Titles and activity lines come from your conversations, so anyone who can see the TV can read
them: start the server with `--titles=false` or `--activity=false` to leave them out. With `--transcripts`, the
helper also serves each open session's recent conversation to the TV's session view, behind the same token.

To read plan limits the helper uses the logins Claude Code and the Muse CLI already keep on your computer, and sends
them only to Anthropic and Meta respectively. Muse's usage call also mints an inference key, which the helper discards
without storing. Traffic to the TV is plain HTTP on your network, protected by the token.

## Also in this repository

The LG webOS app in [`webos/`](webos/README.md) (the shared TV screen lives in `webos/app`), the Roku channel in
[`roku/`](roku/README.md), the macOS menu bar app in [`macos/`](macos/README.md), and the managents USB desk display.

## Credits

- **[lg-agent-monitor](https://github.com/mgd34msu/lg-agent-monitor)**: the LG webOS version this repository is built
  from, with the same TV screen, multi-agent detection, plan usage and session view. Its git history is this one's.
- **[managents](https://github.com/tonylook/managents)** by **Antonio Alvino**: the original USB desk display for AI
  coding agents, its Go helper, session detection for Claude Code, Codex and OpenCode, and the serial protocol.
- **[rokuagents](https://github.com/jonsykes/rokuagents)** by **Jon Sykes**: the fork lg-agent-monitor forks. It added
  `managents serve` (the HTTP API and LAN discovery), the Roku channel and the macOS app. The card design on the TV
  follows its Roku channel.
- **[CodexBar](https://github.com/steipete/CodexBar)** by **Peter Steinberger**: how to read Muse Code's subscription
  usage ([docs/muse.md](https://github.com/steipete/CodexBar/blob/main/docs/muse.md)).

Built with [gopsutil](https://github.com/shirou/gopsutil), [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) and the
other components listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Released under the [MIT license](LICENSE).

Claude and Claude Code are trademarks of Anthropic, Codex of OpenAI, Muse of Meta, Fire TV of Amazon, LG and webOS of LG
Electronics, and Roku of Roku, Inc. This project is not affiliated with any of them.
