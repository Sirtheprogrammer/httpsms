package com.httpsms.gateway.core

import android.content.Context
import android.net.Uri
import com.google.gson.JsonParser

/** Persisted pairing + run settings shared by the UI, service, receivers and watchdog. */
class GatewayConfig(context: Context) {
    private val prefs = context.applicationContext
        .getSharedPreferences("httpsms_config", Context.MODE_PRIVATE)

    var serverUrl: String
        get() = prefs.getString(KEY_SERVER_URL, "") ?: ""
        set(value) = prefs.edit().putString(KEY_SERVER_URL, normalizeServerUrl(value)).apply()

    var deviceToken: String
        get() = prefs.getString(KEY_DEVICE_TOKEN, "") ?: ""
        set(value) = prefs.edit().putString(KEY_DEVICE_TOKEN, value.trim()).apply()

    var deviceId: String
        get() = prefs.getString(KEY_DEVICE_ID, "") ?: ""
        set(value) = prefs.edit().putString(KEY_DEVICE_ID, value).apply()

    /** True while the user wants the gateway running; boot/watchdog restarts honour this. */
    var serviceEnabled: Boolean
        get() = prefs.getBoolean(KEY_SERVICE_ENABLED, false)
        set(value) = prefs.edit().putBoolean(KEY_SERVICE_ENABLED, value).apply()

    val isPaired: Boolean
        get() = serverUrl.isNotBlank() && deviceToken.isNotBlank()

    fun webSocketUrl(): String {
        val base = serverUrl
            .replaceFirst("https://", "wss://")
            .replaceFirst("http://", "ws://")
        return "$base/ws/device?token=${Uri.encode(deviceToken)}"
    }

    fun savePairing(pairing: PairingPayload) {
        prefs.edit()
            .putString(KEY_SERVER_URL, normalizeServerUrl(pairing.serverUrl))
            .putString(KEY_DEVICE_TOKEN, pairing.token)
            .putString(KEY_DEVICE_ID, pairing.deviceId)
            .apply()
    }

    companion object {
        private const val KEY_SERVER_URL = "server_url"
        private const val KEY_DEVICE_TOKEN = "device_token"
        private const val KEY_DEVICE_ID = "device_id"
        private const val KEY_SERVICE_ENABLED = "service_enabled"

        fun normalizeServerUrl(raw: String): String {
            var url = raw.trim().trimEnd('/')
            if (url.isNotEmpty() && !url.startsWith("http://") && !url.startsWith("https://")) {
                url = "http://$url"
            }
            return url
        }
    }
}

data class PairingPayload(val serverUrl: String, val token: String, val deviceId: String) {
    companion object {
        /**
         * Parses the dashboard's pairing QR:
         * {"v":1,"server_url":"http://192.168.1.20:8080","device_id":"…","token":"htdev_…"}
         */
        fun parse(raw: String): PairingPayload? = try {
            val obj = JsonParser.parseString(raw.trim()).asJsonObject
            val server = obj.get("server_url")?.asString.orEmpty()
            val token = obj.get("token")?.asString.orEmpty()
            val deviceId = obj.get("device_id")?.asString.orEmpty()
            if (server.isBlank() || !token.startsWith("htdev_")) null
            else PairingPayload(GatewayConfig.normalizeServerUrl(server), token, deviceId)
        } catch (e: Exception) {
            null
        }
    }
}
