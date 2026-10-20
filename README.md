# Rouse Relay Server

A lightweight relay server for [Rouse](https://oraserrata.net) that receives authenticated HTTP requests and broadcasts Wake-on-LAN magic packets on the local network.

## Quick Start

Pick one. They all do the same job, so pick whichever matches your always-on device.

### Docker (NAS, Raspberry Pi, any Linux box)

```bash
docker run -d \
    --name rouse-relay \
    --network host \
    --restart always \
    -e AUTH_TOKEN=your-password-here \
    oraserrata/rouse-relay:latest
```

Or with Docker Compose:

```yaml
services:
  rouse-relay:
    image: oraserrata/rouse-relay:latest
    container_name: rouse-relay
    restart: always
    network_mode: host
    environment:
      - AUTH_TOKEN=your-password-here
```

### Standalone download (Mac, Windows, Linux)

Visit [oraserrata.net/relay](https://oraserrata.net/relay) and grab the zip for your platform. Each zip contains the relay binary plus a per-OS install script that registers the relay as a system service:

- **macOS**: `install-macos.sh` registers a `LaunchAgent` that starts at login
- **Linux**: `install-linux.sh` registers a `systemd` service running as `DynamicUser`
- **Windows**: `install-windows.bat` registers a Scheduled Task that runs at boot (no NSSM required)

Edit `AUTH_TOKEN` at the top of the install script before running it.

### Mac running Rouse from the App Store

Open Rouse → Settings → "Run Rouse as a Relay." The macOS app embeds the same relay protocol directly. No separate download.

## Waking from away: Tailscale subnet routing

To wake a computer when you are away from home, your iPhone, iPad or Mac has to reach the relay. If you use [Tailscale](https://tailscale.com), install it on the relay host and on each device you wake from, then enter the relay's Tailscale address (the `100.x.y.z` one) in Rouse as its VPN address. That is enough to wake.

We also recommend making the relay host a Tailscale **subnet router**. It then carries your whole home network over Tailscale, so after a computer wakes you can connect to it (Remote Desktop, SSH, file sharing) at its normal home address, without installing Tailscale on it. A Mac with Apple silicon is different: a magic packet only wakes it partway, with the network on and the display off, for a minute or two. Then it goes back to sleep, so a remote session may drop.

You can't count on reaching a sleeping computer over its own Tailscale connection. It is usually offline while it sleeps. A Mac may answer for less than a minute during a brief background wake and then sleep again, and that answer does not mean it has fully woken. The relay host is the one that stays awake, so it is the dependable way in.

### On a Raspberry Pi or another Linux computer

With Tailscale installed as a normal package:

```bash
# 1. Let the host forward traffic for the home network
echo 'net.ipv4.ip_forward = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
echo 'net.ipv6.conf.all.forwarding = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
sudo sysctl -p /etc/sysctl.d/99-tailscale.conf

# 2. Advertise your home subnet (replace with yours)
sudo tailscale set --advertise-routes=192.168.1.0/24
```

### On a Synology NAS

Tailscale's package from Package Center routes the subnet itself, so skip step 1. Add your home subnet in the Tailscale package's web page (Settings, Subnet router, then Advertise routes), or over SSH with `sudo tailscale set --advertise-routes=192.168.1.0/24`. If you turned on TUN for Tailscale and the DSM firewall is on, add a firewall rule that allows `100.64.0.0/10`. Through a Synology subnet route, home computers may not answer ping, but connections such as Remote Desktop, SSH and file sharing still work. Tailscale's guide: [Synology](https://tailscale.com/kb/1131/synology).

### Then, on either

3. In the Tailscale admin console, open the **Machines** page and find the relay host (it shows a Subnets badge). Open its menu (the three dots at the end of its row), choose **Edit subnet routes**, tick the route you advertised, and select **Save**.
4. From the same menu, choose **Disable Key Expiry**. Otherwise the relay's Tailscale sign-in expires (after 180 days by default), and you cannot reach it from away until it signs in again, which usually means doing it at home.
5. On the devices you wake from, make sure they use subnet routes. The Tailscale apps for iPhone, iPad and Mac do this by default. A Linux client needs `sudo tailscale set --accept-routes`.

Rouse keeps using the relay's `100.x.y.z` address. Nothing changes for the devices you wake.

Good to know:

- The relay host must stay on and awake. The computers it wakes can sleep.
- If the network you are visiting uses the same address range as your home (for example, both are `192.168.1.x`), you may not be able to reach home computers at their home addresses while you are there. Waking still works, because Rouse falls back to the relay's `100.x.y.z` address. Moving your home network to a less common range, such as `192.168.47.0/24`, makes a clash unlikely. If you do, advertise and approve the new range in Tailscale, and in Rouse update the relay's local address and any broadcast address you set to the old range.
- Tailscale's guide covers other systems and options: [Subnet routers](https://tailscale.com/kb/1019/subnets).

## Configuration

All configuration is via environment variables:

| Variable     | Default     | Description                                                |
| ------------ | ----------- | ---------------------------------------------------------- |
| `AUTH_TOKEN` | *(empty)*   | Shared secret for authentication. Strongly recommended.    |
| `PORT`       | `9876`      | Port to listen on                                          |
| `HOST`       | `0.0.0.0`   | Host/IP to bind to                                         |

## API

### `GET /health`

Unauthenticated reachability probe. Used by Rouse to show "Relay Up" / "Relay Down" status. Always returns 200 if the relay is reachable.

**Response:**

```json
{
    "status": "ok",
    "service": "rouse-relay",
    "version": "1.0.0",
    "auth_required": true
}
```

### `GET /verify`

Authenticated connection test. Used by the "Test Connection" button in Rouse on macOS, iPad, and iPhone to confirm the AUTH_TOKEN is correct (not just that the relay is reachable).

**Headers:** `Authorization: Bearer {AUTH_TOKEN}`

**Response (200, good token):**

```json
{ "status": "ok", "auth": "valid" }
```

**Response (401, missing or wrong token):**

```json
{ "error": "Unauthorized" }
```

### `POST /wake`

Send a Wake-on-LAN magic packet on the local network. Authenticated.

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
{ "success": true, "mac": "AA:BB:CC:DD:EE:FF", "broadcast": "255.255.255.255", "port": 9 }
```

## Building from Source

Requires Go 1.22+.

```bash
# Build for current platform
go build -ldflags="-s -w -X main.version=1.0.0" -o rouse-relay .

# Cross-compile every platform's raw binary
make all

# Cross-compile AND assemble the GitHub Releases zips
# (binary + install script + service file per platform)
make release

# Build the Docker image
make docker
```

`make release` produces these archives in `build/release/`:

- `RouseRelay-macOS-arm64.zip`: Apple Silicon Mac
- `RouseRelay-macOS-amd64.zip`: Intel Mac
- `RouseRelay-linux-amd64.zip`
- `RouseRelay-linux-arm64.zip`
- `RouseRelay-windows-amd64.zip`

## License

© 2026 Ora Serrata LLC. All rights reserved.
