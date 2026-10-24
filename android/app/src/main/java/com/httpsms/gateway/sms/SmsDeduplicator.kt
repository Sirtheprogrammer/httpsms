package com.httpsms.gateway.sms

import android.content.Context
import com.httpsms.gateway.data.local.AppDatabase
import com.httpsms.gateway.data.local.SmsDedupEntity
import java.security.MessageDigest

class SmsDeduplicator(context: Context) {
    private val db = AppDatabase.getInstance(context)
    private val dedupDao = db.dedupDao()

    /**
     * Checks if the message has already been processed within the dedup window.
     * If unique, inserts the fingerprint into the dedup table and returns true.
     * If duplicate, returns false.
     */
    suspend fun isUniqueAndRecord(
        sender: String,
        body: String,
        timestamp: Long,
        simSlot: Int
    ): Pair<Boolean, String> {
        val fingerprint = computeFingerprint(sender, body, timestamp, simSlot)

        // Prune entries older than 48 hours to keep the local database compact
        val fortyEightHoursAgo = System.currentTimeMillis() - (48 * 60 * 60 * 1000L)
        dedupDao.pruneOldRecords(fortyEightHoursAgo)

        val exists = dedupDao.exists(fingerprint) > 0
        if (exists) {
            return Pair(false, fingerprint)
        }

        val rowId = dedupDao.insert(SmsDedupEntity(fingerprint = fingerprint, createdAt = timestamp))
        return Pair(rowId != -1L, fingerprint)
    }

    companion object {
        fun computeFingerprint(
            sender: String,
            body: String,
            timestamp: Long,
            simSlot: Int
        ): String {
            // Bucket timestamp into 60-second windows to absorb network/carrier delivery latency
            val timeBucket = timestamp / 60000L
            val normalizedSender = sender.trim().replace(Regex("[^0-9+]"), "")
            val normalizedBody = body.trim()

            val rawString = "$normalizedSender:$timeBucket:$simSlot:$normalizedBody"

            val digest = MessageDigest.getInstance("SHA-256")
            val hashBytes = digest.digest(rawString.toByteArray(Charsets.UTF_8))
            return hashBytes.joinToString("") { "%02x".format(it) }
        }
    }
}

