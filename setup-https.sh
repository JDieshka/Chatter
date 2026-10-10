#!/usr/bin/env bash
# Автоматическая настройка HTTPS (Caddy) для Chatter на VPS.
#
# Два режима:
#   sudo ./setup-https.sh chat.example.com   — домен: Let's Encrypt, зелёный замок в браузере
#   sudo ./setup-https.sh                    — без домена: автоопределение публичного IP,
#                                              самоподписанный сертификат (браузер покажет
#                                              предупреждение — принять один раз; микрофон и wss работают)
set -euo pipefail

DOMAIN="${1:-}"
if [[ -z "$DOMAIN" ]]; then
  echo "Домен не указан. Варианты:"
  echo "  1) Ввести домен (нужна A-запись на IP сервера) — Let's Encrypt, полноценный сертификат"
  echo "  2) Оставить пустым — будет взят публичный IP этого сервера, самоподписанный сертификат"
  read -rp "Домен [Enter = режим 2, IP]: " DOMAIN
fi

OVERLAY="docker-compose.caddy.yml"
MODE="Let's Encrypt (домен)"
if [[ -z "$DOMAIN" ]]; then
  # Режим без домена: определяем публичный IP сервера
  DOMAIN=$(curl -fsS --max-time 10 https://api.ipify.org || true)
  [[ -z "$DOMAIN" ]] && { echo "[!] Не удалось определить публичный IP (нет доступа к api.ipify.org)."; exit 1; }
  OVERLAY="docker-compose.caddy-ip.yml"
  MODE="самоподписанный сертификат на IP ${DOMAIN}"
  echo "[!] Это HTTP-доступ через IP. Браузер при первом заходе по https://${DOMAIN}"
  echo "    покажет предупреждение о сертификате — нажмите 'Дополнительно' -> 'Перейти'."
  echo "    После этого getUserMedia (микрофон) и WebSocket (wss://) будут работать."
fi
echo "[+] Режим: ${MODE}"

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

# 4. Быстрая проверка DNS (только для режима с доменом): домен должен резолвиться в IP этого сервера
if [[ "$OVERLAY" == "docker-compose.caddy.yml" ]]; then
  SERVER_IP=$(curl -fsS https://api.ipify.org || echo "")
  DOMAIN_IP=$(getent hosts "$DOMAIN" | awk '{print $1}' | head -1 || true)
  if [[ -n "$SERVER_IP" && -n "$DOMAIN_IP" && "$SERVER_IP" != "$DOMAIN_IP" ]]; then
    echo "[!] ВНИМАНИЕ: ${DOMAIN} указывает на ${DOMAIN_IP}, а сервер — ${SERVER_IP}."
    echo "    Направьте A-запись домена на IP сервера, иначе сертификат не будет выдан."
  fi
fi

# 5. Пересборка с overlay Caddy
docker compose -f docker-compose.yml -f "${OVERLAY}" up -d --build

echo
echo "[+] Готово. Через 1-2 минуты сайт будет доступен по https://${DOMAIN}"
if [[ "$OVERLAY" == "docker-compose.caddy-ip.yml" ]]; then
  echo "    Это самоподписанный сертификат: при первом заходе браузер покажет предупреждение —"
  echo "    примите его ('Дополнительно' -> 'Перейти на сайт'), HTTPS-контекст будет работать,"
  echo "    микрофон (getUserMedia) и wss:// станут доступны."
  echo "    При смене IP сервера перезапустите настройку: sudo ./setup-https.sh"
fi
echo "    Логи выдачи сертификата: docker compose logs caddy"
[[ "$OVERLAY" == "docker-compose.caddy.yml" ]] && echo "    Ожидаемая строка: 'certificate obtained successfully'" || true
