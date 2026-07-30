# Projector Web Remote

A phone-friendly web remote for the living-room **HiSilicon Hi3751V350 Android 12
projector** (Zeasn Whale OS), served **from the projector itself** over its own
`adb`/root. Open a URL in any phone browser and control the box — no app store, no
Google account, no companion PC.

- **Remote URL:** `http://192.168.220.53:8790` (iPhone: Safari → Share → *Add to Home Screen*)
- **Two modes** (tabs at top, remembered per phone): **Keys** (D-pad/media) and **Touchpad** (air-mouse: drag = move cursor, tap = click, 2-finger = scroll). A keyboard bar is shared by both.
- **iOS home-screen note:** the page is served over plain HTTP. It is deliberately **not**
  `apple-mobile-web-app-capable`, because that flag makes the home-screen icon open a standalone
  WebView that blocks HTTP ("HTTPS-only"/ATS). Without it the icon opens in **Safari**, where HTTP
  works — the trade-off is you see Safari's chrome instead of a fullscreen app. True fullscreen
  would require serving HTTPS (this box has no TLS tooling, so that'd mean shipping a TLS proxy +
  installing a trusted cert profile on the phone — not done).
- **Runs on:** the projector (busybox `httpd` + a CGI that injects key events as root)
- **Boot-persistent:** yes — an Android `init` service starts and supervises it

---

## Why this exists (root cause)

The projector reports as "Android TV" but it is **Zeasn Whale OS on HiSilicon, not a
Google-certified Android TV**. So:

- It never advertises `_androidtvremote2._tcp` and has no Google Cast, which is exactly
  and only what the iOS **Google TV / Android TV Remote** app looks for → that app can
  **never** discover it. This is not a WiFi/router/mDNS problem.
- The Google remote service **can't be sideloaded to work either**: it needs
  `INJECT_EVENTS`, which is `protectionLevel=signature` (platform-signed apps only). A
  `priv-app` allowlist does **not** grant it. A sideloaded Google-signed service would
  pair but every button would be dead.

Workaround: skip Google entirely. We already have **root over network adb**, and a
process launched as root can call `input keyevent` directly. So we run a tiny web server
on the projector that turns button taps into local key events.

---

## Architecture

```
 iPhone Safari  ──HTTP──▶  busybox httpd (:8790, on projector, root)
   button tap              │  serves device/index.html
                           └▶ /cgi-bin/k?<name>  ──▶  input keyevent <code>
                                                        (injected locally as root)
 Android init ──supervises──▶ the httpd process (auto-restart, starts on boot)
```

## Files

| Path | What it is |
|---|---|
| `device/index.html` | the remote UI (D-pad, volume, media keys). Pure HTML/JS, no deps. |
| `device/cgi-bin/k` | CGI: maps `?name` → Android keycode → `input keyevent`. **Edit here to add buttons.** |
| `device/cgi-bin/t` | CGI: types text into the focused field (URL-decode → lowercase → char-by-char). |
| `device/cgi-bin/m` | CGI: pointer injection. Direct: `tap=X,Y`, `swipe=…`. Air-mouse: `rel=DX,DY`, `click=1`, `wheel=N` (via `sendevent` to the "Hi mouse" node). Digits/comma/minus only (validated). |
| `device/boot.sh` | launcher run by init; waits for `/data`, then `exec busybox httpd -f`. Holds the **port**. |
| `device/tvremote.rc` | Android init service; starts `boot.sh` on `sys.boot_completed=1`, seclabel `u:r:su:s0`. |
| `deploy.sh` | push everything, set perms/SELinux contexts, (re)start. **Idempotent — this is how you update.** |
| `restart.sh` | restart the running server, no redeploy. |
| `uninstall.sh` | remove service + files. |
| `logs.sh` | service state + httpd log + init logcat (troubleshooting). |

On-device install layout: web app in `/data/local/tmp/tvremote/`, service in
`/vendor/etc/init/tvremote.rc`.

---

## Requirements (on the Mac)

- `adb` (`brew install android-platform-tools`) — that's it.
- Projector reachable on the LAN with network adb up. Recovery is guaranteed by two
  device props already set: `persist.adb.tcp.port=5555` and `ro.debuggable=1`, so adb
  returns on every boot even if the remote fails.

## Quick start / update workflow

