#!/usr/bin/env bash
set -euo pipefail
repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir/android"
nice -n 10 ionice -c 2 -n 7 ./gradlew --no-daemon --max-workers=1 \
    '-Dorg.gradle.jvmargs=-Xmx768m -XX:MaxMetaspaceSize=256m -XX:+UseSerialGC' \
    testReleaseUnitTest lintRelease
