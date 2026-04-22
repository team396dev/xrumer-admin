package detector

import (
	"bufio"
	"errors"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	proxyFilePath   = "proxy.txt"
	maxProxyRetries = 3
	requestTimeout  = 20 * time.Second
	maxBodyBytes    = 4 * 1024 * 1024
)

var (
	reLang        = regexp.MustCompile(`(?is)<html[^>]*\blang\s*=\s*['"]?([^'"\s>]+)`)
	reGenerator   = regexp.MustCompile(`(?is)<meta[^>]*name\s*=\s*['"]generator['"][^>]*content\s*=\s*['"]([^'"]+)['"][^>]*>`)
	reAssetAttr   = regexp.MustCompile(`(?is)\b(?:src|href|data-src|data-href|content)\s*=\s*['"]([^'"#]+)['"]`)
	reComments    = regexp.MustCompile(`(?is)<!--(.*?)-->`)
	reShopifyJS   = regexp.MustCompile(`(?is)\b(?:window\.|)Shopify\b`)
	reWordPressJS = regexp.MustCompile(`(?is)\bwp-json\b|\bwp-embed\b`)
	reDrupalJS    = regexp.MustCompile(`(?is)\bDrupal\.settings\b|\bdrupalSettings\b`)
	reBitrixJS    = regexp.MustCompile(`(?is)\bBX\.(?:message|ready|setCSSList)\b`)
	reTYPO3JS     = regexp.MustCompile(`(?is)\bTYPO3\b|\bTYPO3\.settings\b`)
	reOctoberJS   = regexp.MustCompile(`(?is)\boctober\.(?:request|ajax|framework)\b|\boc\.(?:request|ajax)\b`)
	reMODXJS      = regexp.MustCompile(`(?is)\bMODx\b|\bmodx\.(?:config|lang|loadRTE)\b`)
	reStrapiJS    = regexp.MustCompile(`(?is)\bstrapi\b|\b_strapi\b`)
	reContentful  = regexp.MustCompile(`(?is)\bcontentful\b|\bctfassets\.net\b|\bcdn\.contentful\.com\b`)
	rePhpBB       = regexp.MustCompile(`(?is)\bphpbb\b|\bviewtopic\.php\b|\bviewforum\.php\b`)
	reVBulletin   = regexp.MustCompile(`(?is)\bvbulletin\b|\bclientscript/vbulletin_\b`)
	reXenForo     = regexp.MustCompile(`(?is)\bxenforo\b|\bjs/xf/\b|\bxf\.[a-z0-9_]+\b`)
	reDiscourse   = regexp.MustCompile(`(?is)\bdiscourse\b|\bdata-discourse-\b|\bdiscourseComputed\b`)
	reIPS         = regexp.MustCompile(`(?is)\bips\.(?:app|ui|utils)\b|\binvision\s+community\b`)
	reMyBB        = regexp.MustCompile(`(?is)\bmybb\b|\bmybb\[[^\]]+\]\b`)
	reSMF         = regexp.MustCompile(`(?is)\bsmf\b|\bindex\.php\?action=\b`)
	reFlarum      = regexp.MustCompile(`(?is)\bflarum\b|\bassets/forum-[a-z0-9]+\.js\b`)
	reVanilla     = regexp.MustCompile(`(?is)\bvanilla\s+forums\b|\bjs/vanilla\b|\bvanilla\.[a-z0-9_]+\b`)
	reNodeBB      = regexp.MustCompile(`(?is)\bnodebb\b|\bassets/nodebb(?:\.min)?\.js\b|\bapp\.config\['relative_path'\]\b`)
)

type DetectionResult struct {
	Lang   string
	CMS    string
	Status int
}

type signalStrength int

const (
	weakSignal signalStrength = iota + 1
	mediumSignal
	strongSignal
)

type cmsEvidence struct {
	CMS       string
	Strong    int
	Medium    int
	Weak      int
	HasDirect bool
}

type pageSnapshot struct {
	URL       string
	Status    int
	Headers   http.Header
	Cookies   []*http.Cookie
	Body      string
	BodyLower string
}

