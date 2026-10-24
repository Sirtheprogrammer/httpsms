package com.httpsms.gateway.receiver

import android.app.Activity
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build
import android.provider.Telephony
import android.telephony.SmsMessage
import android.util.Log
import com.google.gson.Gson
import com.httpsms.gateway.data.local.AppDatabase
import com.httpsms.gateway.data.local.OutboxEntity
import com.httpsms.gateway.service.GatewayForegroundService
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

                // Extract SIM slot if available from intent extras
                val simSlot = intent.getIntExtra("android.telephony.extra.SLOT_INDEX",
                    intent.getIntExtra("subscription", 0))

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

                // Trigger foreground service to push to Go backend
                val serviceIntent = Intent(context, GatewayForegroundService::class.java).apply {
                    action = GatewayForegroundService.ACTION_FLUSH_OUTBOX
                }
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                    context.startForegroundService(serviceIntent)
                } else {
                    context.startService(serviceIntent)
                }

            } catch (e: Exception) {
                Log.e(TAG, "Error processing incoming SMS: ${e.message}", e)
            } finally {
                pendingResult.finish()
            }
        }
    }
}

class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_BOOT_COMPLETED) {
            val serviceIntent = Intent(context, GatewayForegroundService::class.java)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                context.startForegroundService(serviceIntent)
            } else {
                context.startService(serviceIntent)
            }
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

        // Notify Gateway Foreground Service to sync status with server
        val serviceIntent = Intent(context, GatewayForegroundService::class.java).apply {
            this.action = GatewayForegroundService.ACTION_SYNC_STATUS
            putExtra("message_id", messageId)
            putExtra("status", status)
            putExtra("error_message", errorMessage)
        }
        context.startService(serviceIntent)
    }
}

