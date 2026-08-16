# KindleTeleSync

KindleTeleSync downloads books and documents from a Telegram bot directly to a jailbroken Amazon Kindle.

This repository is a maintained reconstruction of the original KindleTeleSync project. The original application source was lost; the remaining KUAL/KOReader package was used as the behavioral reference. This fork modernizes the reconstructed Go code, removes unnecessary shell wrappers, adds a KOReader menu, and keeps the user-facing interface in English.

## Features

- Downloads supported document formats from a configured Telegram chat.
- Remembers Telegram update state so old messages are not downloaded again.
- Supports MTProto, SOCKS5, and HTTP CONNECT proxies.
- Includes browser-based configuration protected by a device-generated access token.
- Includes an updater that downloads releases from this fork.
- Integrates with both KUAL and KOReader.
- Builds static ARMv6 and ARMv7 binaries with GitHub Actions.

## Requirements

- A jailbroken Kindle.
- KUAL and/or KOReader.
- Wi-Fi access.
- A Telegram bot token created with BotFather.
- A Telegram chat ID for the conversation with the bot.

The reconstructed application has primarily been tested on newer ARMv7 Kindles. Older ARMv6 builds are produced automatically, but device-specific testing is still recommended.

## Installation

1. Open the latest GitHub release.
2. Download the archive for your Kindle CPU:
   - `KindleTeleSync-arm7.tar.gz` for most newer jailbroken Kindles.
   - `KindleTeleSync-arm6.tar.gz` for older ARMv6 models.
3. Extract the archive into `/mnt/us` on the Kindle.
4. Restart KUAL or KOReader if the new menu entry does not appear immediately.

The package installs the KUAL extension under:

```text
/mnt/us/extensions/KindleTeleSync
```

and the KOReader plugin under:

```text
/mnt/us/koreader/plugins/KindleTeleSync.koplugin
```

## Configuration

### From KOReader

Open:

```text
Main menu -> KindleTeleSync -> Open web settings
```

KOReader displays a QR code and a temporary URL. Open that URL from a phone or computer connected to the same Wi-Fi network.

### From KUAL

Open:

```text
KindleTeleSync -> Open web settings
```

The Go web-settings process displays the URL/QR code on the Kindle screen and automatically exits after a short period.

The settings page lets you configure:

- Telegram bot token.
- Chat ID.
- Allowed file extensions.
- Download directory.
- Proxy type and credentials.

The web settings URL contains a random device-generated access token. Treat the complete URL as temporary sensitive information while the server is running.

## Usage

### Synchronize now

In KUAL or KOReader select **Sync now**. On the first successful connection KindleTeleSync stores the current Telegram update state and intentionally skips older messages. Send a new supported file to the bot and run synchronization again.

### Update

Select **Check for updates**. The updater checks releases from `dpolarov/KindleTeleSync-re`, downloads the matching ARM package, keeps an existing `config.json`, and replaces application files atomically.

## Configuration file

The default configuration is stored at:

```text
/mnt/us/extensions/KindleTeleSync/config.json
```

Example:

```json
{
  "bot_token": "123456789:AA...",
  "chat_id": 123456789,
  "allowed_extensions": [".epub", ".mobi", ".pdf", ".zip", ".fb2"],
  "download_path": "/mnt/us/books",
  "proxy": {
    "enabled": false,
    "type": "socks5",
    "address": "",
    "username": "",
    "password": "",
    "mtproto_secret": ""
  }
}
```

KindleTeleSync saves the configuration atomically and restricts the file permissions because it contains the Telegram token and optional proxy credentials.

## Proxy support

Supported proxy modes:

- `socks5`
- `http` using HTTP CONNECT
- `mtproto`

Telegram MTProto is sensitive to the device clock. KindleTeleSync attempts NTP synchronization before opening the Telegram session. If all configured NTP servers fail, it continues with the Kindle system time and logs the failure.

## Building locally

Go 1.25 or the version specified by `go.mod` is required.

Run tests:

```bash
go test ./...
go vet ./...
```

Build for a typical ARMv7 Kindle:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -ldflags="-s -w" -o kindle_sync_d ./cmd/kindle_sync_d
```

The release workflow also builds `updater` and `webconfig` for ARMv6 and ARMv7.

## Project layout

```text
cmd/kindle_sync_d   Telegram synchronization backend
cmd/updater         GitHub release updater
cmd/webconfig       Protected web configuration server and Kindle UI launcher
internal/config     Shared configuration handling
build/package       KUAL and KOReader release package skeleton
```

The KOReader plugin intentionally remains Lua because KOReader plugins are Lua modules. It is kept as a thin UI adapter; synchronization, update handling, web configuration, QR generation for KUAL, networking, and persistent configuration are implemented in Go.

## Security notes

- `config.json` contains secrets and is saved with mode `0600`.
- The browser configuration endpoint requires a random token embedded in the temporary URL.
- Downloaded document filenames are reduced to their base name before writing to the configured directory.
- The updater rejects archive paths that escape the extraction directory and installs files through atomic replacement.

## Credits

Original KindleTeleSync concept and package: **XroM**.

Reconstructed project: **antikuz**.

This fork and ongoing maintenance: **dpolarov**.

Thanks to the original testers and community members who documented the behavior of the lost-source version.
