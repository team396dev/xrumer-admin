<script setup>
const config = useRuntimeConfig()
const apiBaseUrl = import.meta.server ? config.apiBaseUrl : config.public.apiBaseUrl

const importType = ref(null) // 'good' | 'bad' | null

const actionsImport = [
  { label: 'Хорошие', onSelect: () => { importType.value = 'good' } },
  { label: 'Плохие', onSelect: () => { importType.value = 'bad' } }
]

const importOpen = computed({
  get: () => importType.value !== null,
  set: (v) => { if (!v) importType.value = null }
})

const importTitle = computed(() =>
  importType.value === 'good' ? 'Хорошие площадки' : 'Плохие площадки'
)

const importFile = ref(null)
const importing = ref(false)
const importError = ref('')
const importResult = ref(null)

const page = ref(1)
const perPage = 20

const selectedCms = ref([])
const selectedLang = ref([])
const selectedTags = ref([])
const selectedForum = ref([])
const selectedAccepted = ref([])
const selectedDetected = ref([])

const websites = ref([])
const loading = ref(false)
const loadingMore = ref(false)
const exportingType = ref('')

const pagination = reactive({
  page: 1,
  per_page: perPage,
  total: 0,
  has_next_page: false
})

const meta = reactive({
  cms: [],
  lang: [],
  tags: [],
  is_forum: [],
  detected: []
})

const boolLabelByValue = {
  true: 'Да',
  false: 'Нет'
}

function toFacetOption(item) {
  const value = String(item?.value || 'undefined')
  const count = Number(item?.count || 0)

  return {
    value,
    label: `${value} (${count})`,
    disabled: count === 0
  }
}

function toBoolFacetOption(item) {
  const value = Boolean(item?.value)
  const count = Number(item?.count || 0)

  return {
    value,
    label: `${boolLabelByValue[String(value)]} (${count})`,
    disabled: count === 0
  }
}

const cmsOptions = computed(() => {
  return meta.cms
    .map(toFacetOption)
    .filter(Boolean)
})

const langOptions = computed(() => {
  return meta.lang
    .map(toFacetOption)
    .filter(Boolean)
})

const tagOptions = computed(() => {
  return meta.tags
    .map(toFacetOption)
    .filter(Boolean)
})

const forumOptions = computed(() => {
  return meta.is_forum
    .map(toBoolFacetOption)
    .filter(Boolean)
})

const acceptedOptions = computed(() => {
  return [
    { value: true, label: 'Да' },
    { value: false, label: 'Нет' }
  ]
})

const detectedOptions = computed(() => {
  return meta.detected
    .map(toBoolFacetOption)
    .filter(Boolean)
})

const headerDescription = computed(() => `Всего площадок: ${pagination.total}`)

const tableRows = computed(() => {
  return websites.value.map(item => ({
    ID: item.ID ?? item.id,
    Домен: item.Domain ?? item.domain,
    CMS: (item.CMS ?? item.cms) || '—',
    Язык: (item.Lang ?? item.lang) || '—',
    Форум: (item.IsForum ?? item.is_forum) ? 'Да' : 'Нет',
    Статус: item.Status ?? item.status,
    Принят: (item.Accepted ?? item.accepted) ? 'Да' : 'Нет'
  }))
})

function buildFilters() {
  const filters = {}

  if (selectedCms.value.length) {
    filters.cms = selectedCms.value
  }

  if (selectedLang.value.length) {
    filters.lang = selectedLang.value
  }

  if (selectedTags.value.length) {
    filters.tags = selectedTags.value
  }

  if (selectedForum.value.length) {
    filters.is_forum = selectedForum.value
  }

  if (selectedAccepted.value.length) {
    filters.accepted = selectedAccepted.value
  }

  if (selectedDetected.value.length) {
    filters.detected = selectedDetected.value
  }

  return filters
}

