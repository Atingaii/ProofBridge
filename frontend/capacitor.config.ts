/** @deprecated Historical Capacitor tooling only. Production uses the platform
 * Android WebView host (ADR 028); do not run cap sync for release builds. */
import type { CapacitorConfig } from '@capacitor/cli';

const config: CapacitorConfig = {
  appId: 'tech.sparkapi.codexbridge',
  appName: 'Codex Bridge',
  webDir: '../internal/web/static',
  server: {
    url: 'https://codex.sparkon.cn',
    cleartext: false,
  },
  android: {
    path: '../android',
  },
};

export default config;
