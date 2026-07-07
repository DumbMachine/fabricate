#!/bin/sh
# Bootstrap script for the ops-host profile. Runs once at container
# create time, as root, via `docker exec`. Keep it POSIX-sh-clean —
# the linuxserver/openssh-server image is Alpine, no bash by default.
set -eu

mkdir -p /var/log/app
mkdir -p /etc/systemd-style
mkdir -p /run

# Fake "wedged service" — a pidfile pointing at a PID that no longer
# exists. Common real-world failure mode: process died, init system
# didn't notice, restart attempts blocked by `if [ -f /run/api.pid ]`.
echo 31337 > /run/api.pid

# Drop a placeholder unit file so the agent has something to grep.
cat > /etc/systemd-style/api.service <<'UNIT'
[Unit]
Description=Fictional API service
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/api --listen 0.0.0.0:8080
Restart=on-failure
PIDFile=/run/api.pid

[Install]
WantedBy=multi-user.target
UNIT

chmod 0644 /etc/systemd-style/api.service
chmod 0644 /run/api.pid
