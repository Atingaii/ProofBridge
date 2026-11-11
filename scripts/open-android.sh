#!/usr/bin/env bash
set -euo pipefail
repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
if command -v studio >/dev/null 2>&1; then
    exec studio "$repo_dir/android"
elif command -v studio.sh >/dev/null 2>&1; then
    exec studio.sh "$repo_dir/android"
else
    printf 'Open this project in Android Studio: %s/android\n' "$repo_dir"
fi
