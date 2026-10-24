package com.httpsms.gateway.service

import android.content.Context
import android.util.Log
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.httpsms.gateway.core.GatewayConfig
import java.util.concurrent.TimeUnit

/**
 * Runs every ~15 minutes (WorkManager's minimum) and restarts the gateway if the OS or an
 * OEM battery manager killed it while it was supposed to be running. Survives reboots.
 */
class GatewayWatchdogWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val config = GatewayConfig(applicationContext)
        if (!config.serviceEnabled || !config.isPaired) {
            WorkManager.getInstance(applicationContext).cancelUniqueWork(WORK_NAME)
            return Result.success()
        }
        if (GatewayForegroundService.instance == null) {
            Log.w(TAG, "Gateway service not running, restarting")
            GatewayForegroundService.start(applicationContext)
        }
        return Result.success()
    }

    companion object {
        private const val TAG = "GatewayWatchdog"
        private const val WORK_NAME = "gateway-watchdog"

        fun schedule(context: Context) {
            val request = PeriodicWorkRequestBuilder<GatewayWatchdogWorker>(15, TimeUnit.MINUTES).build()
            WorkManager.getInstance(context)
                .enqueueUniquePeriodicWork(WORK_NAME, ExistingPeriodicWorkPolicy.KEEP, request)
        }
    }
}
