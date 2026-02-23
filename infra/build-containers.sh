#!/bin/bash
set -e

PATH_TO_ASTERISK="/Users/maximdervis/v5u90/studies/concierge/project/ai-concierge/asterisk_caller"
PATH_TO_VOICE_AGENT="/Users/maximdervis/v5u90/studies/concierge/project/ai-concierge/mvp"
TERRAFORM_ROOT="/Users/maximdervis/v5u90/studies/concierge/project/ai-concierge/infra/terraform"
REGISTRY_ID=$(cd $TERRAFORM_ROOT && terraform output -raw registry_id)

echo "Registry ID: $REGISTRY_ID"

if ! docker-buildx version >/dev/null 2>&1; then
    echo "❌ docker-buildx не установлен или не доступен."
fi

if ! docker-buildx ls | grep -q multiplatform; then
    echo "🔧 Создание buildx builder..."
    docker-buildx create --name multiplatform --driver docker-container
fi

docker-buildx use multiplatform

PLATFORM="linux/amd64"

echo "🔨 Сборка voice-agent..."
cd $PATH_TO_VOICE_AGENT
docker-buildx build \
    --platform $PLATFORM \
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

echo "🔨 Сборка asterisk..."
cd $PATH_TO_ASTERISK
docker-buildx build \
    --platform $PLATFORM \
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

echo "🔍 Проверка архитектуры образов..."
VOICE_ARCH=$(docker inspect "cr.yandex/${REGISTRY_ID}/voice-agent:latest" | grep -i '"Architecture"' | head -1 | cut -d'"' -f4 || echo "unknown")
ASTERISK_ARCH=$(docker inspect "cr.yandex/${REGISTRY_ID}/asterisk:latest" | grep -i '"Architecture"' | head -1 | cut -d'"' -f4 || echo "unknown")

echo "voice-agent: $VOICE_ARCH"
echo "asterisk: $ASTERISK_ARCH"
echo "✅ Все образы пересобраны и загружены!"
