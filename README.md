# Chatter (MIN)

Веб-приложение для текстовых чатов: Go + PostgreSQL + WebSocket, фронтенд раздаётся тем же сервером.

## Возможности

- Регистрация и вход (bcrypt, JWT access + refresh с ротацией)
- Приватные и групповые чаты, участники
- Отправка сообщений по WebSocket в реальном времени (+ REST-fallback)
- История сообщений с курсорной пагинацией (`before` / `next_before` / `has_more`)
- Автоматические SQL-миграции при старте
- Голосовые чаты — только интерфейс, в разработке (следующий спринт)

## Стек

| Компонент | Выбор |
|---|---|
| Язык | Go 1.26+ |
| HTTP-роутер | chi/v5 |
| WebSocket | coder/websocket |
| БД | PostgreSQL 15+, драйвер pgx/v5 |
| Миграции | golang-migrate (применяются автоматически при старте) |
| Auth | golang-jwt/jwt/v5 + bcrypt |
| Логирование | log/slog |

## Структура проекта

```
cmd/server/            # точка входа: конфиг, миграции, DI, запуск HTTP/WS
internal/config/       # конфигурация из env
internal/domain/       # сущности User, Chat, Message
internal/usecase/      # бизнес-логика (AuthUsecase, ChatUsecase) + Broadcaster
internal/infra/repository/postgres/  # репозитории на pgx
internal/infra/web/http/             # REST-хендлеры, раздача фронтенда
internal/infra/web/websocket/        # Hub, Client (readPump/writePump)
internal/pkg/middleware/             # JWT, CORS, rate-limit
migrations/                          # .up.sql / .down.sql
frontend/                            # index.html + css/ + js/ (vanilla JS)
```

## Запуск через Docker Compose (рекомендуется)

Требуется только установленный Docker.

```bash
git clone https://github.com/JDieshka/Chatter
cd Chatter

# 1. Создайте .env (JWT_SECRET обязателен)
cp .env.example .env
# сгенерируйте секрет, например: openssl rand -hex 32
# и впишите его в .env: JWT_SECRET=<ваш секрет>

# 2. Сборка и запуск (поднимется приложение + PostgreSQL)
docker compose up -d --build

# 3. Открыть в браузере
http://localhost:8080
```

Переменные `.env` (все опциональны, кроме `JWT_SECRET`):

| Переменная | По умолчанию | Описание |
|---|---|---|
| `JWT_SECRET` | — (обязательна) | Секрет подписи JWT |
| `APP_PORT` | `8080` | Порт приложения на хосте |
| `DB_USER` / `DB_PASSWORD` / `DB_NAME` | `chatter` | Учётные данные PostgreSQL |

Полезные команды:

```bash
docker compose logs -f app    # логи приложения
docker compose down           # остановить
docker compose down -v        # остановить и удалить данные БД
```

## Локальный запуск без Docker

Требуется Go 1.26+ и PostgreSQL 15+.

```bash
createdb chatter
export DATABASE_URL="postgres://chatter:chatter@localhost:5432/chatter?sslmode=disable"
export JWT_SECRET="$(openssl rand -hex 32)"   # или любой свой секрет
go run ./cmd/server
# приложение: http://localhost:8080  (миграции применятся сами)
```

Через Makefile: `make run`, `make build`, `make test`, `make vet`.

## Конфигурация (env)

| Переменная | По умолчанию | Описание |
|---|---|---|
| `PORT` | `8080` | HTTP-порт внутри контейнера/хоста |
| `DATABASE_URL` | `postgres://chatter:chatter@localhost:5432/chatter?sslmode=disable` | DSN PostgreSQL |
| `JWT_SECRET` | — | Обязательный секрет подписи токенов |
| `ACCESS_TOKEN_TTL` | `30m` | Время жизни access-токена |
| `REFRESH_TOKEN_TTL` | `720h` | Время жизни refresh-токена (30 дней) |
| `MIGRATIONS_DIR` | `migrations` | Путь к SQL-миграциям |
| `FRONTEND_DIR` | `frontend` | Путь к статике фронтенда |

## API

Базовый префикс `/api`. Авторизованные запросы: `Authorization: Bearer <access_token>`.
WebSocket: `ws://<host>:<port>/ws?token=<access_token>`.

| Метод | Путь | Описание |
|---|---|---|
| POST | `/api/auth/register` | `{username, email, password}` → пользователь + токены |
| POST | `/api/auth/login` | `{username, password}` → access + refresh |
| POST | `/api/auth/refresh` | `{refresh_token}` → новая пара (ротация) |
| GET | `/api/users/me` | Текущий пользователь |
| GET | `/api/chats` | Чаты пользователя |
| POST | `/api/chats/private` | `{username}` → создать/получить приватный чат |
| POST | `/api/chats/group` | `{title, members[]}` → групповой чат |
| POST | `/api/chats/{id}/members` | Добавить участника |
| GET | `/api/chats/{id}` | Детали чата |
| GET | `/api/chats/{id}/messages?before=&limit=` | История: `{messages, next_before, has_more}` |
| POST | `/api/chats/{id}/messages` | Отправить сообщение (REST-fallback) |
| GET | `/healthz` | Проверка работоспособности |

WS-протокол: клиент шлёт `message.send` / `chat.join` / `chat.leave`;
сервер присылает `message.new` / `error`.

## Деплой на VPS

```bash
git clone https://github.com/JDieshka/Chatter && cd Chatter
cp .env.example .env && nano .env        # задайте надёжный JWT_SECRET
docker compose up -d --build
```

Приложение слушает `APP_PORT` (по умолчанию 8080). Для продакшена рекомендуется
положить перед ним reverse-proxy с HTTPS (Caddy/Nginx) — WebSocket тогда доступен
через `wss://` через тот же прокси.

## Статус разработки

- ✅ Спринты 1–5: фундамент, auth, REST-чаты, WebSocket, полировка + Docker
- ⬜ Спринт 6: голосовые чаты (WebRTC/SFU, сигналинг поверх существующего WS)
