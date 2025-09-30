#!/usr/bin/env bash
set -e

echo "=== Independent 3x-ui Telegram Bot Installer ==="

read -p "Enter Panel URL (e.g. https://panel.example.com): " PANEL_URL
read -p "Enter Panel Username: " PANEL_USERNAME
read -s -p "Enter Panel Password: " PANEL_PASSWORD
echo
read -p "Enter Telegram Bot Token: " TELEGRAM_BOT_TOKEN
read -p "Enter Admin Telegram Chat ID (numeric): " ADMIN_CHAT_ID
read -p "Enter cron spec for periodic status (default: 0 * * * *): " CRON_SPEC
CRON_SPEC=${CRON_SPEC:-"0 * * * *"}

sudo apt update -y
sudo apt install -y git golang-go build-essential

INSTALL_DIR="/opt/threexui-bot"
sudo rm -rf $INSTALL_DIR
sudo mkdir -p $INSTALL_DIR
sudo chown $USER:$USER $INSTALL_DIR
cd $INSTALL_DIR

cp ~/threexui-bot/main.go ./main.go
cp ~/threexui-bot/go.mod ./go.mod

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

go mod tidy
go build -o threexui-bot main.go

sudo cp threexui-bot /usr/local/bin/threexui-bot
sudo cp config.json /etc/threexui-bot-config.json

SERVICE_FILE="/etc/systemd/system/threexui-bot.service"
sudo bash -c "cat > $SERVICE_FILE" <<EOL
[Unit]
Description=Independent 3x-ui Telegram Bot
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/threexui-bot /etc/threexui-bot-config.json
WorkingDirectory=/opt/threexui-bot
Restart=always
RestartSec=5
User=$USER

[Install]
WantedBy=multi-user.target
EOL

sudo systemctl daemon-reload
sudo systemctl enable threexui-bot
sudo systemctl restart threexui-bot

echo "=== Installation complete! ==="
echo "Check logs with: sudo journalctl -u threexui-bot -f"
