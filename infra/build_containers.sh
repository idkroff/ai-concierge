#!/bin/bash
set -e

# Использование: ./build-containers.sh --voice_agent_version=1.2.0 [--asterisk_version=1.0.1]
# Если параметр не указан — образ не пересобирается.

VOICE_AGENT_VERSION=""
ASTERISK_VERSION=""
for arg in "$@"; do
  case $arg in
    --voice_agent_version=*) VOICE_AGENT_VERSION="${arg#*=}" ;;
    --asterisk_version=*)    ASTERISK_VERSION="${arg#*=}" ;;
  esac
done

PATH_TO_ASTERISK="/Users/maximdervis/v5u90/studies/concierge/project/ai-concierge/asterisk_caller"
PATH_TO_VOICE_AGENT="/Users/maximdervis/v5u90/studies/concierge/project/ai-concierge/mvp"
TERRAFORM_ROOT="/Users/maximdervis/v5u90/studies/concierge/project/ai-concierge/infra/terraform"
REGISTRY_ID=$(cd $TERRAFORM_ROOT && terraform output -raw registry_id)

echo "Registry ID: $REGISTRY_ID"

if [[ -z "$VOICE_AGENT_VERSION" && -z "$ASTERISK_VERSION" ]]; then
    echo "Укажите версию: ./build-containers.sh --voice_agent_version=X [--asterisk_version=Y]"
    exit 0
fi

if ! docker-buildx version >/dev/null 2>&1; then
    echo "❌ docker-buildx не установлен или не доступен."
fi

if ! docker-buildx ls | grep -q multiplatform; then
    echo "🔧 Создание buildx builder..."
    docker-buildx create --name multiplatform --driver docker-container
fi

docker-buildx use multiplatform
PLATFORM="linux/amd64"

if [[ -n "$VOICE_AGENT_VERSION" ]]; then
    echo "🔨 Сборка voice-agent:$VOICE_AGENT_VERSION..."
    cd $PATH_TO_VOICE_AGENT
    docker-buildx build \
        --platform $PLATFORM \
        --load \
        -t "cr.yandex/${REGISTRY_ID}/voice-agent:${VOICE_AGENT_VERSION}" \
        -f Dockerfile . || { echo "❌ Ошибка сборки voice-agent"; exit 1; }
    docker push "cr.yandex/${REGISTRY_ID}/voice-agent:${VOICE_AGENT_VERSION}" || { echo "❌ Ошибка загрузки voice-agent"; exit 1; }
    echo "✅ voice-agent:$VOICE_AGENT_VERSION собран и загружен"
fi

if [[ -n "$ASTERISK_VERSION" ]]; then
    echo "🔨 Сборка asterisk:$ASTERISK_VERSION..."
    cd $PATH_TO_ASTERISK
    docker-buildx build \
        --platform $PLATFORM \
        --load \
        -t "cr.yandex/${REGISTRY_ID}/asterisk:${ASTERISK_VERSION}" \
        -f Dockerfile . || { echo "❌ Ошибка сборки asterisk"; exit 1; }
    docker push "cr.yandex/${REGISTRY_ID}/asterisk:${ASTERISK_VERSION}" || { echo "❌ Ошибка загрузки asterisk"; exit 1; }
    echo "✅ asterisk:$ASTERISK_VERSION собран и загружен"
fi

echo "✅ Готово."
