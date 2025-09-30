#!/usr/bin/env bash
set -e

echo "=== Independent 3x-ui Telegram Bot Installer ==="

# --- Ask user for input ---
read -p "Enter Panel URL (e.g. https://panel.example.com): " PANEL_URL
read -p "Enter Panel Username: " PANEL_USERNAME
read -s -p "Enter Panel Password: " PANEL_PASSWORD
echo
read -p "Enter Telegram Bot Token: " TELEGRAM_BOT_TOKEN
read -p "Enter Admin Telegram Chat ID (numeric): " ADMIN_CHAT_ID
read -p "Enter cron spec for periodic status (default: 0 * * * *): " CRON_SPEC
CRON_SPEC=${CRON_SPEC:-"0 * * * *"}

# --- Install dependencies ---
echo "[*] Installing dependencies..."
sudo apt update -y
sudo apt install -y git golang-go build-essential -y

# --- Prepare install dir ---
INSTALL_DIR="/opt/3x-ui/bot-controler"
sudo rm -rf $INSTALL_DIR
sudo mkdir -p $INSTALL_DIR
sudo chown $USER:$USER $INSTALL_DIR
cd $INSTALL_DIR

# --- Copy project files (assuming install.sh is in the repo root) ---
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cp $SCRIPT_DIR/main.go ./main.go
cp $SCRIPT_DIR/go.mod ./go.mod

# --- Create config.json ---
cat > config.json <<EOF
{
  "panel_url": "$PANEL_URL",
  "username": "$PANEL_USERNAME",
  "password": "$PANEL_PASSWORD",
  "telegram_bot_token": "$TELEGRAM_BOT_TOKEN",
  "admin_chat_ids": [$ADMIN_CHAT_ID],
  "cron_spec": "$CRON_SPEC",
  "insecure_skip_verify": false,
  "request_timeout_seconds": 15
}
EOF

echo "[*] Config file created at $INSTALL_DIR/config.json"

# --- Build the bot ---
echo "[*] Building bot binary..."
go mod tidy
go build -o threexui-bot main.go

# --- Install binary + config ---
sudo cp threexui-bot /usr/local/bin/threexui-bot
sudo cp config.json /etc/threexui-bot-config.json

# --- Create systemd service ---
SERVICE_FILE="/etc/systemd/system/threexui-bot.service"
sudo bash -c "cat > $SERVICE_FILE" <<EOL
[Unit]
Description=Independent 3x-ui Telegram Bot
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/threexui-bot /etc/threexui-bot-config.json
WorkingDirectory=$INSTALL_DIR
Restart=always
RestartSec=5
User=$USER

[Install]
WantedBy=multi-user.target
EOL

# --- Enable and start service ---
echo "[*] Enabling and starting service..."
sudo systemctl daemon-reload
sudo systemctl enable threexui-bot
sudo systemctl restart threexui-bot

echo "=== Installation complete! ==="
echo "Check logs with:"
echo "  sudo journalctl -u threexui-bot -f"
