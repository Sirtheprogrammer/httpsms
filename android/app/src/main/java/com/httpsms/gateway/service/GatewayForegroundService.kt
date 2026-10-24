package com.httpsms.gateway.service

import android.app.*
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.*
import android.util.Log
import androidx.core.app.NotificationCompat
import com.google.gson.Gson
import com.google.gson.JsonObject
import com.google.gson.JsonParser
import com.httpsms.gateway.data.local.AppDatabase
import com.httpsms.gateway.ota.OtaUpdateManager
import com.httpsms.gateway.sms.SmsManagerHandler
import kotlinx.coroutines.*
import okhttp3.*
import java.util.concurrent.TimeUnit

class GatewayForegroundService : Service() {

    companion object {
        const val CHANNEL_ID = "httpsms_gateway_service_channel"
        const val NOTIFICATION_ID = 1001

        const val ACTION_START = "com.httpsms.gateway.START"
        const val ACTION_STOP = "com.httpsms.gateway.STOP"
        const val ACTION_FLUSH_OUTBOX = "com.httpsms.gateway.FLUSH_OUTBOX"
        const val ACTION_SYNC_STATUS = "com.httpsms.gateway.SYNC_STATUS"

        private const val TAG = "GatewayService"
    }

    private val serviceScope = CoroutineScope(Dispatchers.IO + SupervisorJob())
    private var webSocket: WebSocket? = null
    private var wakeLock: PowerManager.WakeLock? = null
    private val gson = Gson()
    private val client = OkHttpClient.Builder()
        .pingInterval(20, TimeUnit.SECONDS)
        .retryOnConnectionFailure(true)
        .build()

    private lateinit var smsHandler: SmsManagerHandler
    private lateinit var otaManager: OtaUpdateManager
    private var isConnected = false

