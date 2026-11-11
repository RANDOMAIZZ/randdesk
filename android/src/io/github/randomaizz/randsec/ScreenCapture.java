package io.github.randomaizz.randsec;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.hardware.display.DisplayManager;
import android.hardware.display.VirtualDisplay;
import android.media.MediaCodec;
import android.media.MediaCodecInfo;
import android.media.MediaFormat;
import android.media.projection.MediaProjection;
import android.media.projection.MediaProjectionManager;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.util.DisplayMetrics;
import android.util.Log;
import android.view.Display;
import android.view.Surface;

import java.nio.ByteBuffer;

/**
 * Показ экрана телефона: MediaProjection → виртуальный экран → аппаратный кодировщик H.264.
 * Готовые кадры уходят в узел RANDSEC через {@link ScreenBridge}.
 */
final class ScreenCapture {
    static final String TAG = "RANDSEC";
    private static final String CHANNEL = "randsec-screen";
    private static final int NOTE_ID = 7;

    private static final Object lock = new Object();
    private static MediaProjection projection;
    private static VirtualDisplay display;
    private static MediaCodec codec;
    private static Surface surface;
    private static Thread drain;
    private static volatile int w, h;
    private static volatile String encName = "";
    private static int maxSide = 1920, fps = 60, kbps = 8000;
    private static byte[] csd;
    private static int rotation = -1;
    private static final Handler main = new Handler(Looper.getMainLooper());

    static int width() { return w; }
    static int height() { return h; }
    static String encoderName() { return encName; }

    /** ПК открыл рабочий стол телефона: показываем запрос разрешения или перезапускаем кодировщик. */
    static void request(Context c, int maxSide, int fps, int kbps) {
        synchronized (lock) {
            ScreenCapture.maxSide = Math.max(480, Math.min(maxSide, 2560));
            ScreenCapture.fps = Math.max(5, Math.min(fps, 60));
            ScreenCapture.kbps = Math.max(500, Math.min(kbps, 40000));
            if (projection != null) {
                restartEncoder(c);
                return;
            }
        }
        ScreenBridge b = ScreenBridge.get();
        if (b != null) b.status("waiting", null);
        // разрешение даёт только сам пользователь телефона: открываем системный запрос,
        // а если Android не даёт открыть окно из фона — показываем уведомление
        try { c.startActivity(ScreenBridge.captureIntent(c)); } catch (Exception ignored) {}
        notifyAsk(c);
    }

