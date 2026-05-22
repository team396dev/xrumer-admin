# Plan: Pages Import & Storage Overhaul

## Задача

Сейчас при импорте ссылок вида `https://domain.tld/path/` из URL вырезается только домен.
Нужно хранить полные URL как страницы (`pages`) с привязкой к домену (`websites`).
Страницы получают собственные теги и поле `accepted`, имеют отдельный пункт меню.
Логика импорта — сложная, описана ниже. Экспорт с фильтрами — везде.

---

## Текущее состояние (что есть)

### Таблицы БД
- `websites` — домены: `id, domain, cms, is_forum, lang, status, accepted (bool nullable), created_at, updated_at, deleted_at`
- `pages` — пустая таблица: `id, target_uri (unique), website_id, created_at, updated_at, deleted_at`
- `website_tags` + `website_tag_websites` — теги доменов
- `website_import_staging` — временная: `(job_id text, domain text, tags text[], PK(job_id, domain))`

### API
- `GET /websites` — список с фильтрами и мета
- `GET /websites/export` — TSV экспорт
- `POST /websites/accepted/import` + `GET /websites/accepted/import/:jobID` — импорт с джобом
- `GET /pages` — примитивный список без фильтров
- `GET /dashboard` — статистика

### Проблемы текущего кода
1. `normalizeWebsiteDomain` всегда вырезает только hostname из URL — нет возможности сохранить путь
2. `pages` таблица не используется при импорте вообще
3. Staging: `PK(job_id, domain)` — нельзя хранить несколько URL от одного домена
4. `Page` модель: нет `accepted`, нет тегов
5. `crud/Page.go` — нет фильтров, нет экспорта

---

## Схема изменений

### A. Модели / БД (4 таблицы)

#### 1. `pages` — добавить колонки

```go
// api/models/Page.go — CURRENT
type Page struct {
    gorm.Model
    TargetUri string `gorm:"not null, unique"`
    WebsiteID uint   `gorm:"not null;index"`
    Website   Website
}

// api/models/Page.go — NEW
type Page struct {
    gorm.Model
    TargetUri string    `gorm:"not null;uniqueIndex"`
    WebsiteID uint      `gorm:"not null;index"`
    Accepted  *bool     `gorm:"default null;index:idx_pages_accepted"`
    Website   Website
    PageTags  []PageTag `gorm:"many2many:page_tag_pages;" json:"-"`
}
```

Замечание: исправить тег `"not null, unique"` → `"not null;uniqueIndex"` (в GORM v2 теги разделяются `;`, а не `,`).

#### 2. `page_tags` — новая таблица (аналог `website_tags`)

```go
// api/models/PageTag.go — NEW FILE
package models

import "gorm.io/gorm"

type PageTag struct {
    gorm.Model
    Tag   string `gorm:"not null;uniqueIndex"`
    Pages []Page `gorm:"many2many:page_tag_pages;" json:"-"`
}

type PageTagPage struct {
    PageID    uint `gorm:"not null;uniqueIndex:idx_page_tag_pages_unique"`
    PageTagID uint `gorm:"not null;uniqueIndex:idx_page_tag_pages_unique"`
}

func (PageTagPage) TableName() string {
    return "page_tag_pages"
}
```

#### 3. `website_import_staging` — новая схема

Текущая: `PK(job_id, domain)` — не позволяет хранить несколько URL одного домена.
Новая:

```sql
CREATE TABLE IF NOT EXISTS website_import_staging (
    job_id  TEXT NOT NULL,
    url     TEXT NOT NULL,   -- полный нормализованный URL или просто домен если пути нет
    domain  TEXT NOT NULL,   -- только hostname
    tags    TEXT[],
    PRIMARY KEY (job_id, url)
);
CREATE INDEX IF NOT EXISTS idx_website_import_staging_job    ON website_import_staging(job_id);
CREATE INDEX IF NOT EXISTS idx_website_import_staging_domain ON website_import_staging(job_id, domain);
```

Правило: если у входного значения нет пути (или путь = `/`), то `url = domain`. Это позволяет одним запросом `WHERE url = domain` находить domain-only строки, а `WHERE url <> domain` — строки с полным URL.

#### 4. AutoMigrate в `api/api.go`

```go
// Добавить PageTag и PageTagPage
db.AutoMigrate(
    &models.Website{},
    &models.Page{},
    &models.WebsiteTag{},
    &models.WebsiteTagWebsite{},
    &models.PageTag{},     // NEW
    &models.PageTagPage{}, // NEW
)
```

