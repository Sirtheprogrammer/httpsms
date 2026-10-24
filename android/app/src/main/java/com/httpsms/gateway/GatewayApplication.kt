package com.httpsms.gateway

import android.app.Application
import com.httpsms.gateway.core.GatewayConfig
import com.httpsms.gateway.service.GatewayWatchdogWorker

class GatewayApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        // Re-arm the watchdog whenever the process starts while the gateway should be running.
        val config = GatewayConfig(this)
        if (config.serviceEnabled && config.isPaired) GatewayWatchdogWorker.schedule(this)
    }
}
