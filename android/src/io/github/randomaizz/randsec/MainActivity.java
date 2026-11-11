package io.github.randomaizz.randsec;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.content.Intent;
import android.content.SharedPreferences;
import android.graphics.Color;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.provider.Settings;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowInsets;
import android.view.WindowInsetsController;
import android.view.WindowManager;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;

import java.net.HttpURLConnection;
import java.net.URL;

/**
 * Окно RANDSEC: страница узла RANDSEC (http://localhost:8420) во встроенном браузере.
 * На ней настраивается телефон и открываются ПК, которыми администратор разрешил управлять.
 */
public class MainActivity extends Activity {
    static final String HOME = "http://localhost:" + NodeService.PORT + "/";

    private WebView web;
    private FrameLayout root;
    private View custom;
    private WebChromeClient.CustomViewCallback customCb;
    private final Handler main = new Handler(Looper.getMainLooper());
    private volatile boolean waiting;

    @SuppressLint("SetJavaScriptEnabled")
    @Override protected void onCreate(Bundle b) {
        super.onCreate(b);
        NodeService.start(this);

        root = new FrameLayout(this);
        root.setBackgroundColor(Color.parseColor("#0F1115"));
        web = new WebView(this);
        web.setBackgroundColor(Color.parseColor("#0F1115"));
        root.addView(web, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        setContentView(root);

        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setDatabaseEnabled(true);
        s.setMediaPlaybackRequiresUserGesture(false);
        s.setLoadWithOverviewMode(true);
        s.setUseWideViewPort(true);
        s.setBuiltInZoomControls(true);
        s.setDisplayZoomControls(false);
        s.setAllowFileAccess(false);
        s.setUserAgentString(s.getUserAgentString() + " RANDSEC-Android/" + BuildInfo.VERSION);

        web.setWebViewClient(new WebViewClient() {
            @Override public boolean shouldOverrideUrlLoading(WebView v, WebResourceRequest r) {
                Uri u = r.getUrl();
                String h = u.getHost();
                if ("localhost".equals(h) || "127.0.0.1".equals(h)) return false;
                // внешние ссылки (справка, релизы) — в обычном браузере
                try { startActivity(new Intent(Intent.ACTION_VIEW, u)); } catch (Exception ignored) {}
                return true;
            }

            @Override public void onReceivedError(WebView v, WebResourceRequest r, WebResourceError e) {
                if (r.isForMainFrame()) showWaiting();
            }
        });
        web.setWebChromeClient(new WebChromeClient() {
            // полноэкранный режим удалённого рабочего стола
            @Override public void onShowCustomView(View view, CustomViewCallback cb) {
                if (custom != null) { cb.onCustomViewHidden(); return; }
                custom = view; customCb = cb;
                root.addView(view, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
                web.setVisibility(View.GONE);
                immersive(true);
            }

            @Override public void onHideCustomView() {
                if (custom == null) return;
                root.removeView(custom);
                custom = null;
                web.setVisibility(View.VISIBLE);
                immersive(false);
                if (customCb != null) customCb.onCustomViewHidden();
                customCb = null;
            }
        });

        if (b != null) web.restoreState(b);
        else showWaiting();
        askBatteryOnce();
    }

    /** Заставка, пока узел RANDSEC запускается; как только страница отвечает — открываем её. */
    private void showWaiting() {
        if (waiting) return;
        waiting = true;
        web.loadDataWithBaseURL(null, "<html><head><meta name=viewport content='width=device-width'>"
                + "<style>body{margin:0;height:100vh;display:flex;align-items:center;justify-content:center;"
                + "background:#0F1115;color:#c9d1e0;font:16px system-ui,sans-serif;flex-direction:column;gap:14px}"
                + ".s{width:34px;height:34px;border:3px solid #2a3040;border-top-color:#5B8CFF;border-radius:50%;"
                + "animation:r 1s linear infinite}@keyframes r{to{transform:rotate(360deg)}}</style></head>"
                + "<body><div class=s></div><div>RANDSEC запускается…</div></body></html>", "text/html", "utf-8", null);
        new Thread(() -> {
            for (int i = 0; waiting && i < 600; i++) {
                if (alive()) {
                    main.post(() -> { waiting = false; web.loadUrl(HOME); });
                    return;
                }
                try { Thread.sleep(500); } catch (InterruptedException e) { return; }
            }
            waiting = false;
        }, "randsec-wait").start();
    }

    private static boolean alive() {
        try {
            HttpURLConnection c = (HttpURLConnection) new URL(HOME).openConnection();
            c.setConnectTimeout(800);
            c.setReadTimeout(1500);
            int code = c.getResponseCode();
            c.disconnect();
            return code > 0 && code < 500;
        } catch (Exception e) {
            return false;
        }
    }

    /** Один раз просим не ограничивать RANDSEC в фоне — иначе Android усыпит связь с ПК. */
    private void askBatteryOnce() {
        SharedPreferences p = getSharedPreferences("randsec", MODE_PRIVATE);
        if (p.getBoolean("askedBattery", false) || NodeService.ignoringBattery(this)) return;
        p.edit().putBoolean("askedBattery", true).apply();
        try {
            @SuppressLint("BatteryLife")
            Intent i = new Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:" + getPackageName()));
            startActivity(i);
        } catch (Exception ignored) {}
    }

    private void immersive(boolean on) {
        if (on) getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        else getWindow().clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        if (android.os.Build.VERSION.SDK_INT >= 30) {
            WindowInsetsController c = getWindow().getInsetsController();
            if (c == null) return;
            if (on) {
                c.hide(WindowInsets.Type.systemBars());
                c.setSystemBarsBehavior(WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE);
            } else c.show(WindowInsets.Type.systemBars());
        } else {
            getWindow().getDecorView().setSystemUiVisibility(on
                    ? View.SYSTEM_UI_FLAG_FULLSCREEN | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION | View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                    | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION
                    : 0);
        }
    }

    @Override public void onBackPressed() {
        if (custom != null) { exitFullscreen(); return; }
        if (web.canGoBack()) { web.goBack(); return; }
        moveTaskToBack(true);
    }

    private void exitFullscreen() {
        web.evaluateJavascript("document.exitFullscreen && document.exitFullscreen()", null);
    }

    @Override protected void onSaveInstanceState(Bundle out) {
        super.onSaveInstanceState(out);
        web.saveState(out);
    }

    @Override protected void onResume() { super.onResume(); web.onResume(); }
    @Override protected void onPause() { web.onPause(); super.onPause(); }

    @Override protected void onDestroy() {
        waiting = false;
        web.destroy();
        super.onDestroy();
    }
}
