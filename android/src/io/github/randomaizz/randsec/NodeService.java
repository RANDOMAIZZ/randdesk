package io.github.randomaizz.randsec;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.net.ConnectivityManager;
import android.net.LinkProperties;
import android.net.Network;
import android.net.wifi.WifiManager;
import android.os.BatteryManager;
import android.os.Build;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;
import android.os.PowerManager;
import android.provider.Settings;
import android.util.DisplayMetrics;
import android.util.Log;
import android.view.WindowManager;

import java.io.File;
import java.io.FileOutputStream;
import java.net.InetAddress;
import java.nio.charset.StandardCharsets;
import java.util.Map;

/**
 * Служба RANDSEC: держит запущенным узел RANDSEC (Go-программа librandsec.so из папки
 * нативных библиотек приложения) и раз в полминуты записывает для него сведения о телефоне.
 * Работает в фоне с постоянным уведомлением — так Android её не останавливает.
 */
public class NodeService extends Service {
    static final String TAG = "RANDSEC";
    static final String CHANNEL = "randsec";
    static final int PORT = 8420;

    private volatile boolean running;
    private volatile Process proc;
    private Thread worker;
    private WifiManager.MulticastLock mlock;
    private WifiManager.WifiLock wlock;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final Runnable infoTick = new Runnable() {
        @Override public void run() {
            writeInfo();
            main.postDelayed(this, 2_000);
        }
    };

    static void start(Context c) {
        Intent i = new Intent(c, NodeService.class);
        if (Build.VERSION.SDK_INT >= 26) c.startForegroundService(i);
        else c.startService(i);
    }

    static File dataDir(Context c) {
        File d = new File(c.getFilesDir(), "data");
        d.mkdirs();
        return d;
    }

    @Override public void onCreate() {
        super.onCreate();
        NotificationManager nm = getSystemService(NotificationManager.class);
        NotificationChannel ch = new NotificationChannel(CHANNEL, "Работа RANDSEC", NotificationManager.IMPORTANCE_LOW);
        ch.setDescription("RANDSEC работает в фоне и на связи с вашими ПК");
        ch.setShowBadge(false);
        nm.createNotificationChannel(ch);
        PendingIntent open = PendingIntent.getActivity(this, 0, new Intent(this, MainActivity.class),
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
        Notification n = new Notification.Builder(this, CHANNEL)
                .setSmallIcon(R.drawable.ic_stat)
                .setContentTitle("RANDSEC на связи")
                .setContentText("Управление ПК и этим телефоном")
                .setContentIntent(open)
                .setOngoing(true)
                .build();
        startForeground(1, n);

        WifiManager wm = (WifiManager) getApplicationContext().getSystemService(Context.WIFI_SERVICE);
        if (wm != null) {
            // без этого Android выбрасывает широковещательные пакеты — ПК в сети не найдутся
            mlock = wm.createMulticastLock("randsec");
            mlock.setReferenceCounted(false);
            mlock.acquire();
            wlock = wm.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "randsec");
            wlock.setReferenceCounted(false);
            wlock.acquire();
        }
        main.post(infoTick);
        ScreenBridge.start(this);
        running = true;
        worker = new Thread(this::superviseNode, "randsec-node");
        worker.start();
    }

    @Override public int onStartCommand(Intent intent, int flags, int startId) {
        return START_STICKY;
    }

    @Override public void onDestroy() {
        running = false;
        main.removeCallbacks(infoTick);
        Process p = proc;
        if (p != null) p.destroy();
        if (worker != null) worker.interrupt();
        if (mlock != null && mlock.isHeld()) mlock.release();
        if (wlock != null && wlock.isHeld()) wlock.release();
        super.onDestroy();
    }

    @Override public IBinder onBind(Intent intent) { return null; }

    /** Запускает узел RANDSEC и перезапускает его, если он завершился. */
    private void superviseNode() {
        File exe = new File(getApplicationInfo().nativeLibraryDir, "librandsec.so");
        File data = dataDir(this);
        File log = new File(data, "randdesk.log");
        long backoff = 2000;
        while (running) {
            long started = System.currentTimeMillis();
            try {
                if (log.length() > 5L << 20) log.renameTo(new File(data, "randdesk.log.old"));
                ProcessBuilder pb = new ProcessBuilder(exe.getAbsolutePath(), "node", "console",
                        "-dir", data.getAbsolutePath(), "-port", String.valueOf(PORT));
                Map<String, String> env = pb.environment();
                env.put("RANDSEC_ANDROID", "1");
                env.put("RANDSEC_HOSTNAME", deviceName());
                env.put("RANDSEC_DNS", dnsServers());
                env.put("RANDSEC_SCREEN", "127.0.0.1:" + ScreenBridge.PORT + "/" + ScreenBridge.TOKEN);
                env.put("RANDSEC_ANDROID_INFO", new File(data, "android.json").getAbsolutePath());
                env.put("HOME", getFilesDir().getAbsolutePath());
                env.put("TMPDIR", getCacheDir().getAbsolutePath());
                env.put("TZ", java.util.TimeZone.getDefault().getID());
                env.put("SSL_CERT_DIR", "/system/etc/security/cacerts"); // корневые сертификаты Android
                pb.directory(data);
                pb.redirectErrorStream(true);
                pb.redirectOutput(ProcessBuilder.Redirect.appendTo(log));
                writeInfo();
                proc = pb.start();
                int code = proc.waitFor();
                Log.w(TAG, "узел RANDSEC завершился с кодом " + code);
            } catch (InterruptedException e) {
                break;
            } catch (Exception e) {
                Log.e(TAG, "не удалось запустить узел RANDSEC", e);
            } finally {
                proc = null;
            }
            if (!running) break;
            if (System.currentTimeMillis() - started > 60_000) backoff = 2000;
            try { Thread.sleep(backoff); } catch (InterruptedException e) { break; }
            backoff = Math.min(backoff * 2, 30_000);
        }
    }

