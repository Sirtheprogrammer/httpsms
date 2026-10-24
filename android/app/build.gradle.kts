import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("com.google.devtools.ksp")
}

// Permanent release key, so updates install over older versions without uninstalling.
// CI passes it via ANDROID_KEYSTORE_FILE / _PASSWORD / ANDROID_KEY_ALIAS / ANDROID_KEY_PASSWORD;
// locally, point `signing.properties` in local.properties at a file with
// storeFile, storePassword, keyAlias and keyPassword.
val releaseSigning: Map<String, String>? = run {
    System.getenv("ANDROID_KEYSTORE_FILE")?.takeIf { it.isNotBlank() }?.let { file ->
        return@run mapOf(
            "storeFile" to file,
            "storePassword" to System.getenv("ANDROID_KEYSTORE_PASSWORD").orEmpty(),
            "keyAlias" to System.getenv("ANDROID_KEY_ALIAS").orEmpty(),
            "keyPassword" to System.getenv("ANDROID_KEY_PASSWORD").orEmpty(),
        )
    }
    val local = rootProject.file("local.properties")
    if (!local.exists()) return@run null
    val path = Properties().apply { local.inputStream().use { load(it) } }.getProperty("signing.properties")
    val file = path?.let { File(it) }?.takeIf { it.exists() } ?: return@run null
    val props = Properties().apply { file.inputStream().use { load(it) } }
    props.stringPropertyNames().associateWith { props.getProperty(it) }
}

android {
    namespace = "com.httpsms.gateway"
    compileSdk = 34

    defaultConfig {
        applicationId = "com.httpsms.gateway"
        minSdk = 26
        targetSdk = 34
        versionCode = 4
        versionName = "1.2.1"

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    signingConfigs {
        if (releaseSigning != null) {
            create("release") {
                storeFile = file(releaseSigning.getValue("storeFile"))
                storePassword = releaseSigning.getValue("storePassword")
                keyAlias = releaseSigning.getValue("keyAlias")
                keyPassword = releaseSigning.getValue("keyPassword")
            }
        }
    }

    buildTypes {
        release {
            signingConfig = signingConfigs.findByName("release")
            isMinifyEnabled = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
        }
        debug {
            // Same key for local debug builds, so they can upgrade a release install.
            signingConfigs.findByName("release")?.let { signingConfig = it }
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
    buildFeatures {
        viewBinding = true
        buildConfig = true
    }
}

dependencies {
    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("com.google.android.material:material:1.12.0")
    implementation("androidx.constraintlayout:constraintlayout:2.1.4")
    implementation("androidx.lifecycle:lifecycle-service:2.8.4")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.4")

    // Room Database
    implementation("androidx.room:room-runtime:2.6.1")
    implementation("androidx.room:room-ktx:2.6.1")
    ksp("androidx.room:room-compiler:2.6.1")

    // Networking (OkHttp for WebSockets & HTTP)
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("com.squareup.okhttp3:logging-interceptor:4.12.0")

    // JSON serialization
    implementation("com.google.code.gson:gson:2.11.0")

    // Coroutines
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")

    // Background watchdog that restarts the gateway if the OS kills it
    implementation("androidx.work:work-runtime-ktx:2.9.1")

    // QR scanner for dashboard pairing (no Google Play services needed)
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
}

