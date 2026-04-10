#!/bin/sh
set -e

if [ -f /etc/asterisk/pjsip.conf.template ] && [ -n "$SIP_USER" ] && [ -n "$SIP_PASS" ]; then
    envsubst '${SIP_USER} ${SIP_PASS}' < /etc/asterisk/pjsip.conf.template > /etc/asterisk/pjsip.conf
    chown asterisk:asterisk /etc/asterisk/pjsip.conf
    echo "✓ pjsip.conf generated"
else
    echo "⚠ SIP_USER/SIP_PASS not set or template missing, PJSIP may be unavailable"
fi

# После старта Asterisk: макс. verbose/debug + SIP packet log (pjsip set logger on)
(
	set +e
	i=0
	while ! asterisk -rx "core show uptime" >/dev/null 2>&1; do
		sleep 0.3
		i=$((i + 1))
		[ "$i" -lt 200 ] || exit 0
	done
	asterisk -rx "core set verbose 10"
	asterisk -rx "core set debug 10"
	asterisk -rx "pjsip set logger on" || true
) &

exec asterisk -f -vvvvv -U asterisk -G asterisk