func Detect(domain string) DetectionResult {
	result := DetectionResult{CMS: "undefined", Status: -1}

	normalizedDomain := normalizeDomain(domain)
	if normalizedDomain == "" {
		return result
	}

	proxies := readProxyList(resolveProxyPath())
	page, err := fetchHomepage(normalizedDomain, proxies)
	if err != nil {
		return result
	}

	result.Status = page.Status
	result.Lang = extractLang(page.Body)
	result.CMS = detectCMS(page)

	return result
}

func fetchHomepage(domain string, proxies []*url.URL) (pageSnapshot, error) {
	var lastErr error

	for _, targetURL := range []string{"https://" + domain, "http://" + domain} {
		page, err := fetchWithProxyRetry(targetURL, proxies)
		if err == nil {
			return page, nil
		}

		lastErr = err
	}

	if lastErr == nil {
		lastErr = errors.New("failed to fetch homepage")
	}

	return pageSnapshot{}, lastErr
}

func fetchWithProxyRetry(targetURL string, proxies []*url.URL) (pageSnapshot, error) {
	if len(proxies) == 0 {
		return fetchWithClient(targetURL, nil)
	}

	proxyPool := shuffledProxyPool(proxies)
	attempts := maxProxyRetries
	if len(proxyPool) < attempts {
		attempts = len(proxyPool)
	}

	if attempts == 0 {
		return fetchWithClient(targetURL, nil)
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		proxyURL := proxyPool[i]
		page, err := fetchWithClient(targetURL, proxyURL)
		if err == nil {
			return page, nil
		}

		if isProxyError(err) {
			lastErr = err
			continue
		}

		return pageSnapshot{}, err
	}

	if lastErr == nil {
		lastErr = errors.New("proxy retries exhausted")
	}

	return pageSnapshot{}, lastErr
}

func fetchWithClient(targetURL string, proxyURL *url.URL) (pageSnapshot, error) {
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	client := &http.Client{
		Timeout:   requestTimeout,
		Transport: transport,
	}

	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		return pageSnapshot{}, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Connection", "close")

	resp, err := client.Do(req)
	if err != nil {
		return pageSnapshot{}, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return pageSnapshot{}, err
	}

	body := string(bodyBytes)

	return pageSnapshot{
		URL:       resp.Request.URL.String(),
		Status:    resp.StatusCode,
		Headers:   resp.Header.Clone(),
		Cookies:   resp.Cookies(),
		Body:      body,
		BodyLower: strings.ToLower(body),
	}, nil
}

