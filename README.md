# XRumer Admin

Запуск проекта через `docker-compose.yaml` (PostgreSQL + API + Frontend).

## Что важно перед запуском

В `docker-compose.yaml` примонтированы пути из **корня проекта**:

- `./proxy.txt -> /app/proxy.txt` (для API)
- `./domains -> /app/domains` (папка со списками доменов)

Подготовьте структуру:

```text
/opt/xrumer-admin
├── docker-compose.yaml
├── proxy.txt
└── domains/
    ├── list-1.txt
    └── list-2.txt
```

## Формат `proxy.txt`

Один прокси на строку.

Рекомендуемый рабочий формат (URL-style):

```text
user:pass@ip:port
```

Пример:

```text
myuser:mypass@203.0.113.10:8080
myuser:mypass@203.0.113.11:8080
```

Если у вас прокси в формате `user:pass:ip:port`, преобразуйте в `user:pass@ip:port`, иначе прокси может не распознаться корректно.

## Формат файлов в `domains/`

- В папке `domains/` можно хранить несколько файлов со списками доменов.
- В каждом файле - по одному домену на строку.
- Допустимо с `http://`/`https://` или без схемы (домен будет нормализован API).

Пример `domains/list-1.txt`:

```text
example.com
https://nuxt.com
subdomain.example.org
```

## Запуск

Из корня проекта:

```bash
docker compose up -d --build
```

Проверка статуса:

```bash
docker compose ps
```

## Доступ к сервисам

- Frontend: `http://localhost:3001`
- API: `http://localhost:8080`
- PostgreSQL: `localhost:5434`

## Логи

```bash
docker compose logs -f api
docker compose logs -f frontend
docker compose logs -f postgres
```

## Остановка

```bash
docker compose down
```

С удалением томов БД:

```bash
docker compose down -v
```

## Полезно

- Swagger/документации API в проекте нет, но есть описание CRUD-эндпоинтов в `api/README.md`.
- Если контейнер `api` стартует, но задачи не используют прокси, сначала проверьте формат строк в `proxy.txt`.
