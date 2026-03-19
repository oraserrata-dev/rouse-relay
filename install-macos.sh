#!/bin/bash
# Rouse Relay — macOS Installer
#
# Usage:
#   1. Edit AUTH_TOKEN below
#   2. Run: chmod +x install-macos.sh && ./install-macos.sh

set -e

AUTH_TOKEN="YOUR_PASSWORD_HERE"
PORT="9876"

BINARY="rouse-relay"
INSTALL_DIR="/usr/local/bin"
PLIST_NAME="com.oraserrata.rouse-relay.plist"
PLIST_SRC="$(dirname "$0")/$PLIST_NAME"
PLIST_DST="$HOME/Library/LaunchAgents/$PLIST_NAME"
LOG_DIR="/usr/local/var/log"

echo ""
echo "  Rouse Relay — macOS Installer"
echo "  =============================="
echo ""

# Check binary exists
if [ ! -f "$(dirname "$0")/$BINARY" ]; then
    echo "  ERROR: $BINARY not found in the same folder as this script."
    exit 1
fi

# Check plist exists
if [ ! -f "$PLIST_SRC" ]; then
    echo "  ERROR: $PLIST_NAME not found in the same folder as this script."
    exit 1
fi

# Stop existing service if running
if launchctl list | grep -q "com.oraserrata.rouse-relay"; then
    echo "  Stopping existing Rouse Relay service..."
    launchctl unload "$PLIST_DST" 2>/dev/null || true
fi

# Install binary
echo "  Installing binary to $INSTALL_DIR..."
mkdir -p "$INSTALL_DIR"
cp "$(dirname "$0")/$BINARY" "$INSTALL_DIR/$BINARY"
chmod +x "$INSTALL_DIR/$BINARY"

# Create log directory
mkdir -p "$LOG_DIR"

# Install plist with user's AUTH_TOKEN
echo "  Installing launch agent..."
mkdir -p "$HOME/Library/LaunchAgents"
sed "s/YOUR_PASSWORD_HERE/$AUTH_TOKEN/g" "$PLIST_SRC" > "$PLIST_DST"

# Load and start
echo "  Starting Rouse Relay..."
launchctl load "$PLIST_DST"

echo ""
echo "  Done! Rouse Relay is running on port $PORT."
echo "  Logs: $LOG_DIR/rouse-relay.log"
echo ""
echo "  To stop:    launchctl unload $PLIST_DST"
echo "  To restart: launchctl unload $PLIST_DST && launchctl load $PLIST_DST"
echo "  To remove:  launchctl unload $PLIST_DST && rm $PLIST_DST $INSTALL_DIR/$BINARY"
echo ""