func detectCMS(page pageSnapshot) string {
	allEvidence := map[string]*cmsEvidence{}
	add := func(cms string, strength signalStrength, direct bool) {
		entry, ok := allEvidence[cms]
		if !ok {
			entry = &cmsEvidence{CMS: cms}
			allEvidence[cms] = entry
		}

		switch strength {
		case strongSignal:
			entry.Strong++
		case mediumSignal:
			entry.Medium++
		case weakSignal:
			entry.Weak++
		}

		if direct {
			entry.HasDirect = true
		}
	}

	headersText := headersToLowerText(page.Headers)
	cookieNames := cookiesToLowerNames(page.Cookies)
	generator := strings.ToLower(extractGenerator(page.Body))
	assets := extractAssetURLs(page.Body)
	comments := strings.ToLower(strings.Join(extractHTMLComments(page.Body), "\n"))
	body := page.BodyLower

	if strings.Contains(generator, "wordpress") || strings.Contains(headersText, "wordpress") {
		add("WordPress", strongSignal, true)
	}
	if hasAny(cookieNames, "wordpress_test_cookie", "wp-settings-", "wp-postpass_") {
		add("WordPress", strongSignal, false)
	}
	if assetsContain(assets, "/wp-content/", "/wp-includes/") {
		add("WordPress", strongSignal, false)
	}
	if strings.Contains(body, "wp-json") || reWordPressJS.MatchString(body) || strings.Contains(comments, "wp-content") {
		add("WordPress", mediumSignal, false)
	}

	if strings.Contains(generator, "datalife engine") || strings.Contains(generator, "dle") {
		add("DLE", strongSignal, true)
	}
	if hasAny(cookieNames, "dle_hash", "dle_password", "dle_user_id") {
		add("DLE", strongSignal, false)
	}
	if assetsContain(assets, "/engine/classes/js/dle_js.js", "/templates/", "engine/data/emoticons") {
		add("DLE", mediumSignal, false)
	}

	if strings.Contains(generator, "joomla") || strings.Contains(headersText, "joomla") {
		add("Joomla!", strongSignal, true)
	}
	if assetsContain(assets, "/media/system/js/", "/modules/mod_", "/components/com_", "/templates/system/") {
		add("Joomla!", mediumSignal, false)
	}

	if strings.Contains(generator, "drupal") || strings.Contains(headersText, "drupal") {
		add("Drupal", strongSignal, true)
	}
	if hasPrefixAny(cookieNames, "sse", "sess") || hasAny(cookieNames, "has_js") {
		add("Drupal", mediumSignal, false)
	}
	if assetsContain(assets, "/sites/default/files/", "/core/misc/drupal.js", "/misc/drupal.js") || reDrupalJS.MatchString(page.Body) {
		add("Drupal", strongSignal, false)
	}

	if strings.Contains(generator, "shopify") || strings.Contains(headersText, "x-shopify") {
		add("Shopify", strongSignal, true)
	}
	if hasAny(cookieNames, "_shopify_y", "_shopify_s", "_shopify_sa_p", "_shopify_sa_t") {
		add("Shopify", strongSignal, false)
	}
	if assetsContain(assets, "cdn.shopify.com", "/s/files/1/", "shopifycdn.net") || reShopifyJS.MatchString(page.Body) {
		add("Shopify", strongSignal, false)
	}

	if strings.Contains(generator, "bitrix") || strings.Contains(headersText, "bitrix") {
		add("1C-Битрикс", strongSignal, true)
	}
	if hasPrefixAny(cookieNames, "bitrix_sm_") {
		add("1C-Битрикс", strongSignal, false)
	}
	if assetsContain(assets, "/bitrix/js/", "/bitrix/templates/", "/bitrix/cache/") || reBitrixJS.MatchString(page.Body) {
		add("1C-Битрикс", strongSignal, false)
	}

	if strings.Contains(generator, "modx") || strings.Contains(headersText, "modx") {
		add("MODX", strongSignal, true)
	}
	if hasAny(cookieNames, "modx_sid") {
		add("MODX", strongSignal, false)
	}
	if assetsContain(assets, "/assets/components/", "/manager/templates/", "/connectors/system/") || reMODXJS.MatchString(page.Body) {
		add("MODX", mediumSignal, false)
	}

	if strings.Contains(generator, "octobercms") || strings.Contains(generator, "october cms") || strings.Contains(headersText, "october") {
		add("OctoberCMS", strongSignal, true)
	}
	if hasAny(cookieNames, "october_session") {
		add("OctoberCMS", strongSignal, false)
	}
	if assetsContain(assets, "/modules/system/assets/", "/themes/demo/assets/", "/storage/app/media/") || reOctoberJS.MatchString(page.Body) {
		add("OctoberCMS", mediumSignal, false)
	}

	if strings.Contains(generator, "typo3") || strings.Contains(headersText, "typo3") {
		add("TYPO3", strongSignal, true)
	}
	if hasAny(cookieNames, "fe_typo_user") {
		add("TYPO3", strongSignal, false)
	}
	if assetsContain(assets, "/typo3conf/", "/typo3temp/", "/fileadmin/") || reTYPO3JS.MatchString(page.Body) {
		add("TYPO3", mediumSignal, false)
	}

	if strings.Contains(generator, "magento") || strings.Contains(headersText, "mage") {
		add("Magento", strongSignal, true)
	}
	if hasAny(cookieNames, "form_key", "mage-cache-sessid", "mage-cache-storage", "store") {
		add("Magento", mediumSignal, false)
	}
	if assetsContain(assets, "/static/version", "/frontend/", "/mage/", "/skin/frontend/") {
		add("Magento", mediumSignal, false)
	}

	if strings.Contains(generator, "opencart") {
		add("OpenCart", strongSignal, true)
	}
	if assetsContain(assets, "index.php?route=", "/catalog/view/theme/", "route=common/home") {
		add("OpenCart", mediumSignal, false)
	}

	if strings.Contains(generator, "prestashop") {
		add("PrestaShop", strongSignal, true)
	}
	if hasAny(cookieNames, "prestashop-") || assetsContain(assets, "/modules/", "/themes/", "/img/cms/") {
		add("PrestaShop", mediumSignal, false)
	}

	if strings.Contains(generator, "wix") || strings.Contains(headersText, "x-wix") {
		add("Wix", strongSignal, true)
	}
	if assetsContain(assets, "wixstatic.com", "parastorage.com") || strings.Contains(body, "wixbi") {
		add("Wix", strongSignal, false)
	}

	if strings.Contains(generator, "tilda") {
		add("Tilda", strongSignal, true)
	}
	if assetsContain(assets, "tilda", "tildacdn.com", "tilda.ws") {
		add("Tilda", mediumSignal, false)
	}

	if strings.Contains(generator, "webflow") {
		add("Webflow", strongSignal, true)
	}
	if assetsContain(assets, "website-files.com", "webflow") {
		add("Webflow", mediumSignal, false)
	}

	if strings.Contains(generator, "ghost") {
		add("Ghost", strongSignal, true)
	}
	if assetsContain(assets, "/ghost/assets/", "ghost-sdk") {
		add("Ghost", mediumSignal, false)
	}

	if strings.Contains(generator, "strapi") || strings.Contains(headersText, "x-powered-by:strapi") || strings.Contains(headersText, "strapi") {
		add("Strapi", strongSignal, true)
	}
	if assetsContain(assets, "/uploads/", "/admin/init", "/_strapi") || reStrapiJS.MatchString(page.Body) {
		add("Strapi", mediumSignal, false)
	}

	if strings.Contains(generator, "contentful") || strings.Contains(headersText, "contentful") {
		add("Contentful", strongSignal, true)
	}
	if assetsContain(assets, "ctfassets.net", "cdn.contentful.com", "images.ctfassets.net") || reContentful.MatchString(page.Body) {
		add("Contentful", mediumSignal, false)
	}

	if strings.Contains(generator, "phpbb") {
		add("phpBB", strongSignal, true)
	}
	if hasPrefixAny(cookieNames, "phpbb", "phpbb3_") {
		add("phpBB", strongSignal, false)
	}
	if assetsContain(assets, "/styles/prosilver/", "/styles/all/theme/", "/viewtopic.php", "/viewforum.php") || rePhpBB.MatchString(page.Body) {
		add("phpBB", mediumSignal, false)
	}

	if strings.Contains(generator, "vbulletin") || strings.Contains(headersText, "vbulletin") {
		add("vBulletin", strongSignal, true)
	}
	if hasAny(cookieNames, "bb_sessionhash", "bblastvisit", "bblastactivity") {
		add("vBulletin", strongSignal, false)
	}
	if assetsContain(assets, "/clientscript/vbulletin_", "/images/misc/vbulletin", "/ajax.php?do=") || reVBulletin.MatchString(page.Body) {
		add("vBulletin", mediumSignal, false)
	}

	if strings.Contains(generator, "xenforo") || strings.Contains(headersText, "xenforo") {
		add("XenForo", strongSignal, true)
	}
	if hasAny(cookieNames, "xf_session", "xf_user", "xf_csrf") {
		add("XenForo", strongSignal, false)
	}
	if assetsContain(assets, "/js/xf/", "/styles/default/xenforo/", "/threads/") || reXenForo.MatchString(page.Body) {
		add("XenForo", mediumSignal, false)
	}

	if strings.Contains(generator, "discourse") || strings.Contains(headersText, "x-discourse") {
		add("Discourse", strongSignal, true)
	}
	if hasAny(cookieNames, "_forum_session", "_t", "__profilin") {
		add("Discourse", mediumSignal, false)
	}
	if assetsContain(assets, "/assets/discourse-", "/session/current.json", "/letter_avatar_proxy/") || reDiscourse.MatchString(page.Body) {
		add("Discourse", mediumSignal, false)
	}

	if strings.Contains(generator, "invision community") || strings.Contains(generator, "ips community") || strings.Contains(headersText, "invision") {
		add("Invision Community", strongSignal, true)
	}
	if hasPrefixAny(cookieNames, "ips4_") || hasAny(cookieNames, "ipsconnect", "member_id", "pass_hash") {
		add("Invision Community", strongSignal, false)
	}
	if assetsContain(assets, "/applications/core/interface/", "/uploads/monthly_", "/forums/topic/") || reIPS.MatchString(page.Body) {
		add("Invision Community", mediumSignal, false)
	}

	if strings.Contains(generator, "mybb") || strings.Contains(headersText, "mybb") {
		add("MyBB", strongSignal, true)
	}
	if hasAny(cookieNames, "mybb[lastvisit]", "mybb[lastactive]", "mybb[threadsread]", "mybb[user]", "mybb[password]") {
		add("MyBB", strongSignal, false)
	}
	if assetsContain(assets, "/jscripts/general.js", "/cache/themes/theme", "/showthread.php?tid=") || reMyBB.MatchString(page.Body) {
		add("MyBB", mediumSignal, false)
	}

	if strings.Contains(generator, "simple machines forum") || strings.Contains(generator, "smf") || strings.Contains(headersText, "smf") {
		add("SMF", strongSignal, true)
	}
	if hasAny(cookieNames, "smfcookie", "smf_session") {
		add("SMF", strongSignal, false)
	}
	if assetsContain(assets, "/themes/default/scripts/script.js", "/index.php?action=forum", "/index.php?topic=") || reSMF.MatchString(page.Body) {
		add("SMF", mediumSignal, false)
	}

	if strings.Contains(generator, "flarum") || strings.Contains(headersText, "flarum") {
		add("Flarum", strongSignal, true)
	}
	if hasAny(cookieNames, "flarum_remember", "flarum_session") {
		add("Flarum", strongSignal, false)
	}
	if assetsContain(assets, "/assets/forum-", "/api/discussions", "/d/", "/p/") || reFlarum.MatchString(page.Body) {
		add("Flarum", mediumSignal, false)
	}

	if strings.Contains(generator, "vanilla forums") || strings.Contains(generator, "vanilla") || strings.Contains(headersText, "x-vanilla") {
		add("Vanilla Forums", strongSignal, true)
	}
	if hasPrefixAny(cookieNames, "vanilla") {
		add("Vanilla Forums", strongSignal, false)
	}
	if assetsContain(assets, "/applications/vanilla/", "/plugins/vanilla/", "/discussion/", "/entry/signin") || reVanilla.MatchString(page.Body) {
		add("Vanilla Forums", mediumSignal, false)
	}

	if strings.Contains(generator, "nodebb") || strings.Contains(headersText, "nodebb") {
		add("NodeBB", strongSignal, true)
	}
	if hasAny(cookieNames, "express.sid", "io", "nodebb") {
		add("NodeBB", mediumSignal, false)
	}
	if assetsContain(assets, "/assets/nodebb", "/socket.io/", "/topic/") || reNodeBB.MatchString(page.Body) {
		add("NodeBB", mediumSignal, false)
	}

	if len(allEvidence) == 0 {
		return "undefined"
	}

	ranked := make([]*cmsEvidence, 0, len(allEvidence))
	for _, entry := range allEvidence {
		ranked = append(ranked, entry)
	}

	sort.Slice(ranked, func(i, j int) bool {
		left := rankWeight(ranked[i])
		right := rankWeight(ranked[j])
		if left == right {
			if ranked[i].Strong == ranked[j].Strong {
				return ranked[i].Medium > ranked[j].Medium
			}
			return ranked[i].Strong > ranked[j].Strong
		}
		return left > right
	})

	top := ranked[0]
	if top.HasDirect {
		return top.CMS
	}

	if top.Strong >= 2 {
		return top.CMS
	}

	if top.Strong >= 1 && top.Medium >= 1 {
		return top.CMS
	}

	if top.Strong == 1 || top.Medium >= 2 {
		if len(ranked) > 1 {
			second := ranked[1]
			if rankWeight(top)-rankWeight(second) <= 1 && second.Strong > 0 {
				return "undefined"
			}
		}

		return top.CMS
	}

	return "undefined"
}