    /** Имя телефона из настроек («Об устройстве → Имя»), иначе — модель. */
    private String deviceName() {
        String n = null;
        try { n = Settings.Global.getString(getContentResolver(), "device_name"); } catch (Exception ignored) {}
        if (n == null || n.trim().isEmpty()) {
            try { n = Settings.Secure.getString(getContentResolver(), "bluetooth_name"); } catch (Exception ignored) {}
        }
        if (n == null || n.trim().isEmpty()) n = Build.MODEL;
        return n.trim();
    }

    private String dnsServers() {
        StringBuilder sb = new StringBuilder();
        try {
            ConnectivityManager cm = getSystemService(ConnectivityManager.class);
            Network net = cm.getActiveNetwork();
            LinkProperties lp = net == null ? null : cm.getLinkProperties(net);
            if (lp != null) for (InetAddress a : lp.getDnsServers()) {
                if (sb.length() > 0) sb.append(',');
                sb.append(a.getHostAddress());
            }
        } catch (Exception ignored) {}
        return sb.toString();
    }

    /** Сведения о телефоне для узла: модель, версия Android, батарея, экран. */
    private void writeInfo() {
        try {
            Intent b = registerReceiver(null, new IntentFilter(Intent.ACTION_BATTERY_CHANGED));
            int level = -1, plugged = 0, status = 0, temp = 0;
            if (b != null) {
                int l = b.getIntExtra(BatteryManager.EXTRA_LEVEL, -1), s = b.getIntExtra(BatteryManager.EXTRA_SCALE, 100);
                if (l >= 0 && s > 0) level = Math.round(l * 100f / s);
                plugged = b.getIntExtra(BatteryManager.EXTRA_PLUGGED, 0);
                status = b.getIntExtra(BatteryManager.EXTRA_STATUS, 0);
                temp = b.getIntExtra(BatteryManager.EXTRA_TEMPERATURE, 0);
            }
            boolean charging = status == BatteryManager.BATTERY_STATUS_CHARGING || status == BatteryManager.BATTERY_STATUS_FULL;
            String plug = plugged == BatteryManager.BATTERY_PLUGGED_AC ? "ac"
                    : plugged == BatteryManager.BATTERY_PLUGGED_USB ? "usb"
                    : plugged == BatteryManager.BATTERY_PLUGGED_WIRELESS ? "wireless" : "";
            DisplayMetrics dm = new DisplayMetrics();
            ((WindowManager) getSystemService(Context.WINDOW_SERVICE)).getDefaultDisplay().getRealMetrics(dm);
            String json = "{"
                    + "\"model\":" + q(Build.MODEL)
                    + ",\"manufacturer\":" + q(Build.MANUFACTURER)
                    + ",\"android\":" + q(Build.VERSION.RELEASE)
                    + ",\"sdk\":" + Build.VERSION.SDK_INT
                    + ",\"battery\":" + level
                    + ",\"charging\":" + charging
                    + ",\"plugged\":" + q(plug)
                    + ",\"temp\":" + (temp / 10.0)
                    + ",\"rx\":" + Math.max(0, android.net.TrafficStats.getTotalRxBytes())
                    + ",\"tx\":" + Math.max(0, android.net.TrafficStats.getTotalTxBytes())
                    + ",\"t\":" + System.currentTimeMillis()
                    + ",\"screen\":" + q(Math.min(dm.widthPixels, dm.heightPixels) + "×" + Math.max(dm.widthPixels, dm.heightPixels))
                    + "}";
            File f = new File(dataDir(this), "android.json");
            File tmp = new File(f.getPath() + ".tmp");
            try (FileOutputStream o = new FileOutputStream(tmp)) { o.write(json.getBytes(StandardCharsets.UTF_8)); }
            tmp.renameTo(f);
        } catch (Exception e) {
            Log.w(TAG, "сведения о телефоне", e);
        }
    }

    private static String q(String s) {
        if (s == null) s = "";
        StringBuilder sb = new StringBuilder("\"");
        for (char c : s.toCharArray()) {
            if (c == '"' || c == '\\') sb.append('\\').append(c);
            else if (c < 0x20) sb.append(String.format("\\u%04x", (int) c));
            else sb.append(c);
        }
        return sb.append('"').toString();
    }

    static boolean ignoringBattery(Context c) {
        PowerManager pm = c.getSystemService(PowerManager.class);
        return pm != null && pm.isIgnoringBatteryOptimizations(c.getPackageName());
    }
}
