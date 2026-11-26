#!/usr/bin/env bash
# Starts KaraokeDock. Extra arguments go to the server, such as --port 7465.
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
exec "$root/bin/karaokedock" --metadata "$root/data/metadata" --sidecar "$root/sidecar" "$@"
