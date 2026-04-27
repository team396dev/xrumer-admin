Математика задачи
29 млн сайтов за приемлемое время (скажем, 3 дня):
29 000 000 / (3 * 86400) = ~112 сайтов/секунду
Сейчас ты делаешь:
1 700 000 / (3 * 86400) = ~6.5 сайтов/секунду
Нужно ускориться в ~17 раз. Это вполне достижимо.

Блок 1 — Оптимизация одного сервера (сделай это ПЕРВЫМ)
Большинство краулеров упираются не в железо, а в неправильную конфигурацию.
1.1 HTTP-клиент — главный виновник
go// Типичная ошибка — дефолтный клиент или неправильный transport
// Правильная конфигурация:
transport := &http.Transport{
    MaxIdleConns:        10000,
    MaxIdleConnsPerHost: 50,      // ключевой параметр
    MaxConnsPerHost:     50,
    IdleConnTimeout:     30 * time.Second,
    DisableKeepAlives:   false,
    DialContext: (&net.Dialer{
        Timeout:   5 * time.Second,  // connect timeout
        KeepAlive: 30 * time.Second,
        DualStack: true,
    }).DialContext,
    TLSHandshakeTimeout:   5 * time.Second,
    ResponseHeaderTimeout: 10 * time.Second,
    ExpectContinueTimeout: 1 * time.Second,
    // Не читай весь body если не нужно
    DisableCompression: false,
}

client := &http.Client{
    Transport: transport,
    Timeout:   15 * time.Second,  // общий таймаут
    // НЕ следуй редиректам больше 2 раз
    CheckRedirect: func(req *http.Request, via []*http.Request) error {
        if len(via) >= 2 {
            return http.ErrUseLastResponse
        }
        return nil
    },
}
1.2 Читай только то, что нужно
go// Не читай весь body — тебе нужны первые ~50KB для определения CMS
resp, err := client.Do(req)
if err != nil { return }
defer resp.Body.Close()

// Ограничь чтение
limitedReader := io.LimitReader(resp.Body, 50*1024) // 50KB
body, err := io.ReadAll(limitedReader)

// Для CMS часто достаточно только заголовков!
// WordPress: X-Powered-By, headers, /wp-login.php
// Проверяй заголовки ДО чтения body

// Дефолтный Go DNS resolver однопоточный и медленный
// Используй кастомный resolver

import "github.com/miekg/dns"

// Или настрой системный resolver на быстрый DNS
// /etc/resolv.conf:
// nameserver 8.8.8.8
// nameserver 1.1.1.1
// options timeout:2 attempts:2 rotate

// Либо используй dnscache библиотеку
// github.com/rs/dnscache
resolver := &dnscache.Resolver{}
// Обновляй кэш каждые 5 минут