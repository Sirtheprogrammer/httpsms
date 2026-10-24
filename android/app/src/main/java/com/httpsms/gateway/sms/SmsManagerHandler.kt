package com.httpsms.gateway.sms

import android.annotation.SuppressLint
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build
import android.telephony.SmsManager
import android.telephony.SubscriptionInfo
import android.telephony.SubscriptionManager
import android.util.Log

data class SimCardInfo(
    val slotIndex: Int,
    val subId: Int,
    val carrierName: String,
    val phoneNumber: String,
    val countryIso: String,
    val signalLevel: Int = 3,
    val signalDbm: Int = -85,
    val isActive: Boolean = true
)

class SmsManagerHandler(private val context: Context) {

    companion object {
        const val ACTION_SMS_SENT = "com.httpsms.gateway.SMS_SENT"
        const val ACTION_SMS_DELIVERED = "com.httpsms.gateway.SMS_DELIVERED"
        const val EXTRA_MESSAGE_ID = "message_id"
        private const val TAG = "SmsManagerHandler"
    }

    @SuppressLint("MissingPermission")
    fun getActiveSimCards(): List<SimCardInfo> {
        val simList = mutableListOf<SimCardInfo>()
        val subscriptionManager = context.getSystemService(Context.TELEPHONY_SUBSCRIPTION_SERVICE) as? SubscriptionManager
            ?: return simList

        try {
            val activeSubscriptions: List<SubscriptionInfo>? = subscriptionManager.activeSubscriptionInfoList
            activeSubscriptions?.forEach { subInfo ->
                simList.add(
                    SimCardInfo(
                        slotIndex = subInfo.simSlotIndex,
                        subId = subInfo.subscriptionId,
                        carrierName = subInfo.carrierName?.toString() ?: "Unknown",
                        phoneNumber = subInfo.number ?: "",
                        countryIso = subInfo.countryIso ?: "",
                        isActive = true
                    )
                )
            }
        } catch (e: Exception) {
            Log.e(TAG, "Error enumerating active subscriptions: ${e.message}")
        }
        return simList
    }

    fun sendSms(messageId: String, recipient: String, body: String, preferredSlot: Int = 0): Boolean {
        return try {
            val smsManager = getSmsManagerForSlot(preferredSlot)
            val parts = smsManager.divideMessage(body)

            val sentIntents = ArrayList<PendingIntent>()
            val deliveryIntents = ArrayList<PendingIntent>()

            val flags = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
            } else {
                PendingIntent.FLAG_UPDATE_CURRENT
            }

            for (i in parts.indices) {
                val sentIntent = Intent(ACTION_SMS_SENT).apply {
                    setPackage(context.packageName)
                    putExtra(EXTRA_MESSAGE_ID, messageId)
                    putExtra("part_index", i)
                    putExtra("total_parts", parts.size)
                }
                sentIntents.add(PendingIntent.getBroadcast(context, messageId.hashCode() + i, sentIntent, flags))

                val deliveryIntent = Intent(ACTION_SMS_DELIVERED).apply {
                    setPackage(context.packageName)
                    putExtra(EXTRA_MESSAGE_ID, messageId)
                    putExtra("part_index", i)
                    putExtra("total_parts", parts.size)
                }
                deliveryIntents.add(PendingIntent.getBroadcast(context, messageId.hashCode() + i + 1000, deliveryIntent, flags))
            }

            if (parts.size > 1) {
                smsManager.sendMultipartTextMessage(recipient, null, parts, sentIntents, deliveryIntents)
            } else {
                smsManager.sendTextMessage(recipient, null, body, sentIntents.firstOrNull(), deliveryIntents.firstOrNull())
            }

            Log.i(TAG, "Dispatched SMS $messageId to $recipient via slot $preferredSlot (${parts.size} parts)")
            true
        } catch (e: Exception) {
            Log.e(TAG, "Failed to send SMS $messageId: ${e.message}", e)
            false
        }
    }

    private fun getSmsManagerForSlot(slotIndex: Int): SmsManager {
        val sims = getActiveSimCards()
        val targetSim = sims.firstOrNull { it.slotIndex == slotIndex }

        return if (targetSim != null && Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP_MR1) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                context.getSystemService(SmsManager::class.java).createForSubscriptionId(targetSim.subId)
            } else {
                @Suppress("DEPRECATION")
                SmsManager.getSmsManagerForSubscriptionId(targetSim.subId)
            }
        } else {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                context.getSystemService(SmsManager::class.java)
            } else {
                @Suppress("DEPRECATION")
                SmsManager.getDefault()
            }
        }
    }
}

