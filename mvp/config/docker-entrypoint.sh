#!/bin/sh
set -e

echo "Generating Asterisk configuration from templates..."

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



