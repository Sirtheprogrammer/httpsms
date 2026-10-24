package com.httpsms.gateway.receiver

import android.app.Activity
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build
import android.provider.Telephony
import android.telephony.SubscriptionManager
import android.telephony.SmsMessage
import android.util.Log
import com.google.gson.Gson
import com.httpsms.gateway.data.local.AppDatabase
import com.httpsms.gateway.data.local.OutboxEntity
import com.httpsms.gateway.core.GatewayConfig
import com.httpsms.gateway.core.GatewayState
import com.httpsms.gateway.service.GatewayForegroundService
import com.httpsms.gateway.service.GatewayWatchdogWorker
import com.httpsms.gateway.sms.SmsDeduplicator
import com.httpsms.gateway.sms.SmsManagerHandler
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import java.util.UUID

class SmsBroadcastReceiver : BroadcastReceiver() {
    companion object {
        private const val TAG = "SmsBroadcastReceiver"
    }

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Telephony.Sms.Intents.SMS_RECEIVED_ACTION) return

        val pendingResult = goAsync()
        val messages: Array<SmsMessage>? = Telephony.Sms.Intents.getMessagesFromIntent(intent)
        if (messages.isNullOrEmpty()) {
            pendingResult.finish()
            return
        }

        CoroutineScope(Dispatchers.IO).launch {
            try {
                // Reassemble multipart message
                val sender = messages[0].displayOriginatingAddress ?: "Unknown"
                val timestamp = messages[0].timestampMillis
                val bodyBuilder = StringBuilder()
                for (msg in messages) {
                    bodyBuilder.append(msg.displayMessageBody)
                }
                val fullBody = bodyBuilder.toString()

                val simSlot = resolveSimSlot(intent)

                val deduplicator = SmsDeduplicator(context)
                val (isUnique, fingerprint) = deduplicator.isUniqueAndRecord(
                    sender = sender,
                    body = fullBody,
                    timestamp = timestamp,
                    simSlot = simSlot
                )

                if (!isUnique) {
                    Log.w(TAG, "Duplicate SMS frame detected and discarded! Fingerprint: $fingerprint")
                    return@launch
                }

                Log.i(TAG, "Unique incoming SMS from $sender: $fullBody (fingerprint: $fingerprint)")

                val payload = mapOf(
                    "sender" to sender,
                    "recipient" to "local_sim",
                    "body" to fullBody,
                    "sim_slot" to simSlot,
                    "parts_count" to messages.size,
                    "idempotency_key" to fingerprint
                )

                val payloadJson = Gson().toJson(payload)
                val outboxEntity = OutboxEntity(
                    id = UUID.randomUUID().toString(),
                    idempotencyKey = fingerprint,
                    payloadJson = payloadJson,
                    status = "PENDING"
                )

                val db = AppDatabase.getInstance(context)
                db.outboxDao().insert(outboxEntity)
                GatewayState.smsReceived()

                // Hand off to the running service; the outbox row survives if it isn't running
                // and is flushed on the next connection.
                val service = GatewayForegroundService.instance
                if (service != null) {
                    service.flushOutbox()
                } else if (GatewayConfig(context).serviceEnabled) {
                    GatewayForegroundService.start(context, GatewayForegroundService.ACTION_FLUSH_OUTBOX)
                }

            } catch (e: Exception) {
                Log.e(TAG, "Error processing incoming SMS: ${e.message}", e)
            } finally {
                pendingResult.finish()
            }
        }
    }
}

/** Which SIM slot an SMS_RECEIVED broadcast arrived on (0-based). */
private fun resolveSimSlot(intent: Intent): Int {
    val slot = intent.getIntExtra("android.telephony.extra.SLOT_INDEX", -1)
    if (slot >= 0) return slot
    val subId = intent.getIntExtra(
        "android.telephony.extra.SUBSCRIPTION_INDEX",
        intent.getIntExtra("subscription", SubscriptionManager.INVALID_SUBSCRIPTION_ID),
    )
    if (subId != SubscriptionManager.INVALID_SUBSCRIPTION_ID && Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
        val mapped = SubscriptionManager.getSlotIndex(subId)
        if (mapped >= 0) return mapped
    }
    return 0
}

class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        // Boot and app-update broadcasts are exempt from background start limits.
        val config = GatewayConfig(context)
        if (config.serviceEnabled && config.isPaired) {
            GatewayForegroundService.start(context)
            GatewayWatchdogWorker.schedule(context)
        }
    }
}

class SmsStatusReceiver : BroadcastReceiver() {
    companion object {
        private const val TAG = "SmsStatusReceiver"
    }

    override fun onReceive(context: Context, intent: Intent) {
        val messageId = intent.getStringExtra(SmsManagerHandler.EXTRA_MESSAGE_ID) ?: return
        val action = intent.action

        var status = "unknown"
        var errorMessage = ""

        when (action) {
            SmsManagerHandler.ACTION_SMS_SENT -> {
                if (resultCode == Activity.RESULT_OK) {
                    status = "sent"
                } else {
                    status = "failed"
                    errorMessage = "SmsManager send error code: $resultCode"
                }
            }
            SmsManagerHandler.ACTION_SMS_DELIVERED -> {
                status = "delivered"
            }
        }

        Log.i(TAG, "Message $messageId status updated: $status")

        val service = GatewayForegroundService.instance
        if (service != null) {
            service.reportMessageStatus(messageId, status, errorMessage)
        } else {
            val extras = Intent()
                .putExtra("message_id", messageId)
                .putExtra("status", status)
                .putExtra("error_message", errorMessage)
            GatewayForegroundService.start(context, GatewayForegroundService.ACTION_SYNC_STATUS, extras)
        }
    }
}

