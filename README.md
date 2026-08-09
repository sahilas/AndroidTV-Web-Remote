# Android TV Web Remote

**Turn your phone into a remote for an Android TV box, using just a web browser.**

Lost the remote? Got a TV box that Google's remote app refuses to see? Open a web page on
your phone and control the box — D-pad, volume, play/pause, typing, launching apps.

- **No app to install** on your phone. It's a web page.
- **No account, no cloud.** Everything stays on your home WiFi.
- **Nothing to install on the TV** either — one small program is copied over and runs there.

*Why Google's app doesn't work on these boxes: many cheap "Android TV" projectors and boxes
aren't Google-certified, so they don't broadcast the service the official remote looks for.
It can never find them. [Longer explanation below](#why-this-exists-root-cause).*

---

## What you need

| | |
|---|---|
| **A TV box** | Any Android TV / Google TV / projector running Android. It must let you turn on "ADB debugging" (a developer setting — see step 1). |
| **A computer** | Mac or Linux, on the **same WiFi** as the TV box. This is only needed for setup; afterwards you use your phone. |
| **Two free tools** | `adb` and `Go`. Install on Mac with:<br>`brew install android-platform-tools go` |

You do **not** need to root or modify your TV box.

## Setup

### Step 1 — Turn on ADB debugging on the TV box

This is the switch that lets your computer talk to the box. It's hidden by default.

On most Android TV boxes: **Settings → Device Preferences → About →** click **Build**
seven times. You'll see "You are now a developer". Then go back to
**Settings → Device Preferences → Developer options** and turn on **ADB debugging** (also
called "USB debugging" or "Network debugging").

On a **Fire TV**: Settings → My Fire TV → About → click the device name seven times, then
Developer Options → ADB Debugging.

Then find the box's IP address — usually **Settings → Network** — something like
`192.168.1.42`.

### Step 2 — Tell the project where your box is

```bash
cp config.env config.local.env
```

Open `config.local.env` and put in your box's IP address:

```bash
TV_ADDR=192.168.1.42:5555
```

(`5555` is the standard port — leave it. `config.local.env` is ignored by git, so your
address never gets committed.)

### Step 3 — Run it

```bash
./gen-cert.sh    # once — creates the security certificate
./deploy.sh      # copies everything over and starts it
```

The first time, your TV may show a popup asking you to allow the connection — say yes
(and tick "always allow").

**When it finishes, `deploy.sh` prints your remote's web address and the exact steps to
set it up on your phone.** Follow what it prints — including a one-time certificate step
that lets the page run fullscreen like a real app.

That's it. To update later, just run `./deploy.sh` again.

### Something went wrong?

`./deploy.sh` checks its own work and refuses to claim success unless everything passes. If
it fails it prints the reason and the TV box's own log. Run `./logs.sh` to see what the
box thinks is happening. There's a [troubleshooting section](#troubleshooting) further down.

## Will it work on your box?

Almost certainly for the basics. `./deploy.sh` tests your specific box and prints exactly
what it can do, so you never have to guess.

| Feature | Works on a normal, unmodified TV box? |
|---|---|
| D-pad, volume, play/pause, back, home | ✅ yes |
| Typing into search boxes | ✅ yes |
| Launching apps | ✅ yes |
| **Touchpad** (move a mouse cursor) | ⚠️ only on rooted / developer-build boxes |
| **Hold-OK** (opens an app's context menu) | ⚠️ same |
| **Survives a reboot** | ⚠️ same — otherwise re-run `./deploy.sh` after restarting the box |

The remote hides the features your box can't do, so you won't see buttons that don't work.

<details>
<summary><b>The technical reason, if you care</b></summary>

The touchpad and hold-OK write directly to the Linux input devices (`/dev/input`). Whether
that's allowed is decided by SELinux, and it's stricter than file permissions suggest. The
`shell` user is in the `input` group on both boxes tested, and on a **Permissive** build
that's enough — everything works without root. On an **Enforcing** build, which is what a
normal retail box is, policy denies the shell domain `/dev/input` *and* `/dev/uinput`
regardless of group. Keys, text and app launching are unaffected because those go through
Android's own `input` command, which never touches evdev.

Boot persistence needs `init` to read a service definition from a partition a locked box
won't let you write.

Measured, not inferred:

| | HiSilicon Hi3751V350 | Google ATV emulator |
|---|---|---|
| build / SELinux | `userdebug`, Permissive | `user`, **Enforcing** |
| ABI | armeabi-v7a | arm64-v8a |
| `adb root` | yes | refused — *"adbd cannot run as root in production builds"* |
| keys / text / apps | ✅ | ✅ |
| air-mouse, hold-OK | ✅ (even as uid 2000) | ❌ `/proc/bus/input/devices`: permission denied |
| boot persistence | ✅ | ❌ |

**Port conflicts are real.** The Google ATV image already had something on `8443`, and ships
the actual Android TV Remote Service on `6466`/`6467`. If `deploy.sh` reports
`address already in use`, set a different `HTTPS_PORT` in `config.local.env`.

</details>

> **Tested on two boxes**, deliberately unalike: a HiSilicon Hi3751V350 projector
> (Android 12, `userdebug`, SELinux Permissive, armv7) and a Google Android TV emulator
> image (Android 12, `user`, SELinux **Enforcing**, arm64, `adb root` refused). Between them
> they cover both ABIs, both SELinux modes, and both the rooted and locked cases.
> `192.168.220.53` throughout is the projector's DHCP address, used as a concrete example.

> **Working on this with an AI agent?** Read [CLAUDE.md](CLAUDE.md) instead of this file.
> It is a ~100-line map of the codebase — what each file owns, the invariants, and the traps
> that have already cost time — and Claude Code loads it automatically.

---

# How it works

*Everything below is optional reading — the setup above is all you need to use it.*

[Using it](#using-the-remote) · [Commands](#everyday-commands) · [Settings](#changing-the-settings) ·
[Why this exists](#why-this-exists-root-cause) · [Architecture](#architecture) · [Files](#files) ·
[Buttons & modes](#modes-button-by-button) · [Auth](#auth-token) · [Hold OK](#hold-ok--context-menu) ·
[Apps](#apps--4-editable-favourite-slots) · [Typing](#keyboard--voice-dictation-input) ·
[Troubleshooting](#troubleshooting) · [What was measured](#verified-on-device) ·
[Security](#security-note) · [Fire TV](#fire-tv) · [Limits](#not-supported-yet)

## Using the remote

Two tabs at the top, remembered per phone: **Keys** (D-pad, volume, media) and **Touchpad**
(drag = move cursor, tap = click, two fingers = scroll). Below both: four app shortcuts
(tap = launch, hold = change which app) and a keyboard bar.

Add it to your phone's home screen and it opens fullscreen, like an app.

## Everyday commands

```bash
./deploy.sh                  # install, and every future update
./deploy.sh --rotate-token   # new secret (every phone must reopen the new link)
./restart.sh                 # bounce the server without redeploying
./logs.sh                    # what is it doing / why did it fail
./uninstall.sh               # remove it completely
```

`deploy.sh` rebuilds, re-copies, restarts, then asserts these and refuses to report success
unless all hold:

| check | want |
|---|---|
| `https://IP:8443/` with no token | **401** |
| `https://IP:8443/` with the token | **200** |
| `<html` present in that response body | **1** — a 200 alone would also pass on an empty embed |
| `http://IP:8443/ca.crt` (plaintext) | **200** — trust bootstrap intact |
| `…/cgi-bin/k?volup` with the token | **200** — injection really runs (paired with a `voldown` so volume ends where it started) |
| `…/key.pem` **with** the token | **404** — TLS private key not downloadable |
| `…/token` **with** the token | **404** — the secret can't be read back out |
| listeners on `:8790` | **0** — no busybox survived from the old layout |

## Changing the settings

Everything lives in `config.local.env` (your gitignored copy of `config.env`):

| Setting | What it does |
|---|---|
| `TV_ADDR` | Your box's address. Also accepts a plain adb serial like `emulator-5554` or a USB device serial — verification then goes through an `adb forward` tunnel automatically. |
| `HTTPS_PORT` | Change if something else on the box already uses 8443. |
| `REMOTE_DIR` | Where it installs on the box. |
| `MDNS_HOST` | The `.local` name it advertises. |

**If the box's IP changes** — it's usually a DHCP lease — the certificate no longer matches
and your phone will complain. Re-run `./gen-cert.sh && ./deploy.sh`. Setting a fixed
(reserved) IP on your router avoids this for good.

### Overrides you probably will not need

| Flag | For |
|---|---|
| `TVR_FORCE_SHELL=1 ./deploy.sh` | deploy as the unprivileged `shell` user even where `adb root` would work |
| `-pointer-name` / `-key-name` | pin an input device by exact name when capability detection picks the wrong one |

Input devices are found by **capability**, not by name: a pointer is whatever declares
`EV_REL` with `REL_X`/`REL_Y`, and a key device is whatever declares `EV_KEY` with
`KEY_ENTER`. That is why no vendor-specific device name appears anywhere in the source.
The proxy logs which nodes it bound on every start; if it guesses wrong, pin it.

## Requirements, precisely

- `adb` (`brew install android-platform-tools`) and **Go 1.25+** (to cross-compile the
  server — `golang.org/x/crypto` requires it).
- **On the box: nothing.** No busybox, no web server, no scripting runtime. The binary is
  static and self-contained.

On the projector this repo was built against, `persist.adb.tcp.port=5555` and
`ro.debuggable=1` are set, so adb comes back on every boot even if the remote fails — that
is the recovery path. A retail box without those props needs USB and `adb tcpip 5555` again
after a reboot, which is worth knowing before you rely on it.

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

One process, no backend, nothing served off the filesystem.

```
 phone                    tlsproxy :8443   (root, or uid 2000 "shell")
 home-screen ──TLS──▶ ┌───────────────────────────────────────────────────┐
   app                │ token gate (cookie)                               │
                      │   /ca.crt ungated, and answered over PLAINTEXT    │
                      │   everything else 401 without the cookie          │
                      ├───────────────────────────────────────────────────┤
                      │ /              → index.html, //go:embed'd         │
                      │                  (no document root exists)        │
                      │                                                   │
                      │ evdev, fd held open — one 48-byte write:          │
                      │ /cgi-bin/m?rel|click|wheel                        │
                      │   └▶ struct input_event → /dev/input/eventN       │
                      │      node found by CAPABILITY (EV_REL+REL_X/Y)    │
                      │ /cgi-bin/k?long_ok                                │
                      │   └▶ held EV_KEY 28 for 800 ms on the node with   │
                      │      EV_KEY+KEY_ENTER — a real hold, so the app   │
                      │      opens its context menu                       │
                      │                                                   │
                      │ argv exec, never a shell:                         │
                      │ /cgi-bin/k?<name>  → input keyevent <code>        │
                      │ /cgi-bin/t?<text>  → input text, char by char     │
                      │ /cgi-bin/m?tap|swipe → input tap/swipe            │
                      │ /cgi-bin/a?list    → cmd package query-activities │
                      │ /cgi-bin/a?pkg=    → cmd activity start-activity  │
                      │ /cgi-bin/a?set     → android.settings.SETTINGS    │
                      └───────────────────────────────────────────────────┘
 Android init ──supervises──▶ boot.sh → tlsproxy      (only where /vendor is writable;
                                                       otherwise launched over adb)
```

The `/cgi-bin/` paths are not CGI any more — there is no CGI, and no shell. They are kept
as the wire format because the UI already speaks it and renaming them would buy nothing.

## Files

| Path | What it is |
|---|---|
| `config.env` | every tunable: adb endpoint, ports, install dir, mDNS name. Copy to `config.local.env` (gitignored) rather than editing this. |
| `lib.sh` | sourced by every script: loads the config, connects adb, maps the device's ABI to a binary. |
| `tls-proxy/ui/index.html` | the remote UI (D-pad, volume, media keys). Pure HTML/JS, no deps. Compiled into the binary with `//go:embed`. |
| `tls-proxy/main.go` | the server: token gate, TLS/plaintext split, evdev pointer + held key, app list/launch, routing. |
| `tls-proxy/input.go` | everything the CGI scripts used to do — keycodes, text, tap/swipe, settings — via `input`/`cmd` with argv exec, never a shell. |
| `tls-proxy/discover.go` | finds the pointer and key nodes by capability bits from `/proc/bus/input/devices`, so no vendor device name is hardcoded. |
| `tls-proxy/*_test.go` | host-runnable: `input_event` byte layout, capability detection against a real device dump, app-list parsing, package validation, URL decoding, tap/swipe validation, that no filesystem path is served, and the auth gate (401/403/302, cookie flags, fail-closed). |
| `device/boot.sh` | launcher; derives its own install dir from `$0`, waits for `/data`, `exec`s the server with flags from the device config. |
| `device/tvremote.rc` | Android init service; starts `boot.sh` on `sys.boot_completed=1`, seclabel `u:r:su:s0`. Only installed where `/vendor` is writable. |
| `build.sh` | cross-compiles arm64-v8a, armeabi-v7a, x86_64 and x86 (static, cgo off) after running the tests. `deploy.sh` calls it and picks by the device's ABI. |
| `device/token` (on device only) | the shared secret, `0600`, generated once by `deploy.sh`. Gitignored; never written to the Mac. |
| `device/cert.pem` | fullchain (leaf + CA) served by the proxy. **Gitignored** — `gen-cert.sh` output. |
| `device/ca.crt` | the CA cert the iPhone downloads + trusts. **Gitignored** — same. |
| `device/ca.key`,`key.pem`,`leaf.*` | private keys / leaf — **gitignored**. |
| — | **No TLS material at all is committed.** The CA's private key signs every leaf your phone will trust, so publishing the CA (even just its public half) would either leak that trust or, since `gen-cert.sh` only creates a CA when `device/ca.pem` is missing, make a fresh clone skip CA generation and then fail signing against a `ca.key` it never had. Run `./gen-cert.sh` first. |
| `gen-cert.sh` | (re)generate CA (once) + leaf (IP SAN, 397d). |
| `deploy.sh` | push everything, set perms/SELinux contexts, (re)start. **Idempotent — this is how you update.** |
| `restart.sh` | restart the running server, no redeploy. Kills a survivor first — one still holding `:8443` makes the new binary die on bind while the old one keeps serving, so a health check would pass against the code you thought you replaced. |
| `uninstall.sh` | remove service + files. |
| `logs.sh` | service state, listeners, processes, logs, init logcat (troubleshooting). |

On-device install layout: `/data/local/tmp/tvremote/` holds the binary, `boot.sh`, the
cert pair, the config and the token — seven files, no document root. The boot service, where
one is possible, is `/vendor/etc/init/tvremote.rc`.

---

## Modes, button by button

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

Measurements, in date order, including the things that turned out to be wrong. Nothing here
is inferred — where a claim could not be measured, it says so.

### The first deploy (2026-08-05)

Measured against the projector. What was found, including the things that were wrong:

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

### A second box disproved a documented claim (2026-08-09)

Tested on a Google Android TV emulator image — `user` build, SELinux **Enforcing**, arm64,
`adb root` refused. It contradicted something this README had asserted one commit earlier.

The claim was that root is not needed for the touchpad or hold-OK, because the `shell` user
is in the `input` group. That was measured on the projector, which is **Permissive**, and it
does not generalise. On Enforcing, policy denies the shell domain `/dev/input` *and*
`/dev/uinput` regardless of group: `/proc/bus/input/devices` is not even readable, and
`sendevent` fails. Keys, text and app launch are unaffected — those go through Android's
`input` command and never touch evdev.

- ✅ ABI selection, capability detection, non-root deploy and the config layer all worked
  unmodified on hardware they had never seen.
- ❌ Touchpad and hold-OK return 500 there. The server now reports this at `/caps`, and the
  UI hides both rather than offering controls that cannot work.
- ⚠️ Port `8443` was already occupied on that image, which also ships the real Android TV
  Remote Service on `6466`/`6467`. `deploy.sh` now prints the box's own startup log on
  failure instead of a wall of `000`.

Full comparison in [Will it work on your box?](#will-it-work-on-your-box).

### Launcher categories: querying one hides apps (2026-08-09)

Found while adding Fire TV support, and it affected every Android TV box rather than only
Amazon's. The app list queried `android.intent.category.LAUNCHER` only, but TV apps declare
`LEANBACK_LAUNCHER`. **Neither category is a superset of the other** — measured on both
boxes:

| | Only in `LEANBACK_LAUNCHER` | Only in `LAUNCHER` |
|---|---|---|
| Google ATV emulator | **`com.android.tv.settings`** | `com.google.android.tv.remote.service` |
| HiSilicon projector | *(none)* | `com.newlink.cast`, `com.newlink.filemanager`, `com.zhiying.bluetoothmodelservice` |

So on the emulator **Settings was invisible in the picker and could not be launched by
package**; querying leanback alone would instead have dropped three apps on the projector.
Both are now queried and merged, leanback winning ties so the TV-flavoured activity is the
one started.

- ✅ **Emulator after the fix:** list grew 7 → 8, `a?pkg=com.android.tv.settings` → 200,
  `a?set` → 200.
- ✅ **Projector after the fix:** still 17 packages, all three `LAUNCHER`-only ones present.

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

## Fire TV

Fire OS is the case this project was built for — Amazon's boxes ship no Google remote
service at all. The code path is in place; **no Fire TV was available to test on**, so treat
the setup below as documented-not-verified and please report what actually happens.

**What was changed for it, and is verified elsewhere:** the app list now queries
`LEANBACK_LAUNCHER` as well as `LAUNCHER`. Fire OS is TV-first, so its apps declare the
leanback category, and querying `LAUNCHER` alone would have shown a short or empty list.
Measured on a Google Android TV image, where `com.android.tv.settings` is leanback-only and
was invisible before this. Settings also falls back to launching a known settings package
(`com.amazon.tv.settings` first) if the generic action intent does not resolve.

**Enabling network adb on a Fire TV:**

1. Settings → My Fire TV → About → click the device name **7 times** to unlock Developer
   Options.
2. Settings → My Fire TV → **Developer Options** → turn **ADB Debugging** on.
3. Note the IP: Settings → My Fire TV → About → Network.
4. Put it in `config.local.env` as `TV_ADDR=<ip>:5555`, then
   `./gen-cert.sh && ./deploy.sh`. Accept the authorisation prompt on the TV.

**What to expect.** Fire OS is a locked `user` build with SELinux Enforcing, so `deploy.sh`
should report the **NO-PERSIST** tier: keys, text, app launch and tap work; the touchpad and
hold-OK will be unavailable and the UI will hide them; the server needs relaunching over adb
after a reboot. Both Fire TV ABIs (armeabi-v7a on older sticks, arm64-v8a on 4K and Cube)
are already built.

**Most likely to need attention if it misbehaves:** Amazon replaces large parts of the
framework, so `cmd package` / `cmd activity` output could be shaped differently enough to
break parsing — `./logs.sh` and the `/caps` endpoint are where that would show.

## Not supported yet

**A real retail set-top.** Both boxes tested so far are a dev-build projector and an
emulator. The emulator is the right *profile* (`user` build, Enforcing, `adb root` refused)
but its input devices are virtual, so a physical box's node layout is still unverified.

**Air-mouse and hold-OK on a locked box.** Not a gap that can be closed in this design —
both evdev routes (`/dev/input` and `/dev/uinput`) are denied to the shell domain under
SELinux Enforcing. It would take a companion APK with an AccessibilityService, which is a
much larger project than this one.

## Device facts (for future edits)

- HiSilicon Hi3751V350, Android 12 (SDK 31), **armeabi-v7a** (32-bit), 1 GB RAM.
- SELinux **Permissive**; `/`, `/vendor` remountable rw; root via `adb root`.
- `input` binary present; busybox at `/vendor/bin/busybox` (v1.26.2, has `httpd`).
- adb quirk: `adb shell input text` types UPPERCASE — toggle with `input keyevent 115` first.
