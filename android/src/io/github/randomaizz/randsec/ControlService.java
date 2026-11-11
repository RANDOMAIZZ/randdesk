package io.github.randomaizz.randsec;

import android.accessibilityservice.AccessibilityService;
import android.accessibilityservice.GestureDescription;
import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Paint;
import android.graphics.Path;
import android.graphics.PixelFormat;
import android.os.Handler;
import android.os.Looper;
import android.view.Gravity;
import android.view.View;
import android.view.WindowManager;
import android.os.Build;
import android.os.Bundle;
import android.util.DisplayMetrics;
import android.view.accessibility.AccessibilityEvent;
import android.view.accessibility.AccessibilityNodeInfo;

import org.json.JSONObject;

import java.util.ArrayList;
import java.util.List;

/**
 * Управление телефоном с ПК через специальные возможности Android: касания, свайпы,
 * кнопки «Назад», «Домой», «Недавние» и ввод текста в поле, где стоит курсор.
 * Включается один раз вручную: Настройки → Специальные возможности → RANDSEC.
 */
public class ControlService extends AccessibilityService {
    private static volatile ControlService instance;

    // текущее касание мышью (от md до mu)
    private static final List<float[]> path = new ArrayList<>();
    private static long downAt;
    private static float lastX, lastY;

    private static final Handler main = new Handler(Looper.getMainLooper());
    private CursorView cursor;
    private WindowManager.LayoutParams cursorLp;

    static boolean enabled() { return instance != null; }

    @Override protected void onServiceConnected() {
        instance = this;
        // список событий задаём здесь: из XML Android берёт его только при включении службы
        android.accessibilityservice.AccessibilityServiceInfo info = getServiceInfo();
        if (info != null) {
            info.eventTypes = AccessibilityEvent.TYPE_VIEW_CLICKED | AccessibilityEvent.TYPE_VIEW_LONG_CLICKED
                    | AccessibilityEvent.TYPE_VIEW_FOCUSED | AccessibilityEvent.TYPE_VIEW_SCROLLED
                    | AccessibilityEvent.TYPE_VIEW_TEXT_SELECTION_CHANGED | AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED;
            info.notificationTimeout = 50;
            setServiceInfo(info);
        }
    }
    @Override public boolean onUnbind(android.content.Intent i) { hideCursorNow(); instance = null; return super.onUnbind(i); }
    @Override public void onDestroy() { hideCursorNow(); instance = null; super.onDestroy(); }

    /** Стрелка мыши ПК поверх экрана телефона — её видно и на телефоне, и на картинке на ПК. */
    private void moveCursor(float x, float y) {
        main.post(() -> {
            WindowManager wm = getSystemService(WindowManager.class);
            float d = getResources().getDisplayMetrics().density;
            if (cursor == null) {
                cursor = new CursorView(this, d);
                cursorLp = new WindowManager.LayoutParams(
                        Math.round(16 * d), Math.round(22 * d),
                        WindowManager.LayoutParams.TYPE_ACCESSIBILITY_OVERLAY,
                        WindowManager.LayoutParams.FLAG_NOT_TOUCHABLE | WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE
                                | WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN | WindowManager.LayoutParams.FLAG_LAYOUT_NO_LIMITS,
                        PixelFormat.TRANSLUCENT);
                cursorLp.gravity = Gravity.TOP | Gravity.START;
                if (android.os.Build.VERSION.SDK_INT >= 28) {
                    cursorLp.layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES;
                }
                cursorLp.x = Math.round(x); cursorLp.y = Math.round(y);
                try { wm.addView(cursor, cursorLp); } catch (Exception e) { cursor = null; return; }
                return;
            }
            cursorLp.x = Math.round(x); cursorLp.y = Math.round(y);
            try { wm.updateViewLayout(cursor, cursorLp); } catch (Exception ignored) {}
        });
    }

    static void hideCursor() {
        ControlService s = instance;
        if (s != null) main.post(s::hideCursorNow);
    }

    private void hideCursorNow() {
        if (cursor == null) return;
        try { getSystemService(WindowManager.class).removeView(cursor); } catch (Exception ignored) {}
        cursor = null;
    }