function getFilenameFromDisposition(contentDisposition, fallbackName) {
  if (!contentDisposition) {
    return fallbackName
  }

  const match = contentDisposition.match(/filename="?([^";]+)"?/i)
  if (!match || !match[1]) {
    return fallbackName
  }

  return match[1]
}

async function exportWebsites(type) {
  if (exportingType.value) {
    return
  }

  exportingType.value = type

  try {
    const filters = buildFilters()
    const query = { type }

    if (Object.keys(filters).length > 0) {
      query.filters = JSON.stringify(filters)
    }

    const response = await $fetch.raw('/websites/export', {
      baseURL: apiBaseUrl,
      query,
      responseType: 'blob'
    })

    const blob = response._data
    const fallbackName = `websites_${type}.tsv`
    const filename = getFilenameFromDisposition(response.headers.get('content-disposition'), fallbackName)
    const objectURL = URL.createObjectURL(blob)
    const link = document.createElement('a')

    link.href = objectURL
    link.download = filename
    document.body.appendChild(link)
    link.click()
    link.remove()
    URL.revokeObjectURL(objectURL)
  } finally {
    exportingType.value = ''
  }
}

function onImportFileChange(event) {
  const target = event.target
  importFile.value = target?.files?.[0] || null
  importError.value = ''
  importResult.value = null
}

watch(importOpen, (isOpen) => {
  if (!isOpen) {
    importFile.value = null
    importError.value = ''
    importResult.value = null
  }
})

async function submitImport() {
  if (importing.value) {
    return
  }

  if (!importType.value) {
    importError.value = 'Не выбран тип импорта'
    return
  }

  if (!importFile.value) {
    importError.value = 'Выберите .tsv файл'
    return
  }

  importError.value = ''
  importResult.value = null
  importing.value = true

  try {
    const formData = new FormData()
    formData.append('type', importType.value)
    formData.append('file', importFile.value)

    const response = await $fetch.raw('/websites/accepted/import', {
      baseURL: apiBaseUrl,
      method: 'POST',
      body: formData,
      ignoreResponseError: true
    })

    const payload = response?._data || {}
    if (!response.ok) {
      throw new Error(payload?.error || `Import failed with status ${response.status}`)
    }

    importResult.value = payload
    await resetAndReload()
  } catch (error) {
    importError.value = error?.message || 'Ошибка импорта'
  } finally {
    importing.value = false
  }
}

const actionsExport = computed(() => [
  {
    label: 'Все',
    description: `${pagination.total}`,
    onSelect: () => exportWebsites('all')
  },
  {
    label: 'К проверке',
    description: `${meta.to_review}`,
    onSelect: () => exportWebsites('to_review')
  },
  {
    label: 'Готовые к размещению',
    description: `${meta.accepted}`,
    onSelect: () => exportWebsites('placement')
  }
])

