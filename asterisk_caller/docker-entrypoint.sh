#!/bin/sh
set -e

if [ -f /etc/asterisk/pjsip.conf.template ] && [ -n "$SIP_USER" ] && [ -n "$SIP_PASS" ]; then
    envsubst '${SIP_USER} ${SIP_PASS}' < /etc/asterisk/pjsip.conf.template > /etc/asterisk/pjsip.conf
    chown asterisk:asterisk /etc/asterisk/pjsip.conf
    echo "✓ pjsip.conf generated"
else
    echo "⚠ SIP_USER/SIP_PASS not set or template missing, PJSIP may be unavailable"
fi

exec asterisk -f -vvvv -U asterisk -G asterisk
