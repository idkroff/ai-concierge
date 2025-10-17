FROM alpine:3.19

RUN apk add --no-cache \
    asterisk \
    asterisk-sample-config \
    asterisk-sounds-en \
    && rm -rf /var/cache/apk/*

RUN mkdir -p /var/lib/asterisk/sounds/custom \
    && mkdir -p /var/run/asterisk \
    && chown -R asterisk:asterisk /var/lib/asterisk \
    && chown -R asterisk:asterisk /var/run/asterisk

COPY config/asterisk/* /etc/asterisk/

RUN chown -R asterisk:asterisk /etc/asterisk

EXPOSE 5060/udp 5038/tcp 10000-20000/udp

CMD ["asterisk", "-f", "-vvvv", "-U", "asterisk", "-G", "asterisk"]

