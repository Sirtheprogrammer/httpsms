package com.httpsms.gateway.ui

import android.Manifest
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.res.ColorStateList
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.text.format.DateUtils
import android.view.View
import android.widget.Toast
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import com.httpsms.gateway.BuildConfig
import com.httpsms.gateway.R
import com.httpsms.gateway.core.BackgroundSurvival
import com.httpsms.gateway.core.GatewayConfig
import com.httpsms.gateway.core.GatewayState
import com.httpsms.gateway.core.GatewayState.Connection
import com.httpsms.gateway.core.PairingPayload
import com.httpsms.gateway.databinding.ActivityMainBinding
import com.httpsms.gateway.databinding.ItemCheckRowBinding
import com.httpsms.gateway.ota.OtaUpdateManager
import com.httpsms.gateway.service.GatewayForegroundService
import com.httpsms.gateway.sms.SmsManagerHandler
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

class MainActivity : AppCompatActivity() {

    private lateinit var binding: ActivityMainBinding
    private lateinit var config: GatewayConfig
    private lateinit var otaManager: OtaUpdateManager
    private lateinit var smsHandler: SmsManagerHandler

    /** Start the gateway once the permission dialog closes. */
    private var startAfterPermissions = false

    private val smsPermissions: Array<String> = buildList {
        add(Manifest.permission.RECEIVE_SMS)
        add(Manifest.permission.SEND_SMS)
        add(Manifest.permission.READ_PHONE_STATE)
        add(Manifest.permission.READ_PHONE_NUMBERS)
    }.toTypedArray()

