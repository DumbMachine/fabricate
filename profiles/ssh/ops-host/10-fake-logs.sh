#!/bin/sh
# Generate a synthetic nginx-style access log with a deliberate 5xx
# burst near the end. The seed runs once at create time.
set -eu

LOG=/var/log/app/api.log
mkdir -p /var/log/app
: > "$LOG"

# 5000 mostly-200 lines, scattered 404s, then a tail-end 5xx burst.
i=1
while [ "$i" -le 5000 ]; do
  if [ "$i" -gt 4800 ] && [ "$(( i % 3 ))" -eq 0 ]; then
    status=503
  elif [ "$(( i % 47 ))" -eq 0 ]; then
    status=404
  else
    status=200
  fi
  printf '10.0.0.%d - - [01/Jun/2024:%02d:%02d:%02d +0000] "GET /api/v1/items HTTP/1.1" %d %d\n' \
    "$(( i % 254 + 1 ))" \
    "$(( i / 360 % 24 ))" "$(( i / 60 % 60 ))" "$(( i % 60 ))" \
    "$status" \
    "$(( 200 + i % 800 ))" >> "$LOG"
  i=$(( i + 1 ))
done

chmod 0644 "$LOG"
