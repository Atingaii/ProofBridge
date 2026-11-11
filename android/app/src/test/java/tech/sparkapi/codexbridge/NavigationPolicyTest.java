package tech.sparkapi.codexbridge;

import org.junit.Test;
import static org.junit.Assert.*;

public class NavigationPolicyTest {
    @Test public void acceptsOnlyTheHttpsProductionOrigin() {
        assertTrue(NavigationPolicy.isFirstParty("https://codex.sparkon.cn/orchestration?runId=existing"));
        assertTrue(NavigationPolicy.isFirstParty("HTTPS://CODEX.SPARKON.CN:443/settings"));
        for (String url : new String[] {null, "", "not a uri", "http://codex.sparkon.cn/",
                "https://codex.sparkon.cn:444/", "https://codex.sparkon.cn.evil.example/",
                "https://evil.example/codex.sparkon.cn", "https://codex.sparkon.cn@evil.example/",
                "https://user@codex.sparkon.cn/", "file:///etc/passwd", "javascript:alert(1)",
                "https://codex.sparkon.cn./", "//codex.sparkon.cn/"}) {
            assertFalse("Unexpected first-party URL: " + url, NavigationPolicy.isFirstParty(url));
        }
    }

    @Test public void externalHandlersAreExplicitlyBounded() {
        assertTrue(NavigationPolicy.isExternal("https://developer.android.com/"));
        assertTrue(NavigationPolicy.isExternal("mailto:support@example.com"));
        assertTrue(NavigationPolicy.isExternal("tel:+86123456"));
        for (String url : new String[] {"intent://example", "javascript:alert(1)", "file:///tmp",
                "content://private/data", "data:text/html,x", "blob:https://codex.sparkon.cn/id",
                "https://user:secret@example.com", null}) {
            assertFalse(NavigationPolicy.isExternal(url));
        }
    }
}