    private static final class CursorView extends View {
        private final Path p = new Path();
        private final Paint fill = new Paint(Paint.ANTI_ALIAS_FLAG), edge = new Paint(Paint.ANTI_ALIAS_FLAG);
        CursorView(Context c, float d) {
            super(c);
            // классическая стрелка с кончиком в (0,0)
            float k = d * 0.8f;
            p.moveTo(1 * k, 1 * k);
            p.lineTo(1 * k, 20 * k);
            p.lineTo(6 * k, 15.5f * k);
            p.lineTo(9.5f * k, 23.5f * k);
            p.lineTo(12.5f * k, 22.2f * k);
            p.lineTo(9.2f * k, 14.4f * k);
            p.lineTo(15.5f * k, 14.4f * k);
            p.close();
            fill.setColor(Color.WHITE);
            fill.setStyle(Paint.Style.FILL);
            edge.setColor(Color.BLACK);
            edge.setStyle(Paint.Style.STROKE);
            edge.setStrokeWidth(1.2f * d);
            edge.setStrokeJoin(Paint.Join.ROUND);
        }
        @Override protected void onDraw(Canvas c) {
            c.drawPath(p, fill);
            c.drawPath(p, edge);
        }
    }
    // экранная клавиатура: пока телефоном управляет ПК, она не выскакивает от его нажатий;
    // как только владелец сам коснётся экрана — снова работает как обычно
    private static volatile long lastRemote;
    private boolean kbHidden;

    private View touchWatch;

    private void remoteAction() {
        lastRemote = Math.max(lastRemote, android.os.SystemClock.uptimeMillis());
        if (kbHidden) return;
        try {
            if (getSoftKeyboardController().setShowMode(SHOW_MODE_HIDDEN)) {
                kbHidden = true;
                main.post(this::watchTouches);
            }
        } catch (Exception ignored) {}
    }

    /**
     * Невидимое окно 1×1 в углу экрана: Android сообщает ему (ACTION_OUTSIDE) о каждом касании
     * экрана. Касание не от ПК — значит, телефон трогает владелец: клавиатура снова как обычно
     * (и сама откроется, если приложение её уже попросило).
     */
    @android.annotation.SuppressLint("ClickableViewAccessibility")
    private void watchTouches() {
        if (touchWatch != null) return;
        View v = new View(this);
        v.setOnTouchListener((view, ev) -> {
            if (ev.getActionMasked() == android.view.MotionEvent.ACTION_OUTSIDE
                    && android.os.SystemClock.uptimeMillis() > lastRemote + 250) {
                restoreKeyboard(false);
            }
            return false;
        });
        WindowManager.LayoutParams lp = new WindowManager.LayoutParams(1, 1,
                WindowManager.LayoutParams.TYPE_ACCESSIBILITY_OVERLAY,
                WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE | WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL
                        | WindowManager.LayoutParams.FLAG_WATCH_OUTSIDE_TOUCH,
                PixelFormat.TRANSLUCENT);
        lp.gravity = Gravity.TOP | Gravity.START;
        try { getSystemService(WindowManager.class).addView(v, lp); touchWatch = v; } catch (Exception e) {
            android.util.Log.w("RANDSEC", "наблюдение за касаниями", e);
        }
    }

    private void unwatchTouches() {
        if (touchWatch == null) return;
        try { getSystemService(WindowManager.class).removeView(touchWatch); } catch (Exception ignored) {}
        touchWatch = null;
    }

    private void restoreKeyboard(boolean reopen) {
        unwatchTouches();
        if (!kbHidden) return;
        kbHidden = false;
        try { getSoftKeyboardController().setShowMode(SHOW_MODE_AUTO); } catch (Exception ignored) {}
        if (!reopen) return;
        // владелец нажал в поле ввода, пока клавиатура была спрятана, — открываем её
        main.postDelayed(() -> {
            AccessibilityNodeInfo n = findFocus(AccessibilityNodeInfo.FOCUS_INPUT);
            if (n != null && n.isEditable()) n.performAction(AccessibilityNodeInfo.ACTION_CLICK);
        }, 60);
    }

    /** Показ экрана закончился — клавиатура снова как обычно. */
    static void sessionEnded() {
        ControlService s = instance;
        if (s != null) main.post(() -> s.restoreKeyboard(false));
    }

    @Override public void onAccessibilityEvent(AccessibilityEvent e) {
        if (!kbHidden) return;
        switch (e.getEventType()) {
            case AccessibilityEvent.TYPE_VIEW_CLICKED:
            case AccessibilityEvent.TYPE_VIEW_LONG_CLICKED:
            case AccessibilityEvent.TYPE_VIEW_FOCUSED:
            case AccessibilityEvent.TYPE_VIEW_SCROLLED:
                // наши касания тоже порождают эти события — считаем чужими только те,
                // что случились заметно позже последней команды с ПК
                if (android.os.SystemClock.uptimeMillis() - lastRemote > 1200) {
                    restoreKeyboard(e.getEventType() != AccessibilityEvent.TYPE_VIEW_SCROLLED);
                }
                break;
        }
    }
    @Override public void onInterrupt() {}