```bash
./deploy.sh        # first install AND every future update — edit files, then re-run
./restart.sh       # just bounce the server
./logs.sh          # what's it doing / why did it fail
./uninstall.sh     # remove completely
```

Edit → `./deploy.sh` → refresh the phone. The script re-pushes, fixes perms/contexts,
and restarts via `ctl.restart` (no reboot).

## Modes

Two tabs at the top switch the controls (choice is saved per phone via `localStorage`):

- **Keys** — power/home/back, D-pad + OK, menu/mute, volume, media transport. Uses `cgi-bin/k`.
- **Touchpad** — a relative **air-mouse** that moves the box's real hardware cursor (like a
  laptop trackpad). **Drag = move cursor**, **tap = left-click**, **2-finger drag = wheel
  scroll**; plus **Click** and Home/Back/OK buttons below. Works because this box has a real
  mouse HID ("Hi mouse"); we inject `REL_X/REL_Y`/`BTN_MOUSE`/`REL_WHEEL` via `sendevent` (NOT
  `input roll` — that source does nothing here). Cursor is OS-rendered, so it works in any app.

Air-mouse sensitivity is the `ROLL` constant in `index.html` (default 2.2). Moves are batched
~40 ms client-side to keep the HTTP chatter down. The mouse device node is auto-resolved by
name from `/proc/bus/input/devices` and cached in `.mouseev`, so it survives reboots/renumbering.
(The `m` CGI still accepts `tap`/`swipe` for absolute touch, unused by the current UI.)

## Keyboard & voice (dictation) input

The remote page has a **text box + Send**. Type into it on the phone and tap Send;
the text is injected into whatever field currently has focus on the projector (a
browser URL bar, a search box, etc. — focus it on the TV first). **Enter** and **Del**
buttons submit / backspace.

- **Voice:** there is no separate mic button and no Web Speech API (that needs HTTPS,
  which this plain-HTTP server can't provide). Instead, tap the text box → the **iOS/
  Android on-screen keyboard has a 🎤 dictation key** → speak → tap Send. Dictation
  happens on the phone at the OS level; the projector only receives text.
- **Case:** text is sent **lowercase on purpose.** This device has a firmware bug where
  `input text` latches SHIFT/caps ON after the first capital, corrupting the rest of the
  string. Never pressing shift avoids it entirely. TV search and URL hosts are
  case-insensitive, so this is invisible in normal use. Consequence: you can't type a
  case-sensitive value (e.g. a password) with this — a known, documented limit.
- Handled by `device/cgi-bin/t` (URL-decode → lowercase → inject char-by-char, space via
  keycode 62 so it isn't dropped).

## Add or change buttons

1. **`device/cgi-bin/k`** — add a `case` entry: `mykey) c=<keycode> ;;`
   (keycodes: <https://developer.android.com/reference/android/view/KeyEvent>).
2. **`device/index.html`** — add `<button onclick="k('mykey')">Label</button>`.
3. `./deploy.sh`.

Change the **port**: edit it in `device/boot.sh` (the source of truth) and in the
`PORT=` line of `deploy.sh`/`restart.sh`, then `./deploy.sh`.

## Troubleshooting

- **Phone can't load the page** → same WiFi? `./logs.sh` (is it listening on 8790?).
  `./restart.sh`. Confirm projector IP is still `192.168.220.53` (it's a DHCP lease).
- **Page loads, buttons do nothing** → `./logs.sh`; a manual test:
  `adb -s 192.168.220.53:5555 shell input keyevent 24` should change volume.
- **Didn't survive a reboot** → `adb ... shell getprop init.svc.tvremote` should be
  `running`; check `logs.sh` init section for parse errors in `tvremote.rc`.

## Security note

The whole thing relies on the projector's **adb being open as root to the entire WiFi**
(port 5555). That is convenient but also means *any* device on the WiFi can root the
projector, and the remote itself is unauthenticated. Fine for a trusted home LAN; if you
want it locked down (firewall adb to one IP, or add a token to the remote), that's a
follow-up.

## Device facts (for future edits)

- HiSilicon Hi3751V350, Android 12 (SDK 31), **armeabi-v7a** (32-bit), 1 GB RAM.
- SELinux **Permissive**; `/`, `/vendor` remountable rw; root via `adb root`.
- `input` binary present; busybox at `/vendor/bin/busybox` (v1.26.2, has `httpd`).
- adb quirk: `adb shell input text` types UPPERCASE — toggle with `input keyevent 115` first.
