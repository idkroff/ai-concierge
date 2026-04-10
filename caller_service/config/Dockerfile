FROM --platform=linux/amd64 alpine:3.19

# Устанавливаем Asterisk и зависимости
RUN apk add --no-cache \
    asterisk \
    asterisk-sample-config \
    asterisk-sounds-en \
    gettext \
    && rm -rf /var/cache/apk/*

# Создаем директории
RUN mkdir -p /var/lib/asterisk/sounds/custom \
    && mkdir -p /var/run/asterisk \
    && chown -R asterisk:asterisk /var/lib/asterisk \
    && chown -R asterisk:asterisk /var/run/asterisk

# Копируем конфигурацию
COPY asterisk/* /etc/asterisk/

# Копируем entrypoint скрипт
COPY docker-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Права на конфиги
RUN chown -R asterisk:asterisk /etc/asterisk

# Открываем порты
EXPOSE 5060/udp 5038/tcp 10000-20000/udp

# Используем entrypoint для генерации конфигов
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]

# Запуск Asterisk от root (для биндинга портов < 1024)
CMD ["asterisk", "-f", "-vvvvv", "-U", "asterisk", "-G", "asterisk"]