---

### B. Нормализация URL при парсинге (новая функция)

Файл: `api/crud/Website.go`

Текущая `normalizeWebsiteDomain` — оставить как есть (используется в `tasks/Index.go`).
Добавить новую функцию:

```go
// normalizeImportLine возвращает (domain, url):
// - domain — только hostname (нижний регистр, без точки на конце)
// - url    — нормализованный полный URL если есть значимый путь, иначе == domain
func normalizeImportLine(raw string) (domain string, pageURL string) {
    // 1. Очистить строку, убрать кавычки/пробелы
    // 2. Добавить схему если её нет
    // 3. url.Parse
    // 4. domain = strings.ToLower(parsed.Hostname()), убрать trailing "."
    // 5. path = strings.TrimRight(parsed.Path, "/")
    //    if path == "" → pageURL = domain (domain-only)
    //    else → pageURL = "https://" + domain + path (+ query если есть)
    // Возвращает ("", "") если невалидный URL
}
```

Ключевые решения:
- Схема в сохранённом URL всегда `https` (нормализация)
- Trailing slash на конце пути убирается для единообразия (`/path/` → `/path`)
- Query string сохраняется (`/path?id=1` — уникальный URL)
- Fragment (`#anchor`) отбрасывается

---

### C. Обновление staging (дедупликация)

Функция `bulkUpsertWebsiteImportStaging`:
- PK изменился с `(job_id, domain)` на `(job_id, url)`
- Дедупликация при коллизии: если один и тот же URL встречается дважды — объединяем теги (как было с доменами раньше)
- Структура строки: `{URL string, Domain string, Tags []string}`

```go
type websiteImportStagingRow struct {
    URL    string   // полный URL или просто домен
    Domain string   // только hostname
    Tags   []string
}
```

---

### D. Логика импорта — `processWebsiteImport` (полная переработка)

Файл: `api/crud/Website.go`

Функция остаётся той же сигнатурой, но меняется реализация. Всё выполняется в одной транзакции.

#### Шаг 1: Парсинг файла → staging

Вместо `normalizeWebsiteDomain(record[0])` вызывать `normalizeImportLine(record[0])`.
Если `domain == ""` — пропустить строку.
Если `url == ""` — пропустить строку.

#### Шаг 2: Подсчёт уникальных URL и доменов в staging

```sql
SELECT COUNT(*) FROM website_import_staging WHERE job_id = ?                -- total urls
SELECT COUNT(DISTINCT domain) FROM website_import_staging WHERE job_id = ?   -- total domains
```

#### Шаг 3: Транзакция — обработка доменов (websites)

**Для типа BAD (`accepted = false`):**

```sql
-- 3.1. Вставить новые домены (не существующие в websites)
INSERT INTO websites (domain, accepted, created_at, updated_at)
SELECT DISTINCT s.domain, false, NOW(), NOW()
FROM website_import_staging s
LEFT JOIN websites w ON w.domain = s.domain
WHERE s.job_id = ? AND w.id IS NULL
ON CONFLICT (domain) DO NOTHING
-- Результат: createdDomains

-- 3.2. Обновить updated_at у совпавших ПЛОХИХ доменов (type не меняем — уже false)
-- (не нужно ничего обновлять для bad→bad)

-- 3.3. Для ХОРОШИХ доменов — ничего не трогаем (accepted не меняем, теги не смотрим)
-- → Уже выполнено тем, что ON CONFLICT DO NOTHING не обновляет accepted
```

**Для типа GOOD (`accepted = true`):**

```sql
-- 3.1. Вставить новые домены
INSERT INTO websites (domain, accepted, created_at, updated_at)
SELECT DISTINCT s.domain, true, NOW(), NOW()
FROM website_import_staging s
LEFT JOIN websites w ON w.domain = s.domain
WHERE s.job_id = ? AND w.id IS NULL
-- Результат: createdDomains

-- 3.2. Обновить BAD домены → GOOD
UPDATE websites w
SET accepted = true, updated_at = NOW()
FROM (SELECT DISTINCT domain FROM website_import_staging WHERE job_id = ?) s
WHERE w.domain = s.domain AND w.accepted = false
-- Результат: updatedDomains (bad→good)

-- 3.3. GOOD домены — тип не трогаем, теги обновим на шаге 5
```

#### Шаг 4: Транзакция — обработка страниц (pages)

Только строки где `url <> domain` (есть реальный путь).

**Для типа BAD:**

