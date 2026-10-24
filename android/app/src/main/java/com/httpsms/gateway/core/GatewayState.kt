package com.httpsms.gateway.core

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update

/** Live gateway status published by the service and observed by the UI. */
object GatewayState {

    enum class Connection { STOPPED, CONNECTING, CONNECTED, DISCONNECTED, AUTH_FAILED }

    data class Snapshot(
        val connection: Connection = Connection.STOPPED,
        val detail: String = "",
        val connectedSince: Long = 0L,
        val lastHeartbeatAt: Long = 0L,
        val smsSent: Int = 0,
        val smsReceived: Int = 0,
    )

    private val _state = MutableStateFlow(Snapshot())
    val state: StateFlow<Snapshot> = _state

    fun setConnection(connection: Connection, detail: String = "") = _state.update {
        it.copy(
            connection = connection,
            detail = detail,
            connectedSince = when {
                connection == Connection.CONNECTED && it.connection != Connection.CONNECTED -> System.currentTimeMillis()
                connection == Connection.CONNECTED -> it.connectedSince
                else -> 0L
            },
        )
    }

    fun heartbeatSent() = _state.update { it.copy(lastHeartbeatAt = System.currentTimeMillis()) }
    fun smsSent() = _state.update { it.copy(smsSent = it.smsSent + 1) }
    fun smsReceived() = _state.update { it.copy(smsReceived = it.smsReceived + 1) }
}
