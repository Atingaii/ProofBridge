package tech.sparkapi.codexbridge;

import android.content.Context;
import android.net.Uri;

import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;

/** Streams into a user-chosen SAF document. Redirects cannot carry the login cookie to another origin. */
final class NativeDownload {
    private final Context context;
    private final String url;
    private final String userAgent;
    private final String cookie;
    private final Uri destination;
    private volatile HttpURLConnection connection;
    private volatile boolean cancelled;

    NativeDownload(Context context, String url, String userAgent, String cookie, Uri destination) {
        this.context = context;
        this.url = url;
        this.userAgent = userAgent;
        this.cookie = cookie;
        this.destination = destination;
    }

    void cancel() {
        cancelled = true;
        HttpURLConnection active = connection;
        if (active != null) active.disconnect();
    }

    boolean save() {
        try {
            URL next = new URL(url);
            for (int redirects = 0; redirects <= 5 && !cancelled; redirects++) {
                if (!"https".equalsIgnoreCase(next.getProtocol()) || next.getUserInfo() != null) return false;
                HttpURLConnection active = (HttpURLConnection) next.openConnection();
                connection = active;
                if (cancelled) return false;
                active.setInstanceFollowRedirects(false);
                active.setConnectTimeout(15000);
                active.setReadTimeout(20000);
                if (userAgent != null) active.setRequestProperty("User-Agent", userAgent);
                if (cookie != null && NavigationPolicy.isFirstParty(next.toString())) active.setRequestProperty("Cookie", cookie);
                int status = active.getResponseCode();
                if (status == 301 || status == 302 || status == 303 || status == 307 || status == 308) {
                    String location = active.getHeaderField("Location");
                    active.disconnect();
                    if (location == null) return false;
                    next = new URL(next, location);
                    continue;
                }
                if (status < 200 || status >= 300 || cancelled) return false;
                try (InputStream input = active.getInputStream();
                     OutputStream output = context.getContentResolver().openOutputStream(destination, "wt")) {
                    if (output == null) return false;
                    byte[] buffer = new byte[32768];
                    int count;
                    while (!cancelled && (count = input.read(buffer)) != -1) output.write(buffer, 0, count);
                    return !cancelled;
                }
            }
        } catch (Exception ignored) {
            // Do not log download URLs, login cookies or document-provider paths.
            return false;
        } finally {
            HttpURLConnection active = connection;
            connection = null;
            if (active != null) active.disconnect();
        }
        return false;
    }
}