```sql
-- 4.1. Вставить новые страницы (URL нет в pages вообще)
INSERT INTO pages (target_uri, website_id, accepted, created_at, updated_at)
SELECT s.url, w.id, false, NOW(), NOW()
FROM website_import_staging s
JOIN websites w ON w.domain = s.domain
LEFT JOIN pages p ON p.target_uri = s.url
WHERE s.job_id = ? AND s.url <> s.domain AND p.id IS NULL
-- Результат: createdPages

-- 4.2. Существующие страницы (и хорошие, и плохие) — не трогаем
```

**Для типа GOOD:**

```sql
-- 4.1. Обновить BAD страницы → GOOD
UPDATE pages p
SET accepted = true, updated_at = NOW()
FROM website_import_staging s
WHERE s.job_id = ? AND s.url = p.target_uri AND s.url <> s.domain AND p.accepted = false
-- Результат: updatedPages (bad→good)

-- 4.2. Вставить новые страницы (URL нет в pages вообще)
INSERT INTO pages (target_uri, website_id, accepted, created_at, updated_at)
SELECT s.url, w.id, true, NOW(), NOW()
FROM website_import_staging s
JOIN websites w ON w.domain = s.domain
LEFT JOIN pages p ON p.target_uri = s.url
WHERE s.job_id = ? AND s.url <> s.domain AND p.id IS NULL
-- Результат: createdPages

-- 4.3. Существующие GOOD страницы — тип не трогаем, теги обновим на шаге 5
```

#### Шаг 5: Транзакция — теги страниц (page_tags + page_tag_pages)

```sql
-- 5.1. Создать отсутствующие page_tags
INSERT INTO page_tags (tag, created_at, updated_at)
SELECT t.tag, NOW(), NOW()
FROM (
    SELECT DISTINCT UNNEST(tags) AS tag FROM website_import_staging WHERE job_id = ?
) t
LEFT JOIN page_tags pt ON pt.tag = t.tag
WHERE t.tag IS NOT NULL AND t.tag <> '' AND pt.id IS NULL

-- 5.2. Привязать теги к страницам
-- BAD: только новые страницы (созданные на шаге 4.1)
-- GOOD: новые страницы + обновлённые (bad→good) + существующие хорошие
```

Для BAD:
```sql
INSERT INTO page_tag_pages (page_id, page_tag_id)
SELECT p.id, pt.id
FROM website_import_staging s
JOIN pages p ON p.target_uri = s.url
JOIN LATERAL UNNEST(s.tags) AS t(tag) ON TRUE
JOIN page_tags pt ON pt.tag = t.tag
LEFT JOIN page_tag_pages ptp ON ptp.page_id = p.id AND ptp.page_tag_id = pt.id
WHERE s.job_id = ? AND s.url <> s.domain
  AND p.accepted = false                  -- только страницы которые мы создали/трогали
  AND t.tag IS NOT NULL AND t.tag <> ''
  AND ptp.page_id IS NULL
ON CONFLICT DO NOTHING
```

Для GOOD аналогично, но без фильтра `p.accepted = false`.

#### Шаг 6: Транзакция — теги доменов (website_tags + website_tag_websites)

Правила по типу импорта:
- **BAD**: добавить теги только к доменам которые `accepted = false` (и плохие существующие, и новые)
  - К хорошим доменам теги НЕ добавляем
- **GOOD**: добавить теги ко ВСЕМ доменам из импорта (и bad→good, и хорошие существующие, и новые)

```sql
-- 6.1. Создать отсутствующие website_tags (уже есть в текущем коде)
INSERT INTO website_tags (tag, created_at, updated_at) ...

-- 6.2. Привязать теги к доменам
-- BAD: только домены с accepted = false
-- GOOD: все домены из импорта
INSERT INTO website_tag_websites (website_id, website_tag_id)
SELECT w.id, wt.id
FROM website_import_staging s
JOIN websites w ON w.domain = s.domain
JOIN LATERAL UNNEST(s.tags) AS t(tag) ON TRUE
JOIN website_tags wt ON wt.tag = t.tag
LEFT JOIN website_tag_websites wtw ON wtw.website_id = w.id AND wtw.website_tag_id = wt.id
WHERE s.job_id = ?
  AND [BAD: w.accepted = false | GOOD: TRUE]
  AND t.tag IS NOT NULL AND t.tag <> ''
  AND wtw.website_id IS NULL
ON CONFLICT DO NOTHING
```

#### Результат импорта (возвращаемая map)

