package com.httpsms.gateway.core

import android.annotation.SuppressLint
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings

/**
 * Helpers for keeping the gateway alive: Doze/battery-optimisation exemption and the
 * vendor-specific "autostart"/"app launch" screens that aggressive OEMs hide it behind.
 */
object BackgroundSurvival {

    fun isIgnoringBatteryOptimizations(context: Context): Boolean {
        val pm = context.getSystemService(Context.POWER_SERVICE) as PowerManager
        return pm.isIgnoringBatteryOptimizations(context.packageName)
    }

    @SuppressLint("BatteryLife") // An SMS gateway is an accepted use case for this exemption.
    fun batteryOptimizationIntent(context: Context): Intent =
        Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS)
            .setData(Uri.parse("package:${context.packageName}"))

    private val autostartComponents = listOf(
        // Xiaomi / Redmi / POCO
        ComponentName("com.miui.securitycenter", "com.miui.permcenter.autostart.AutoStartManagementActivity"),
        // Huawei / Honor
        ComponentName("com.huawei.systemmanager", "com.huawei.systemmanager.startupmgr.ui.StartupNormalAppListActivity"),
        ComponentName("com.huawei.systemmanager", "com.huawei.systemmanager.optimize.process.ProtectActivity"),
        ComponentName("com.hihonor.systemmanager", "com.hihonor.systemmanager.startupmgr.ui.StartupNormalAppListActivity"),
        // Oppo / Realme / OnePlus (ColorOS)
        ComponentName("com.coloros.safecenter", "com.coloros.safecenter.permission.startup.StartupAppListActivity"),
        ComponentName("com.coloros.safecenter", "com.coloros.safecenter.startupapp.StartupAppListActivity"),
        ComponentName("com.oppo.safe", "com.oppo.safe.permission.startup.StartupAppListActivity"),
        // Vivo / iQOO
        ComponentName("com.vivo.permissionmanager", "com.vivo.permissionmanager.activity.BgStartUpManagerActivity"),
        ComponentName("com.iqoo.secure", "com.iqoo.secure.ui.phoneoptimize.AddWhiteListActivity"),
        // Samsung (Device care > Battery)
        ComponentName("com.samsung.android.lool", "com.samsung.android.sm.battery.ui.BatteryActivity"),
        // Asus
        ComponentName("com.asus.mobilemanager", "com.asus.mobilemanager.autostart.AutoStartActivity"),
        // Letv
        ComponentName("com.letv.android.letvsafe", "com.letv.android.letvsafe.AutobootManageActivity"),
    )

    /** The vendor autostart screen if this phone has one, else null. */
    fun autostartIntent(context: Context): Intent? {
        val pm = context.packageManager
        return autostartComponents
            .map { Intent().setComponent(it).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK) }
            .firstOrNull { it.resolveActivity(pm) != null }
    }

    fun appDetailsIntent(context: Context): Intent =
        Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:${context.packageName}"))

    /** Per-manufacturer instructions maintained by the community. */
    fun dontKillMyAppIntent(): Intent {
        val vendor = Build.MANUFACTURER.lowercase().replace(" ", "-")
        return Intent(Intent.ACTION_VIEW, Uri.parse("https://dontkillmyapp.com/$vendor"))
    }
}
