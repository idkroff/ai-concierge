#!/bin/sh
set -e

echo "Generating Asterisk configuration from templates..."

# Декодируем base64 значения, если они в base64 формате
# Kubernetes Secret автоматически декодирует, но на всякий случай проверяем
if [ -n "$SIP_USER" ]; then
    # Проверяем, является ли значение base64 строкой (попытка декодирования)
    DECODED_USER=$(echo "$SIP_USER" | base64 -d 2>/dev/null || echo "$SIP_USER")
    # Если декодирование успешно и результат отличается, используем декодированное значение
    if [ "$DECODED_USER" != "$SIP_USER" ] && [ -n "$DECODED_USER" ]; then
        export SIP_USER="$DECODED_USER"
        echo "✓ SIP_USER decoded from base64"
    fi
fi

if [ -n "$SIP_PASS" ]; then
    DECODED_PASS=$(echo "$SIP_PASS" | base64 -d 2>/dev/null || echo "$SIP_PASS")
    if [ "$DECODED_PASS" != "$SIP_PASS" ] && [ -n "$DECODED_PASS" ]; then
        export SIP_PASS="$DECODED_PASS"
        echo "✓ SIP_PASS decoded from base64"
    fi
fi

# Генерируем pjsip.conf из шаблона с подстановкой переменных окружения
if [ -f /etc/asterisk/pjsip.conf.template ]; then
    envsubst '${SIP_USER} ${SIP_PASS}' < /etc/asterisk/pjsip.conf.template > /etc/asterisk/pjsip.conf
    echo "✓ pjsip.conf generated from template"
else
    echo "⚠ Warning: pjsip.conf.template not found, using existing pjsip.conf"
fi

# Проверяем наличие обязательных переменных
if [ -z "$SIP_USER" ] || [ -z "$SIP_PASS" ]; then
    echo "⚠ Warning: SIP_USER or SIP_PASS not set. Make sure to set them in docker-compose.yml or .env file"
fi

echo "Starting Asterisk..."
exec "$@"