    /** Команда от ПК; координаты — в пикселях картинки w×h. */
    static void handle(JSONObject m, int w, int h) {
        ControlService s = instance;
        if (s == null || w <= 0 || h <= 0) return;
        DisplayMetrics dm = s.getResources().getDisplayMetrics();
        android.view.WindowManager wm = s.getSystemService(android.view.WindowManager.class);
        DisplayMetrics real = new DisplayMetrics();
        wm.getDefaultDisplay().getRealMetrics(real);
        float kx = (float) real.widthPixels / w, ky = (float) real.heightPixels / h;
        float x = (float) m.optDouble("x", 0) * kx, y = (float) m.optDouble("y", 0) * ky;
        String t = m.optString("t");
        int b = m.optInt("b", 0);
        if ("mm".equals(t) || "md".equals(t) || "mu".equals(t)) s.moveCursor(x, y);
        synchronized (path) {
            switch (t) {
                case "mm":
                    lastX = x; lastY = y;
                    if (!path.isEmpty()) path.add(new float[]{x, y});
                    break;
                case "md":
                    lastX = x; lastY = y;
                    if (b == 2) { s.stroke(new float[][]{{x, y}}, 650); break; } // правая кнопка — долгое касание
                    if (b == 1) { s.performGlobalAction(GLOBAL_ACTION_BACK); break; }
                    path.clear();
                    path.add(new float[]{x, y});
                    downAt = System.currentTimeMillis();
                    break;
                case "mu":
                    if (b != 0 || path.isEmpty()) break;
                    path.add(new float[]{x, y});
                    long dur = Math.max(40, Math.min(System.currentTimeMillis() - downAt, 3000));
                    float[][] pts = path.toArray(new float[0][]);
                    path.clear();
                    float[] a = pts[0];
                    boolean moved = Math.hypot(x - a[0], y - a[1]) > 12 * dm.density;
                    if (!moved) s.stroke(new float[][]{a}, dur > 500 ? dur : 50);
                    else s.stroke(pts, dur);
                    break;
                case "wh": { // колесо — свайп по экрану в точке курсора
                    float dy = (float) m.optDouble("dy", 0), dx = (float) m.optDouble("dx", 0);
                    float dist = Math.max(-real.heightPixels * 0.6f, Math.min(real.heightPixels * 0.6f, dy * 2.5f));
                    float distX = Math.max(-real.widthPixels * 0.6f, Math.min(real.widthPixels * 0.6f, -dx * 2.5f));
                    if (Math.abs(dist) < 8 && Math.abs(distX) < 8) break;
                    float sx = lastX > 0 ? lastX : real.widthPixels / 2f, sy = lastY > 0 ? lastY : real.heightPixels / 2f;
                    s.stroke(new float[][]{{sx, sy}, {sx + distX, sy + dist}}, 180);
                    break;
                }
                case "kd": {
                    String key = m.optString("key"), code = m.optString("c");
                    boolean mod = m.optBoolean("ctrl") || m.optBoolean("alt");
                    String ch = key.codePointCount(0, key.length()) == 1 ? key : charFor(code);
                    if (ch != null && !mod) s.type(ch);
                    else s.key(code);
                    break;
                }
                case "text":
                    s.type(m.optString("s"));
                    break;
            }
        }
    }

    /** Символ по коду клавиши — если браузер не прислал сам символ (латиница, цифры, знаки). */
    private static String charFor(String code) {
        if (code == null) return null;
        if (code.length() == 4 && code.startsWith("Key")) return code.substring(3).toLowerCase();
        if (code.length() == 6 && code.startsWith("Digit")) return code.substring(5);
        if (code.startsWith("Numpad") && code.length() == 7 && Character.isDigit(code.charAt(6))) return code.substring(6);
        switch (code) {
            case "Space": return " ";
            case "Minus": case "NumpadSubtract": return "-";
            case "Equal": return "=";
            case "Comma": return ",";
            case "Period": case "NumpadDecimal": return ".";
            case "Slash": case "NumpadDivide": return "/";
            case "Semicolon": return ";";
            case "Quote": return "'";
            case "BracketLeft": return "[";
            case "BracketRight": return "]";
            case "Backslash": return "\\";
            case "Backquote": return "`";
            case "NumpadAdd": return "+";
            case "NumpadMultiply": return "*";
        }
        return null;
    }

