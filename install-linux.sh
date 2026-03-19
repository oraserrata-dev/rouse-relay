#!/bin/bash
# Rouse Relay — Linux Installer
#
# Usage:
#   1. Edit AUTH_TOKEN below
#   2. Run: chmod +x install-linux.sh && sudo ./install-linux.sh

set -e

AUTH_TOKEN="YOUR_PASSWORD_HERE"
PORT="9876"

BINARY="rouse-relay"
INSTALL_DIR="/usr/local/bin"
SERVICE_NAME="rouse-relay"
SERVICE_SRC="$(dirname "$0")/$SERVICE_NAME.service"
SERVICE_DST="/etc/systemd/system/$SERVICE_NAME.service"

echo ""
echo "  Rouse Relay — Linux Installer"
echo "  =============================="
echo ""

# Check for root
if [ "$(id -u)" -ne 0 ]; then
    echo "  ERROR: This script must be run as root (use sudo)."
    exit 1
fi

# Check binary exists
if [ ! -f "$(dirname "$0")/$BINARY" ]; then
    echo "  ERROR: $BINARY not found in the same folder as this script."
    exit 1
fi

# Check service file exists
if [ ! -f "$SERVICE_SRC" ]; then
    echo "  ERROR: $SERVICE_NAME.service not found in the same folder as this script."
    exit 1
fi

# Stop existing service if running
if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
    echo "  Stopping existing Rouse Relay service..."
    systemctl stop "$SERVICE_NAME"
fi

# Install binary
echo "  Installing binary to $INSTALL_DIR..."
cp "$(dirname "$0")/$BINARY" "$INSTALL_DIR/$BINARY"
chmod +x "$INSTALL_DIR/$BINARY"

# Install service file with user's AUTH_TOKEN
echo "  Installing systemd service..."
sed "s/YOUR_PASSWORD_HERE/$AUTH_TOKEN/g" "$SERVICE_SRC" > "$SERVICE_DST"

# Enable and start
echo "  Starting Rouse Relay..."
systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl start "$SERVICE_NAME"

echo ""
echo "  Done! Rouse Relay is running on port $PORT."
echo "  Logs: journalctl -u $SERVICE_NAME -f"
echo ""
echo "  To stop:    sudo systemctl stop $SERVICE_NAME"
echo "  To restart: sudo systemctl restart $SERVICE_NAME"
echo "  To remove:  sudo systemctl disable $SERVICE_NAME && sudo rm $SERVICE_DST $INSTALL_DIR/$BINARY"
echo ""
