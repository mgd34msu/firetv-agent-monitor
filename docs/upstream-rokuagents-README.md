<h1 align="center">rokuagents</h1>

<p align="center">
  <img src="docs/images/rokuagents-tv.jpg" alt="A TV showing nine Claude Code session cards: three green working, two yellow needing you, one red error and three idle, each with its title and folder" width="720">
</p>

<p align="center">
  <b>Your Claude Code sessions on the TV, so you can see at a glance which one needs you.</b>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT license"></a>
</p>

Every open [Claude Code](https://claude.com/claude-code) session is a card on your Roku: green while it works, yellow
when it waits for you, red when it failed, dimmed once it has been idle for hours. Each card shows the session's title
as the Claude app shows it in its sidebar, the folder it runs in, how long it has been in that state, and a bar for
context-window usage. It also works as a Roku screensaver.

rokuagents is a fork of [managents](https://github.com/tonylook/managents) by Antonio Alvino, a USB desk display for
the same job. It adds a small HTTP server to the managents helper and a Roku channel that reads from it. The USB
display still works as upstream does.

## How it works

```
Claude Code ──> managents serve ──── HTTP, your LAN ────> Roku channel
(your Mac)      reads session files   GET /state, once      draws the cards
                every second          a second, with a token
```

- **`managents serve`** runs on your computer. It reads Claude Code's session registry and the end of each open
  transcript, works out each session's status, and answers `GET /state` with JSON. Requests need a bearer token, and
  the server refuses to listen beyond localhost without one. The format is in [docs/http-api.md](docs/http-api.md).
- **The Roku channel** in [`roku/`](roku/README.md) polls that address once a second. Your computer's address and the
  token are built into the channel when you install it. If the address changes, the channel finds the server again
  with a broadcast on your LAN that only the server holding the same token answers.

Only Claude Code sessions are shown on the TV. OpenCode and Codex appear only on the USB display.

## What you need

- **A Mac** with Claude Code (tested on macOS 26). Linux should work for the server but is untested.
- **Go 1.26 or newer** to build the helper: `brew install go`.
- **A Roku** on the same network, in developer mode (step 4 below). Tested on a TCL Roku TV with Roku OS 15.3.
- The computer and the Roku on the same network. The channel follows your computer when its LAN address changes.

## Get started

**The easy way: the menu bar app.** It runs the server, installs the channel on the Roku from its Settings window,
and reinstalls it by itself when your Mac's address changes. Its menu lists your sessions:

<p align="center">
  <img src="docs/images/rokuagents-menu.png" alt="The rokuagents menu listing nine sessions with coloured status dots, and its commands" width="380">
</p>

Build and open it with:

```sh
git clone https://github.com/jonsykes/rokuagents.git
cd rokuagents/macos
./build.sh --install
```

Then put the Roku in developer mode (step 4 below) and follow [the app's first run](macos/README.md#first-run).

**Or set it up from the command line:**

1. **Build the helper.**

   ```sh
   git clone https://github.com/jonsykes/rokuagents.git
   cd rokuagents/helper
   go build -o ~/bin/managents ./cmd/managents
   ```

2. **Create a token and start the server** on your LAN address (`ipconfig getifaddr en0` prints it):

   ```sh
   mkdir -p ~/.config/managents
   (umask 077; openssl rand -hex 16 > ~/.config/managents/token)
   MANAGENTS_TOKEN=$(cat ~/.config/managents/token) ~/bin/managents serve --addr 192.168.1.20:8787
   ```

   Check it from another terminal. You should see your sessions as JSON:

   ```sh
   curl -H "Authorization: Bearer $(cat ~/.config/managents/token)" http://192.168.1.20:8787/state
   ```

   If macOS asks whether `managents` may accept incoming connections, allow it.

3. **Start it at login.** Stop the server from step 2, save this as
   `~/Library/LaunchAgents/com.managents.serve.plist` with your address, and load it with
   `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.managents.serve.plist`. It reads the token from the
   file, so the token never sits in the plist.

   ```xml
   <?xml version="1.0" encoding="UTF-8"?>
   <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
   <plist version="1.0">
   <dict>
     <key>Label</key><string>com.managents.serve</string>
     <key>ProgramArguments</key>
     <array>
       <string>/bin/sh</string>
       <string>-c</string>
       <string>MANAGENTS_TOKEN="$(cat "$HOME/.config/managents/token")" exec "$HOME/bin/managents" serve --addr 192.168.1.20:8787</string>
     </array>
     <key>RunAtLoad</key><true/>
     <key>KeepAlive</key><true/>
     <key>ThrottleInterval</key><integer>10</integer>
     <key>StandardOutPath</key><string>/Users/YOU/Library/Logs/managents-serve.log</string>
     <key>StandardErrorPath</key><string>/Users/YOU/Library/Logs/managents-serve.log</string>
   </dict>
   </plist>
   ```

4. **Put the Roku in developer mode** (once). On the remote press **Home ×3, Up ×2, Right, Left, Right, Left,
   Right**, enable the installer, accept the agreement and set a password. The Roku restarts and shows its IP address.

5. **Build and install the channel.**

   ```sh
   cd ../roku
   ROKU_IP=192.168.1.50 ROKU_PASS=your-dev-password ./build.sh http://192.168.1.20:8787 "$(cat ~/.config/managents/token)"
   ```

   The channel opens on the TV. Run this again when the token changes. When your computer's address changes, the
   channel finds the server again by itself, but a server started with `--addr` on the old address stops: update the
   plist, or use `--addr :8787` to listen on every interface (the token still guards it).

## Using it

- Nine cards per page: working sessions first, then the most recently changed. Left/right or up/down turn pages, and it returns to the first page after
  30 seconds.
- The header shows how many sessions are working and how many need you, your computer's clock and the page.
- A session without a title yet shows its folder name as the headline. Several sessions in one folder are numbered
  (`api #1`, `api #2`).
- Fresh errors blink for their first minute.
- **Screensaver:** Settings > Theme > Screensavers > Claude Sessions. Pages turn every 10 seconds and the layout
  drifts a little each minute to avoid burn-in.
- The channel stays installed until you sideload another one or turn developer mode off.

To restart the server or read its log:

```sh
launchctl kickstart -k gui/$(id -u)/com.managents.serve
tail -f ~/Library/Logs/managents-serve.log
```

## Troubleshooting

| What you see | Cause and fix |
|---|---|
| "Reconnecting..." | Your computer is asleep or the server is stopped. Check the log. The channel finds a new address by itself while the server runs; with the plist above, an address change stops the server, so update `--addr` (or use `--addr :8787`). |
| "The helper rejected the token" | The token in the channel differs from the server's. Rerun `build.sh` with the current token. |
| No cards | `curl` the server as in step 2. An empty list means Claude Code has no open sessions it can see in `~/.claude/sessions`. |
| `build.sh` fails to install | Check `ROKU_IP`, the developer password, and that the computer and the Roku are on the same network. |

## Privacy

The server sends each Claude Code session's folder name, title, status, age and context usage. Nothing else from
your conversations leaves the computer: transcripts are read only for status, token counts and the title entries.
Titles are written from your conversations, so anyone who can see the TV can read them. Start the server with
`--titles=false` to show folder names only.

The traffic is plain HTTP on your network, protected by the token. Keep the server on your LAN address, not on a
public interface. Details: [docs/privacy.md](docs/privacy.md#network-displays-rokuagents).

## The USB desk display

Everything from managents is still here: the 4-inch ESP32 display, its firmware, the 3D-printed stand and
`managents run`, which also shows OpenCode and Codex sessions. Start with the [build guide](docs/build.md) and
[Install and flash](docs/install.md). The one-line installer in those docs installs upstream's release, which does not
include `serve`; build from this repository to get both.

## More

| | |
|---|---|
| [Menu bar app](macos/README.md) | Settings, first run, how the app is built |
| [Roku channel](roku/README.md) | The channel's files and how it polls |
| [HTTP API](docs/http-api.md) | What `serve` answers, for building another display |
| [Privacy](docs/privacy.md) | What the helper reads and what it sends |
| [How it works](docs/architecture.md) | The helper's architecture and the [protocol](docs/protocol.md) the frames come from |
| [Contributing](CONTRIBUTING.md) | Tests and lint; the [changelog](CHANGELOG.md) |

## Credits and license

Built on [managents](https://github.com/tonylook/managents) by Antonio Alvino, with
[gopsutil](https://github.com/shirou/gopsutil), [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) and the other
components listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Released under the [MIT license](LICENSE).

Claude and Claude Code are trademarks of Anthropic, and Roku is a trademark of Roku, Inc. This project is not
affiliated with either.
