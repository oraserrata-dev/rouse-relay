#!/bin/bash
# Rouse Relay - macOS Installer
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
echo "  Rouse Relay - macOS Installer"
echo "  =============================="
echo ""

# Refuse to install with the placeholder (or an empty) token. Shipping the
# literal "YOUR_PASSWORD_HERE" would deploy a relay anyone can drive, since
# that value is public in this script.
if [ "$AUTH_TOKEN" = "YOUR_PASSWORD_HERE" ] || [ -z "$AUTH_TOKEN" ]; then
    echo "  ERROR: AUTH_TOKEN is still the placeholder."
    echo "  Edit this script and set AUTH_TOKEN to the token from the Rouse app"
    echo "  (Settings > Relay > Generate), then run the installer again."
    echo "  Refusing to deploy an unauthenticated relay."
    exit 1
fi

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

# Install plist with user's AUTH_TOKEN.
# Escape /, &, and \ in the token so passwords containing those characters
# don't break the sed substitution. Without this, a password like "a/b" is
# treated as a sed delimiter and corrupts the plist.
echo "  Installing launch agent..."
mkdir -p "$HOME/Library/LaunchAgents"
escaped_token=$(printf '%s\n' "$AUTH_TOKEN" | sed -e 's/[\\/&]/\\&/g')
sed "s/YOUR_PASSWORD_HERE/$escaped_token/g" "$PLIST_SRC" > "$PLIST_DST"
# The plist now holds the auth token in plaintext. Lock it down to the
# owner so other local users can't read it.
chmod 600 "$PLIST_DST"

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
