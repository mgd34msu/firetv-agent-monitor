#!/bin/sh
# Build the Agent Sessions Fire TV app: firetv/agent-sessions.apk. The APK
# holds no server address or token; setup.sh gives them to the installed app.
#
# Needs JDK 17 and the Android SDK (ANDROID_HOME, default ~/Android/Sdk).
set -eu
cd "$(dirname "$0")"

export ANDROID_HOME="${ANDROID_HOME:-$HOME/Android/Sdk}"
if [ -z "${JAVA_HOME:-}" ] && [ -d /usr/lib/jvm/java-17-openjdk ]; then
  export JAVA_HOME=/usr/lib/jvm/java-17-openjdk
fi
[ -f local.properties ] || echo "sdk.dir=$ANDROID_HOME" > local.properties

./gradlew -q --console=plain assembleRelease
cp app/build/outputs/apk/release/app-release.apk agent-sessions.apk
echo "built firetv/agent-sessions.apk"
