# Projector Web Remote

A phone-friendly web remote for the living-room **HiSilicon Hi3751V350 Android 12
projector** (Zeasn Whale OS), served **from the projector itself** over its own
`adb`/root. Open a URL in any phone browser and control the box — no app store, no
Google account, no companion PC.

- **Remote URL:** **`https://192.168.220.53:8443`** (fullscreen home-screen app; needs the CA
  trusted once — see below). This is the working URL.
  A hostname `https://projectorremote.local:8443` is also advertised via mDNS and is in the cert's
  SAN, but only resolves on networks that pass mDNS between clients (this WiFi blocks it, so use the IP).
  Plain `http://192.168.220.53:8790` serves the same page (proxy backend + cert download).
- **Two modes** (tabs at top, remembered per phone): **Keys** (D-pad/media) and **Touchpad** (air-mouse: drag = move cursor, tap = click, 2-finger = scroll). A keyboard bar is shared by both.

## HTTPS + fullscreen home-screen app

The page **is** `apple-mobile-web-app-capable`, so "Add to Home Screen" launches a fullscreen
standalone app. That standalone WebView refuses plain HTTP (ATS / "HTTPS-only"), so we serve
HTTPS: a tiny Go **TLS reverse proxy** (`tls-proxy/main.go`, static armv7 binary at
`device/bin/tlsproxy`) listens on **:8443** and forwards to the busybox httpd on `127.0.0.1:8790`.

The proxy also **301-redirects plaintext HTTP that lands on :8443 to https://** (it peeks the
first byte — `0x16` = TLS handshake — and redirects anything else), so hitting `http://…:8443`
by mistake self-corrects instead of showing "Client sent an HTTP request to an HTTPS server".

TLS material (`gen-cert.sh`) is a **local CA + short-lived leaf**, not a bare self-signed cert —
iOS rejects a self-signed cert that is `CA:TRUE` used as a server leaf. So:
- **CA** (`ca.pem`, CN "Projector Remote Local CA", `CA:TRUE`, `keyCertSign`) — installed + trusted on the phone.
- **leaf** (`CA:FALSE`, `serverAuth`, SAN `IP:192.168.220.53`, 397 days) signed by the CA.
- Proxy serves the **fullchain** (`cert.pem` = leaf then CA). Verified against Apple's own
  evaluator: `security verify-cert -c leaf.pem -r ca.pem -p ssl -s 192.168.220.53` → success, so
  Apple's SSL policy accepts the IP SAN (no hostname needed).

**iPhone one-time trust (required — "Not Secure" = this wasn't completed):**
1. **Delete any old "Projector Remote" profile** (Settings → General → VPN & Device Management).
2. Safari → `http://192.168.220.53:8790/ca.crt` → tap through → install the **"Projector Remote Local CA"** profile.
3. Settings → General → About → **Certificate Trust Settings** → toggle it **ON** (mandatory; the
   app silently fails as "Not Secure" without it).
4. **Force-quit Safari** (WebKit caches cert failures), then open `https://192.168.220.53:8443` →
   Share → **Add to Home Screen**. Fullscreen + Secure.

Leaf renewal (`./gen-cert.sh && ./deploy.sh`) needs **no** re-trust as long as the CA is unchanged.

**The one that actually mattered:** the "Not Secure" symptom was the **Certificate Trust Settings
toggle not being on** (Settings → General → About). With the CA trusted, the IP-SAN cert works fine
in iOS Safari — the earlier "iOS refuses IP certs" guess was wrong. The mDNS hostname
(`projectorremote.local`, via `grandcat/zeroconf`) is kept as a fallback for networks that pass
mDNS, but this WiFi blocks client-to-client mDNS, so the IP is the URL to use.
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
| `device/boot.sh` | launcher run by init; waits for `/data`+cert, starts busybox httpd (:8790), then `exec`s the TLS proxy (:8443). |
| `device/tvremote.rc` | Android init service; starts `boot.sh` on `sys.boot_completed=1`, seclabel `u:r:su:s0`. |
| `tls-proxy/main.go` | Go HTTPS reverse proxy (:8443 → 127.0.0.1:8790). Cross-compiles to `device/bin/tlsproxy` (static armv7). |
| `device/cert.pem` | fullchain (leaf + CA) served by the proxy. Committed (public). |
| `device/ca.crt` | the CA cert the iPhone downloads + trusts. Committed (public). |
| `device/ca.key`,`key.pem`,`leaf.*` | private keys / leaf — **gitignored**; regenerate with `gen-cert.sh`. |
| `gen-cert.sh` | (re)generate CA (once) + leaf (IP SAN, 397d). |
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
