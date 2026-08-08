# Projector Web Remote

A phone-friendly web remote for the living-room **HiSilicon Hi3751V350 Android 12
projector** (Zeasn Whale OS), served **from the projector itself** over its own
`adb`/root. Open a URL in any phone browser and control the box — no app store, no
Google account, no companion PC.

- **Remote URL:** **`https://<projector-ip>:8443/?t=<token>`** (fullscreen home-screen app; needs the
  CA trusted once and the token once — see below). `./deploy.sh` prints the exact URL.
  A hostname `https://projectorremote.local:8443` is also advertised via mDNS and is in the cert's
  SAN, but only resolves on networks that pass mDNS between clients (mine blocks it, so I use the IP).
  The backend on `8790` is **loopback-only** and not reachable from the LAN.
- **Two modes** (tabs at top, remembered per phone): **Keys** (D-pad/media) and **Touchpad** (air-mouse: drag = move cursor, tap = click, 2-finger = scroll). A 4-slot app row (tap = launch, hold = change app) and a keyboard bar are shared by both.

> Throughout this README `192.168.220.53` is **my** projector's DHCP address, used as a
> concrete example. See [Point it at your device](#point-it-at-your-device) for the three
> places to change it.

## Contents

- [Requirements](#requirements-on-the-mac) · [Quick start](#quick-start--update-workflow) · [Point it at your device](#point-it-at-your-device)
- [HTTPS + fullscreen home-screen app](#https--fullscreen-home-screen-app) — cert trust, the iPhone one-time steps
- [Why this exists](#why-this-exists-root-cause) — why the Google TV remote app can never work here
- [Architecture](#architecture) · [Files](#files)
- [Modes](#modes) · [Auth](#auth-token) · [Hold OK](#hold-ok--context-menu) · [Apps](#apps--4-editable-favourite-slots) · [Keyboard & voice](#keyboard--voice-dictation-input)
- [Add or change buttons](#add-or-change-buttons) · [Troubleshooting](#troubleshooting)
- [Verified on device](#verified-on-device) · [Security note](#security-note) · [Device facts](#device-facts-for-future-edits)

## Requirements (on the Mac)

- `adb` (`brew install android-platform-tools`) and **Go 1.21+** (to cross-compile the proxy).
- Projector reachable on the LAN with network adb up. Recovery is guaranteed by two
  device props already set: `persist.adb.tcp.port=5555` and `ro.debuggable=1`, so adb
  returns on every boot even if the remote fails.

## Quick start / update workflow

```bash
./gen-cert.sh                # once: local CA + leaf (no TLS material is committed)
./deploy.sh                  # first install AND every future update — edit files, then re-run
```

Then trust the CA on the phone once — see
[HTTPS + fullscreen home-screen app](#https--fullscreen-home-screen-app). After that:

```bash
./deploy.sh --rotate-token   # new secret (invalidates every phone's saved shortcut)
./build.sh                   # just rebuild the proxy (runs its tests first)
./restart.sh                 # just bounce the server
./logs.sh                    # what's it doing / why did it fail
./uninstall.sh               # remove completely
```

Edit → `./deploy.sh` → refresh the phone. The script **builds the proxy**, re-pushes, fixes
perms/contexts, and restarts via `ctl.restart` (no reboot). It then asserts the security
properties and refuses to report success unless all of them hold:

| check | want |
|---|---|
| `http://IP:8790/` (backend from the LAN) | **000** — closed |
| `https://IP:8443/` with no token | **401** |
| `https://IP:8443/` with the token | **200** |
| `http://IP:8443/ca.crt` (plaintext) | **200** — trust bootstrap intact |
| `…/cgi-bin/k?volup` with the token | **200** — CGI really execs (paired with a `voldown` so volume ends where it started) |
| `…/key.pem` **with** the token | **404** — TLS private key not downloadable |
| `…/token` **with** the token | **404** — the secret can't be read back out |

`device/bin/tlsproxy` is **gitignored**, so a fresh clone has no binary at all —
`deploy.sh` calling `build.sh` is what makes a clone deployable, and what stops an
edit to `main.go` from silently shipping the previous binary. The TLS material is
gitignored for the same reason in reverse: a committed CA would be a CA whose private
key is in a public repo, so `gen-cert.sh` is a required first step, not an optional one.

## Point it at your device

The projector's address is hardcoded in three places (it's an RFC1918 LAN address, not a
secret — this is a single-device tool, not a configurable product). To adopt it, change:

| Where | Line |
|---|---|
| `deploy.sh`, `restart.sh`, `logs.sh`, `uninstall.sh` | `TV=192.168.220.53:5555` |
| `gen-cert.sh` | `IP=192.168.220.53` (goes into the cert's SAN — must match, or iOS rejects it) |
| `tls-proxy/main.go` | `defHost` / `ip` (~line 50) |

Then `./gen-cert.sh && ./deploy.sh`. If your projector's IP moves (it's a DHCP lease), the
leaf cert's IP SAN no longer matches and the phone will report the certificate as invalid —
re-run both. A DHCP reservation on the router avoids this.

Ports (`8443` HTTPS, `8790` loopback backend) are in `deploy.sh`/`restart.sh`,
`device/boot.sh`, and `tls-proxy/main.go`.

## HTTPS + fullscreen home-screen app

The page **is** `apple-mobile-web-app-capable`, so "Add to Home Screen" launches a fullscreen
standalone app. That standalone WebView refuses plain HTTP (ATS / "HTTPS-only"), so we serve
HTTPS: a tiny Go **TLS reverse proxy** (`tls-proxy/main.go`, static armv7 binary at
`device/bin/tlsproxy`) listens on **:8443** and forwards to the busybox httpd on `127.0.0.1:8790`.

The proxy also **301-redirects plaintext HTTP that lands on :8443 to https://** (it peeks the
first byte — `0x16` = TLS handshake — and redirects anything else), so hitting `http://…:8443`
by mistake self-corrects instead of showing "Client sent an HTTP request to an HTTPS server".

**One deliberate exception:** `/ca.crt` **is** served over that plaintext path, ungated. It has to
be — the phone cannot validate our certificate until it has already installed this file, so gating
or TLS-only-ing it would make a fresh phone unable to bootstrap trust at all. It's a public
certificate, so serving it in the clear costs nothing. This exception is what lets the busybox
backend stay bound to loopback.

TLS material (`gen-cert.sh`) is a **local CA + short-lived leaf**, not a bare self-signed cert —
iOS rejects a self-signed cert that is `CA:TRUE` used as a server leaf. So:
- **CA** (`ca.pem`, CN "Projector Remote Local CA", `CA:TRUE`, `keyCertSign`) — installed + trusted on the phone.
- **leaf** (`CA:FALSE`, `serverAuth`, SAN `IP:192.168.220.53`, 397 days) signed by the CA.
- Proxy serves the **fullchain** (`cert.pem` = leaf then CA). Verified against Apple's own
  evaluator: `security verify-cert -c leaf.pem -r ca.pem -p ssl -s 192.168.220.53` → success, so
  Apple's SSL policy accepts the IP SAN (no hostname needed).

**iPhone one-time trust (required — "Not Secure" = this wasn't completed):**
1. **Delete any old "Projector Remote" profile** (Settings → General → VPN & Device Management).
2. Safari → `http://192.168.220.53:8443/ca.crt` → tap through → install the **"Projector Remote Local CA"** profile.
   (Note the port: **8443**, not 8790 — the backend is loopback-only now.)
3. Settings → General → About → **Certificate Trust Settings** → toggle it **ON** (mandatory; the
   app silently fails as "Not Secure" without it).
4. **Force-quit Safari** (WebKit caches cert failures), then open the **tokenized URL** printed by
   `./deploy.sh` → Share → **Add to Home Screen**. Fullscreen + Secure.
   **Add it with `?t=…` still in the URL** — then if iOS ever evicts the cookie, launching the
   shortcut silently re-authorizes instead of showing "not authorized".

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
 iPhone           tlsproxy :8443 (root)                busybox httpd 127.0.0.1:8790
 home-screen ──TLS──▶ ┌──────────────────────┐              (loopback only)
   app               │ token gate (cookie)  │  ──HTTP──▶  index.html
                     │  ├ /ca.crt ungated   │             /cgi-bin/k  → input keyevent
                     │  └ everything else   │             /cgi-bin/t  → input text
                     │     401 without it   │             /cgi-bin/a  → fixed shortcuts only
                     │                      │             /cgi-bin/m  → input tap/swipe
                     │
                     │ handled natively, never reaching busybox:
                     │  /cgi-bin/m?rel|click|wheel
                     │    └─▶ struct input_event → /dev/input/eventN
                     │        ("Hi mouse"), fd held open
                     │  /cgi-bin/k?long_ok
                     │    └─▶ held EV_KEY 28 on "Hi keyboard" for 800 ms
                     │        (real hold → app opens its context menu)
                     │  /cgi-bin/a?list  → cmd package query-activities
                     │  /cgi-bin/a?pkg=  → cmd activity start-activity
                     │        (argv exec, no shell; the package must be in
                     │         the device's own launcher list)
                     └──────────────────────┘
 Android init ──supervises──▶ boot.sh → httpd + tlsproxy (restart on exit, start on boot)
```

## Files

| Path | What it is |
|---|---|
| `device/index.html` | the remote UI (D-pad, volume, media keys). Pure HTML/JS, no deps. |
| `device/cgi-bin/k` | CGI: maps `?name` → Android keycode → `input keyevent`. **Edit here to add buttons.** `?long_ok` never reaches here — the proxy holds that key on the evdev node. |
| `device/cgi-bin/t` | CGI: types text into the focused field (URL-decode → lowercase → char-by-char). |
| `device/cgi-bin/a` | CGI: the **fixed** shortcut labels (`?cine`, `?vlc`, `?just`, `?next`, `?set`) via `resolve-activity` + `am start`. The query picks a `case` label; no package string from the network reaches the shell. `?list` and `?pkg=` never get here — the proxy handles those. |
| `device/cgi-bin/m` | CGI: **legacy/absolute only** — `tap=X,Y`, `swipe=…`. The air-mouse ops (`rel`/`click`/`wheel`) are handled natively in the proxy and never reach this script; it's kept so those two still work. |
| `device/boot.sh` | launcher run by init; waits for `/data`+cert, starts busybox httpd on **127.0.0.1**:8790, then `exec`s the TLS proxy (:8443). |
| `device/tvremote.rc` | Android init service; starts `boot.sh` on `sys.boot_completed=1`, seclabel `u:r:su:s0`. |
| `tls-proxy/main.go` | Go HTTPS front end (:8443): token gate, native pointer + held-key injection, app list/launch, reverse proxy to 127.0.0.1:8790, plaintext `/ca.crt`. |
| `tls-proxy/main_test.go` | host-runnable tests: `input_event` byte layout (pointer + held key), device-node parsing by name, app-list parsing, package validation, route allowlist, and the auth gate (401/403/302, cookie flags, fail-closed). |
| `build.sh` | cross-compiles the proxy to `device/bin/tlsproxy` (static armv7, cgo off) after running the tests. `deploy.sh` calls it. |
| `device/token` (on device only) | the shared secret, `0600`, generated once by `deploy.sh`. Gitignored; never written to the Mac. |
| `device/cert.pem` | fullchain (leaf + CA) served by the proxy. **Gitignored** — `gen-cert.sh` output. |
| `device/ca.crt` | the CA cert the iPhone downloads + trusts. **Gitignored** — same. |
| `device/ca.key`,`key.pem`,`leaf.*` | private keys / leaf — **gitignored**. |
| — | **No TLS material at all is committed.** The CA's private key signs every leaf your phone will trust, so publishing the CA (even just its public half) would either leak that trust or, since `gen-cert.sh` only creates a CA when `device/ca.pem` is missing, make a fresh clone skip CA generation and then fail signing against a `ca.key` it never had. Run `./gen-cert.sh` first. |
| `gen-cert.sh` | (re)generate CA (once) + leaf (IP SAN, 397d). |
| `deploy.sh` | push everything, set perms/SELinux contexts, (re)start. **Idempotent — this is how you update.** |
| `restart.sh` | restart the running server, no redeploy. Kills a surviving `tlsproxy` too — a stale one holding `:8443` makes the new binary die on bind while the old one keeps serving. |
| `uninstall.sh` | remove service + files. |
| `logs.sh` | service state + httpd log + init logcat (troubleshooting). |

On-device install layout: web app in `/data/local/tmp/tvremote/`, service in
`/vendor/etc/init/tvremote.rc`.

---

## Modes

Two tabs at the top switch the controls (choice is saved per phone via `localStorage`):

- **Keys** — power/home/back, D-pad + OK (**hold OK = context menu**, see below), menu/mute, volume, media transport (prev / **rewind** /
  play / **fast-forward** / next). Uses `cgi-bin/k`. Rewind and FF send `KEYCODE_MEDIA_REWIND` (89)
  and `KEYCODE_MEDIA_FAST_FORWARD` (90), which were already mapped in the CGI but had no buttons.
  **Measured: VLC seeks exactly ±10 s** on these. There is deliberately no subtitle-toggle or
  audio-track button — Android has no keycode for either, and inventing one would just produce a dead
  button.
- **Touchpad** — a relative **air-mouse** that moves the box's real hardware cursor (like a
  laptop trackpad). **Drag = move cursor**, **tap = left-click**, **2-finger drag = wheel
  scroll**; plus **Click** and Home/Back/OK buttons below. Works because this box has a real
  mouse HID ("Hi mouse"); we inject `REL_X/REL_Y`/`BTN_MOUSE`/`REL_WHEEL` via `sendevent` (NOT
  `input roll` — that source does nothing here). Cursor is OS-rendered, so it works in any app.

Air-mouse **sensitivity is a slider** in Touchpad mode (1.0–5.0, default 2.2, saved per phone in
`localStorage`). Moves are batched ~40 ms client-side.

**Pointer injection is native to the proxy** (`pointer()` in `main.go`), not the `m` CGI. The CGI
version cost roughly **five fork+execs per move batch** — busybox forked `sh`, which ran `cat` for
the cached node path and then three separate `sendevent` processes — at one batch per 40 ms on a
1.0 GHz ARMv7. That was the air-mouse jitter; it was never the TLS handshake (`httputil.ReverseProxy`
already strips `Connection` from backend responses, so client keep-alive was fine). The proxy now
holds one write fd on `/dev/input/eventN` and a move is a single 48-byte write.

**Measured on device: 33.8 ms → 2.9 ms** per request (mean of 20, connection reused). At 33.8 ms the
old path nearly saturated its own 40 ms batch interval.

Two details that are easy to get wrong there:
- `struct input_event` is **16 bytes** on this 32-bit ARM target (`timeval` = 2 × 32-bit `long`, not
  2 × 64), so the bytes are encoded by hand rather than via a Go struct. `TestEvLayout` pins them.
  The time field is left zero — the kernel's input core stamps injected events itself.
- The fd is dropped and the node re-resolved by name on any write error, since it renumbers across
  reboots and USB re-enumerates (the old CGI did the same via its `.mouseev` cache check).

(The `m` CGI still accepts `tap`/`swipe` for absolute touch, unused by the current UI, and those
still fall through to it.)

## Auth (token)

Everything except `/ca.crt` requires a shared token. Without it the CGIs — which inject key events
**as root** — were reachable by anything on the WiFi.

- The secret lives only on the device: `/data/local/tmp/tvremote/token`, `0600`, generated once by
  `deploy.sh` with `openssl rand -hex 16`. It is **reused across deploys on purpose**; rotating it
  invalidates every phone's saved shortcut, so that needs `./deploy.sh --rotate-token`.
- `?t=<token>` exchanges the token for a `Secure`/`HttpOnly`/`SameSite=Lax` cookie and **302s to `/`**,
  so the secret doesn't linger in the URL bar, history or a `Referer`. Compared with
  `subtle.ConstantTimeCompare`.
- **Fails closed.** No token file on the device → the proxy answers **503** ("run ./deploy.sh") for
  everything, rather than falling open and serving root key injection to the LAN. `./logs.sh` reports
  whether the token exists.
- The UI shows a status dot next to the title: green = last request OK, red = unreachable, amber =
  token rejected. This matters because `fetch` **resolves** on a 401 — checking only `.catch()` would
  make an expired token look like a working remote with dead buttons.

**A token alone isn't enough — routes are allowlisted too.** busybox is started with
`-h <install dir>`, so it will serve *everything* in there: `key.pem` (the TLS private key), `token`
(this gate's own secret), `boot.sh`, the logs, the proxy binary. File modes are no defence because
httpd runs as root. So `routes()` proxies only `/`, `/index.html` and `/cgi-bin/{k,t,a,m}`; anything
else is a **404 even with a valid cookie**. `TestRoutesDoNotExposeTheInstallDir` pins that, and
`deploy.sh` asserts `/key.pem` and `/token` both return 404 before it will report success.

## Hold OK = context menu

Holding **OK** for 550 ms opens the focused app's context menu, the same as holding Select on the
physical remote. Tap still sends a plain `DPAD_CENTER`. Both OK buttons (the Keys d-pad and the
Touchpad row) behave identically — it's the same key, so it shouldn't matter which one you use. The
button turns **amber** while the hold is counting down, and a hold never also fires the tap.

**`input keyevent --longpress` does not work for this and was removed.** It looks like the obvious
answer — `input` even documents the flag — but it injects the key down, a repeat carrying
`FLAG_LONG_PRESS`, and the key up **all at the same timestamp**. Apps implementing `onKeyLongPress`
would react, but any app that measures down→up elapsed time itself sees 0 ms and treats it as a tap.
Measured on this box, in both apps tried:

| app | `--longpress 23` did | expected |
|---|---|---|
| X-plore | collapsed the folder (a plain select) | open the item's context menu |
| SmartTube | started playing the video (a plain select) | open the video's context menu |

So the proxy **holds the key for real on the evdev node** instead — `EV_KEY` down, wait 600 ms, up —
which gives genuine elapsed time plus kernel autorepeat, exactly like the physical remote. X-plore
then opens its context menu (Mark files / Show details / Rename / New folder / Play / Find / …),
verified end-to-end through the deployed proxy.

Details that matter:

- The key goes to the **"Hi keyboard"** node (the IR receiver), not the "Hi mouse" one used by the
  air-mouse. Both are resolved by name, since event numbers move across reboots.
- **`KEY_ENTER` (28) is `DPAD_CENTER`** per `/vendor/usr/keylayout/Vendor_0001_Product_0001.kl`,
  which is the layout for that device (`Bus=0000 Vendor=0001 Product=0001`). `TestHeldKeyEncoding`
  pins the exact down/up bytes.
- The evdev mutex is **not** held across the 600 ms sleep, and the key-up is retried once if it
  fails — a key left logically down would wedge the projector's whole UI.
- `?long_ok` is handled natively and must never fall through to `cgi-bin/k`, where it would 400 and
  become a silently dead button. `TestLongOkIsHandledNatively` asserts that, and that every plain
  keycode still reaches the CGI untouched.

Only OK has a hold action. Back/Home were left alone, and the d-pad deliberately does not
auto-repeat on hold (that's a different mechanism — repeated taps, not one held key).

### Where a held OK actually produces a menu

**Whether a menu appears is the app's choice, not the remote's.** Confirmed working: X-plore, and the
Arc launcher's **All Apps** view (menu offering Add to Category / Reorder / Add to Fav / Hide /
Uninstall). Confirmed *not* working: the Arc launcher's **home row**, where a held OK just launches the
focused tile — Arc has no per-tile context menu there. `KEYCODE_MENU` (82) and `KEYCODE_SEARCH` (84)
were both tried on that home row and don't open one either, so there's nothing the remote can send to
produce it.

**Hold duration is not the variable.** 0.6 / 0.7 / 0.9 / 1.1 / 1.3 / 1.4 / 1.5 / 2.0 s were all
measured and behave identically in the same view. An early result suggesting 1.5 s worked where 600 ms
didn't was a bad measurement — the launcher was in a different view, and `home` does not dismiss Arc's
menu, so a stale open menu made two screenshots compare byte-identical. `holdDur` is 800 ms purely for
margin over Android's 500 ms default.

### The bug that actually caused "hold behaves like a normal OK press"

The touch handler cancelled the hold on **any** `touchmove`. A finger resting on a button always drifts
a pixel or two, so the timer was killed, and `touchend` then fell through to the tap — sending a plain
`?ok`. The symptom is indistinguishable from a working gesture hitting an app with no long-press
action, which is what made it confusing.

This was missed because the browser used for testing reports `'ontouchstart' in window === false`, so
only the **mouse** path was ever exercised — and a mouse doesn't drift. The touch branch now:

- cancels only past a **14 px** slop threshold, so jitter no longer kills the hold;
- suppresses the tap when a real drag happened, so swiping to scroll off the app row no longer
  launches an app;
- keeps `touchstart` **passive**. `preventDefault()`ing it would suppress iOS's long-press callout, but
  it also blocks page scrolling for swipes starting on a button — and the app row sits at the bottom
  edge where you'd start one. The callout is suppressed with `-webkit-touch-callout:none` +
  `touch-action:manipulation` instead.

Holding OK now flashes **"held OK sent"** in the remote. That exists specifically to separate "the
gesture never fired" from "the app ignored it" — without it both look identical from the couch.

## Apps — 4 editable favourite slots

A shared row of **four slots** launches apps directly, so you don't have to D-pad through the
launcher. **Tap = launch, hold (550 ms) = pick a different app for that slot** — there's a hint line
under the row, and holding shows a sheet listing every app the projector's own launcher lists.

Slots are saved **per phone** in `localStorage` (like the mode and sensitivity settings). Defaults are
CinePulse / VLC / Just Player / Settings. The stored value is validated on load — `Array.isArray`, four
entries, all non-empty strings — so corrupt or half-written storage falls back to the defaults instead
of throwing (a 4-character *string* would otherwise satisfy a naive `.length === 4` check).

**Settings is a special case.** It exposes no launcher activity on this build, so it can never appear
in the app list. It's a sentinel slot value (`@set`) that routes to the fixed `cgi-bin/a?set`
shortcut, which uses `am start -a android.settings.SETTINGS` → Whale OS's own
`com.zhiying.settings/.MainZYWhiteActivity`.

### How this stays safe now that a package name comes from the request

The fixed shortcuts (`?cine`, `?vlc`, `?just`, `?next`, `?set`) still work unchanged — they fall
through to the CGI's `case` whitelist. But an editable row needs `?pkg=<package>`, which is a real
change: the endpoint now accepts a name instead of picking a label. Three things bound it, all in the
proxy (`apps()` / `appList.launch()` in `main.go`):

1. **Charset validation** — must match `^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)*$`. Rejected with
   **400**. `TestLaunchRejectsMalformedPackageWithoutExec` checks that `;`, `` ` ``, `$( )`, `|`, `/`,
   spaces and traversal are all refused *before* anything is exec'd.
2. **The allowlist is still authoritative — it just comes from the system.** The package must appear
   in the device's own `cmd package query-activities -c LAUNCHER` output, and the activity used is the
   *resolver's*, never the caller's. So only an app the TV's launcher would itself show can be
   started; anything else is **404**. `parseActivities` drops any line that isn't a clean
   `package/activity` pair.
3. **No shell at all.** `/system/bin/cmd` is exec'd with an argv array. (`/system/bin/am` turns out to
   be just a shell wrapper — literally `cmd activity "$@"` — so calling `cmd` directly skips a process
   *and* any shell parsing.)

The launcher list is cached for 30 s with a forced refresh on lookup miss, so a newly installed app
still launches on first try without every press paying the ~130 ms query.

Failures are surfaced, not swallowed: **400** malformed, **404** not an installed launchable app,
**500** launch failed — all of which show as a red/amber dot rather than a silently dead button.
`cmd activity`'s exit status alone isn't trusted (it prints `Error:` while still exiting 0), so its
output is checked too; a "Warning: Activity not started, its current task has been brought to the
front" is success, because the app was already open.

Display names are cosmetic and client-side. Real labels aren't reachable — the device reports
`labelRes` resource IDs and has no `aapt` — so there's a small name map with a derived fallback, and
the picker always shows the package name underneath so a slot is never ambiguous.

**No code change is needed to add an app any more** — install it on the projector and it appears in the
hold-to-pick sheet automatically. The only reason to touch `index.html` is to give it a nicer display
name in the `NAMES` map (otherwise it gets a derived one). The `case` labels in `device/cgi-bin/a` are
now only the fixed shortcuts kept for backward compatibility.

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

Change the **port**: edit it in `device/boot.sh` (the source of truth — keep the
`127.0.0.1:` prefix) and in the `PORT=` line of `deploy.sh`/`restart.sh`, then `./deploy.sh`.

## Troubleshooting

- **"not authorized" (amber dot) / 401** → the cookie is gone. Reopen the tokenized URL from
  `./deploy.sh`. If it keeps happening, re-add the home-screen shortcut **with `?t=…` in it**.
- **Everything returns 503** → no token file on the device (the proxy fails closed by design).
  `./logs.sh` says whether it exists; `./deploy.sh` recreates it.
- **Phone can't load the page** → same WiFi? `./logs.sh` (is it listening on 8443?).
  `./restart.sh`. Confirm projector IP is still `192.168.220.53` (it's a DHCP lease).
  Note `http://IP:8790` **is supposed to be dead** now — that's the fix, not a fault.
- **Page loads, buttons do nothing** → check the status dot first. `./logs.sh`; a manual test:
  `adb -s 192.168.220.53:5555 shell input keyevent 24` should change volume.
- **Cursor moves but clicks don't (or nothing moves)** → `./logs.sh` for `pointer:` lines. The
  proxy needs write access to the "Hi mouse" node; `adb shell cat /proc/bus/input/devices` should
  list `Name="Hi mouse"`. A `pointer unavailable` 500 means open/write failed.
- **Didn't survive a reboot** → `adb ... shell getprop init.svc.tvremote` should be
  `running`; check `logs.sh` init section for parse errors in `tvremote.rc`.

## Verified on device

Deployed and measured against the projector on **2026-08-05**. What was found, including the
things that were wrong:

**The vulnerability was live before the fix.** `curl http://192.168.220.53:8790/key.pem` returned the
**TLS private key** to the whole WiFi, unauthenticated, and `netstat` showed `[::]:8790`. After the
change the socket is `127.0.0.1:8790` and `/key.pem` is a 404 even with a valid token.

- ✅ **`busybox httpd -p 127.0.0.1:8790` accepted.** v1.26.2's own usage string documents
  `-p [IP:]PORT`. Confirmed listening on loopback only.
- ✅ **`ca.crt` served over plaintext on :8443** → HTTP 200 (asserted by `deploy.sh` every run).
- ✅ **Native pointer works, and the 16-byte struct is right.** Read back with
  `getevent -lt /dev/input/event1` while injecting `rel=37,-11`: `REL_X 00000025` (=37),
  `REL_Y fffffff5` (=−11), clean `SYN_REPORT`, `BTN_MOUSE` DOWN/UP, `REL_WHEEL 1`. The kernel parses
  the hand-encoded events exactly as intended. Malformed input (`rel=9999,0`, `rel=abc,1`, `rel=1`,
  `wheel=500`) all correctly 400.
- ✅ **Latency, measured with connection reuse: 33.8 ms → 2.9 ms per request** (mean of 20, native
  handler vs a request that still forks the CGI shell). ~12×. The client batches every 40 ms, so the
  old path at 33.8 ms was nearly saturating its own batch interval — any hiccup queued up as visible
  jitter. Also confirms keep-alive was already working (2.9 ms round-trips contain no handshake), so
  the TLS-handshake theory was wrong.
- ✅ **VLC honours keycodes 89/90 as exactly ±10 s.** Measured while paused (so playback couldn't
  contaminate the reading): `98071 → 108071 → 98071` ms. `k?play` toggles pause (state 3 ↔ 2).
- ⚠️ **`monkey -c LAUNCHER` is broken on this build — removed.** It fails for *every* package,
  including installed ones: `** No activities found to run, monkey aborted.`, exit 252. This is a
  TV-flavoured build whose launcher intents don't match what monkey filters on. `cgi-bin/a` now uses
  `cmd package resolve-activity --brief -c LAUNCHER` + `am start -n`, which resolves correctly
  (`org.videolan.vlc/.StartActivity`, `com.example.cinepulse/.MainActivity`,
  `com.brouken.player/.PlayerActivity`).
- ⚠️ **Settings has no LAUNCHER activity at all** on this build, so it goes through
  `am start -a android.settings.SETTINGS` → lands on Whale OS's own
  `com.zhiying.settings/.MainZYWhiteActivity`.
- ⚠️ **NextPlayer is no longer installed** (`dev.anilbeesetti.nextplayer` absent; only
  `com.brouken.player` remains). The UI button is now **Just** (Just Player). The `next` whitelist
  entry is kept so it works if reinstalled — pressing it today returns **500**, i.e. a red dot rather
  than a false "ok".
- ✅ **App launch end-to-end**, foreground activity confirmed changing for all four: `a?vlc`, `a?just`,
  `a?cine`, `a?set` → 200; `a?next` → 500; `a?bogus` → 400.
- ✅ **`boot.sh` itself was exercised**, not bypassed: `deploy.sh` restarts via `setprop ctl.restart`,
  which makes init re-run `boot.sh` — the same path used at boot. No reboot needed to trust it.

**Still needs your phone** (can't be checked from here):

1. **iOS accepts the CA profile** fetched from `http://192.168.220.53:8443/ca.crt`. The file is served
   correctly (200, `application/x-x509-ca-cert`); whether iOS installs and trusts it is a device-side
   step. Your phone already trusts this CA, so this only matters for a *new* phone.
2. **iOS cookie persistence** in the standalone home-screen WebView across days/reboots — which is why
   the shortcut should be saved **with `?t=…`** in it.

## Security note

**The remote is authenticated** (token cookie, see above) and its **backend is loopback-only**, so
a device on the WiFi can no longer press buttons on the projector.

**The remaining hole is not this repo's:** the projector's **adb is open as root to the entire WiFi**
(port 5555, `persist.adb.tcp.port=5555` + `ro.debuggable=1`). Anything on the LAN can `adb root` the
box, which trivially includes reading `/data/local/tmp/tvremote/token`. So the token raises the bar
for casual access on a shared network; it is **not** a defence against someone who can reach adb.
Closing that means firewalling 5555 or dropping the prop — and the props are also the guaranteed
recovery path if the remote wedges, so it's a deliberate trade, not an oversight.

Also note SELinux on this box is **Permissive**, so the `u:r:su:s0` service isn't confined.

## Device facts (for future edits)

- HiSilicon Hi3751V350, Android 12 (SDK 31), **armeabi-v7a** (32-bit), 1 GB RAM.
- SELinux **Permissive**; `/`, `/vendor` remountable rw; root via `adb root`.
- `input` binary present; busybox at `/vendor/bin/busybox` (v1.26.2, has `httpd`).
- adb quirk: `adb shell input text` types UPPERCASE — toggle with `input keyevent 115` first.
