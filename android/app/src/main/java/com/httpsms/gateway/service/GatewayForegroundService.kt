package com.httpsms.gateway.service

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.Network
import android.net.wifi.WifiManager
import android.os.BatteryManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import com.google.gson.Gson
import com.google.gson.JsonObject
import com.google.gson.JsonParser
import com.httpsms.gateway.BuildConfig
import com.httpsms.gateway.core.GatewayConfig
import com.httpsms.gateway.core.GatewayState
import com.httpsms.gateway.core.GatewayState.Connection
import com.httpsms.gateway.data.local.AppDatabase
import com.httpsms.gateway.ota.OtaUpdateManager
import com.httpsms.gateway.sms.SmsManagerHandler
import com.httpsms.gateway.ui.MainActivity
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.util.concurrent.ConcurrentLinkedQueue
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
        private const val HEARTBEAT_INTERVAL_MS = 30_000L
        private const val MIN_BACKOFF_MS = 2_000L
        private const val MAX_BACKOFF_MS = 60_000L
        private const val AUTH_RETRY_MS = 5 * 60_000L
        private const val WAKE_LOCK_TIMEOUT_MS = 10 * 60_000L

        /** The running service, so receivers can hand work over without a new start request. */
        @Volatile
        var instance: GatewayForegroundService? = null
            private set

        /** Starts (or pokes) the gateway. Returns false if Android refused a background start. */
        fun start(context: Context, action: String = ACTION_START, extras: Intent? = null): Boolean {
            val intent = Intent(context, GatewayForegroundService::class.java).setAction(action)
            extras?.extras?.let { intent.putExtras(it) }
            return try {
                ContextCompat.startForegroundService(context, intent)
                true
            } catch (e: Exception) {
                // ForegroundServiceStartNotAllowedException (Android 12+) when started from the background
                // without an exemption. Battery-optimisation exemption lifts this restriction.
                Log.w(TAG, "Could not start gateway service: ${e.message}")
                false
            }
        }

        fun stop(context: Context) {
            GatewayConfig(context).serviceEnabled = false
            instance?.shutdown() ?: GatewayState.setConnection(Connection.STOPPED)
        }
    }

    private val serviceScope = CoroutineScope(Dispatchers.IO + SupervisorJob())
    private val gson = Gson()
    private val client = OkHttpClient.Builder()
        .pingInterval(20, TimeUnit.SECONDS)
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(0, TimeUnit.MILLISECONDS)
        .retryOnConnectionFailure(true)
        .build()

    private lateinit var config: GatewayConfig
    private lateinit var smsHandler: SmsManagerHandler
    private lateinit var otaManager: OtaUpdateManager

    private var webSocket: WebSocket? = null
    @Volatile private var isConnected = false
    private var reconnectJob: Job? = null
    private var backoffMs = MIN_BACKOFF_MS
    private var notificationText = "Starting…"
    private val pendingFrames = ConcurrentLinkedQueue<String>()

    private var wakeLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null
    private var networkCallback: ConnectivityManager.NetworkCallback? = null

    override fun onCreate() {
        super.onCreate()
        instance = this
        config = GatewayConfig(this)
        smsHandler = SmsManagerHandler(this)
        otaManager = OtaUpdateManager(this)

        createNotificationChannel()
        startInForeground()
        acquireLocks()
        registerNetworkCallback()
        startHeartbeatLoop()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        // Every startForegroundService() call must be answered with startForeground().
        startInForeground()

        val action = intent?.action
        if (action == ACTION_STOP) {
            config.serviceEnabled = false
            shutdown()
            return START_NOT_STICKY
        }
        if (action == ACTION_START) config.serviceEnabled = true

        if (!config.serviceEnabled || !config.isPaired) {
            // Sticky restart after the user stopped it, or nothing to connect to.
            GatewayState.setConnection(
                Connection.STOPPED,
                if (config.isPaired) "" else "Not paired with a server yet",
            )
            shutdown()
            return START_NOT_STICKY
        }

        GatewayWatchdogWorker.schedule(this)

        when (action) {
            ACTION_FLUSH_OUTBOX -> flushOutbox()
            ACTION_SYNC_STATUS -> {
                val messageId = intent?.getStringExtra("message_id")
                val status = intent?.getStringExtra("status")
                if (messageId != null && status != null) {
                    reportMessageStatus(messageId, status, intent.getStringExtra("error_message") ?: "")
                }
            }
        }

        ensureConnected()
        return START_STICKY
    }

    override fun onDestroy() {
        instance = null
        serviceScope.cancel()
        webSocket?.close(1000, "Service destroyed")
        webSocket = null
        isConnected = false
        unregisterNetworkCallback()
        releaseLocks()
        if (config.serviceEnabled && config.isPaired) {
            GatewayState.setConnection(Connection.DISCONNECTED, "Service was stopped by the system, restarting…")
        }
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    /** Drops the current socket and connects again, e.g. after re-pairing with another server. */
    fun reconnectNow() {
        config.serviceEnabled = true
        backoffMs = MIN_BACKOFF_MS
        serviceScope.launch { connect() }
    }

    fun shutdown() {
        reconnectJob?.cancel()
        webSocket?.close(1000, "Stopped by user")
        webSocket = null
        isConnected = false
        GatewayState.setConnection(Connection.STOPPED, if (config.isPaired) "" else "Not paired with a server yet")
        ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    // region Foreground notification

    private fun startInForeground() {
        val notification = buildNotification(notificationText)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            ServiceCompat.startForeground(
                this, NOTIFICATION_ID, notification,
                ServiceInfo.FOREGROUND_SERVICE_TYPE_REMOTE_MESSAGING,
            )
        } else {
            startForeground(NOTIFICATION_ID, notification)
        }
    }

    private fun createNotificationChannel() {
        val channel = NotificationChannel(
            CHANNEL_ID,
            "Gateway status",
            NotificationManager.IMPORTANCE_LOW,
        ).apply {
            description = "Keeps the SMS gateway connected to your server"
            setShowBadge(false)
        }
        getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
    }

    private fun buildNotification(statusText: String): Notification {
        val openApp = PendingIntent.getActivity(
            this, 0,
            Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val stop = PendingIntent.getService(
            this, 1,
            Intent(this, GatewayForegroundService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("HttpSMS Gateway")
            .setContentText(statusText)
            .setSmallIcon(com.httpsms.gateway.R.drawable.ic_stat_gateway)
            .setContentIntent(openApp)
            .addAction(0, "Stop", stop)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
            .build()
    }

    private fun updateNotification(text: String) {
        notificationText = text
        getSystemService(NotificationManager::class.java).notify(NOTIFICATION_ID, buildNotification(text))
    }

    // endregion

    // region Keep-alive: CPU/Wi-Fi locks, network changes, heartbeat

    private fun acquireLocks() {
        val pm = getSystemService(Context.POWER_SERVICE) as PowerManager
        wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "HttpSms::Gateway").apply {
            setReferenceCounted(false)
            acquire(WAKE_LOCK_TIMEOUT_MS)
        }
        val wm = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
        @Suppress("DEPRECATION") // HIGH_PERF is still the mode that stops Wi-Fi sleeping with the screen off
        wifiLock = wm.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "HttpSms::GatewayWifi").apply {
            setReferenceCounted(false)
            acquire()
        }
    }

    private fun renewWakeLock() {
        // A timed lock that is re-armed by the heartbeat loop: never leaks if the loop dies.
        wakeLock?.acquire(WAKE_LOCK_TIMEOUT_MS)
    }

    private fun releaseLocks() {
        wakeLock?.let { if (it.isHeld) it.release() }
        wifiLock?.let { if (it.isHeld) it.release() }
    }

    private fun registerNetworkCallback() {
        val cm = getSystemService(ConnectivityManager::class.java)
        val callback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                if (!isConnected && config.serviceEnabled) {
                    Log.i(TAG, "Network available, reconnecting now")
                    backoffMs = MIN_BACKOFF_MS
                    serviceScope.launch { connect() }
                }
            }
        }
        try {
            cm.registerDefaultNetworkCallback(callback)
            networkCallback = callback
        } catch (e: Exception) {
            Log.w(TAG, "Network callback unavailable: ${e.message}")
        }
    }

    private fun unregisterNetworkCallback() {
        networkCallback?.let {
            try {
                getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(it)
            } catch (_: Exception) {
            }
        }
        networkCallback = null
    }

    private fun startHeartbeatLoop() {
        serviceScope.launch {
            while (isActive) {
                delay(HEARTBEAT_INTERVAL_MS)
                renewWakeLock()
                val ws = webSocket
                if (isConnected && ws != null) {
                    sendHeartbeat(ws)
                } else {
                    ensureConnected()
                }
            }
        }
    }

    // endregion

    // region WebSocket connection

    @Synchronized
    private fun ensureConnected() {
        if (isConnected || webSocket != null || reconnectJob?.isActive == true) return
        connect()
    }

    @Synchronized
    private fun connect() {
        if (!config.isPaired) return
        reconnectJob?.cancel()
        reconnectJob = null

        // Detach the old socket first so its late callbacks are ignored.
        val old = webSocket
        webSocket = null
        isConnected = false
        old?.cancel()

        val request = try {
            Request.Builder().url(config.webSocketUrl()).build()
        } catch (e: IllegalArgumentException) {
            GatewayState.setConnection(Connection.DISCONNECTED, "Invalid server URL: ${config.serverUrl}")
            updateNotification("Invalid server URL")
            return
        }

        Log.i(TAG, "Connecting to ${config.serverUrl}")
        GatewayState.setConnection(Connection.CONNECTING, config.serverUrl)
        updateNotification("Connecting to ${config.serverUrl}…")
        webSocket = client.newWebSocket(request, Listener())
    }

    @Synchronized
    private fun scheduleReconnect(delayMs: Long) {
        if (reconnectJob?.isActive == true) return
        reconnectJob = serviceScope.launch {
            delay(delayMs)
            connect()
        }
    }

    private fun onDisconnected(ws: WebSocket, reason: String, httpCode: Int?) {
        synchronized(this) {
            if (ws !== webSocket) return
            webSocket = null
            isConnected = false
        }
        if (httpCode == 401) {
            Log.w(TAG, "Server rejected device token")
            GatewayState.setConnection(
                Connection.AUTH_FAILED,
                "The server rejected this phone's token. It may have been unpaired. Scan a new pairing QR.",
            )
            updateNotification("Pairing rejected · scan a new QR code")
            scheduleReconnect(AUTH_RETRY_MS)
            return
        }

        val waitSec = backoffMs / 1000
        Log.w(TAG, "Disconnected: $reason. Retrying in ${waitSec}s")
        GatewayState.setConnection(Connection.DISCONNECTED, "$reason · retrying in ${waitSec}s")
        updateNotification("Offline · retrying in ${waitSec}s")
        scheduleReconnect(backoffMs)
        backoffMs = (backoffMs * 2).coerceAtMost(MAX_BACKOFF_MS)
    }

    private inner class Listener : WebSocketListener() {
        override fun onOpen(webSocket: WebSocket, response: Response) {
            if (webSocket !== this@GatewayForegroundService.webSocket) return
            isConnected = true
            backoffMs = MIN_BACKOFF_MS
            Log.i(TAG, "Connected")
            GatewayState.setConnection(Connection.CONNECTED, config.serverUrl)
            updateNotification("Online · ${config.serverUrl}")
            sendHeartbeat(webSocket)
            flushPendingFrames(webSocket)
            flushOutbox()
        }

        override fun onMessage(webSocket: WebSocket, text: String) {
            if (webSocket === this@GatewayForegroundService.webSocket) handleIncomingFrame(text)
        }

        override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
            webSocket.close(1000, null)
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
            onDisconnected(webSocket, "Connection closed${if (reason.isNotBlank()) ": $reason" else ""}", null)
        }

        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
            val reason = when {
                response != null -> "Server responded HTTP ${response.code}"
                else -> t.message ?: t.javaClass.simpleName
            }
            onDisconnected(webSocket, reason, response?.code)
        }
    }

    /** Sends now if online, otherwise keeps the frame until the next connection. */
    private fun sendOrQueue(frame: String) {
        val ws = webSocket
        if (!(isConnected && ws != null && ws.send(frame))) pendingFrames.add(frame)
    }

    private fun flushPendingFrames(ws: WebSocket) {
        while (true) {
            val frame = pendingFrames.peek() ?: break
            if (!ws.send(frame)) break
            pendingFrames.poll()
        }
    }

    // endregion

    // region Protocol

    private fun sendHeartbeat(ws: WebSocket) {
        val (batteryPct, isCharging) = getBatteryTelemetry()
        val frame = mapOf(
            "action" to "device.heartbeat",
            "data" to mapOf(
                "battery_level" to batteryPct,
                "is_charging" to isCharging,
                "app_version" to BuildConfig.VERSION_NAME,
                "app_version_code" to BuildConfig.VERSION_CODE,
                "sim_cards" to smsHandler.getActiveSimCards(),
            ),
        )
        if (ws.send(gson.toJson(frame))) GatewayState.heartbeatSent()
    }

    private fun getBatteryTelemetry(): Pair<Int, Boolean> {
        val batteryStatus: Intent? = registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
        val level = batteryStatus?.getIntExtra(BatteryManager.EXTRA_LEVEL, -1) ?: -1
        val scale = batteryStatus?.getIntExtra(BatteryManager.EXTRA_SCALE, -1) ?: -1
        val batteryPct = if (level >= 0 && scale > 0) (level * 100) / scale else 0
        val status = batteryStatus?.getIntExtra(BatteryManager.EXTRA_STATUS, -1) ?: -1
        val isCharging = status == BatteryManager.BATTERY_STATUS_CHARGING ||
            status == BatteryManager.BATTERY_STATUS_FULL
        return Pair(batteryPct, isCharging)
    }

    private fun handleIncomingFrame(jsonStr: String) {
        try {
            val root = JsonParser.parseString(jsonStr).asJsonObject
            when (root.get("action")?.asString) {
                "sms.send" -> {
                    val data = root.getAsJsonObject("data")
                    val messageId = data.get("id").asString
                    val recipient = data.get("recipient").asString
                    val body = data.get("body").asString
                    val simSlot = data.get("sim_slot")?.asInt ?: 0

                    Log.i(TAG, "Sending message $messageId")
                    if (smsHandler.sendSms(messageId, recipient, body, simSlot)) {
                        GatewayState.smsSent()
                    } else {
                        reportMessageStatus(messageId, "failed", "Phone could not hand the SMS to the carrier")
                    }
                }
                "ack" -> {
                    val key = root.get("idempotency_key")?.asString ?: return
                    serviceScope.launch {
                        val dao = AppDatabase.getInstance(applicationContext).outboxDao()
                        dao.markAcknowledged(key)
                        dao.deleteAcknowledged()
                    }
                }
                "ota.notify" -> serviceScope.launch {
                    val res = otaManager.checkForUpdates(config.serverUrl)
                    if (res?.update_available == true && res.release != null && res.download_url != null) {
                        otaManager.downloadAndInstall(config.serverUrl, res.download_url, res.release.checksum_sha256)
                    }
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "Error handling incoming frame: ${e.message}", e)
        }
    }

    fun flushOutbox() {
        serviceScope.launch {
            val ws = webSocket
            if (!isConnected || ws == null) return@launch
            val dao = AppDatabase.getInstance(applicationContext).outboxDao()
            for (item in dao.getPending()) {
                val frame = JsonObject().apply {
                    addProperty("action", "sms.received")
                    add("data", JsonParser.parseString(item.payloadJson))
                    addProperty("idempotency_key", item.idempotencyKey)
                }
                if (!ws.send(gson.toJson(frame))) break
                dao.incrementRetry(item.id)
            }
        }
    }

    fun reportMessageStatus(messageId: String, status: String, errorMessage: String) {
        val frame = mapOf(
            "action" to "sms.status",
            "data" to mapOf(
                "message_id" to messageId,
                "status" to status,
                "error_message" to errorMessage,
            ),
        )
        sendOrQueue(gson.toJson(frame))
    }

    // endregion
}
