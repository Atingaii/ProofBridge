package tech.sparkapi.codexbridge;

import java.net.URI;
import java.net.URISyntaxException;

/** Exact origin boundary shared by navigation, recovery and authenticated downloads. */
final class NavigationPolicy {
    static final String HOME = "https://codex.sparkon.cn/";

    private NavigationPolicy() {}

    static boolean isFirstParty(String url) {
        try {
            URI uri = new URI(url);
            return "https".equalsIgnoreCase(uri.getScheme())
                    && "codex.sparkon.cn".equalsIgnoreCase(uri.getHost())
                    && (uri.getPort() == -1 || uri.getPort() == 443)
                    && uri.getRawUserInfo() == null;
        } catch (URISyntaxException | NullPointerException e) {
            return false;
        }
    }

    static boolean isExternal(String url) {
        try {
            URI uri = new URI(url);
            String scheme = uri.getScheme();
            if ("https".equalsIgnoreCase(scheme) || "http".equalsIgnoreCase(scheme)) {
                return uri.getHost() != null && uri.getRawUserInfo() == null;
            }
            return "mailto".equalsIgnoreCase(scheme) || "tel".equalsIgnoreCase(scheme);
        } catch (URISyntaxException | NullPointerException e) {
            return false;
        }
    }
}
