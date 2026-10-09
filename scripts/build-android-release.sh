#!/usr/bin/env bash
# Produce a signed online-host release; never run Vite or Capacitor sync here.
set -euo pipefail
repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
: "${JAVA_HOME:?Set JAVA_HOME to JDK 21}"
: "${ANDROID_HOME:?Set ANDROID_HOME to the minimal Android SDK}"
: "${ANDROID_SIGNING_PROPERTIES:?Set ANDROID_SIGNING_PROPERTIES to a private signing properties file}"
[[ -r "$ANDROID_SIGNING_PROPERTIES" ]] || { echo 'Signing properties are not readable' >&2; exit 1; }
[[ -x "$JAVA_HOME/bin/java" ]] || { echo 'JAVA_HOME has no Java executable' >&2; exit 1; }
android_apksigner="$ANDROID_HOME/build-tools/35.0.0/apksigner"
[[ -x "$android_apksigner" ]] || { echo 'Install Android build-tools 35.0.0' >&2; exit 1; }
# This limit is a heap budget, not the total JVM/tool-process footprint.
cd "$repo_dir/android"
nice -n 10 ionice -c 2 -n 7 ./gradlew --no-daemon --max-workers=1 \
    '-Dorg.gradle.jvmargs=-Xmx768m -XX:MaxMetaspaceSize=256m -XX:+UseSerialGC' \
    testReleaseUnitTest lintRelease assembleRelease
android_apk="$repo_dir/android/app/build/outputs/apk/release/app-release.apk"
[[ -f "$android_apk" ]] || { echo 'Signed release APK missing (unsigned output is not a release)' >&2; exit 1; }
"$android_apksigner" verify --verbose --print-certs "$android_apk"
python3 "$repo_dir/scripts/check-android-apk.py" "$android_apk"
sha256sum "$android_apk" > "$android_apk.sha256"
printf 'Release APK: %s\n' "$android_apk"
