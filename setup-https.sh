#!/usr/bin/env bash
# Автоматическая настройка HTTPS (Caddy + Let's Encrypt) для Chatter на VPS.
# Запуск: sudo ./setup-https.sh chat.example.com   (или без аргумента — спросит)
set -euo pipefail

DOMAIN="${1:-}"
if [[ -z "$DOMAIN" ]]; then
  read -rp "Введите домен, указывающий на этот сервер (A-запись -> IP VPS): " DOMAIN
fi
[[ -z "$DOMAIN" ]] && { echo "Домен не задан"; exit 1; }

cd "$(dirname "$0")"

# 1. Сохраняем домен в .env (используется docker-compose.caddy.yml и Caddyfile через {$DOMAIN})
if [[ -f .env ]] && grep -q '^DOMAIN=' .env; then
  sed -i "s/^DOMAIN=.*/DOMAIN=${DOMAIN}/" .env
else
  echo "DOMAIN=${DOMAIN}" >> .env
fi
echo "[+] DOMAIN=${DOMAIN} записан в .env"

# 2. Проверка, что порты 80/443 свободны (иначе Let's Encrypt не получит сертификат)
for port in 80 443; do
  if ss -tlnp 2>/dev/null | grep -q ":${port} "; then
    echo "[!] Порт ${port} занят. Освободите его (обычно nginx/apache):"
    ss -tlnp | grep ":${port} " || true
    echo "    Пример: sudo systemctl stop nginx && sudo systemctl disable nginx"
    exit 1
  fi
done
echo "[+] Порты 80/443 свободны"

# 3. Разрешаем домен в firewall (если ufw активен)
if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
  ufw allow 80/tcp >/dev/null && ufw allow 443/tcp >/dev/null
  echo "[+] ufw: порты 80/443 открыты"
fi

# 4. Быстрая проверка DNS: домен должен резолвиться в IP этого сервера
SERVER_IP=$(curl -fsS https://api.ipify.org || echo "")
DOMAIN_IP=$(getent hosts "$DOMAIN" | awk '{print $1}' | head -1 || true)
if [[ -n "$SERVER_IP" && -n "$DOMAIN_IP" && "$SERVER_IP" != "$DOMAIN_IP" ]]; then
  echo "[!] ВНИМАНИЕ: ${DOMAIN} указывает на ${DOMAIN_IP}, а сервер — ${SERVER_IP}."
  echo "    Направьте A-запись домена на IP сервера, иначе сертификат не будет выдан."
fi

# 5. Пересборка с overlay Caddy
docker compose -f docker-compose.yml -f docker-compose.caddy.yml up -d --build

echo
echo "[+] Готово. Через 1-2 минуты сайт будет доступен по https://${DOMAIN}"
echo "    Логи выдачи сертификата: docker compose logs caddy"
echo "    Ожидаемая строка: 'certificate obtained successfully'"
