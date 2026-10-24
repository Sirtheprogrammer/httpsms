package com.httpsms.gateway.ota

import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.util.Log
import androidx.core.content.FileProvider
import com.google.gson.Gson
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.File
import java.io.FileOutputStream
import java.security.MessageDigest

data class OtaCheckResponse(
    val update_available: Boolean,
    val release: OtaReleaseDto?,
    val download_url: String?
)

data class OtaReleaseDto(
    val id: String,
    val version: String,
    val version_code: Int,
    val checksum_sha256: String,
    val file_size: Long,
    val changelog: String
)

class OtaUpdateManager(private val context: Context) {
    private val client = OkHttpClient()
    private val gson = Gson()

    companion object {
        private const val TAG = "OtaUpdateManager"
    }

    fun getCurrentVersionCode(): Int {
        return try {
            val pInfo = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                context.packageManager.getPackageInfo(context.packageName, PackageManager.PackageInfoFlags.of(0))
            } else {
                @Suppress("DEPRECATION")
                context.packageManager.getPackageInfo(context.packageName, 0)
            }
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                pInfo.longVersionCode.toInt()
            } else {
                @Suppress("DEPRECATION")
                pInfo.versionCode
            }
        } catch (e: Exception) {
            1
        }
    }

    suspend fun checkForUpdates(serverUrl: String): OtaCheckResponse? = withContext(Dispatchers.IO) {
        val currentCode = getCurrentVersionCode()
        val url = "$serverUrl/api/v1/ota/check?target=android&version_code=$currentCode"

        try {
            val req = Request.Builder().url(url).build()
            client.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) return@withContext null
                val body = resp.body?.string() ?: return@withContext null
                return@withContext gson.fromJson(body, OtaCheckResponse::class.java)
            }
        } catch (e: Exception) {
            Log.e(TAG, "OTA update check error: ${e.message}")
            null
        }
    }

    suspend fun downloadAndInstall(serverUrl: String, downloadPath: String, expectedChecksum: String): Boolean = withContext(Dispatchers.IO) {
        val fullUrl = if (downloadPath.startsWith("http")) downloadPath else "$serverUrl$downloadPath"

        try {
            val req = Request.Builder().url(fullUrl).build()
            client.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) return@withContext false

                val updatesDir = File(context.cacheDir, "updates")
                if (!updatesDir.exists()) updatesDir.mkdirs()

                val apkFile = File(updatesDir, "update.apk")
                val digest = MessageDigest.getInstance("SHA-256")

                resp.body?.byteStream()?.use { input ->
                    FileOutputStream(apkFile).use { output ->
                        val buffer = ByteArray(8192)
                        var bytesRead: Int
                        while (input.read(buffer).also { bytesRead = it } != -1) {
                            output.write(buffer, 0, bytesRead)
                            digest.update(buffer, 0, bytesRead)
                        }
                        output.flush()
                    }
                }

                // Verify checksum
                val computedHash = digest.digest().joinToString("") { "%02x".format(it) }
                if (!computedHash.equals(expectedChecksum, ignoreCase = true)) {
                    Log.e(TAG, "SHA-256 verification failed! Expected: $expectedChecksum, Computed: $computedHash")
                    apkFile.delete()
                    return@withContext false
                }

                Log.i(TAG, "APK downloaded & verified successfully. Launching package installer...")
                launchInstaller(apkFile)
                return@withContext true
            }
        } catch (e: Exception) {
            Log.e(TAG, "Download and install failed: ${e.message}", e)
            false
        }
    }

    private fun launchInstaller(apkFile: File) {
        val uri: Uri = FileProvider.getUriForFile(
            context,
            "${context.packageName}.fileprovider",
            apkFile
        )

        val installIntent = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, "application/vnd.android.package-archive")
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        }

        context.startActivity(installIntent)
    }
}

