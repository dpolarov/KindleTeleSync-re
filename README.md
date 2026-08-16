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
- Self-update from releases in this fork.
- SHA-256 verification before an update archive is installed.
- ARMv6 and ARMv7 release builds, plus a conservative universal archive.

## Requirements

- A jailbroken Kindle.
- KUAL and/or KOReader.
- Wi-Fi access.
- A Telegram bot token created with BotFather.
- The Telegram chat ID used with that bot.

The reconstructed application has primarily been tested on newer ARMv7 Kindles. Device-specific testing is still recommended, especially on older ARMv6 models.

## Installation

1. Open the latest GitHub release.
2. Download one archive:
   - `KindleTeleSync-arm7.tar.gz` — optimized for newer ARMv7 Kindles.
   - `KindleTeleSync-arm6.tar.gz` — optimized for older ARMv6 Kindles.
   - `KindleTeleSync-universal.tar.gz` — conservative ARMv6 build intended for maximum compatibility.
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

### Browser settings

KOReader and KUAL can start a temporary settings server on port `8880`.

The URL contains a random device-generated bearer token, for example:

```text
http://192.168.1.20:8880/?token=...
```

Secret fields are not pre-filled in the page. Leaving a secret field empty keeps its current value. The server automatically stops after the configured timeout and can also be stopped explicitly.

The settings server uses HTTP on the local network, not TLS, so only run it on a network you trust and treat the complete tokenized URL as sensitive while it is active.

## Safety limits

KindleTeleSync checks Telegram document metadata before downloading a file. By default it rejects work that would exceed:

- 100 MiB for one file.
- 20 downloaded files in one synchronization run.
- 250 MiB total downloaded data in one synchronization run.

Partial output is removed when a download fails. Telegram filenames are reduced to their base name before a destination path is created.

## Proxy support

Supported modes:

- `socks5`
- `http` using HTTP CONNECT
- `mtproto`

Telegram MTProto depends on a reasonably correct clock. KindleTeleSync tries NTP synchronization before synchronization and reports clock skew in diagnostics.

## Unified command line

The package contains one executable:

```text
kindletelesync sync [--json]
kindletelesync test [--json]
kindletelesync diagnostics [--json]
kindletelesync update [--json]
kindletelesync web [--kindle-ui]
kindletelesync web-url
kindletelesync web-stop [--json]
kindletelesync version [--json]
```

`--json` returns a stable result envelope used by the KOReader plugin, including downloaded files, skipped items, errors and diagnostic details.

## Updates

The updater:

1. Queries the latest release from `dpolarov/KindleTeleSync-re`.
2. Selects the ARM build matching the executable, or the universal fallback.
3. Downloads `SHA256SUMS`.
4. Downloads the release archive with a size limit.
5. Verifies its SHA-256 digest.
6. Rejects path traversal, symbolic links and unsupported archive entries.
7. Preserves an existing user `config.json`.
8. Replaces installed files atomically.

An update is refused if `SHA256SUMS` is missing or the checksum does not match.

## Diagnostics

`kindletelesync diagnostics` checks, among other things:

- configuration validity;
- download-directory write access;
- configured safety limits;
- NTP clock skew;
- DNS availability;
- configuration file permissions;
- free storage space;
- detected GOARM target and installed binary information.

`kindletelesync test` performs a real Telegram bot login, low-level ping and sends a test message to the configured chat.

## Logs

Runtime logs are stored in:

```text
/mnt/us/extensions/KindleTeleSync/sync.log
```

The Go executable rotates it at approximately 5 MiB to `sync.log.1`. The most recent structured command result is stored in `last_result.json` with private permissions.

## Building locally

The Go version is defined in `go.mod`.

Run checks:

```bash
gofmt -w cmd internal
go vet ./...
go test ./...
```

Build for ARMv7:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -o kindletelesync ./cmd/kindletelesync
```

Build for ARMv6 by changing `GOARM=7` to `GOARM=6`.

GitHub Actions runs host tests plus both ARM cross-builds on pull requests. Tagged releases build and compress both architectures and publish `SHA256SUMS` for the built-in updater.

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