Расширить текущий result:
```go
return map[string]any{
    "type":              job.Type,
    "accepted":          job.Accepted,
    "total_lines":       totalLines,
    "valid_urls":        validURLs,        // было valid_domains
    "unique_urls":       uniqueURLs,
    "unique_domains":    uniqueDomains,
    "created_pages":     createdPages,     // NEW
    "updated_pages":     updatedPages,     // NEW (bad→good)
    "created_domains":   createdDomains,
    "updated_domains":   updatedDomains,   // NEW (bad→good)
    "tags_created":      tagsCreated,
    "page_tag_links":    pageTagLinks,     // NEW
    "website_tag_links": websiteTagLinks,
}, nil
```

---

### E. API — новые и обновлённые роуты

Файл: `api/api.go`

```go
// Существующие — без изменений:
router.GET("/websites", crud.WebsiteListHandler(db))
router.GET("/websites/export", crud.WebsiteExportTSVHandler(db))
router.POST("/websites/accepted/import", crud.WebsiteBulkAcceptedImportHandler(db))
router.GET("/websites/accepted/import/:jobID", crud.WebsiteBulkAcceptedImportStatusHandler())
router.GET("/dashboard", crud.DashboardGetHandler(db))

// Страницы — обновить + добавить:
router.GET("/pages", crud.PageListHandler(db))       // обновить: добавить фильтры
router.GET("/pages/export", crud.PageExportTSVHandler(db))  // NEW
```

---

### F. `crud/Page.go` — полная переработка

Аналогично `crud/Website.go`. Добавить:

#### Фильтры страниц

```go
type pageFilters struct {
    CMS      []string `json:"cms"`       // через website.cms
    Lang     []string `json:"lang"`      // через website.lang
    Tag      string   `json:"tag"`       // page tag ИЛИ website tag
    Tags     []string `json:"tags"`
    IsForum  []bool   `json:"is_forum"`  // через website.is_forum
    Accepted []bool   `json:"accepted"`
    Detected []bool   `json:"detected"`  // через website.cms
}
```

Важно: фильтры по `cms`, `lang`, `is_forum`, `detected` — через JOIN с `websites`.
Фильтр по тегам — объединение тегов страницы (`page_tag_pages`) и тегов домена (`website_tag_websites`).

```sql
-- Фильтр по тегу (страница ИЛИ домен):
WHERE EXISTS (
    SELECT 1 FROM page_tag_pages ptp
    JOIN page_tags pt ON pt.id = ptp.page_tag_id
    WHERE ptp.page_id = pages.id AND pt.tag IN ?
)
OR EXISTS (
    SELECT 1 FROM website_tag_websites wtw
    JOIN website_tags wt ON wt.id = wtw.website_tag_id
    WHERE wtw.website_id = pages.website_id AND wt.tag IN ?
)
```

#### Мета для страниц

Аналогично `buildWebsiteMeta`:
```go
type pageListMeta struct {
    CMS      []valueCountString
    Lang     []valueCountString
    Tags     []valueCountString  // page tags + website tags (объединённые)
    IsForum  []valueCountBool
    Detected []valueCountBool
    Accepted int64              // кол-во хороших
    ToReview int64              // кол-во без решения
}
```

#### Ответ списка страниц

```go
type pageListItem struct {
    ID        uint       `json:"id"`
    CreatedAt time.Time  `json:"created_at"`
    TargetUri string     `json:"target_uri"`
    Domain    string     `json:"domain"`      // из website
    CMS       string     `json:"cms"`
    IsForum   bool       `json:"is_forum"`
    Lang      string     `json:"lang"`
    Accepted  *bool      `json:"accepted"`
    Tags      []string   `json:"tags"`        // page tags + website tags (объединённые, uniq)
}
```

#### Экспорт страниц

`GET /pages/export?type=all|to_review|placement&filters=...`
Формат TSV: `url, domain, cms, lang, is_forum, accepted`

---

### G. `crud/Website.go` — обновление списка доменов

Добавить `pages_count` в ответ списка доменов.

```go
type websiteListItem struct {
    // ... существующие поля ...
    PagesCount int64 `json:"pages_count"`  // NEW
}
```

Запрос: JOIN или subquery к `pages`:

```sql
SELECT w.*, 
       COUNT(p.id) as pages_count
FROM websites w
LEFT JOIN pages p ON p.website_id = w.id
GROUP BY w.id
```

Или через Preload + len, или через subquery в Select. Рекомендую subquery для эффективности при пагинации.

---

### H. Frontend

#### 1. `app/app.vue` — добавить пункт меню

