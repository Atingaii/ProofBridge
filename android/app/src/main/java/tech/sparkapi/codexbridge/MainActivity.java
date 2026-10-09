package tech.sparkapi.codexbridge;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.content.res.Configuration;
import android.graphics.Color;
import android.net.Uri;
import android.net.http.SslError;
import android.os.Build;
import android.os.Bundle;
import android.view.Gravity;
import android.view.View;
import android.view.WindowInsets;
import android.view.inputmethod.InputMethodManager;
import android.webkit.CookieManager;
import android.webkit.RenderProcessGoneDetail;
import android.webkit.SslErrorHandler;
import android.webkit.URLUtil;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Button;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;
import android.window.OnBackInvokedCallback;
import android.window.OnBackInvokedDispatcher;

import java.util.ArrayList;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** Online presentation host. Business state, authentication and execution stay on the website. */
public final class MainActivity extends Activity {
    private static final int FIRST_FILE_PICKER = 100;
    private static final int SAVE_DOWNLOAD = 42;
    private FrameLayout root;
    private LinearLayout errorSurface;
    private WebView webView;
    private ValueCallback<Uri[]> fileCallback;
    private int nextFileRequest = FIRST_FILE_PICKER;
    private int activeFileRequest = -1;
    private String currentPage = NavigationPolicy.HOME;
    private boolean documentFailed;
    private boolean handlingBack;
    private PendingDownload pendingDownload;
    private NativeDownload activeDownload;
    private final ExecutorService downloadExecutor = Executors.newSingleThreadExecutor();
    private OnBackInvokedCallback backCallback;

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        root = new FrameLayout(this);
        root.setBackgroundColor(Color.WHITE);
        setContentView(root);
        configureInsets();
        createErrorSurface();
        WebView.setWebContentsDebuggingEnabled(BuildConfig.DEBUG);
        createWebView();
        if (state != null) {
            String savedPage = state.getString("page");
            if (NavigationPolicy.isFirstParty(savedPage)) currentPage = savedPage;
        }
        // Recover a GET to the same path; never restore/replay a submitted form body.
        webView.loadUrl(currentPage);
        if (Build.VERSION.SDK_INT >= 33) {
            backCallback = this::handleBack;
            getOnBackInvokedDispatcher().registerOnBackInvokedCallback(
                    OnBackInvokedDispatcher.PRIORITY_DEFAULT, backCallback);
        }
    }

    private void configureInsets() {
        if (Build.VERSION.SDK_INT >= 30) {
            getWindow().setDecorFitsSystemWindows(false);
            root.setOnApplyWindowInsetsListener((view, insets) -> {
                android.graphics.Insets safe = insets.getInsets(
                        WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout()
                                | WindowInsets.Type.ime());
                view.setPadding(safe.left, safe.top, safe.right, safe.bottom);
                // Native padding owns system/IME space. Do not pass it into CSS a second time.
                return WindowInsets.CONSUMED;
            });
            root.requestApplyInsets();
        } else {
            // API 24–29: platform decor owns the bars, adjustResize owns the IME.
            root.setFitsSystemWindows(true);
        }
    }

    private void createErrorSurface() {
        errorSurface = new LinearLayout(this);
        errorSurface.setOrientation(LinearLayout.VERTICAL);
        errorSurface.setGravity(Gravity.CENTER);
        errorSurface.setPadding(dp(24), dp(24), dp(24), dp(24));
        errorSurface.setBackgroundColor(Color.WHITE);
        TextView title = new TextView(this);
        title.setText(R.string.load_failed);
        title.setTextSize(20);
        title.setTextColor(Color.rgb(23, 23, 23));
        title.setGravity(Gravity.CENTER);
        errorSurface.addView(title);
        TextView explanation = new TextView(this);
        explanation.setText(R.string.load_failed_hint);
        explanation.setTextSize(16);
        explanation.setGravity(Gravity.CENTER);
        explanation.setPadding(0, dp(16), 0, dp(24));
        errorSurface.addView(explanation);
        Button retry = new Button(this);
        retry.setText(R.string.retry);
        retry.setMinHeight(dp(48));
        retry.setOnClickListener(v -> {
            if (webView == null) createWebView();
            webView.loadUrl(currentPage);
        });
        errorSurface.addView(retry);
        errorSurface.setVisibility(View.GONE);
        root.addView(errorSurface, new FrameLayout.LayoutParams(-1, -1));
    }

    @SuppressLint("SetJavaScriptEnabled")
    private void createWebView() {
        WebView view = new WebView(this);
        webView = view;
        root.addView(view, 0, new FrameLayout.LayoutParams(-1, -1));
        WebSettings settings = view.getSettings();
        settings.setJavaScriptEnabled(true);
        settings.setDomStorageEnabled(true);
        settings.setTextZoom(Math.round(getResources().getConfiguration().fontScale * 100));
        settings.setAllowFileAccess(false);
        // SAF grants selected content URIs. No broad storage permission or local FileProvider.
        settings.setAllowContentAccess(true);
        settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
        settings.setSupportZoom(true);
        settings.setBuiltInZoomControls(true);
        settings.setDisplayZoomControls(false);
        settings.setUseWideViewPort(true);
        settings.setLoadWithOverviewMode(true);
        // target=_blank becomes a navigation of this WebView, handled by exact-origin policy.
        // No hidden second WebView or popup renderer is ever allocated.
        settings.setSupportMultipleWindows(false);
        settings.setJavaScriptCanOpenWindowsAutomatically(false);
        CookieManager cookies = CookieManager.getInstance();
        cookies.setAcceptCookie(true);
        cookies.setAcceptThirdPartyCookies(view, false);
        view.setWebViewClient(new WebViewClient() {
            @Override public boolean shouldOverrideUrlLoading(WebView target, WebResourceRequest request) {
                String url = request.getUrl().toString();
                if (NavigationPolicy.isFirstParty(url)) return false;
                if (request.isForMainFrame()) openExternal(url);
                return true;
            }

            @Override public void onPageStarted(WebView target, String url, android.graphics.Bitmap icon) {
                if (!NavigationPolicy.isFirstParty(url)) {
                    target.stopLoading();
                    openExternal(url);
                    return;
                }
                currentPage = url;
                documentFailed = false;
                errorSurface.setVisibility(View.GONE);
            }

            @Override public void onPageCommitVisible(WebView target, String url) {
                if (!documentFailed) errorSurface.setVisibility(View.GONE);
            }

            @Override public void onReceivedError(WebView target, WebResourceRequest request,
                                                 WebResourceError error) {
                if (request.isForMainFrame()) showDocumentError();
            }

            @Override public void onReceivedHttpError(WebView target, WebResourceRequest request,
                                                     WebResourceResponse response) {
                if (request.isForMainFrame()) showDocumentError();
            }

            @Override public void onReceivedSslError(WebView target, SslErrorHandler handler, SslError error) {
                handler.cancel();
                if (currentPage.equals(error.getUrl())) showDocumentError();
            }

            @Override public boolean onRenderProcessGone(WebView target, RenderProcessGoneDetail detail) {
                // Destroy the unusable view first. Recovery is an explicit GET, never a form replay.
                if (NavigationPolicy.isFirstParty(target.getUrl())) currentPage = target.getUrl();
                handlingBack = false;
                cancelFileSelection();
                destroyWebView(target);
                showDocumentError();
                return true;
            }
        });
        view.setWebChromeClient(new WebChromeClient() {
            @Override public boolean onShowFileChooser(WebView target, ValueCallback<Uri[]> callback,
                                                       FileChooserParams params) {
                cancelFileSelection();
                fileCallback = callback;
                activeFileRequest = nextFileRequest++;
                if (nextFileRequest >= 65000) nextFileRequest = FIRST_FILE_PICKER;
                Intent picker = new Intent(Intent.ACTION_OPEN_DOCUMENT);
                picker.addCategory(Intent.CATEGORY_OPENABLE);
                String[] accepted = params.getAcceptTypes();
                ArrayList<String> mimeTypes = new ArrayList<>();
                for (String type : accepted) {
                    if (type != null && type.matches("[\\w.+*-]+/[\\w.+*-]+")) mimeTypes.add(type);
                }
                picker.setType(mimeTypes.size() == 1 ? mimeTypes.get(0) : "*/*");
                if (mimeTypes.size() > 1) picker.putExtra(Intent.EXTRA_MIME_TYPES, mimeTypes.toArray(new String[0]));
                picker.putExtra(Intent.EXTRA_ALLOW_MULTIPLE,
                        params.getMode() == FileChooserParams.MODE_OPEN_MULTIPLE);
                try {
                    startActivityForResult(picker, activeFileRequest);
                } catch (ActivityNotFoundException e) {
                    cancelFileSelection();
                    toast(R.string.no_file_picker);
                }
                return true;
            }
        });
        view.setDownloadListener(this::chooseDownloadDestination);
    }

    private void showDocumentError() {
        documentFailed = true;
        errorSurface.setVisibility(View.VISIBLE);
        errorSurface.bringToFront();
    }

    private void openExternal(String url) {
        if (!NavigationPolicy.isExternal(url)) {
            toast(R.string.unsupported_link);
            return;
        }
        try {
            startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(url)).addCategory(Intent.CATEGORY_BROWSABLE));
        } catch (ActivityNotFoundException e) {
            toast(R.string.no_link_handler);
        }
    }

    // Legacy entry point is needed only on API 24–32. API 33+ gestures are handled
    // by the platform OnBackInvokedDispatcher registration in onCreate and removed
    // in onDestroy. GestureBackNavigation flags the override without recognizing
    // this per-API platform migration; suppress only that legacy method incident.
    @SuppressLint("GestureBackNavigation")
    @Override public void onBackPressed() {
        if (Build.VERSION.SDK_INT < 33) handleBack();
    }

    private void handleBack() {
        if (handlingBack) return;
        if (Build.VERSION.SDK_INT >= 30 && root.getRootWindowInsets() != null
                && root.getRootWindowInsets().isVisible(WindowInsets.Type.ime())) {
            InputMethodManager manager = (InputMethodManager) getSystemService(INPUT_METHOD_SERVICE);
            manager.hideSoftInputFromWindow(root.getWindowToken(), 0);
            return;
        }
        if (webView == null || documentFailed) {
            finish();
            return;
        }
        handlingBack = true;
        WebView target = webView;
        // Only presentation close semantics. No history entries or task/auth native bridge.
        target.evaluateJavascript("(() => { const dialogs = [...document.querySelectorAll('[role=dialog], [role=menu]')];"
                + "const active = dialogs.filter(e => e.getClientRects().length);"
                + "if (!active.length) return false;"
                + "const element = active[active.length - 1];"
                + "const focused = document.activeElement;"
                + "(focused && element.contains(focused) ? focused : element).dispatchEvent(new KeyboardEvent('keydown',"
                + "{ key: 'Escape', code: 'Escape', bubbles: true, cancelable: true })); return true; })()", result -> {
            handlingBack = false;
            if (target != webView || isFinishing() || isDestroyed()) return;
            if ("true".equals(result)) return;
            if (target.canGoBack()) target.goBack();
            else finish();
        });
    }

    private void cancelFileSelection() {
        if (fileCallback != null) {
            ValueCallback<Uri[]> previous = fileCallback;
            fileCallback = null;
            activeFileRequest = -1;
            previous.onReceiveValue(null);
        }
    }

    @Override protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == activeFileRequest && fileCallback != null) {
            ValueCallback<Uri[]> callback = fileCallback;
            fileCallback = null;
            activeFileRequest = -1;
            Uri[] selected = resultCode == RESULT_OK ? WebChromeClient.FileChooserParams.parseResult(resultCode, data) : null;
            if (selected != null) {
                ArrayList<Uri> readable = new ArrayList<>();
                for (Uri uri : selected) if (uri != null && "content".equals(uri.getScheme())) readable.add(uri);
                selected = readable.isEmpty() ? null : readable.toArray(new Uri[0]);
            }
            callback.onReceiveValue(selected);
        } else if (requestCode == SAVE_DOWNLOAD) {
            PendingDownload pending = pendingDownload;
            pendingDownload = null;
            if (pending != null && resultCode == RESULT_OK && data != null && data.getData() != null
                    && "content".equals(data.getData().getScheme())) {
                NativeDownload download = new NativeDownload(getApplicationContext(), pending.url,
                        pending.userAgent, pending.cookie, data.getData());
                activeDownload = download;
                toast(R.string.downloading);
                downloadExecutor.execute(() -> {
                    boolean success = download.save();
                    runOnUiThread(() -> {
                        if (activeDownload == download) activeDownload = null;
                        if (!isFinishing() && !isDestroyed()) toast(success ? R.string.download_saved : R.string.download_failed);
                    });
                });
            }
        }
    }

    private void chooseDownloadDestination(String url, String userAgent, String disposition, String mimeType, long length) {
        if (!NavigationPolicy.isFirstParty(url)) {
            openExternal(url);
            return;
        }
        if (pendingDownload != null || activeDownload != null) {
            toast(R.string.download_busy);
            return;
        }
        String filename = URLUtil.guessFileName(url, disposition, mimeType).replaceAll("[\\\\/\\p{Cntrl}]", "_");
        Intent picker = new Intent(Intent.ACTION_CREATE_DOCUMENT);
        picker.addCategory(Intent.CATEGORY_OPENABLE);
        picker.setType(mimeType != null && mimeType.matches("[\\w.+*-]+/[\\w.+*-]+") ? mimeType : "application/octet-stream");
        picker.putExtra(Intent.EXTRA_TITLE, filename);
        pendingDownload = new PendingDownload(url, userAgent, CookieManager.getInstance().getCookie(url));
        try {
            startActivityForResult(picker, SAVE_DOWNLOAD);
        } catch (ActivityNotFoundException e) {
            pendingDownload = null;
            toast(R.string.no_file_picker);
        }
    }

    private static final class PendingDownload {
        final String url;
        final String userAgent;
        final String cookie;
        PendingDownload(String url, String userAgent, String cookie) {
            this.url = url;
            this.userAgent = userAgent;
            this.cookie = cookie;
        }
    }

    @Override protected void onSaveInstanceState(Bundle state) {
        if (webView != null && NavigationPolicy.isFirstParty(webView.getUrl())) currentPage = webView.getUrl();
        state.putString("page", currentPage);
        super.onSaveInstanceState(state);
    }

    @Override public void onConfigurationChanged(Configuration configuration) {
        super.onConfigurationChanged(configuration);
        if (webView != null) webView.getSettings().setTextZoom(Math.round(configuration.fontScale * 100));
        root.requestApplyInsets();
    }

    @Override protected void onPause() {
        CookieManager.getInstance().flush();
        if (webView != null) webView.onPause();
        super.onPause();
    }

    @Override protected void onResume() {
        super.onResume();
        if (webView != null) webView.onResume();
    }

    private void destroyWebView(WebView target) {
        target.stopLoading();
        target.setDownloadListener(null);
        target.setWebChromeClient(null);
        target.setWebViewClient(new WebViewClient());
        root.removeView(target);
        target.removeAllViews();
        target.destroy();
        if (target == webView) webView = null;
    }

    @Override protected void onDestroy() {
        cancelFileSelection();
        pendingDownload = null;
        if (activeDownload != null) activeDownload.cancel();
        downloadExecutor.shutdownNow();
        if (Build.VERSION.SDK_INT >= 33 && backCallback != null) {
            getOnBackInvokedDispatcher().unregisterOnBackInvokedCallback(backCallback);
        }
        if (webView != null) destroyWebView(webView);
        super.onDestroy();
    }

    private int dp(int value) { return Math.round(value * getResources().getDisplayMetrics().density); }
    private void toast(int message) { Toast.makeText(this, message, Toast.LENGTH_LONG).show(); }
}
