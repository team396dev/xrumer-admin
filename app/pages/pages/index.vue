<script setup>
const config = useRuntimeConfig()
const apiBaseUrl = import.meta.server ? config.apiBaseUrl : config.public.apiBaseUrl

const page = ref(1)
const perPage = 20

const selectedCms = ref([])
const selectedLang = ref([])
const selectedTags = ref([])
const selectedForum = ref([])
const selectedAccepted = ref([])
const selectedDetected = ref([])

const pages = ref([])
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
  detected: [],
  accepted: 0,
  to_review: 0
})

const boolLabelByValue = {
  true: 'Да',
  false: 'Нет'
}

function toFacetOption(item) {
  const value = String(item?.value || 'undefined')
  const count = Number(item?.count || 0)
  return { value, label: `${value} (${count})`, disabled: count === 0 }
}

function toBoolFacetOption(item) {
  const value = Boolean(item?.value)
  const count = Number(item?.count || 0)
  return { value, label: `${boolLabelByValue[String(value)]} (${count})`, disabled: count === 0 }
}

const cmsOptions = computed(() => meta.cms.map(toFacetOption).filter(Boolean))
const langOptions = computed(() => meta.lang.map(toFacetOption).filter(Boolean))
const tagOptions = computed(() => meta.tags.map(toFacetOption).filter(Boolean))
const forumOptions = computed(() => meta.is_forum.map(toBoolFacetOption).filter(Boolean))
const acceptedOptions = computed(() => [
  { value: true, label: 'Да' },
  { value: false, label: 'Нет' }
])
const detectedOptions = computed(() => meta.detected.map(toBoolFacetOption).filter(Boolean))

const headerDescription = computed(() => `Всего страниц: ${pagination.total}`)

const tableRows = computed(() => {
  return pages.value.map(item => ({
    ID: item.id,
    URL: item.target_uri,
    Домен: item.domain || '—',
    CMS: item.cms || '—',
    Язык: item.lang || '—',
    Форум: item.is_forum ? 'Да' : 'Нет',
    Принят: item.accepted === null || item.accepted === undefined ? '—' : (item.accepted ? 'Да' : 'Нет')
  }))
})

function buildFilters() {
  const filters = {}
  if (selectedCms.value.length) filters.cms = selectedCms.value
  if (selectedLang.value.length) filters.lang = selectedLang.value
  if (selectedTags.value.length) filters.tags = selectedTags.value
  if (selectedForum.value.length) filters.is_forum = selectedForum.value
  if (selectedAccepted.value.length) filters.accepted = selectedAccepted.value
  if (selectedDetected.value.length) filters.detected = selectedDetected.value
  return filters
}

function getFilenameFromDisposition(contentDisposition, fallbackName) {
  if (!contentDisposition) return fallbackName
  const match = contentDisposition.match(/filename="?([^";]+)"?/i)
  if (!match || !match[1]) return fallbackName
  return match[1]
}

const actionsExport = computed(() => [
  {
    label: 'Все',
    description: `${pagination.total}`,
    onSelect: () => exportPages('all')
  },
  {
    label: 'К проверке',
    description: `${meta.to_review}`,
    onSelect: () => exportPages('to_review')
  },
  {
    label: 'Готовые к размещению',
    description: `${meta.accepted}`,
    onSelect: () => exportPages('placement')
  }
])

async function exportPages(type) {
  if (exportingType.value) return
  exportingType.value = type

  try {
    const filters = buildFilters()
    const query = { type }
    if (Object.keys(filters).length > 0) {
      query.filters = JSON.stringify(filters)
    }

    const response = await $fetch.raw('/pages/export', {
      baseURL: apiBaseUrl,
      query,
      responseType: 'blob'
    })

    const blob = response._data
    const fallbackName = `pages_${type}.tsv`
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

async function loadPages({ append = false } = {}) {
  if (append) {
    loadingMore.value = true
  } else {
    loading.value = true
  }

  try {
    const filters = buildFilters()
    const query = { page: page.value, per_page: perPage }
    if (Object.keys(filters).length > 0) {
      query.filters = JSON.stringify(filters)
    }

    const response = await $fetch('/pages', {
      baseURL: apiBaseUrl,
      query
    })

    pages.value = append
      ? [...pages.value, ...(response.items || [])]
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
      meta.accepted = response.meta.accepted || 0
      meta.to_review = response.meta.to_review || 0
    }
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

async function resetAndReload() {
  page.value = 1
  await loadPages()
}

async function loadMore() {
  if (!pagination.has_next_page || loadingMore.value) return
  page.value += 1
  await loadPages({ append: true })
}

watch([selectedCms, selectedLang, selectedTags, selectedForum, selectedAccepted, selectedDetected], () => {
  resetAndReload()
}, { deep: true })

await loadPages()
</script>

<template>
  <div>
    <UPageHeader title="Страницы" :description="headerDescription">
      <template #links>
        <UFieldGroup>
          <UButton
            color="neutral"
            icon="i-lucide-download"
            variant="subtle"
            label="Экспорт"
            :loading="exportingType === 'all'"
            @click="exportPages('all')"
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
</template>
