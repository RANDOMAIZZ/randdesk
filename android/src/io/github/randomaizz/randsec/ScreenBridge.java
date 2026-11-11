package io.github.randomaizz.randsec;

import android.content.Context;
import android.content.Intent;
import android.util.Log;

import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.DataOutputStream;
import java.io.IOException;
import java.io.InputStreamReader;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;

/**
 * Мост между узлом RANDSEC (Go) и показом экрана телефона.
 * Узел подключается к 127.0.0.1:PORT, первой строкой присылает токен, дальше — команды JSON
 * по строке: start (начать показ), key (нужен ключевой кадр), in (касание/клавиша), stop.
 * В ответ идут сообщения [вид u8][длина u32][данные]: 1 — JSON о состоянии, 2 — кадр H.264
 * ([1 — ключевой][Annex B]).
 */
final class ScreenBridge {
    static final String TAG = "RANDSEC";
    static final int PORT = 8429;

    static final String TOKEN;
    static {
        byte[] b = new byte[16];
        new SecureRandom().nextBytes(b);
        StringBuilder sb = new StringBuilder();
        for (byte x : b) sb.append(String.format("%02x", x));
        TOKEN = sb.toString();
    }

    private static ScreenBridge instance;
    private final Context ctx;
    private volatile Client client;

    static synchronized ScreenBridge start(Context c) {
        if (instance == null) {
            instance = new ScreenBridge(c.getApplicationContext());
            new Thread(instance::acceptLoop, "randsec-screen").start();
        }
        return instance;
    }

    static ScreenBridge get() { return instance; }

    private ScreenBridge(Context c) { ctx = c; }

    private void acceptLoop() {
        try (ServerSocket ss = new ServerSocket(PORT, 4, InetAddress.getByName("127.0.0.1"))) {
            while (true) {
                Socket s = ss.accept();
                new Thread(() -> serve(s), "randsec-screen-client").start();
            }
        } catch (IOException e) {
            Log.e(TAG, "мост показа экрана", e);
        }
    }

    private void serve(Socket s) {
        try {
            s.setTcpNoDelay(true);
            BufferedReader in = new BufferedReader(new InputStreamReader(s.getInputStream(), StandardCharsets.UTF_8));
            String tok = in.readLine();
            if (!TOKEN.equals(tok)) { s.close(); return; }
            Client c = new Client(s);
            Client old = client;
            client = c;
            if (old != null) old.close();
            String line;
            while ((line = in.readLine()) != null) {
                JSONObject m;
                try { m = new JSONObject(line); } catch (Exception e) { continue; }
                switch (m.optString("cmd")) {
                    case "start":
                        ScreenCapture.request(ctx, m.optInt("maxSide", 1920), m.optInt("fps", 60), m.optInt("kbps", 8000));
                        break;
                    case "key":
                        ScreenCapture.requestKeyFrame();
                        break;
                    case "in":
                        ControlService.handle(m, ScreenCapture.width(), ScreenCapture.height());
                        break;
                    case "stop":
                        ScreenCapture.stop(ctx);
                        break;
                }
            }
        } catch (IOException ignored) {
        } finally {
            try { s.close(); } catch (IOException ignored) {}
        }
        if (client != null && client.sock == s) {
            client = null;
            ScreenCapture.stop(ctx); // ПК закрыл рабочий стол — показ больше не нужен
        }
    }

    void status(String state, String msg) {
        try {
            JSONObject o = new JSONObject();
            o.put("state", state);
            if (msg != null) o.put("msg", msg);
            if ("started".equals(state)) {
                o.put("w", ScreenCapture.width());
                o.put("h", ScreenCapture.height());
                o.put("encoder", ScreenCapture.encoderName());
                o.put("control", ControlService.enabled());
                if (!ControlService.enabled()) askControl(ctx);
            }
            send(1, o.toString().getBytes(StandardCharsets.UTF_8), -1, 0);
        } catch (Exception ignored) {}
    }

    void frame(byte[] data, int len, boolean key) {
        send(2, data, len, key ? 1 : 0);
    }

    private void send(int kind, byte[] data, int len, int keyFlag) {
        Client c = client;
        if (c == null) return;
        if (len < 0) len = data.length;
        try {
            synchronized (c) {
                c.out.writeByte(kind);
                if (kind == 2) {
                    c.out.writeInt(len + 1);
                    c.out.writeByte(keyFlag);
                } else {
                    c.out.writeInt(len);
                }
                c.out.write(data, 0, len);
                c.out.flush();
            }
        } catch (IOException e) {
            c.close();
        }
    }

    boolean connected() { return client != null; }

    private static final class Client {
        final Socket sock;
        final DataOutputStream out;
        Client(Socket s) throws IOException {
            sock = s;
            out = new DataOutputStream(new java.io.BufferedOutputStream(s.getOutputStream(), 1 << 16));
        }
        void close() { try { sock.close(); } catch (IOException ignored) {} }
    }

    /** Служба управления выключена — уведомление ведёт прямо в её настройки. */
    static void askControl(Context c) {
        android.app.NotificationManager nm = c.getSystemService(android.app.NotificationManager.class);
        nm.createNotificationChannel(new android.app.NotificationChannel("randsec-screen", "Запросы показа экрана",
                android.app.NotificationManager.IMPORTANCE_HIGH));
        Intent i = new Intent(c, CaptureActivity.class).setAction("control").addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
        android.app.PendingIntent pi = android.app.PendingIntent.getActivity(c, 2, i,
                android.app.PendingIntent.FLAG_UPDATE_CURRENT | android.app.PendingIntent.FLAG_IMMUTABLE);
        android.app.Notification n = new android.app.Notification.Builder(c, "randsec-screen")
                .setSmallIcon(R.drawable.ic_stat)
                .setContentTitle("Включите управление с ПК")
                .setContentText("ПК видит экран, но нажимать не может. Специальные возможности → RANDSEC: управление с ПК")
                .setStyle(new android.app.Notification.BigTextStyle().bigText(
                        "ПК видит экран телефона, но нажимать не может. Откройте «Специальные возможности» → «RANDSEC: управление с ПК» и включите."))
                .setContentIntent(pi)
                .setAutoCancel(true)
                .build();
        nm.notify(8, n);
    }

    static Intent captureIntent(Context c) {
        return new Intent(c, CaptureActivity.class).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
    }
}
