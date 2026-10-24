package com.httpsms.gateway

import android.app.Application
import android.util.Log

class GatewayApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        Log.i("HttpSmsGateway", "HttpSMS Gateway Application Initialized")
    }
}

