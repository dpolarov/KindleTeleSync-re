# KindleTeleSync

KindleTeleSync downloads books and documents from a Telegram bot directly to a jailbroken Amazon Kindle.

This repository is a maintained reconstruction of the original KindleTeleSync project. The original application source was lost; the remaining KUAL/KOReader package was used as the behavioral reference. This fork modernizes the reconstructed implementation around a single Go executable and keeps the user-facing interface in English.

## Highlights

- One Go executable: `kindletelesync`.
- KUAL and native KOReader integration.
- Telegram synchronization with persistent update state.
- MTProto, SOCKS5, and HTTP CONNECT proxy support.
- Native KOReader settings plus token-protected browser settings.
- Download safety limits before a Telegram document is fetched.
- Structured JSON results for UI integrations.
- Diagnostics and Telegram connection tests.
- Operation lock to prevent simultaneous sync/update/settings jobs.
- Built-in log rotation.
- Self-update from stable releases, with an explicit prerelease channel for RC testing.
- Semantic version ordering prevents accidental downgrade from an RC to an older stable release.
- SHA-256 verification before an update archive is installed.
- ARMv6 and ARMv7 release builds, plus a conservative universal archive.
- Go 1.23 build baseline retained for compatibility with older Kindle Linux kernels.

## Requirements

- A jailbroken Kindle.
- KUAL and/or KOReader.
- Wi-Fi access.
- A Telegram bot token created with BotFather.
- The Telegram chat ID used with that bot.
- Linux kernel 2.6.32 or newer for the Go runtime used by release builds.

The reconstructed application has primarily been tested on newer ARMv7 Kindles. Device-specific testing is still recommended, especially on older ARMv6 models.

### Kindle kernel compatibility

Release binaries are intentionally built with **Go 1.23.12** and `GOTOOLCHAIN=local`. Go 1.23 supports Linux kernels starting at 2.6.32, while newer Go releases raise the Linux baseline and would exclude several otherwise capable older Kindles.

This means the ARM label alone is not enough to determine compatibility:

- ARMv7 Kindles using Linux 3.x or newer are within the runtime baseline.
- ARMv6 devices are supported only when their Linux kernel is at least 2.6.32.
- Very old Kindles using Linux 2.6.31 are **not supported** by these Go binaries even if the CPU can execute an ARMv6 build.

Run `kindletelesync diagnostics` on an installed build to see the detected CPU target and kernel release.

## Installation

1. Open the latest GitHub release.
2. Download one archive:
   - `KindleTeleSync-arm7.tar.gz` — optimized for ARMv7 Kindles.
   - `KindleTeleSync-arm6.tar.gz` — optimized for ARMv6 Kindles whose kernel meets the requirement above.
   - `KindleTeleSync-universal.tar.gz` — conservative ARMv6-compatible CPU build with runtime ARM detection; it still requires Linux 2.6.32 or newer.
3. Extract the archive into `/mnt/us` on the Kindle.
4. Restart KOReader if its plugin was already loaded.

The package installs:

```text
/mnt/us/extensions/KindleTeleSync
/mnt/us/koreader/plugins/KindleTeleSync.koplugin
```

## KOReader

Open **Main menu → KindleTeleSync**.

Available actions include:

- **Sync now**
- **Test Telegram**
- **Run diagnostics**
- **Settings**
  - Bot token
  - Chat ID
  - Allowed extensions
  - Download directory
  - safety limits and timeouts
  - Telegram summary notifications
  - SOCKS5 / HTTP CONNECT / MTProto proxy settings
  - reset Telegram synchronization state
  - browser-based settings
- **Update KindleTeleSync**
- **Show version**
- **Show last log**

The KOReader plugin intentionally remains Lua because KOReader plugins are Lua modules. It is only a UI adapter; synchronization, networking, updates, validation, diagnostics, locking and browser settings live in Go.

## KUAL

The KUAL menu exposes the same core Go commands directly:

```text
Sync now
Test Telegram
Open web settings
Run diagnostics
Update KindleTeleSync
Show version
```

No shell wrapper scripts are required.

## First synchronization

On the first successful connection, KindleTeleSync stores the current Telegram update state and intentionally skips old history. This prevents every old attachment in the chat from being downloaded.

After initialization:

1. Send a new supported file to the bot.
2. Run **Sync now** again.
3. The file is downloaded to the configured directory.

## Configuration

The configuration file is:

```text
/mnt/us/extensions/KindleTeleSync/config.json
```

Default safety settings are:

```json
{
  "allowed_extensions": [".epub", ".mobi", ".pdf", ".zip", ".fb2"],
  "download_path": "/mnt/us/books",
  "max_file_bytes": 104857600,
  "max_files_per_sync": 20,
  "max_total_bytes": 262144000,
  "sync_timeout_seconds": 600,
  "web_timeout_seconds": 180,
  "send_notifications": true
}
```

Existing configuration files from the reconstructed upstream version are migrated in memory by filling missing defaults. The file is written atomically with mode `0600` because it can contain a Telegram token and proxy credentials.

