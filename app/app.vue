<script setup>
import {ref} from 'vue';

useHead({
  meta: [
    { name: 'viewport', content: 'width=device-width, initial-scale=1' }
  ],
  link: [
    { rel: 'icon', href: '/favicon.ico' }
  ],
  htmlAttrs: {
    lang: 'en'
  }
})

const title = 'Xrumer Serach'
const description = 'Xrumer Search UI'

useSeoMeta({
  title,
  description,
  ogTitle: title,
  ogDescription: description,
})

const open = ref(true)

const items = [
  {
    label: 'Дашборд',
    icon: 'i-lucide-house',
    to: '/',
  },
  {
    label: 'База площадок',
    icon: 'i-lucide-database',
    to: '/websites',
  },
];
</script>

<template>
  <UApp>
    <div class="flex flex-1">
        <USidebar v-model:open="open" title="Xrumer Search" description="Make links great again" collapsible="icon">
            <UNavigationMenu
                :items="items"
                orientation="vertical"
                :ui="{ link: 'p-1.5 overflow-hidden' }"
            />
        </USidebar>

        <div class="flex-1 flex flex-col">
            <div class="h-(--ui-header-height) shrink-0 flex items-center px-4 border-b border-default">
                <UButton
                icon="i-lucide-panel-left"
                color="neutral"
                variant="ghost"
                :aria-label="open ? 'Close sidebar' : 'Open sidebar'"
                @click="open = !open"
                />
            </div>

            <div class="flex-1 p-4">
                <NuxtPage />
            </div>
        </div>
    </div>
  </UApp>
</template>