func rankWeight(e *cmsEvidence) int {
	return e.Strong*6 + e.Medium*3 + e.Weak
}

func extractLang(html string) string {
	match := reLang.FindStringSubmatch(html)
	if len(match) < 2 {
		return ""
	}

	lang := strings.TrimSpace(strings.ToLower(match[1]))
	if lang == "" {
		return ""
	}

	return lang
}

func extractGenerator(html string) string {
	match := reGenerator.FindStringSubmatch(html)
	if len(match) < 2 {
		return ""
	}

	return strings.TrimSpace(match[1])
}

func headersToLowerText(headers http.Header) string {
	if len(headers) == 0 {
		return ""
	}

	b := strings.Builder{}
	for key, values := range headers {
		b.WriteString(strings.ToLower(key))
		b.WriteByte(':')
		b.WriteString(strings.ToLower(strings.Join(values, " ")))
		b.WriteByte('\n')
	}

	return b.String()
}

func cookiesToLowerNames(cookies []*http.Cookie) []string {
	if len(cookies) == 0 {
		return nil
	}

	result := make([]string, 0, len(cookies))
	for _, item := range cookies {
		if item == nil || item.Name == "" {
			continue
		}
		result = append(result, strings.ToLower(item.Name))
	}

	return result
}

func extractHTMLComments(html string) []string {
	matches := reComments.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		return nil
	}

	result := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		result = append(result, strings.TrimSpace(match[1]))
	}

	return result
}

