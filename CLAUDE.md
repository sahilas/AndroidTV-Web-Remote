# Orientation for coding agents

Read this instead of the 606-line README. The README is written for a human adopting the
project; this is what you need to change it safely. Whole repo is ~2,700 lines.

## What it is

A phone web remote for Android TV boxes that Google's remote app cannot control. One static
Go binary is pushed over `adb` to `/data/local/tmp/tvremote/`, serves the UI over HTTPS from
memory, and injects input. **Nothing on the box is a dependency** — no busybox, no web
server, no scripting runtime.

## Map

| File | Owns | Lines |
|---|---|---|
| `tls-proxy/main.go` | token gate, TLS/plaintext split on one port, evdev pointer + held key, app list/launch, routing, flags | 716 |
| `tls-proxy/input.go` | everything that shells out to Android: keycodes, text, tap/swipe, settings intent | 209 |
| `tls-proxy/discover.go` | finds input nodes by capability bits; `probeCaps()` for `/caps` | 203 |
| `tls-proxy/ui/index.html` | the whole UI, `//go:embed`-ed into the binary | 382 |
| `deploy.sh` | probe → push → restart → **assert** → report capabilities | 268 |
| `lib.sh` | config loading, adb connect, ABI→binary map, reachability | 113 |
| `config.env` | every tunable. Users copy to `config.local.env` (gitignored) | 25 |
| `device/boot.sh` | on-device launcher; derives its own dir from `$0` | 38 |
| `build.sh` | cross-compiles 4 ABIs, runs tests first | 45 |

Tests are `tls-proxy/*_test.go` (553 lines) and run on the host — no device needed.

## Invariants — breaking these is the failure mode that matters

1. **Nothing is served off the filesystem.** The install dir holds `key.pem` and `token`. A
   static file handler over it would re-expose the TLS private key to the LAN — that bug was
   real once. `TestRoutesServeNoFilesystemPath` guards it.
2. **`/ca.crt` is ungated and answered over plaintext.** Not an oversight: a phone cannot
   validate our cert until it has this file. Gating it makes trust bootstrap impossible.
3. **The gate fails closed.** No token file → 503, never open access.
4. **`deploy.sh`'s assertion block must keep asserting something real.** It is what caught
   the private-key exposure. If you remove a feature, replace its assertion; don't let it
   decay into a no-op.
5. **The server provisions itself when material is missing, and never overwrites
   material that is present.** `provision.go` generates a CA, a leaf and a token
   into `-dir` if absent. A leaf pushed by `deploy.sh` is signed by a CA the phone
   already trusts, so it is left alone even when stale — silently swapping the
   trust anchor is worse than serving an expired cert. `boot.sh` must NOT wait for
   `cert.pem`: the server creates it, so waiting deadlocks.
6. **No TLS material is committed.** `gen-cert.sh` only creates a CA when `device/ca.pem` is
   absent, so a committed `ca.pem` makes a fresh clone skip CA generation and then fail
   signing against a `ca.key` it never had.

## Where to change things

- **Add a button** → `keyCodes` in `input.go`, plus markup in `ui/index.html`.
  `TestKeyRouting` asserts every name the UI sends exists in the map.
- **Ports / address / install dir** → `config.env` only. Nothing is hardcoded elsewhere.
- **Routes** → `routes()` in `main.go`. The `/cgi-bin/` prefixes are a wire format kept for
  UI compatibility; there is no CGI and no shell.
- **Input node selection** → `discover.go`. Never match on a device *name*; that is the
  HiSilicon-specific bug this replaced.

## Traps that have already cost time

- **`pkill -f tlsproxy` kills its own shell.** The adb command line contains the literal
  string, so pkill matches itself. Use the `[t]lsproxy` bracket idiom. This silently broke
  every non-root deploy.
- **`grep -c` exits 1 on zero matches.** With `set -e`, `x=$(... | grep -c ...)` or a
  trailing `|| echo 0` outside the device shell appends a second value. Put the `|| true`
  *inside* the adb shell.
- **Capability bitmaps in `/proc/bus/input/devices` are printed in words of BITS_PER_LONG**,
  high word first, unpadded, and the format does not say whether that is 32 or 64. Only test
  bits < 32. This is why classification deliberately ignores `BTN_LEFT` (0x110).
- **`input keyevent --longpress` is not a real hold.** It emits down/repeat/up at one
  timestamp, so apps measuring elapsed time see 0 ms. Real hold needs evdev.
- **SELinux decides evdev, not the `input` group.** Permissive box: shell uid can write
  `/dev/input`, so air-mouse and hold-OK work without root. Enforcing box (retail): denied,
  including `/dev/uinput`. `probeCaps()` reports this; the UI hides what cannot work.
- **The cert's IP SAN must match the box's address.** A DHCP move breaks TLS until
  `./gen-cert.sh && ./deploy.sh`.
- **`build.sh` runs before every deploy on purpose.** Skipping it ships a stale binary while
  every source file looks correct.

## Verifying a change

```bash
cd tls-proxy && go test ./...   # host-only, fast, no device
./deploy.sh                     # builds, pushes, asserts, prints capabilities
./logs.sh                       # service state, listeners, processes, logs
```

`./deploy.sh` refuses to report success unless all eight assertions hold. Trust it over a
manual curl. `TVR_FORCE_SHELL=1 ./deploy.sh` exercises the locked-box path on a rooted box.

## Ground truth

Tested on two boxes: a HiSilicon Hi3751V350 projector (`userdebug`, Permissive, armv7) and
a Google ATV emulator image (`user`, **Enforcing**, arm64, `adb root` refused). No physical
retail box has been tested — the emulator has virtual input devices, so a real box's node
layout is unverified. Do not upgrade those claims without a measurement.

**Fire TV**: the code path exists (leanback launcher query, settings package fallback) but
has never run on Fire hardware. The `com.amazon.tv.settings` entry in `settingsPkgs` is the
only speculative line in the codebase — it is guarded by a presence check against the
device's own app list, so it is inert where the package does not exist.

**App listing queries two categories.** `LEANBACK_LAUNCHER` and `LAUNCHER`, merged, leanback
winning ties. Neither is a superset of the other and that is measured, not defensive: on the
Google ATV image `com.android.tv.settings` is leanback-only; on the projector three packages
are launcher-only. Dropping either category silently hides apps.