    override fun onCreate() {
        super.onCreate()
        smsHandler = SmsManagerHandler(this)
        otaManager = OtaUpdateManager(this)

        acquireWakeLock()
        createNotificationChannel()
        startForeground(NOTIFICATION_ID, buildNotification("Gateway Initializing..."))

        connectWebSocket()
        startHeartbeatLoop()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                stopForeground(STOP_FOREGROUND_REMOVE)
                stopSelf()
            }
            ACTION_FLUSH_OUTBOX -> {
                flushOutbox()
            }
            ACTION_SYNC_STATUS -> {
                val messageId = intent.getStringExtra("message_id")
                val status = intent.getStringExtra("status")
                val errorMessage = intent.getStringExtra("error_message") ?: ""
                if (messageId != null && status != null) {
                    reportMessageStatus(messageId, status, errorMessage)
                }
            }
            else -> {
                flushOutbox()
            }
        }
        return START_STICKY
    }

    override fun onDestroy() {
        super.onDestroy()
        serviceScope.cancel()
        webSocket?.close(1000, "Service destroyed")
        releaseWakeLock()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun acquireWakeLock() {
        val powerManager = getSystemService(Context.POWER_SERVICE) as PowerManager
        wakeLock = powerManager.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "HttpSms::GatewayWakeLock").apply {
            setReferenceCounted(false)
            acquire(24 * 60 * 60 * 1000L) // 24h
        }
    }

    private fun releaseWakeLock() {
        wakeLock?.let {
            if (it.isHeld) it.release()
        }
    }

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "HttpSMS Gateway Live Telemetry",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "Keeps persistent cellular modem bridge online"
            }
            val manager = getSystemService(NotificationManager::class.java)
            manager.createNotificationChannel(channel)
        }
    }

    private fun buildNotification(statusText: String): Notification {
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("HttpSMS Gateway")
            .setContentText(statusText)
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setOngoing(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .build()
    }

    private fun updateNotification(text: String) {
        val manager = getSystemService(NotificationManager::class.java)
        manager.notify(NOTIFICATION_ID, buildNotification(text))
    }

    private fun getSavedServerConfig(): Pair<String, String> {
        val prefs = getSharedPreferences("httpsms_config", Context.MODE_PRIVATE)
        val serverUrl = prefs.getString("server_url", "http://10.0.2.2:8080") ?: "http://10.0.2.2:8080"
        val token = prefs.getString("device_token", "htdev_default") ?: "htdev_default"
        return Pair(serverUrl, token)
    }

    private fun connectWebSocket() {
        serviceScope.launch {
            val (serverUrl, token) = getSavedServerConfig()
            val wsBase = serverUrl.replace("http://", "ws://").replace("https://", "wss://")
            val wsUrl = "$wsBase/ws/device?token=$token"

            Log.i(TAG, "Connecting WebSocket to: $wsUrl")
            val request = Request.Builder().url(wsUrl).build()

            client.newWebSocket(request, object : WebSocketListener() {
                override fun onOpen(ws: WebSocket, response: Response) {
                    webSocket = ws
                    isConnected = true
                    Log.i(TAG, "WebSocket Connected successfully")
                    updateNotification("Online · Connected to Gateway Server")

                    sendHeartbeat(ws)
                    flushOutbox()
                }

                override fun onMessage(ws: WebSocket, text: String) {
                    handleIncomingFrame(text)
                }

                override fun onClosed(ws: WebSocket, code: Int, reason: String) {
                    isConnected = false
                    Log.w(TAG, "WebSocket closed: $reason ($code)")
                    updateNotification("Disconnected · Reconnecting...")
                    scheduleReconnect()
                }

                override fun onFailure(ws: WebSocket, t: Throwable, response: Response?) {
                    isConnected = false
                    Log.e(TAG, "WebSocket failure: ${t.message}")
                    updateNotification("Connection Error · Retrying...")
                    scheduleReconnect()
                }
            })
        }
    }

    private fun scheduleReconnect() {
        serviceScope.launch {
            delay(5000)
            if (!isConnected) {
                connectWebSocket()
            }
        }
    }

    private fun startHeartbeatLoop() {
        serviceScope.launch {
            while (isActive) {
                delay(30000)
                webSocket?.let { ws ->
                    if (isConnected) {
                        sendHeartbeat(ws)
                    }
                }
            }
        }
    }

    private fun sendHeartbeat(ws: WebSocket) {
        val batteryStatus = getBatteryTelemetry()
        val sims = smsHandler.getActiveSimCards()

        val data = mapOf(
            "battery_level" to batteryStatus.first,
            "is_charging" to batteryStatus.second,
            "app_version" to "1.0.0",
            "app_version_code" to otaManager.getCurrentVersionCode(),
            "sim_cards" to sims
        )

        val frame = mapOf(
            "action" to "device.heartbeat",
            "data" to data
        )

        ws.send(gson.toJson(frame))
    }

    private fun getBatteryTelemetry(): Pair<Int, Boolean> {
        val ifilter = IntentFilter(Intent.ACTION_BATTERY_CHANGED)
        val batteryStatus: Intent? = registerReceiver(null, ifilter)

        val level = batteryStatus?.getIntExtra(BatteryManager.EXTRA_LEVEL, -1) ?: 50
        val scale = batteryStatus?.getIntExtra(BatteryManager.EXTRA_SCALE, -1) ?: 100
        val batteryPct = ((level / scale.toFloat()) * 100).toInt()

        val status = batteryStatus?.getIntExtra(BatteryManager.EXTRA_STATUS, -1) ?: -1
        val isCharging = status == BatteryManager.BATTERY_STATUS_CHARGING ||
                status == BatteryManager.BATTERY_STATUS_FULL

        return Pair(batteryPct, isCharging)
    }

    private fun handleIncomingFrame(jsonStr: String) {
        try {
            val root = JsonParser.parseString(jsonStr).asJsonObject
            val action = root.get("action")?.asString ?: return

            when (action) {
                "sms.send" -> {
                    val data = root.getAsJsonObject("data")
                    val messageId = data.get("id").asString
                    val recipient = data.get("recipient").asString
                    val body = data.get("body").asString
                    val simSlot = data.get("sim_slot")?.asInt ?: 0

                    Log.i(TAG, "Processing send command for message $messageId to $recipient")
                    val dispatched = smsHandler.sendSms(messageId, recipient, body, simSlot)
                    if (!dispatched) {
                        reportMessageStatus(messageId, "failed", "Local SmsManager dispatch failure")
                    }
                }
                "ack" -> {
                    val key = root.get("idempotency_key")?.asString
                    if (key != null) {
                        serviceScope.launch {
                            val db = AppDatabase.getInstance(applicationContext)
                            db.outboxDao().markAcknowledged(key)
                            db.outboxDao().deleteAcknowledged()
                            Log.i(TAG, "Server ACK received for key: $key")
                        }
                    }
                }
                "ota.notify" -> {
                    // Check for update
                    serviceScope.launch {
                        val (serverUrl, _) = getSavedServerConfig()
                        val res = otaManager.checkForUpdates(serverUrl)
                        if (res?.update_available == true && res.release != null && res.download_url != null) {
                            Log.i(TAG, "New OTA release available: ${res.release.version}. Starting download...")
                            otaManager.downloadAndInstall(serverUrl, res.download_url, res.release.checksum_sha256)
                        }
                    }
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "Error handling incoming frame: ${e.message}", e)
        }
    }

    fun flushOutbox() {
        serviceScope.launch {
            if (!isConnected || webSocket == null) return@launch
            val db = AppDatabase.getInstance(applicationContext)
            val pending = db.outboxDao().getPending()

            for (item in pending) {
                val frame = JsonObject().apply {
                    addProperty("action", "sms.received")
                    add("data", JsonParser.parseString(item.payloadJson))
                    addProperty("idempotency_key", item.idempotencyKey)
                }

                val success = webSocket?.send(gson.toJson(frame)) ?: false
                if (success) {
                    db.outboxDao().incrementRetry(item.id)
                }
            }
        }
    }

    private fun reportMessageStatus(messageId: String, status: String, errorMessage: String) {
        val frame = mapOf(
            "action" to "sms.status",
            "data" to mapOf(
                "message_id" to messageId,
                "status" to status,
                "error_message" to errorMessage
            )
        )
        webSocket?.send(gson.toJson(frame))
    }
}