func extractAssetURLs(html string) []string {
	matches := reAssetAttr.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		return nil
	}

	result := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		urlValue := strings.TrimSpace(strings.ToLower(match[1]))
		if urlValue == "" {
			continue
		}

		result = append(result, urlValue)
	}

	return result
}

func assetsContain(assets []string, markers ...string) bool {
	if len(assets) == 0 || len(markers) == 0 {
		return false
	}

	for _, asset := range assets {
		for _, marker := range markers {
			if strings.Contains(asset, strings.ToLower(marker)) {
				return true
			}
		}
	}

	return false
}

func hasAny(items []string, markers ...string) bool {
	if len(items) == 0 || len(markers) == 0 {
		return false
	}

	for _, item := range items {
		for _, marker := range markers {
			if strings.Contains(item, strings.ToLower(marker)) {
				return true
			}
		}
	}

	return false
}

func hasPrefixAny(items []string, prefixes ...string) bool {
	if len(items) == 0 || len(prefixes) == 0 {
		return false
	}

	for _, item := range items {
		for _, prefix := range prefixes {
			if strings.HasPrefix(item, strings.ToLower(prefix)) {
				return true
			}
		}
	}

	return false
}

func readProxyList(path string) []*url.URL {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024), 1024*1024)

	result := make([]*url.URL, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		proxyURL := parseProxyURL(line)
		if proxyURL == nil {
			continue
		}

		result = append(result, proxyURL)
	}

	return result
}

