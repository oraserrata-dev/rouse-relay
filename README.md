# Rouse Relay Server

A lightweight relay server for [Rouse](https://oraserrata.net) that receives authenticated HTTP requests and broadcasts Wake-on-LAN magic packets on the local network.

## Quick Start (Docker)

```bash
docker run -d \
    --name rouse-relay \
    --network host \
    --restart unless-stopped \
    -e AUTH_TOKEN=your-password-here \
    oraserrata/rouse-relay:latest
```

Or with Docker Compose:

```yaml
services:
  rouse-relay:
    image: oraserrata/rouse-relay:latest
    container_name: rouse-relay
    restart: unless-stopped
    network_mode: host
    environment:
      - AUTH_TOKEN=your-password-here
```

## Quick Start (Native Binary)

Download the binary for your platform from the [releases page], then:

```bash
# macOS / Linux
chmod +x rouse-relay
AUTH_TOKEN=your-password-here ./rouse-relay

# Windows (PowerShell)
$env:AUTH_TOKEN = "your-password-here"
.\rouse-relay.exe
```

## Configuration

All configuration is via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `AUTH_TOKEN` | *(empty)* | Shared secret for authentication. Strongly recommended. |
| `PORT` | `9876` | Port to listen on |
| `HOST` | `0.0.0.0` | Host/IP to bind to |

## API

### GET /health

Unauthenticated health check. Used by Rouse to show "Relay Up" / "Relay Down" status.

**Response:**
```json
{"status": "ok", "service": "rouse-relay", "auth_required": true}
```

### GET /verify

Authenticated connection test. Used by the "Test Connection" button during relay setup.

**Headers:** `Authorization: Bearer {AUTH_TOKEN}`

**Response (200):**
```json
{"status": "ok", "auth": "valid"}
```

**Response (401):**
```json
{"error": "Unauthorized"}
```

### POST /wake

Send a Wake-on-LAN magic packet. Authenticated.

**Headers:** `Authorization: Bearer {AUTH_TOKEN}`, `Content-Type: application/json`

**Body:**
```json
{
    "mac": "AA:BB:CC:DD:EE:FF",
    "broadcast": "255.255.255.255",
    "port": 9,
    "secure_on": "11:22:33:44:55:66"
}
```

- `mac` (required): Target device MAC address
- `broadcast` (optional, default `255.255.255.255`): Broadcast address
- `port` (optional, default `9`): WoL port
- `secure_on` (optional): SecureON password in MAC format (6 hex bytes separated by colons or dashes)

**Response (200):**
```json
{"success": true, "mac": "AA:BB:CC:DD:EE:FF", "broadcast": "255.255.255.255", "port": 9}
```

## Building from Source

Requires Go 1.22+.

```bash
# Build for current platform
go build -ldflags="-s -w" -o rouse-relay .

# Cross-compile all platforms
make all

# Build Docker image
make docker
```

The Makefile produces binaries for:
- macOS (Apple Silicon + Intel)
- Linux (amd64 + arm64)
- Windows (amd64)

## Running as a System Service

### macOS (launchd)

```bash
cp rouse-relay /usr/local/bin/
# Then install the provided com.oraserrata.rouse-relay.plist to ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.oraserrata.rouse-relay.plist
```

### Linux (systemd)

```bash
cp rouse-relay /usr/local/bin/
# Then install the provided rouse-relay.service to /etc/systemd/system/
systemctl enable --now rouse-relay
```

### Windows

```powershell
# Use NSSM or WinSW to install as a Windows Service
nssm install RouseRelay "C:\Program Files\Rouse\rouse-relay.exe"
nssm set RouseRelay AppEnvironmentExtra AUTH_TOKEN=your-password-here
nssm start RouseRelay
```

## License

© 2026 Ora Serrata LLC. All rights reserved.
