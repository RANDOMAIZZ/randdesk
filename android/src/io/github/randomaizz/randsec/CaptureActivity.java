package io.github.randomaizz.randsec;

import android.app.Activity;
import android.content.Intent;
import android.media.projection.MediaProjectionManager;
import android.content.ComponentName;
import android.content.Context;
import android.os.Build;
import android.os.Bundle;
import android.provider.Settings;
import android.widget.Toast;

/** Прозрачное окно: системный запрос «Начать показ экрана?». */
public class CaptureActivity extends Activity {
    private static final int REQ = 1;

    @Override protected void onCreate(Bundle b) {
        super.onCreate(b);
        if (b != null) return; // запрос уже на экране
        if ("control".equals(getIntent().getAction())) { // из уведомления «Включите управление с ПК»
            openControlSettings(this);
            finish();
            return;
        }
        try {
            startActivityForResult(getSystemService(MediaProjectionManager.class).createScreenCaptureIntent(), REQ);
        } catch (Exception e) {
            ScreenCapture.denied(this);
            finish();
        }
    }

    @Override protected void onActivityResult(int req, int code, Intent data) {
        if (req == REQ) {
            if (code == RESULT_OK && data != null) {
                ScreenCapture.granted(getApplicationContext(), code, data);
                if (!ControlService.enabled()) openControlSettings(this);
            } else ScreenCapture.denied(this);
        }
        finish();
    }

    /**
     * Открывает настройки службы управления: сразу её страницу (Android 11+), иначе список
     * специальных возможностей с подсвеченной строкой RANDSEC.
     */
    static void openControlSettings(Context c) {
        ComponentName cn = new ComponentName(c, ControlService.class);
        String flat = cn.flattenToString();
        Bundle args = new Bundle();
        args.putString(":settings:fragment_args_key", flat);
        Intent details = new Intent("android.settings.ACCESSIBILITY_DETAILS_SETTINGS")
                .putExtra(Intent.EXTRA_COMPONENT_NAME, flat)
                .putExtra(":settings:fragment_args_key", flat)
                .putExtra(":settings:show_fragment_args", args)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
        Intent list = new Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS)
                .putExtra(":settings:fragment_args_key", flat)
                .putExtra(":settings:show_fragment_args", args)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
        Toast.makeText(c, "Прокрутите вниз → «Скачанные приложения» → «RANDSEC: управление с ПК» → включите. Тогда ПК сможет нажимать на экран.", Toast.LENGTH_LONG).show();
        if (Build.VERSION.SDK_INT >= 30) {
            try { c.startActivity(details); return; } catch (Exception ignored) {}
        }
        try { c.startActivity(list); } catch (Exception ignored) {}
    }
}