func resolveProxyPath() string {
	if _, err := os.Stat(proxyFilePath); err == nil {
		return proxyFilePath
	}

	if _, err := os.Stat("api/" + proxyFilePath); err == nil {
		return "api/" + proxyFilePath
	}

	if _, err := os.Stat("../api/" + proxyFilePath); err == nil {
		return "../api/" + proxyFilePath
	}

	return proxyFilePath
}

func parseProxyURL(raw string) *url.URL {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}

	if u.Host == "" {
		return nil
	}

	return u
}

func shuffledProxyPool(in []*url.URL) []*url.URL {
	if len(in) == 0 {
		return nil
	}

	out := make([]*url.URL, len(in))
	copy(out, in)

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	rng.Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})

	return out
}

func isProxyError(err error) bool {
	if err == nil {
		return false
	}

	text := strings.ToLower(err.Error())
	if strings.Contains(text, "proxy") || strings.Contains(text, "proxyconnect") {
		return true
	}

	var netErr *net.OpError
	if errors.As(err, &netErr) {
		op := strings.ToLower(netErr.Op)
		if strings.Contains(op, "proxy") {
			return true
		}
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		t := strings.ToLower(urlErr.Error())
		if strings.Contains(t, "proxy") || strings.Contains(t, "proxyconnect") {
			return true
		}
	}

	return false
}

func normalizeDomain(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	raw = strings.TrimPrefix(raw, "http://")
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimSpace(raw)

	if idx := strings.Index(raw, "/"); idx >= 0 {
		raw = raw[:idx]
	}

	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, ".")

	if raw == "" {
		return ""
	}

	if strings.ContainsAny(raw, " \t\n\r") {
		return ""
	}

	if strings.Contains(raw, "@") {
		return ""
	}

	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}

	if raw == "" {
		return ""
	}

	return strings.ToLower(raw)
}
