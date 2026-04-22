# API CRUD: Website и Page

Базовый CRUD для листинга (GET) реализован на эндпоинтах:

- `GET /websites`
- `GET /pages`

Сервер поднимается на порту `API_PORT` (по умолчанию `8080`).

## Пагинация

Оба эндпоинта поддерживают параметры:

- `page` (по умолчанию `1`)
- `per_page` (по умолчанию `20`, максимум `200`)

Формат ответа:

```json
{
  "items": [],
  "pagination": {
    "page": 1,
    "per_page": 20,
    "total": 0,
    "has_next_page": false
  }
}
```

## Фильтрация Website

Для `GET /websites` доступен параметр `filters` в формате JSON-строки.

Поддерживаемые поля:

- `cms`: массив строк (пример: `"wordpress"`, `"dle"`)
- `lang`: массив строк (пример: `"en-US"`, `"ru-RU"`)
- `is_forum`: массив boolean (`true/false`)
- `detected`: массив boolean (`true/false`) — фильтрация по факту распознавания CMS:
  - `true` => `cms IS NOT NULL AND cms <> ''`
  - `false` => `cms IS NULL OR cms = ''`

Если в `detected` передать одновременно `[true, false]`, фильтр не ограничивает выборку.

## CURL примеры

### 1) Website list без фильтров

```bash
curl -G "http://localhost:8080/websites" \
  --data-urlencode "page=1" \
  --data-urlencode "per_page=20"
```

### 2) Website list с фильтрами (cms + is_forum)

```bash
curl -G "http://localhost:8080/websites" \
  --data-urlencode "page=1" \
  --data-urlencode "per_page=50" \
  --data-urlencode 'filters={"cms":["wordpress","dle"],"is_forum":[true]}'
```

### 3) Website list с фильтрацией по detected=true

```bash
curl -G "http://localhost:8080/websites" \
  --data-urlencode "page=1" \
  --data-urlencode "per_page=20" \
  --data-urlencode 'filters={"detected":[true]}'
```

### 4) Website list с фильтрацией по detected=false

```bash
curl -G "http://localhost:8080/websites" \
  --data-urlencode "page=1" \
  --data-urlencode "per_page=20" \
  --data-urlencode 'filters={"detected":[false]}'
```

### 5) Page list

```bash
curl -G "http://localhost:8080/pages" \
  --data-urlencode "page=1" \
  --data-urlencode "per_page=20"
```
