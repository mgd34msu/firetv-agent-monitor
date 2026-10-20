package org.agentmonitor.firetv;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.ApplicationInfo;
import android.os.Bundle;
import android.view.KeyEvent;
import android.view.WindowManager;
import android.webkit.JavascriptInterface;
import android.webkit.WebSettings;
import android.webkit.WebView;

import org.json.JSONObject;

/**
 * The whole app: a full-screen WebView showing the shared TV web code (copied
 * from webos/app into the assets), plus what a web page cannot do on its own:
 * keep the screen on, find the helper on the LAN with a UDP broadcast, and
 * receive the Fire TV remote's keys.
 *
 * The APK carries no server address or token. firetv/setup.sh sends them once
 * over adb as extras of the launch intent ("url", "token"); they are kept in
 * the app's private preferences and handed to the page by AgentBridge.config().
 */
public class MainActivity extends Activity {
    private static final String PREFS = "server";
    private WebView web;

    @SuppressLint({"SetJavaScriptEnabled", "AddJavascriptInterface"})
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        // A status board: never let the screensaver cover it while it is open.
        getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);

        saveConfig(getIntent());
        // chrome://inspect over adb, for builds made debuggable only.
        WebView.setWebContentsDebuggingEnabled((getApplicationInfo().flags & ApplicationInfo.FLAG_DEBUGGABLE) != 0);
        web = new WebView(this);
        WebSettings settings = web.getSettings();
        settings.setJavaScriptEnabled(true);
        settings.setDomStorageEnabled(true); // the last discovered address
        settings.setAllowFileAccess(true);   // the page lives in file:///android_asset
        settings.setMediaPlaybackRequiresUserGesture(true);
        web.setBackgroundColor(0xFF111417);
        web.setFocusable(false); // keys come through dispatchKeyEvent below
        web.addJavascriptInterface(new Bridge(), "AgentBridge");
        setContentView(web);
        web.loadUrl("file:///android_asset/index.html");
    }

    /** setup.sh starts the running app again with new settings: apply them. */
    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        if (saveConfig(intent)) {
            web.reload();
        }
    }

    /** Stores the url and token an intent carries; reports whether it had any. */
    private boolean saveConfig(Intent intent) {
        if (intent == null) {
            return false;
        }
        String url = intent.getStringExtra("url");
        if (url == null || !url.startsWith("http://")) {
            return false;
        }
        String token = intent.getStringExtra("token");
        getSharedPreferences(PREFS, MODE_PRIVATE).edit()
                .putString("url", url.replaceAll("/+$", ""))
                .putString("token", token == null ? "" : token)
                .apply();
        return true;
    }

    /**
     * The remote's keys go to the page as the webOS key codes it already
     * handles, so the web code needs no Fire TV branch for them. Arrow and OK
     * keys repeat while held, like on the LG.
     */
    @Override
    public boolean dispatchKeyEvent(KeyEvent event) {
        int code = webKeyCode(event.getKeyCode());
        if (code == 0) {
            return super.dispatchKeyEvent(event);
        }
        if (event.getAction() == KeyEvent.ACTION_DOWN) {
            web.evaluateJavascript("window.remoteKey && remoteKey(" + code + ")", null);
        }
        return true;
    }

    private static int webKeyCode(int androidKey) {
        switch (androidKey) {
            case KeyEvent.KEYCODE_DPAD_LEFT: return 37;
            case KeyEvent.KEYCODE_DPAD_UP: return 38;
            case KeyEvent.KEYCODE_DPAD_RIGHT: return 39;
            case KeyEvent.KEYCODE_DPAD_DOWN: return 40;
            case KeyEvent.KEYCODE_DPAD_CENTER:
            case KeyEvent.KEYCODE_ENTER:
            case KeyEvent.KEYCODE_NUMPAD_ENTER: return 13;
            case KeyEvent.KEYCODE_BACK: return 461;
            case KeyEvent.KEYCODE_MEDIA_REWIND: return 412;
            case KeyEvent.KEYCODE_MEDIA_FAST_FORWARD: return 417;
            case KeyEvent.KEYCODE_PAGE_UP: return 33;
            case KeyEvent.KEYCODE_PAGE_DOWN: return 34;
            default: return 0;
        }
    }

    @Override
    protected void onDestroy() {
        if (web != null) {
            web.destroy();
        }
        super.onDestroy();
    }

    /** What the page may call, as window.AgentBridge. */
    private final class Bridge {
        /** {"url": ..., "token": ...} as set by setup.sh, or "" before setup. */
        @JavascriptInterface
        public String config() {
            SharedPreferences prefs = getSharedPreferences(PREFS, MODE_PRIVATE);
            String url = prefs.getString("url", "");
            if (url.isEmpty()) {
                return "";
            }
            try {
                return new JSONObject().put("url", url).put("token", prefs.getString("token", "")).toString();
            } catch (Exception e) {
                return "";
            }
        }

        /** Back on the card grid: leave the app. */
        @JavascriptInterface
        public void exit() {
            runOnUiThread(MainActivity.this::finish);
        }

        /**
         * Finds the helper with a UDP broadcast (docs/http-api.md#discovery)
         * off the UI thread, then calls window.agentDiscovered(url) with the
         * verified base url, or "".
         */
        @JavascriptInterface
        public void discover(final String token, final int port) {
            new Thread(() -> {
                String url = Discovery.find(token, port);
                final String js = "window.agentDiscovered && agentDiscovered(" + JSONObject.quote(url) + ")";
                runOnUiThread(() -> web.evaluateJavascript(js, null));
            }, "discovery").start();
        }
    }
}
