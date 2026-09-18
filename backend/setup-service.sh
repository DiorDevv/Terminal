#!/usr/bin/env bash
set -e

pkill -9 -f "go run ./cmd/server" 2>/dev/null || true
pkill -9 -f "go-build.*/exe/server" 2>/dev/null || true

cp /home/dior/Projects/Loyihalar/terminal/backend/squidadmin-backend.service /etc/systemd/system/squidadmin-backend.service
cp /home/dior/Projects/Loyihalar/terminal/backend/squidadmin-sudoers /etc/sudoers.d/squidadmin
chmod 440 /etc/sudoers.d/squidadmin
visudo -c

systemctl daemon-reload
systemctl enable --now squidadmin-backend
sleep 2
systemctl status squidadmin-backend --no-pager
