package io.github.randomaizz.randsec;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;

/** Запускает RANDSEC после включения телефона и после обновления приложения. */
public class BootReceiver extends BroadcastReceiver {
    @Override public void onReceive(Context c, Intent i) {
        String a = i.getAction();
        if (Intent.ACTION_BOOT_COMPLETED.equals(a) || Intent.ACTION_MY_PACKAGE_REPLACED.equals(a)) {
            NodeService.start(c);
        }
    }
}