    private val permissionLauncher =
        registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) {
            refreshChecklist()
            refreshSims()
            if (startAfterPermissions) {
                startAfterPermissions = false
                if (hasSmsPermissions()) startGateway()
                else toast("SMS permissions are required to run the gateway")
            }
        }

    private val scanLauncher = registerForActivityResult(ScanContract()) { result ->
        val contents = result.contents ?: return@registerForActivityResult
        val pairing = PairingPayload.parse(contents)
        if (pairing == null) {
            toast("That isn't a pairing code. Use Gateway Nodes → Pair New Phone in the dashboard.")
            return@registerForActivityResult
        }
        config.savePairing(pairing)
        fillManualFields()
        refreshPairingSummary()
        toast("Paired with ${pairing.serverUrl}")
        startGateway()
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        binding = ActivityMainBinding.inflate(layoutInflater)
        setContentView(binding.root)

        config = GatewayConfig(this)
        otaManager = OtaUpdateManager(this)
        smsHandler = SmsManagerHandler(this)

        binding.textVersionInfo.text = "Version ${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE})"
        fillManualFields()
        setupActions()
        observeGatewayState()

        if (!hasSmsPermissions()) permissionLauncher.launch(requiredPermissions())
    }

    override fun onResume() {
        super.onResume()
        refreshChecklist()
        refreshSims()
        refreshPairingSummary()
    }

    private fun setupActions() {
        binding.btnToggleService.setOnClickListener {
            val connection = GatewayState.state.value.connection
            if (connection == Connection.STOPPED) startGateway()
            else GatewayForegroundService.stop(this)
        }

        binding.btnScanQr.setOnClickListener {
            scanLauncher.launch(
                ScanOptions()
                    .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                    .setPrompt("Scan the QR code from Gateway Nodes → Pair New Phone")
                    .setBeepEnabled(false)
                    .setOrientationLocked(false)
            )
        }

        binding.btnManualToggle.setOnClickListener {
            val show = binding.layoutManual.visibility != View.VISIBLE
            binding.layoutManual.visibility = if (show) View.VISIBLE else View.GONE
            binding.btnManualToggle.text = if (show) "Hide manual entry" else "Enter details manually"
        }

        binding.btnSaveConfig.setOnClickListener {
            val url = GatewayConfig.normalizeServerUrl(binding.editServerUrl.text?.toString().orEmpty())
            val token = binding.editDeviceToken.text?.toString()?.trim().orEmpty()
            if (url.isEmpty() || token.isEmpty()) {
                toast("Enter both the server URL and the device token")
                return@setOnClickListener
            }
            if (!token.startsWith("htdev_")) {
                toast("Device tokens start with htdev_. Copy it from the dashboard's pairing window.")
                return@setOnClickListener
            }
            config.serverUrl = url
            config.deviceToken = token
            binding.editServerUrl.setText(url)
            refreshPairingSummary()
            startGateway()
        }

        binding.btnCheckOta.setOnClickListener {
            if (!config.isPaired) {
                toast("Pair with a server first")
                return@setOnClickListener
            }
            toast("Checking for updates…")
            lifecycleScope.launch {
                val res = otaManager.checkForUpdates(config.serverUrl)
                when {
                    res == null -> toast("Couldn't reach the server")
                    res.update_available && res.release != null && res.download_url != null -> {
                        toast("Downloading ${res.release.version}…")
                        val ok = otaManager.downloadAndInstall(config.serverUrl, res.download_url, res.release.checksum_sha256)
                        if (!ok) toast("Update download failed")
                    }
                    else -> toast("You're on the latest version")
                }
            }
        }

        binding.btnDontkill.text = "Extra steps for ${Build.MANUFACTURER.replaceFirstChar { it.uppercase() }} phones"
        binding.btnDontkill.setOnClickListener { safeStart(BackgroundSurvival.dontKillMyAppIntent()) }
    }

    private fun startGateway() {
        if (!config.isPaired) {
            toast("Scan the pairing QR code from your dashboard first")
            return
        }
        if (!hasSmsPermissions()) {
            startAfterPermissions = true
            permissionLauncher.launch(requiredPermissions())
            return
        }
        val running = GatewayForegroundService.instance
        if (running != null) {
            // Already running: pick up new pairing details immediately.
            running.reconnectNow()
        } else if (!GatewayForegroundService.start(this)) {
            toast("Android blocked the gateway from starting. Allow it in battery settings.")
        }
    }

    // region Live status

    private fun observeGatewayState() {
        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.STARTED) {
                launch { GatewayState.state.collect { renderStatus(it) } }
                // Keep "up for" / "last heartbeat" ages fresh.
                launch {
                    while (true) {
                        delay(5_000)
                        renderStatus(GatewayState.state.value)
                    }
                }
            }
        }
    }

    private fun renderStatus(s: GatewayState.Snapshot) {
        val (title, colorRes) = when (s.connection) {
            Connection.STOPPED -> "Stopped" to R.color.status_stopped
            Connection.CONNECTING -> "Connecting…" to R.color.status_connecting
            Connection.CONNECTED -> "Online" to R.color.status_online
            Connection.DISCONNECTED -> "Offline" to R.color.status_offline
            Connection.AUTH_FAILED -> "Pairing rejected" to R.color.status_offline
        }
        binding.textStatusTitle.text = title
        binding.statusDot.backgroundTintList = ColorStateList.valueOf(ContextCompat.getColor(this, colorRes))

        binding.textStatusDetail.text = when {
            s.connection == Connection.CONNECTED -> "Connected to ${s.detail}"
            s.connection == Connection.CONNECTING -> "Connecting to ${s.detail}"
            s.detail.isNotBlank() -> s.detail
            !config.isPaired -> "Scan the pairing QR code below to connect this phone to your server."
            else -> "Tap Start to connect to ${config.serverUrl}"
        }

        val stats = buildList {
            if (s.connection == Connection.CONNECTED && s.connectedSince > 0) {
                add("Up ${DateUtils.formatElapsedTime((System.currentTimeMillis() - s.connectedSince) / 1000)}")
            }
            if (s.smsSent > 0 || s.smsReceived > 0) add("${s.smsSent} sent · ${s.smsReceived} received")
            if (s.lastHeartbeatAt > 0 && s.connection == Connection.CONNECTED) {
                add("heartbeat ${DateUtils.getRelativeTimeSpanString(s.lastHeartbeatAt, System.currentTimeMillis(), DateUtils.SECOND_IN_MILLIS)}")
            }
        }
        binding.textStatusStats.text = stats.joinToString(" · ")
        binding.textStatusStats.visibility = if (stats.isEmpty()) View.GONE else View.VISIBLE

        binding.btnToggleService.text =
            if (s.connection == Connection.STOPPED) "Start gateway" else "Stop gateway"
    }

    private fun refreshPairingSummary() {
        binding.textPairingSummary.text = if (config.isPaired) {
            "Paired with ${config.serverUrl}. Scan again to switch servers."
        } else {
            "Not paired. In the dashboard open Gateway Nodes → Pair New Phone, then scan the QR code."
        }
        binding.btnScanQr.text = if (config.isPaired) "Scan a new pairing QR" else "Scan pairing QR"
    }

    private fun fillManualFields() {
        binding.editServerUrl.setText(config.serverUrl)
        binding.editDeviceToken.setText(config.deviceToken)
    }

    private fun refreshSims() {
        if (ContextCompat.checkSelfPermission(this, Manifest.permission.READ_PHONE_STATE) != PackageManager.PERMISSION_GRANTED) {
            binding.textActiveSims.text = "Allow phone permissions to see SIM cards."
            return
        }
        val sims = smsHandler.getActiveSimCards()
        binding.textActiveSims.text = if (sims.isEmpty()) {
            "No active SIM card found."
        } else {
            sims.joinToString("\n") { sim ->
                val number = sim.phoneNumber.ifBlank { "number hidden by carrier" }
                "SIM ${sim.slotIndex + 1} · ${sim.carrierName} · $number"
            }
        }
    }

    // endregion

    // region Background survival checklist

    private fun refreshChecklist() {
        var done = 0
        var total = 0

        fun row(
            row: ItemCheckRowBinding,
            title: String,
            ok: Boolean,
            okText: String,
            todoText: String,
            actionText: String?,
            action: () -> Unit,
        ) {
            total++
            if (ok) done++
            row.rowTitle.text = title
            row.rowSubtitle.text = if (ok) okText else todoText
            row.rowIcon.setImageResource(if (ok) R.drawable.ic_check_circle else R.drawable.ic_warning)
            row.rowAction.visibility = if (ok || actionText == null) View.GONE else View.VISIBLE
            row.rowAction.text = actionText
            row.rowAction.setOnClickListener { action() }
        }

        row(
            binding.rowPermissions, "SMS & phone access", hasSmsPermissions(),
            "Granted", "Needed to send and receive SMS", "Allow",
        ) { requestOrOpenSettings(requiredPermissions()) }

        row(
            binding.rowNotifications, "Status notification",
            NotificationManagerCompat.from(this).areNotificationsEnabled(),
            "Shown while the gateway runs",
            "Android stops background apps that can't show their status", "Allow",
        ) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                requestOrOpenSettings(arrayOf(Manifest.permission.POST_NOTIFICATIONS))
            } else {
                safeStart(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, packageName))
            }
        }

        row(
            binding.rowBattery, "Unrestricted battery",
            BackgroundSurvival.isIgnoringBatteryOptimizations(this),
            "Android won't pause the gateway in Doze",
            "Without this, Android pauses the connection when the screen is off", "Allow",
        ) {
            if (!safeStart(BackgroundSurvival.batteryOptimizationIntent(this))) {
                safeStart(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
            }
        }

        val autostart = BackgroundSurvival.autostartIntent(this)
        val prefs = getSharedPreferences("httpsms_ui", Context.MODE_PRIVATE)
        row(
            binding.rowAutostart, "Autostart (${Build.MANUFACTURER})",
            autostart == null || prefs.getBoolean("autostart_opened", false),
            if (autostart == null) "Not needed on this phone" else "Opened. Make sure HttpSMS Gateway is switched on",
            "This phone has an extra switch that blocks apps from restarting", "Open",
        ) {
            prefs.edit().putBoolean("autostart_opened", true).apply()
            if (autostart == null || !safeStart(autostart)) safeStart(BackgroundSurvival.appDetailsIntent(this))
        }

        binding.textReliabilitySummary.text = if (done == total) {
            "All set. The gateway can stay online around the clock."
        } else {
            "$done of $total done. Finish these so Android doesn't stop the gateway."
        }
    }

    private fun requestOrOpenSettings(permissions: Array<String>) {
        // After a permanent denial the dialog no longer shows; send the user to app settings instead.
        val permanentlyDenied = permissions.any {
            ContextCompat.checkSelfPermission(this, it) != PackageManager.PERMISSION_GRANTED &&
                getSharedPreferences("httpsms_ui", Context.MODE_PRIVATE).getBoolean("asked_$it", false) &&
                !shouldShowRequestPermissionRationale(it)
        }
        if (permanentlyDenied) {
            safeStart(BackgroundSurvival.appDetailsIntent(this))
        } else {
            getSharedPreferences("httpsms_ui", Context.MODE_PRIVATE).edit().apply {
                permissions.forEach { putBoolean("asked_$it", true) }
            }.apply()
            permissionLauncher.launch(permissions)
        }
    }

    // endregion

    private fun requiredPermissions(): Array<String> =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            smsPermissions + Manifest.permission.POST_NOTIFICATIONS
        } else smsPermissions

    private fun hasSmsPermissions(): Boolean = smsPermissions.all {
        ContextCompat.checkSelfPermission(this, it) == PackageManager.PERMISSION_GRANTED
    }

    private fun safeStart(intent: Intent): Boolean = try {
        startActivity(intent)
        true
    } catch (e: ActivityNotFoundException) {
        false
    } catch (e: SecurityException) {
        false
    }

    private fun toast(msg: String) = Toast.makeText(this, msg, Toast.LENGTH_LONG).show()
}