    private static void notifyAsk(Context c) {
        NotificationManager nm = c.getSystemService(NotificationManager.class);
        NotificationChannel ch = new NotificationChannel(CHANNEL, "Запросы показа экрана", NotificationManager.IMPORTANCE_HIGH);
        ch.setDescription("ПК просит показать экран этого телефона");
        nm.createNotificationChannel(ch);
        PendingIntent pi = PendingIntent.getActivity(c, 1, ScreenBridge.captureIntent(c),
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
        Notification n = new Notification.Builder(c, CHANNEL)
                .setSmallIcon(R.drawable.ic_stat)
                .setContentTitle("ПК просит показать экран")
                .setContentText("Нажмите, чтобы разрешить показ экрана телефона")
                .setContentIntent(pi)
                .setFullScreenIntent(pi, true)
                .setAutoCancel(true)
                .setCategory(Notification.CATEGORY_CALL)
                .build();
        nm.notify(NOTE_ID, n);
    }

    static void denied(Context c) {
        c.getSystemService(NotificationManager.class).cancel(NOTE_ID);
        ScreenBridge b = ScreenBridge.get();
        if (b != null) b.status("denied", "На телефоне не разрешили показ экрана");
    }

    /** Пользователь разрешил показ экрана. */
    static void granted(Context c, int resultCode, Intent data) {
        c.getSystemService(NotificationManager.class).cancel(NOTE_ID);
        synchronized (lock) {
            stopLocked();
            MediaProjectionManager mpm = c.getSystemService(MediaProjectionManager.class);
            try {
                projection = mpm.getMediaProjection(resultCode, data);
            } catch (Exception e) {
                Log.e(TAG, "MediaProjection", e);
                fail("Android не дал начать показ экрана: " + e.getMessage());
                return;
            }
            if (projection == null) { fail("Android не дал начать показ экрана"); return; }
            projection.registerCallback(new MediaProjection.Callback() {
                @Override public void onStop() {
                    synchronized (lock) { stopLocked(); }
                    ScreenBridge b = ScreenBridge.get();
                    if (b != null) b.status("stopped", "Показ экрана остановлен на телефоне");
                }
            }, main);
            restartEncoder(c);
        }
    }

    private static void fail(String msg) {
        ScreenBridge b = ScreenBridge.get();
        if (b != null) b.status("error", msg);
    }

    static void requestKeyFrame() {
        synchronized (lock) {
            if (codec == null) return;
            try {
                Bundle p = new Bundle();
                p.putInt(MediaCodec.PARAMETER_KEY_REQUEST_SYNC_FRAME, 0);
                codec.setParameters(p);
            } catch (Exception ignored) {}
        }
    }

    static void stop(Context c) {
        synchronized (lock) { stopLocked(); }
    }

    private static void stopLocked() {
        ControlService.hideCursor();
        ControlService.sessionEnded();
        stopEncoderLocked();
        if (display != null) { display.release(); display = null; }
        if (projection != null) { try { projection.stop(); } catch (Exception ignored) {} projection = null; }
        w = h = 0;
    }

    private static void stopEncoderLocked() {
        Thread t = drain;
        drain = null;
        if (t != null) t.interrupt();
        if (codec != null) {
            try { codec.stop(); } catch (Exception ignored) {}
            codec.release();
            codec = null;
        }
        if (surface != null) { surface.release(); surface = null; }
        csd = null;
    }

    /** Размер картинки: экран, уменьшенный до maxSide по длинной стороне, кратно 16. */
    private static int[] size(Context c) {
        DisplayManager dm = c.getSystemService(DisplayManager.class);
        Display d = dm.getDisplay(Display.DEFAULT_DISPLAY);
        DisplayMetrics m = new DisplayMetrics();
        d.getRealMetrics(m);
        rotation = d.getRotation();
        int sw = m.widthPixels, sh = m.heightPixels;
        double k = Math.min(1.0, (double) maxSide / Math.max(sw, sh));
        int ow = Math.max(16, (int) Math.round(sw * k / 16) * 16);
        int oh = Math.max(16, (int) Math.round(sh * k / 16) * 16);
        return new int[]{ow, oh, m.densityDpi};
    }

    private static void restartEncoder(Context c) {
        synchronized (lock) {
            if (projection == null) return;
            stopEncoderLocked();
            int[] s = size(c);
            try {
                MediaFormat f = MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, s[0], s[1]);
                f.setInteger(MediaFormat.KEY_COLOR_FORMAT, MediaCodecInfo.CodecCapabilities.COLOR_FormatSurface);
                f.setInteger(MediaFormat.KEY_BIT_RATE, kbps * 1000);
                f.setInteger(MediaFormat.KEY_FRAME_RATE, fps);
                f.setInteger(MediaFormat.KEY_I_FRAME_INTERVAL, 2);
                f.setInteger(MediaFormat.KEY_BITRATE_MODE, MediaCodecInfo.EncoderCapabilities.BITRATE_MODE_CBR);
                f.setLong(MediaFormat.KEY_REPEAT_PREVIOUS_FRAME_AFTER, 100_000); // неподвижный экран — повторяем кадр
                f.setInteger(MediaFormat.KEY_PRIORITY, 0); // реальное время
                if (Build.VERSION.SDK_INT >= 29) f.setInteger(MediaFormat.KEY_MAX_B_FRAMES, 0);
                if (Build.VERSION.SDK_INT >= 30) f.setInteger(MediaFormat.KEY_LATENCY, 1);
                codec = MediaCodec.createEncoderByType(MediaFormat.MIMETYPE_VIDEO_AVC);
                encName = codec.getName();
                try {
                    codec.configure(f, null, null, MediaCodec.CONFIGURE_FLAG_ENCODE);
                } catch (Exception e) { // часть кодировщиков не принимает необязательные ключи
                    f = MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, s[0], s[1]);
                    f.setInteger(MediaFormat.KEY_COLOR_FORMAT, MediaCodecInfo.CodecCapabilities.COLOR_FormatSurface);
                    f.setInteger(MediaFormat.KEY_BIT_RATE, kbps * 1000);
                    f.setInteger(MediaFormat.KEY_FRAME_RATE, fps);
                    f.setInteger(MediaFormat.KEY_I_FRAME_INTERVAL, 10);
                    f.setLong(MediaFormat.KEY_REPEAT_PREVIOUS_FRAME_AFTER, 100_000);
                    codec.reset();
                    codec.configure(f, null, null, MediaCodec.CONFIGURE_FLAG_ENCODE);
                }
                surface = codec.createInputSurface();
                codec.start();
                w = s[0]; h = s[1];
                if (display == null) {
                    display = projection.createVirtualDisplay("randsec", s[0], s[1], s[2],
                            DisplayManager.VIRTUAL_DISPLAY_FLAG_AUTO_MIRROR, surface, null, null);
                } else { // Android 14+: виртуальный экран создаётся один раз на разрешение
                    display.resize(s[0], s[1], s[2]);
                    display.setSurface(surface);
                }
            } catch (Exception e) {
                Log.e(TAG, "кодировщик H.264", e);
                stopEncoderLocked();
                fail("кодировщик H.264 телефона: " + e.getMessage());
                return;
            }
            // сначала сообщаем размер и кодировщик, потом пускаем кадры — иначе первый ключевой
            // кадр уйдёт раньше и браузер будет ждать следующий
            ScreenBridge b = ScreenBridge.get();
            if (b != null) b.status("started", null);
            final MediaCodec mc = codec;
            drain = new Thread(() -> drainLoop(c, mc), "randsec-h264");
            drain.start();
        }
    }

    private static void drainLoop(Context c, MediaCodec mc) {
        MediaCodec.BufferInfo info = new MediaCodec.BufferInfo();
        byte[] buf = new byte[1 << 20];
        long lastRotCheck = 0;
        DisplayManager dm = c.getSystemService(DisplayManager.class);
        while (!Thread.currentThread().isInterrupted()) {
            int idx;
            try {
                idx = mc.dequeueOutputBuffer(info, 100_000);
            } catch (Exception e) {
                return;
            }
            long now = System.currentTimeMillis();
            if (now - lastRotCheck > 700) { // телефон повернули — меняем размер картинки
                lastRotCheck = now;
                Display d = dm.getDisplay(Display.DEFAULT_DISPLAY);
                if (d != null && d.getRotation() != rotation) {
                    main.post(() -> restartEncoder(c));
                    return;
                }
            }
            if (idx < 0) continue;
            try {
                ByteBuffer ob = mc.getOutputBuffer(idx);
                if (ob != null && info.size > 0) {
                    ob.position(info.offset);
                    ob.limit(info.offset + info.size);
                    boolean config = (info.flags & MediaCodec.BUFFER_FLAG_CODEC_CONFIG) != 0;
                    boolean key = (info.flags & MediaCodec.BUFFER_FLAG_KEY_FRAME) != 0;
                    if (config) {
                        csd = new byte[info.size];
                        ob.get(csd);
                    } else {
                        int pre = key && csd != null ? csd.length : 0;
                        int need = pre + info.size;
                        if (buf.length < need) buf = new byte[need + (need >> 2)];
                        if (pre > 0) System.arraycopy(csd, 0, buf, 0, pre);
                        ob.get(buf, pre, info.size);
                        ScreenBridge b = ScreenBridge.get();
                        if (b != null) b.frame(buf, need, key);
                    }
                }
                mc.releaseOutputBuffer(idx, false);
            } catch (Exception e) {
                return;
            }
        }
    }
}