```js
const items = [
  { label: 'Дашборд', icon: 'i-lucide-house', to: '/' },
  { label: 'База площадок', icon: 'i-lucide-database', to: '/websites' },
  { label: 'Страницы', icon: 'i-lucide-link', to: '/pages' },  // NEW
]
```

#### 2. `app/pages/websites/index.vue` — колонка pages_count

В `tableRows` computed добавить:
```js
'Страниц': item.pages_count ?? 0
```

Также обновить мета-отображение результата импорта (новые поля: created_pages, updated_pages, updated_domains).

#### 3. `app/pages/pages/index.vue` — новый файл

Структура аналогична `websites/index.vue`:
- Фильтры: CMS, Язык, Теги, Форум, Одобрено, Распознано
- Таблица: ID, URL, Домен, CMS, Язык, Форум, Принят
- Пагинация (load more)
- Экспорт с фильтрами (кнопка + выпадающее меню: Все, К проверке, Готовые)
- Нет импорта на этой странице (импорт только через /websites)

---

## Порядок реализации (без поломок)

1. **Модели** (`models/Page.go`, новый `models/PageTag.go`)
   - GORM AutoMigrate добавит новые столбцы и таблицы к существующим данным
   - Добавление `accepted` к `pages`: nullable, default null — не ломает существующих записей

2. **`api.go`** — обновить AutoMigrate

3. **`crud/Website.go`** — переработать:
   - `normalizeImportLine` (новая функция)
   - `websiteImportStagingRow` (добавить поле URL)
   - `ensureWebsiteImportStagingTable` (новая схема + пересоздание если нужно)
   - `bulkUpsertWebsiteImportStaging` (новая дедупликация по URL)
   - `processWebsiteImport` (новая логика по шагам D.1–D.6)
   - `WebsiteListHandler` — добавить `pages_count`

4. **`crud/Page.go`** — полная переработка (добавить фильтры, мета, экспорт)

5. **`api.go`** — зарегистрировать `GET /pages/export`

6. **Frontend** — `app.vue`, `websites/index.vue`, новый `pages/index.vue`

---

## Риски и граничные случаи

### Staging table schema migration
Текущая таблица `website_import_staging` создаётся через `ensureWebsiteImportStagingTable` (не AutoMigrate). При изменении схемы нужно либо:
- **Вариант A**: DROP TABLE + CREATE в `ensureWebsiteImportStagingTable` — безопасно, таблица очищается после каждого джоба
- **Вариант B**: ALTER TABLE — сложнее, не нужен

Рекомендую Вариант A: добавить `DROP TABLE IF EXISTS website_import_staging` перед `CREATE TABLE IF NOT EXISTS`.

### Существующие записи в `pages`
Текущие записи в `pages` (если есть) — не имеют `accepted`. После миграции будет `NULL`. Это корректное состояние "не проверено".

### Дублирование при существующей staging
Если staging table уже существует со старой схемой — DROP + CREATE решает это.

### URL нормализация: коллизии
`https://domain.tld/path/` и `https://domain.tld/path` — после нормализации (strip trailing slash) будут одним URL. Это правильно.

### Теги страниц в фильтре: производительность
Фильтр `OR EXISTS (page tags) OR EXISTS (website tags)` может быть медленным на больших таблицах. Нужен составной индекс или материализованное представление. Пока оставляем OR EXISTS, при необходимости оптимизируем позже.

### `tasks/Index.go`
Использует `normalizeWebsiteDomain` из `scanner.Text()` — не трогает, продолжает работать с доменами (plain text файлы доменов без путей).

### `tasks/Detect.go`
Работает только с `websites` — не затрагивается.

---

## Файлы затронутые изменениями

| Файл | Тип изменения |
|---|---|
| `api/models/Page.go` | Изменение |
| `api/models/PageTag.go` | Новый файл |
| `api/api.go` | AutoMigrate + роут |
| `api/crud/Website.go` | Переработка import + website list |
| `api/crud/Page.go` | Полная переработка |
| `app/app.vue` | Пункт меню |
| `app/pages/websites/index.vue` | Колонка pages_count + результат импорта |
| `app/pages/pages/index.vue` | Новый файл |

**Файлы без изменений:**
- `api/tasks/Index.go`
- `api/tasks/Detect.go`
- `api/detector/Detector.go`
- `api/models/Website.go`
- `api/models/Dashboard.go`
- `api/crud/Dashboard.go`
- `app/pages/index.vue`
- `nuxt.config.ts`
