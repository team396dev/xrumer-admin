// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  runtimeConfig: {
    apiBaseUrl: process.env.API_URL || 'http://api:8080',
    public: {
      apiBaseUrl: process.env.NUXT_PUBLIC_API_BASE_URL || '/backend'
    }
  },

  modules: [
    '@nuxt/eslint',
    '@nuxt/ui'
  ],

  sourcemap: {
    client: false,
    server: false
  },

  nitro: {
    sourceMap: false
  },

  devServer: {
    host: '0.0.0.0',
    port: 3000
  },

  devtools: {
    enabled: true
  },

  css: ['~/assets/css/main.css'],

  routeRules: {
    '/': { prerender: true },
    '/backend/**': {
      proxy: `${process.env.API_URL || 'http://api:8080'}/**`
    }
  },

  compatibilityDate: '2025-01-15',

  eslint: {
    config: {
      stylistic: {
        commaDangle: 'never',
        braceStyle: '1tbs'
      }
    }
  }
})