async function loadWebsites({ append = false } = {}) {
  if (append) {
    loadingMore.value = true
  } else {
    loading.value = true
  }

  try {
    const filters = buildFilters()
    const query = {
      page: page.value,
      per_page: perPage
    }

    if (Object.keys(filters).length > 0) {
      query.filters = JSON.stringify(filters)
    }

    const response = await $fetch('/websites', {
      baseURL: apiBaseUrl,
      query
    })

    websites.value = append
      ? [...websites.value, ...(response.items || [])]
      : response.items || []

    if (response.pagination) {
      pagination.page = response.pagination.page
      pagination.per_page = response.pagination.per_page
      pagination.total = response.pagination.total
      pagination.has_next_page = response.pagination.has_next_page
    }

    if (response.meta) {
      meta.cms = response.meta.cms || []
      meta.lang = response.meta.lang || []
      meta.tags = response.meta.tags || []
      meta.is_forum = response.meta.is_forum || []
      meta.detected = response.meta.detected || []
      meta.to_review = response.meta.to_review || 0
      meta.accepted = response.meta.accepted || 0
    }
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

async function resetAndReload() {
  page.value = 1
  await loadWebsites()
}

async function loadMore() {
  if (!pagination.has_next_page || loadingMore.value) {
    return
  }

  page.value += 1
  await loadWebsites({ append: true })
}

watch([selectedCms, selectedLang, selectedTags, selectedForum, selectedAccepted, selectedDetected], () => {
  resetAndReload()
}, { deep: true })

await loadWebsites()
</script>

<template>
  <div>
    <UPageHeader title="База сайтов" :description="headerDescription">
      <template #links>
        <UFieldGroup>
          <UButton color="neutral" icon="i-lucide-upload" variant="subtle" label="Импорт" />
          <UDropdownMenu :items="actionsImport">
            <UButton color="neutral" variant="outline" icon="i-lucide-chevron-down" />
          </UDropdownMenu>
        </UFieldGroup>

        <UFieldGroup>
          <UButton
            color="neutral"
            icon="i-lucide-download"
            variant="subtle"
            label="Экспорт"
            :loading="exportingType === 'all'"
            @click="exportWebsites('all')"
          />
          <UDropdownMenu :items="actionsExport">
            <UButton color="neutral" variant="outline" icon="i-lucide-chevron-down" :loading="Boolean(exportingType)" />
          </UDropdownMenu>
        </UFieldGroup>
      </template>
    </UPageHeader>

    <UPageGrid class="relative grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-6 gap-4 mt-6 mb-6">
      <USelectMenu v-model="selectedCms" multiple :items="cmsOptions" value-key="value" placeholder="CMS" />
      <USelectMenu v-model="selectedLang" multiple :items="langOptions" value-key="value" placeholder="Язык" />
      <USelectMenu v-model="selectedTags" multiple :items="tagOptions" value-key="value" placeholder="Теги" />
      <USelectMenu v-model="selectedForum" multiple :items="forumOptions" value-key="value" placeholder="Форум" />
      <USelectMenu v-model="selectedAccepted" multiple :items="acceptedOptions" value-key="value" placeholder="Одобрено" />
      <USelectMenu v-model="selectedDetected" multiple :items="detectedOptions" value-key="value" placeholder="Распознано" />
    </UPageGrid>

    <UTable :data="tableRows" class="flex-1" :loading="loading" />

    <div class="mt-4 flex justify-center" v-if="pagination.has_next_page">
      <UButton
        color="neutral"
        variant="outline"
        label="Показать еще"
        :loading="loadingMore"
        @click="loadMore"
      />
    </div>
  </div>
  <UModal :title="importTitle" v-model:open="importOpen">
    <template #body>
      <div class="space-y-4">
        <p class="text-sm text-muted">
          Загрузите TSV файл (.tsv): в колонке A домен/ссылка, в колонке B теги.
          Теги можно передавать как "Тег 1", "Тег 1, Тег 2" или "Тег 1,Тег2".
        </p>

        <input
          type="file"
          accept=".tsv,text/tab-separated-values,text/plain"
          class="block w-full text-sm"
          @change="onImportFileChange"
        >

        <div class="flex items-center justify-between gap-3">
          <span class="text-xs text-muted">Тип: {{ importType }}</span>
          <UButton
            color="primary"
            :label="importType === 'good' ? 'Импортировать хорошие' : 'Импортировать плохие'"
            :loading="importing"
            :disabled="!importFile"
            @click="submitImport"
          />
        </div>

        <UAlert
          v-if="importError"
          color="error"
          variant="soft"
          :title="importError"
        />

        <UAlert
          v-if="importResult"
          color="success"
          variant="soft"
          :title="`Обновлено: ${importResult.updated_rows}`"
          :description="`Строк: ${importResult.total_lines}, валидных доменов: ${importResult.valid_domains}, совпало в БД: ${importResult.matched_domains}, новых доменов: ${importResult.created_domains || 0}, создано тегов: ${importResult.tags_created || 0}, сайтов с тегами: ${importResult.websites_tagged || 0}`"
        />
      </div>
    </template>
  </UModal>
</template>
