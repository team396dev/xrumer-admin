<script setup>
  const config = useRuntimeConfig()
  const apiBaseUrl = import.meta.server ? config.apiBaseUrl : config.public.apiBaseUrl

  const { data: dashboard, refresh: refreshDashboard } = await useFetch('/dashboard', {
    baseURL: apiBaseUrl
  })

  let refreshTimer

  onMounted(() => {
    refreshTimer = setInterval(() => {
      refreshDashboard()
    }, 5 * 60 * 1000)
  })

  onUnmounted(() => {
    if (refreshTimer) {
      clearInterval(refreshTimer)
    }
  })

  const cards = computed(() => {
    if (!dashboard.value) {
      return {
        total: 0,
        checked: 0,
        detected: 0,
        toPlacement: 0,
        proxy: 0
      }
    }

    return {
      total: dashboard.value.Total,
      checked: dashboard.value.Checked,
      detected: dashboard.value.Detected,
      toPlacement: dashboard.value.ToPlacement,
      proxy: `${dashboard.value.ProxyTotal}`
    }
  })

  const cmsTable = computed(() => {
    return (dashboard.value?.CmsTable || []).map(item => ({
      CMS: item.Name,
      Amount: item.Total
    }))
  })

  const tagTable = computed(() => {
    return (dashboard.value?.TagTable || []).map(item => ({
      Tag: item.Name,
      Amount: item.Total
    }))
  })
</script>

<template>
  <div>
    <UPageGrid class="relative grid grid-cols-1 sm:grid-cols-3 lg:grid-cols-5 gap-8">
      <UPageCard :title="String(cards.total)" description="Всего сайтов" icon="i-lucide-earth"/>
      <UPageCard :title="String(cards.checked)" description="Сайтов проверено" icon="i-lucide-list-checks"/>
      <UPageCard :title="String(cards.detected)" description="Сайтов распознано" icon="i-lucide-search-check"/>
      <UPageCard :title="String(cards.toPlacement)" description="Сайтов под размещение" icon="i-lucide-circle-check-big"/>
      <UPageCard :title="String(cards.proxy)" description="Прокси" icon="i-lucide-network"/>
    </UPageGrid>
    <UCard class="mt-5">
      <template #header>
        Статистика тегов
      </template>
      <UTable :data="tagTable" class="flex-1" />
    </UCard>
    <UCard class="mt-5">
      <template #header>
        Статистика CMS
      </template>
      <UTable :data="cmsTable" class="flex-1" />
    </UCard>
  </div>
</template>
