# ADR 028: Lightweight Android system-WebView host

Status: accepted on 2026-10-08.

## Context

The user explicitly requested renewed Android maintenance, low installation and
memory cost, full website functionality, and matching phone-browser adaptation.
They subsequently restricted scope to Android and phone presentation without
changing other logic.

At the start of this change,
`android/app/src/main/java/tech/sparkapi/codexbridge/MainActivity.java:MainActivity`
was an empty Capacitor activity. The frontend had no dedicated native
plugin dependency. `frontend/capacitor.config.ts:config` pointed to an old origin;
the installed Capacitor definitions mark server.url as a development live-reload
facility. Release shrinking was disabled and CI produced a debug APK.

## Decision

Replace the Android packaging host with a platform Activity and one system
WebView that opens https://codex.sparkon.cn. Keep application ID
tech.sparkapi.codexbridge and the current Android API compatibility boundary.
Do not embed the website, Chromium, a native recreation of its business UI, or
CLI credentials. Preserve the website's same-origin authentication and protocol.

Implement only the host facilities required for parity: keyboard/system insets,
web history/back, file selection, clipboard compatibility, external links,
supported downloads, document-load error recovery, and renderer teardown/recovery.
Do not add a general JavaScript-to-native bridge.

Enable release code/resource shrinking, version increments, sustainable signing,
signature/hash verification, and low-concurrency builds. Keep npm dependencies
that are unrelated to this Android packaging change; do not perform broad cleanup.

## Alternatives

- **Shrink the existing Capacitor shell:** lower migration effort, but preserves
  an unused generic plugin boundary and reliance on live-reload configuration.
- **TWA/browser hosting:** adds device-browser dependencies and changes control
  of the host; not needed for the specified same-origin client.
- **Reimplement all website features natively:** duplicates business behavior and
  creates long-term feature drift without evidence of necessary benefit.

## Consequences and limits

Android owns host compatibility checks previously delegated to Capacitor. The
shared frontend remains the feature owner. This decision does not change the Go
module/binary, SQLite, reverse Bridge, model settings, or orchestration execution.

Removing a shell dependency does not eliminate renderer memory. Measure the
release APK and device host/renderer footprint; do not infer memory savings from
dependency removal alone. Unknown old signing prevents a promised in-place
upgrade; never uninstall an existing app automatically.

## Validation and rollback

Follow [feature exit gates](../features/android-mobile-redesign.md). Keep the old
codex static root for rollback. Retain release signing material and deliverable
checksums; never include signing secrets in source or logs. Other sites, Hub,
database, and Bridge processes are outside deployment and rollback scope.

## Sources

- [Android WebView](https://developer.android.com/develop/ui/views/layout/webapps/webview)
- [WebView memory](https://developer.android.com/develop/ui/views/layout/webapps/manage-webview-memory)
- [Capacitor configuration](https://capacitorjs.com/docs/config)
- [Android signing](https://developer.android.com/studio/publish/app-signing)
