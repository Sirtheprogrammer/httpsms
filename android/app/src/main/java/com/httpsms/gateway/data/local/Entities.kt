package com.httpsms.gateway.data.local

import androidx.room.*

@Entity(tableName = "sms_dedup")
data class SmsDedupEntity(
    @PrimaryKey
    @ColumnInfo(name = "fingerprint")
    val fingerprint: String, // SHA-256(sender + timestamp_bucket + sim_slot + normalized_body)

    @ColumnInfo(name = "created_at")
    val createdAt: Long = System.currentTimeMillis()
)

@Entity(tableName = "outbox")
data class OutboxEntity(
    @PrimaryKey
    @ColumnInfo(name = "id")
    val id: String,

    @ColumnInfo(name = "idempotency_key")
    val idempotencyKey: String,

    @ColumnInfo(name = "payload_json")
    val payloadJson: String,

    @ColumnInfo(name = "status")
    val status: String = "PENDING", // PENDING, ACKNOWLEDGED, FAILED

    @ColumnInfo(name = "retry_count")
    val retryCount: Int = 0,

    @ColumnInfo(name = "created_at")
    val createdAt: Long = System.currentTimeMillis()
)

@Dao
interface SmsDedupDao {
    @Query("SELECT COUNT(*) FROM sms_dedup WHERE fingerprint = :fingerprint")
    suspend fun exists(fingerprint: String): Int

    @Insert(onConflict = OnConflictStrategy.IGNORE)
    suspend fun insert(entity: SmsDedupEntity): Long

    // Prune entries older than 24 hours
    @Query("DELETE FROM sms_dedup WHERE created_at < :olderThan")
    suspend fun pruneOldRecords(olderThan: Long)
}

@Dao
interface OutboxDao {
    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun insert(entity: OutboxEntity)

    @Query("SELECT * FROM outbox WHERE status = 'PENDING' ORDER BY created_at ASC")
    suspend fun getPending(): List<OutboxEntity>

    @Query("UPDATE outbox SET status = 'ACKNOWLEDGED' WHERE idempotency_key = :key")
    suspend fun markAcknowledged(key: String)

    @Query("DELETE FROM outbox WHERE status = 'ACKNOWLEDGED'")
    suspend fun deleteAcknowledged()

    @Query("UPDATE outbox SET retry_count = retry_count + 1 WHERE id = :id")
    suspend fun incrementRetry(id: String)
}

@Database(entities = [SmsDedupEntity::class, OutboxEntity::class], version = 1, exportSchema = false)
abstract class AppDatabase : RoomDatabase() {
    abstract fun dedupDao(): SmsDedupDao
    abstract fun outboxDao(): OutboxDao

    companion object {
        @Volatile
        private var INSTANCE: AppDatabase? = null

        fun getInstance(context: android.content.Context): AppDatabase {
            return INSTANCE ?: synchronized(this) {
                val instance = Room.databaseBuilder(
                    context.applicationContext,
                    AppDatabase::class.java,
                    "httpsms_gateway.db"
                ).build()
                INSTANCE = instance
                instance
            }
        }
    }
}

