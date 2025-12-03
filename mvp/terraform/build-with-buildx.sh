#!/bin/bash
set -e

# Скрипт для сборки образов с использованием buildx для правильной кросс-платформенной сборки

REGISTRY_ID=$(cd /Users/maximdervis/v5u90/studies/concierge/ai-concierge/mvp/terraform && terraform output -raw registry_id 2>/dev/null || echo "")

if [ -z "$REGISTRY_ID" ]; then
    echo "❌ Не удалось получить Registry ID. Убедитесь, что Terraform применен."
    exit 1
fi

echo "🔄 Сборка образов с использованием buildx для linux/amd64..."
echo "Registry ID: $REGISTRY_ID"
echo ""

# Настройка Docker
echo "📦 Настройка Docker..."
yc container registry configure-docker --force >/dev/null 2>&1 || echo "⚠️  Предупреждение: не удалось автоматически настроить Docker"

# Проверка buildx
if ! docker-buildx version >/dev/null 2>&1; then
    echo "❌ docker-buildx не установлен или не доступен."
    echo ""
    echo "Для установки buildx:"
    echo "  1. Обновите Docker Desktop до последней версии"
    echo "  2. Или установите buildx вручную:"
    echo "     mkdir -p ~/.docker/cli-plugins"
    echo "     curl -L https://github.com/docker/buildx/releases/latest/download/buildx-v0.12.0.darwin-arm64 -o ~/.docker/cli-plugins/docker-buildx"
    echo "     chmod +x ~/.docker/cli-plugins/docker-buildx"
    echo ""
    echo "Альтернатива: соберите образы на удаленной Linux машине (x86_64)"
    exit 1
fi

# Создание builder, если не существует
if ! docker-buildx ls | grep -q multiplatform; then
    echo "🔧 Создание buildx builder..."
    docker-buildx create --name multiplatform --driver docker-container --use 2>/dev/null || \
    docker-buildx use multiplatform 2>/dev/null || true
fi

# Использование multiplatform builder
docker-buildx use multiplatform 2>/dev/null || docker-buildx use default 2>/dev/null || true

# Voice Agent
echo ""
echo "🔨 Сборка voice-agent..."
cd /Users/maximdervis/v5u90/studies/concierge/ai-concierge/mvp
docker-buildx build \
    --platform linux/amd64 \
    --load \
    -t "cr.yandex/${REGISTRY_ID}/voice-agent:latest" \
    -f Dockerfile . || {
    echo "❌ Ошибка сборки voice-agent"
    exit 1
}
docker push "cr.yandex/${REGISTRY_ID}/voice-agent:latest" || {
    echo "❌ Ошибка загрузки voice-agent"
    exit 1
}
echo "✅ voice-agent собран и загружен"

# Asterisk
echo ""
echo "🔨 Сборка asterisk..."
cd /Users/maximdervis/v5u90/studies/concierge/ai-concierge/mvp/config
docker-buildx build \
    --platform linux/amd64 \
    --load \
    -t "cr.yandex/${REGISTRY_ID}/asterisk:latest" \
    -f Dockerfile . || {
    echo "❌ Ошибка сборки asterisk"
    exit 1
}
docker push "cr.yandex/${REGISTRY_ID}/asterisk:latest" || {
    echo "❌ Ошибка загрузки asterisk"
    exit 1
}
echo "✅ asterisk собран и загружен"

# Проверка архитектуры
echo ""
echo "🔍 Проверка архитектуры образов..."
VOICE_ARCH=$(docker inspect "cr.yandex/${REGISTRY_ID}/voice-agent:latest" 2>/dev/null | grep -i '"Architecture"' | head -1 | cut -d'"' -f4 || echo "unknown")
ASTERISK_ARCH=$(docker inspect "cr.yandex/${REGISTRY_ID}/asterisk:latest" 2>/dev/null | grep -i '"Architecture"' | head -1 | cut -d'"' -f4 || echo "unknown")

echo "  voice-agent: $VOICE_ARCH"
echo "  asterisk: $ASTERISK_ARCH"

if [ "$VOICE_ARCH" != "amd64" ] && [ "$VOICE_ARCH" != "x86_64" ]; then
    echo "⚠️  ВНИМАНИЕ: voice-agent собран для $VOICE_ARCH, а не amd64!"
fi

if [ "$ASTERISK_ARCH" != "amd64" ] && [ "$ASTERISK_ARCH" != "x86_64" ]; then
    echo "⚠️  ВНИМАНИЕ: asterisk собран для $ASTERISK_ARCH, а не amd64!"
fi

echo ""
echo "✅ Все образы пересобраны и загружены!"
echo ""
echo "Теперь удалите старые поды:"
echo "  kubectl delete pods -n concierge --all"
echo ""
echo "Или перезапустите deployment:"
echo "  kubectl rollout restart deployment/asterisk -n concierge"
echo "  kubectl rollout restart deployment/voice-agent -n concierge"

