#!/bin/sh
# Upload a file and print the link.
set -eu
FILE="${1:?usage: up FILE}"
if [ ! -f "$FILE" ]; then
  echo "missing: $FILE" >&2
  exit 1
fi
curl -sS -H "Authorization: Bearer $TOKEN" -F "file=@$FILE" https://snap.example.com/api/v1/items | jq -r '.url'
for i in 1 2 3; do echo $((i * 2)); done
