package com.httpsms.gateway.ui

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.widget.Button
import android.widget.EditText
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.lifecycle.lifecycleScope
import com.httpsms.gateway.R
import com.httpsms.gateway.ota.OtaUpdateManager
import com.httpsms.gateway.service.GatewayForegroundService
import com.httpsms.gateway.sms.SmsManagerHandler
import kotlinx.coroutines.launch

class MainActivity : AppCompatActivity() {

    private lateinit var otaManager: OtaUpdateManager
    private lateinit var smsHandler: SmsManagerHandler

    companion object {
        private const val PERMISSIONS_REQUEST_CODE = 101
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        otaManager = OtaUpdateManager(this)
        smsHandler = SmsManagerHandler(this)

        checkAndRequestPermissions()
        setupUI()
    }

    private fun checkAndRequestPermissions() {
        val permissions = mutableListOf(
            Manifest.permission.RECEIVE_SMS,
            Manifest.permission.SEND_SMS,
            Manifest.permission.READ_PHONE_STATE
        )

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            permissions.add(Manifest.permission.READ_PHONE_NUMBERS)
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            permissions.add(Manifest.permission.POST_NOTIFICATIONS)
        }

        val missing = permissions.filter {
            ContextCompat.checkSelfPermission(this, it) != PackageManager.PERMISSION_GRANTED
        }

        if (missing.isNotEmpty()) {
            ActivityCompat.requestPermissions(this, missing.toTypedArray(), PERMISSIONS_REQUEST_CODE)
        }
    }

    private fun setupUI() {
        val prefs = getSharedPreferences("httpsms_config", Context.MODE_PRIVATE)

        val serverInput = findViewById<EditText>(R.id.edit_server_url)
        val tokenInput = findViewById<EditText>(R.id.edit_device_token)
        val saveBtn = findViewById<Button>(R.id.btn_save_config)
        val startBtn = findViewById<Button>(R.id.btn_start_service)
        val otaBtn = findViewById<Button>(R.id.btn_check_ota)
        val simsText = findViewById<TextView>(R.id.text_active_sims)
        val versionText = findViewById<TextView>(R.id.text_version_info)

        serverInput.setText(prefs.getString("server_url", "http://10.0.2.2:8080"))
        tokenInput.setText(prefs.getString("device_token", "htdev_default"))
        versionText.text = "Version: 1.0.0 (Code: ${otaManager.getCurrentVersionCode()})"

        // Display detected SIMs
        val sims = smsHandler.getActiveSimCards()
        if (sims.isNotEmpty()) {
            val sb = StringBuilder()
            sims.forEach { s ->
                sb.append("Slot ${s.slotIndex + 1}: ${s.carrierName} (${s.phoneNumber})\n")
            }
            simsText.text = sb.toString().trim()
        } else {
            simsText.text = "No SIM cards detected or permissions pending"
        }

        saveBtn.setOnClickListener {
            val sUrl = serverInput.text.toString().trim()
            val dTok = tokenInput.text.toString().trim()

            prefs.edit().putString("server_url", sUrl).putString("device_token", dTok).apply()
            Toast.makeText(this, "Configuration saved!", Toast.LENGTH_SHORT).show()
        }

        startBtn.setOnClickListener {
            val serviceIntent = Intent(this, GatewayForegroundService::class.java).apply {
                action = GatewayForegroundService.ACTION_START
            }
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                startForegroundService(serviceIntent)
            } else {
                startService(serviceIntent)
            }
            Toast.makeText(this, "Gateway Service Started!", Toast.LENGTH_SHORT).show()
        }

        otaBtn.setOnClickListener {
            val sUrl = serverInput.text.toString().trim()
            Toast.makeText(this, "Checking for OTA updates...", Toast.LENGTH_SHORT).show()

            lifecycleScope.launch {
                val res = otaManager.checkForUpdates(sUrl)
                if (res?.update_available == true && res.release != null && res.download_url != null) {
                    Toast.makeText(this@MainActivity, "Update found: ${res.release.version}. Downloading...", Toast.LENGTH_LONG).show()
                    otaManager.downloadAndInstall(sUrl, res.download_url, res.release.checksum_sha256)
                } else {
                    Toast.makeText(this@MainActivity, "App is up to date!", Toast.LENGTH_SHORT).show()
                }
            }
        }
    }
}