The download directory must stay inside Kindle user storage. KindleTeleSync checks both the configured path and its resolved symbolic-link target before writing files.

### Browser settings

KOReader and KUAL can start a temporary settings server on port `8880`.

The URL contains a random device-generated bearer token, for example:

```text
http://192.168.1.20:8880/?token=...
```

Secret fields are not pre-filled in the page. Leaving a secret field empty keeps its current value. The server automatically stops after the configured timeout and can also be stopped explicitly. The access token is removed when the session ends, so an old QR code or URL cannot be reused for a later session.

The settings server uses HTTP on the local network, not TLS, so only run it on a network you trust and treat the complete tokenized URL as sensitive while it is active.

## Safety limits

KindleTeleSync checks Telegram document metadata before downloading a file. By default it rejects work that would exceed:

- 100 MiB for one file.
- 20 downloaded files in one synchronization run.
- 250 MiB total downloaded data in one synchronization run.

Partial output is removed when a download fails. Telegram filenames are reduced to their base name before a destination path is created. The resolved download directory is also confined to Kindle user storage to prevent symbolic-link escapes.

## Proxy support

Supported modes:

- `socks5`
- `http` using HTTP CONNECT
- `mtproto`

Telegram MTProto depends on a reasonably correct clock. KindleTeleSync tries NTP synchronization before synchronization and before a Telegram connection test, and reports clock skew in diagnostics.

## Unified command line

The package contains one executable:

```text
kindletelesync sync [--json]
kindletelesync test [--json]
kindletelesync diagnostics [--json]
kindletelesync update [--prerelease] [--json]
kindletelesync web [--kindle-ui]
kindletelesync web-url
kindletelesync web-stop [--json]
kindletelesync version [--json]
```

`--json` returns a stable result envelope used by the KOReader plugin, including downloaded files, skipped items, errors and diagnostic details.

## Updates

By default, `kindletelesync update` follows the latest **stable** GitHub release. For release-candidate testing, `kindletelesync update --prerelease` also considers published prereleases. This makes it possible to install RC1 manually and then exercise the real self-update path when RC2 is published.

Semantic version comparison is used before installation. KindleTeleSync refuses to replace a newer installed version with an older release, so running the stable updater from an RC cannot accidentally downgrade the device.

The updater:

1. Queries the selected release channel from `dpolarov/KindleTeleSync-re`.
2. Detects the device ARM capability and selects the matching build, or the universal fallback.
3. Downloads `SHA256SUMS`.
4. Downloads the release archive with a size limit.
5. Verifies its SHA-256 digest.
6. Rejects path traversal and unsupported archive entries.
7. Preserves an existing user `config.json`.
8. Replaces installed files atomically.
9. Removes known files left by the older three-binary/shell-wrapper architecture.

An update is refused if `SHA256SUMS` is missing or the checksum does not match. SHA-256 provides release-asset integrity verification; it is not a separate publisher-signature system.

## Diagnostics

`kindletelesync diagnostics` checks, among other things:

- configuration validity;
- download-directory write access and resolved path confinement;
- configured safety limits;
- NTP clock skew;
- DNS availability;
- configuration file permissions;
- free storage space;
- detected GOARM target;
- Linux kernel release and the release-build kernel baseline;
- installed binary information.

`kindletelesync test` performs a real Telegram bot login, low-level ping and sends a test message to the configured chat.

## Logs

Runtime logs are stored in:

```text
/mnt/us/extensions/KindleTeleSync/sync.log
```

The Go executable rotates it at approximately 5 MiB to `sync.log.1`. The most recent structured command result is stored in `last_result.json` with private permissions.

## Building locally

Release compatibility is intentionally pinned to **Go 1.23.12**. Do not silently rebuild release binaries with a newer Go toolchain without re-evaluating the minimum Linux kernel required by that Go release.

Run checks:

```bash
GOTOOLCHAIN=local go mod tidy
gofmt -w cmd internal
go vet ./...
go test ./...
go test -race ./cmd/kindletelesync ./internal/config
```

Build for ARMv7:

```bash
GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -o kindletelesync ./cmd/kindletelesync
```

Build for ARMv6 by changing `GOARM=7` to `GOARM=6`.

GitHub Actions pins Go 1.23.12 with `GOTOOLCHAIN=local`, checks formatting and the tidy module graph, runs `go vet`, unit tests and race tests, then cross-builds both ARMv6 and ARMv7. Tagged `v*` releases publish stable builds. Branches named `rc/v*` publish GitHub prereleases for device testing. Both paths publish ARMv6, ARMv7 and universal archives plus `SHA256SUMS`. Release binaries are not UPX-packed.

## Project layout

```text
cmd/kindletelesync   unified Go application
internal/config      configuration and migration logic
build/package        KUAL and KOReader release package skeleton
```

## Credits

Original KindleTeleSync concept and package: **XroM**.

Reconstructed project: **antikuz**.

This fork and ongoing maintenance: **dpolarov**.

Thanks to the original testers and community members who documented the behavior of the lost-source version.