    private void stroke(float[][] pts, long dur) {
        remoteAction();
        lastRemote = android.os.SystemClock.uptimeMillis() + dur;
        Path p = new Path();
        p.moveTo(pts[0][0], pts[0][1]);
        for (int i = 1; i < pts.length; i++) p.lineTo(pts[i][0], pts[i][1]);
        if (pts.length == 1) p.lineTo(pts[0][0] + 0.5f, pts[0][1] + 0.5f);
        GestureDescription g = new GestureDescription.Builder()
                .addStroke(new GestureDescription.StrokeDescription(p, 0, Math.max(1, dur)))
                .build();
        dispatchGesture(g, null, null);
    }

    private void key(String code) {
        switch (code) {
            case "Escape": case "BrowserBack": case "AndroidBack": performGlobalAction(GLOBAL_ACTION_BACK); break;
            case "Home": case "MetaLeft": case "MetaRight": case "AndroidHome": performGlobalAction(GLOBAL_ACTION_HOME); break;
            case "AndroidRecents": case "Tab": performGlobalAction(GLOBAL_ACTION_RECENTS); break;
            case "AndroidNotifications": performGlobalAction(GLOBAL_ACTION_NOTIFICATIONS); break;
            case "AndroidQuick": performGlobalAction(GLOBAL_ACTION_QUICK_SETTINGS); break;
            case "AndroidPower": performGlobalAction(GLOBAL_ACTION_POWER_DIALOG); break;
            case "AndroidLock": if (Build.VERSION.SDK_INT >= 28) performGlobalAction(GLOBAL_ACTION_LOCK_SCREEN); break;
            case "PrintScreen": if (Build.VERSION.SDK_INT >= 28) performGlobalAction(GLOBAL_ACTION_TAKE_SCREENSHOT); break;
            case "Backspace": edit(null, true); break;
            case "Enter": case "NumpadEnter": enter(); break;
        }
    }

    private void type(String s) {
        if (s == null || s.isEmpty() || s.length() > 20000) return;
        edit(s, false);
    }

    /** Дописывает текст в поле ввода с фокусом (или стирает последний символ). */
    private void edit(String add, boolean backspace) {
        remoteAction();
        AccessibilityNodeInfo n = findFocus(AccessibilityNodeInfo.FOCUS_INPUT);
        if (n == null || !n.isEditable()) return;
        CharSequence cur = n.getText();
        // поле пустое, а приложение отдаёт серую подсказку как текст (Telegram и др.)
        CharSequence hint = n.getHintText();
        boolean isHint = n.isShowingHintText() || (cur != null && hint != null && cur.toString().contentEquals(hint));
        String text = cur == null || isHint ? "" : cur.toString();
        int start = n.getTextSelectionStart(), end = n.getTextSelectionEnd();
        if (!text.isEmpty() && start <= 0 && end <= 0) {
            // курсор в начале непустого поля: так часто выглядит пустое поле, чья подсказка не
            // видна системе. Вставка «из буфера» в позицию курсора верна в обоих случаях:
            // подсказка исчезнет, а настоящий текст останется после вставленного.
            if (backspace) return;
            android.content.ClipboardManager cm = getSystemService(android.content.ClipboardManager.class);
            if (cm != null) {
                cm.setPrimaryClip(android.content.ClipData.newPlainText("RANDSEC", add));
                if (n.performAction(AccessibilityNodeInfo.ACTION_PASTE)) return;
            }
        }
        if (start < 0 || end < 0 || start > text.length() || end > text.length()) { start = end = text.length(); }
        if (start > end) { int t = start; start = end; end = t; }
        String out;
        int caret;
        if (backspace) {
            if (start == end) { if (start == 0) return; start--; }
            out = text.substring(0, start) + text.substring(end);
            caret = start;
        } else {
            out = text.substring(0, start) + add + text.substring(end);
            caret = start + add.length();
        }
        Bundle a = new Bundle();
        a.putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, out);
        n.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, a);
        Bundle sel = new Bundle();
        sel.putInt(AccessibilityNodeInfo.ACTION_ARGUMENT_SELECTION_START_INT, caret);
        sel.putInt(AccessibilityNodeInfo.ACTION_ARGUMENT_SELECTION_END_INT, caret);
        n.performAction(AccessibilityNodeInfo.ACTION_SET_SELECTION, sel);
    }

    private void enter() {
        AccessibilityNodeInfo n = findFocus(AccessibilityNodeInfo.FOCUS_INPUT);
        if (n != null && Build.VERSION.SDK_INT >= 30) {
            n.performAction(AccessibilityNodeInfo.AccessibilityAction.ACTION_IME_ENTER.getId());
        } else if (n != null && n.isMultiLine()) {
            edit("\n", false);
        }
    }
}
