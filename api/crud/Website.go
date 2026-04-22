package crud

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"api/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	defaultPage    = 1
	defaultPerPage = 20
	maxPerPage     = 200
)

type websiteFilters struct {
	CMS      []string `json:"cms"`
	Lang     []string `json:"lang"`
	Tag      string   `json:"tag"`
	Tags     []string `json:"tags"`
	IsForum  []bool   `json:"is_forum"`
	Accepted []bool   `json:"accepted"`
	Detected []bool   `json:"detected"`
}

type paginationMeta struct {
	Page        int   `json:"page"`
	PerPage     int   `json:"per_page"`
	Total       int64 `json:"total"`
	HasNextPage bool  `json:"has_next_page"`
}

type websiteListResponse struct {
	Items      []websiteListItem `json:"items"`
	Pagination paginationMeta    `json:"pagination"`
	Meta       websiteListMeta   `json:"meta"`
}

type websiteListItem struct {
	ID        uint       `json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	Domain    string     `json:"domain"`
	CMS       string     `json:"cms"`
	IsForum   bool       `json:"is_forum"`
	Lang      string     `json:"lang"`
	Status    int        `json:"status"`
	Accepted  bool       `json:"accepted"`
	Tags      []string   `json:"tags"`
}

type valueCountString struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

type valueCountBool struct {
	Value bool  `json:"value"`
	Count int64 `json:"count"`
}

type websiteListMeta struct {
	CMS      []valueCountString `json:"cms"`
	Lang     []valueCountString `json:"lang"`
	Tags     []valueCountString `json:"tags"`
	IsForum  []valueCountBool   `json:"is_forum"`
	Detected []valueCountBool   `json:"detected"`
	ToReview int64              `json:"to_review"`
	Accepted int64              `json:"accepted"`
}

type websiteExportType string

const (
	websiteExportTypeAll       websiteExportType = "all"
	websiteExportTypeToReview  websiteExportType = "to_review"
	websiteExportTypePlacement websiteExportType = "placement"
)

type websiteExportRow struct {
	Domain   string `gorm:"column:domain"`
	CMS      string `gorm:"column:cms"`
	Lang     string `gorm:"column:lang"`
	IsForum  bool   `gorm:"column:is_forum"`
	Status   int    `gorm:"column:status"`
	Accepted *bool  `gorm:"column:accepted"`
}

func WebsiteListHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, perPage := parsePagination(c)

		filters, err := parseWebsiteFilters(c.Query("filters"))
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		query := db.Model(&models.Website{})
		query = applyWebsiteFilters(query, filters)

		var total int64
		if err := query.Count(&total).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to count websites"})
			return
		}

		websites := make([]models.Website, 0, perPage)
		offset := (page - 1) * perPage
		if err := query.Preload("WebsiteTags").Order("id DESC").Offset(offset).Limit(perPage).Find(&websites).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to fetch websites"})
			return
		}

		items := make([]websiteListItem, 0, len(websites))
		for _, website := range websites {
			tags := make([]string, 0, len(website.WebsiteTags))
			for _, websiteTag := range website.WebsiteTags {
				tags = append(tags, websiteTag.Tag)
			}

			items = append(items, websiteListItem{
				ID:        website.ID,
				CreatedAt: website.CreatedAt,
				UpdatedAt: website.UpdatedAt,
				Domain:    website.Domain,
				CMS:       website.CMS,
				IsForum:   website.IsForum,
				Lang:      website.Lang,
				Status:    website.Status,
				Accepted:  website.Accepted,
				Tags:      tags,
			})

			if website.DeletedAt.Valid {
				deletedAt := website.DeletedAt.Time
				items[len(items)-1].DeletedAt = &deletedAt
			}
		}

		meta, err := buildWebsiteMeta(db, filters)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to build websites meta"})
			return
		}

		resp := websiteListResponse{
			Items: items,
			Pagination: paginationMeta{
				Page:        page,
				PerPage:     perPage,
				Total:       total,
				HasNextPage: int64(offset+len(websites)) < total,
			},
			Meta: meta,
		}

		c.JSON(200, resp)
	}
}

func WebsiteExportTSVHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		exportType := c.Query("type")
		if exportType == "" {
			exportType = c.Query("export")
		}

		parsedExportType, err := parseWebsiteExportType(exportType)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		filters, err := parseWebsiteFilters(c.Query("filters"))
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		query := db.Model(&models.Website{}).
			Select("domain, cms, lang, is_forum, status, accepted").
			Order("id DESC")

		query = applyWebsiteFilters(query, filters)

		switch parsedExportType {
		case websiteExportTypeToReview:
			query = query.Where("cms IS NOT NULL AND cms <> '' AND LOWER(cms) <> ? AND accepted IS NULL", "undefined")
		case websiteExportTypePlacement:
			query = query.Where("accepted = ?", true)
		}

		rows := make([]websiteExportRow, 0)
		if err := query.Scan(&rows).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to export websites"})
			return
		}

		var buffer bytes.Buffer
		writer := csv.NewWriter(&buffer)
		writer.Comma = '\t'

		if err := writer.Write([]string{"domain", "cms", "lang", "is_forum", "status", "accepted"}); err != nil {
			c.JSON(500, gin.H{"error": "failed to prepare export file"})
			return
		}

		for _, row := range rows {
			acceptedValue := ""
			if row.Accepted != nil {
				acceptedValue = strconv.FormatBool(*row.Accepted)
			}

			if err := writer.Write([]string{
				row.Domain,
				row.CMS,
				row.Lang,
				strconv.FormatBool(row.IsForum),
				strconv.Itoa(row.Status),
				acceptedValue,
			}); err != nil {
				c.JSON(500, gin.H{"error": "failed to prepare export file"})
				return
			}
		}

		writer.Flush()
		if err := writer.Error(); err != nil {
			c.JSON(500, gin.H{"error": "failed to prepare export file"})
			return
		}

		filename := fmt.Sprintf(
			"websites_%s_%s.tsv",
			parsedExportType,
			time.Now().UTC().Format("20060102_150405"),
		)

		c.Header("Content-Type", "text/tab-separated-values; charset=utf-8")
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		c.Data(200, "text/tab-separated-values; charset=utf-8", buffer.Bytes())
	}
}

func WebsiteBulkAcceptedImportHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawType := strings.ToLower(strings.TrimSpace(c.PostForm("type")))
		if rawType != "good" && rawType != "bad" {
			c.JSON(400, gin.H{"error": "type must be either good or bad"})
			return
		}

		acceptedValue := rawType == "good"

		fileHeader, err := c.FormFile("file")
		if err != nil {
			c.JSON(400, gin.H{"error": "file is required"})
			return
		}

		file, err := fileHeader.Open()
		if err != nil {
			c.JSON(400, gin.H{"error": "failed to open uploaded file"})
			return
		}
		defer file.Close()

		reader := csv.NewReader(bufio.NewReader(file))
		reader.Comma = '\t'
		reader.FieldsPerRecord = -1
		reader.LazyQuotes = true

		totalLines := 0
		validDomains := 0
		domainTagsMap := map[string][]string{}

		for {
			record, err := reader.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				c.JSON(400, gin.H{"error": "failed to parse uploaded tsv"})
				return
			}

			totalLines++
			if len(record) == 0 {
				continue
			}

			domain := normalizeWebsiteDomain(record[0])
			if domain == "" {
				continue
			}

			if totalLines == 1 {
				if domain == "domain" || domain == "домен" {
					continue
				}
			}

			validDomains++

			tags := []string{}
			if len(record) > 1 {
				tags = parseWebsiteTags(record[1])
			}

			existingTags := domainTagsMap[domain]
			tagSet := map[string]struct{}{}
			for _, tag := range existingTags {
				tagSet[tag] = struct{}{}
			}
			for _, tag := range tags {
				tagSet[tag] = struct{}{}
			}

			merged := make([]string, 0, len(tagSet))
			for tag := range tagSet {
				merged = append(merged, tag)
			}

			sort.Strings(merged)
			domainTagsMap[domain] = merged
		}

		if len(domainTagsMap) == 0 {
			c.JSON(400, gin.H{"error": "no valid domains found in file"})
			return
		}

		domains := make([]string, 0, len(domainTagsMap))
		for domain := range domainTagsMap {
			domains = append(domains, domain)
		}

		var matchedDomains int64
		if err := db.Model(&models.Website{}).
			Where("domain IN ?", domains).
			Count(&matchedDomains).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to match domains"})
			return
		}

		var updatedRows int64
		var tagsCreated int
		var websitesTagged int
		var tagLinksCreated int

		err = db.Transaction(func(tx *gorm.DB) error {
			updateResult := tx.Model(&models.Website{}).
				Where("domain IN ?", domains).
				Update("accepted", acceptedValue)
			if updateResult.Error != nil {
				return updateResult.Error
			}
			updatedRows = updateResult.RowsAffected

			allTagsSet := map[string]struct{}{}
			for _, tags := range domainTagsMap {
				for _, tag := range tags {
					allTagsSet[tag] = struct{}{}
				}
			}

			allTags := make([]string, 0, len(allTagsSet))
			for tag := range allTagsSet {
				allTags = append(allTags, tag)
			}

			tagsByValue := map[string]models.WebsiteTag{}
			if len(allTags) > 0 {
				existingTags := []models.WebsiteTag{}
				if err := tx.Where("tag IN ?", allTags).Find(&existingTags).Error; err != nil {
					return err
				}

				existingTagSet := map[string]struct{}{}
				for _, tag := range existingTags {
					existingTagSet[tag.Tag] = struct{}{}
					tagsByValue[tag.Tag] = tag
				}

				newTags := make([]models.WebsiteTag, 0)
				for _, tag := range allTags {
					if _, exists := existingTagSet[tag]; exists {
						continue
					}
					newTags = append(newTags, models.WebsiteTag{Tag: tag})
				}

				if len(newTags) > 0 {
					if err := tx.Create(&newTags).Error; err != nil {
						return err
					}
					tagsCreated = len(newTags)
					for _, tag := range newTags {
						tagsByValue[tag.Tag] = tag
					}
				}
			}

			if len(tagsByValue) == 0 {
				return nil
			}

			websites := []models.Website{}
			if err := tx.Where("domain IN ?", domains).Find(&websites).Error; err != nil {
				return err
			}

			for _, website := range websites {
				tagNames := domainTagsMap[website.Domain]
				if len(tagNames) == 0 {
					continue
				}

				websiteTags := make([]models.WebsiteTag, 0, len(tagNames))
				for _, tagName := range tagNames {
					tag, exists := tagsByValue[tagName]
					if !exists {
						continue
					}
					websiteTags = append(websiteTags, tag)
				}

				if len(websiteTags) == 0 {
					continue
				}

				if err := tx.Model(&website).Association("WebsiteTags").Append(websiteTags); err != nil {
					return err
				}

				websitesTagged++
				tagLinksCreated += len(websiteTags)
			}

			return nil
		})

		if err != nil {
			c.JSON(500, gin.H{"error": "failed to update websites"})
			return
		}

		c.JSON(200, gin.H{
			"type":            rawType,
			"accepted":        acceptedValue,
			"total_lines":     totalLines,
			"valid_domains":   validDomains,
			"unique_domains":  len(domains),
			"matched_domains": matchedDomains,
			"updated_rows":    updatedRows,
			"tags_created":    tagsCreated,
			"websites_tagged": websitesTagged,
			"tag_links":       tagLinksCreated,
		})
	}
}

func parseWebsiteTags(raw string) []string {
	line := strings.TrimSpace(raw)
	if line == "" {
		return nil
	}

	line = strings.Trim(line, "\"'")
	if line == "" {
		return nil
	}

	parts := strings.Split(line, ",")
	if len(parts) == 0 {
		return nil
	}

	tagSet := map[string]struct{}{}
	for _, part := range parts {
		tag := strings.TrimSpace(strings.Trim(part, "\"'"))
		if tag == "" {
			continue
		}
		tagSet[tag] = struct{}{}
	}

	tags := make([]string, 0, len(tagSet))
	for tag := range tagSet {
		tags = append(tags, tag)
	}

	sort.Strings(tags)
	return tags
}

func normalizeWebsiteDomain(raw string) string {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}

	line = strings.Trim(line, "\"'")
	if line == "" {
		return ""
	}

	parts := strings.Fields(line)
	if len(parts) == 0 {
		return ""
	}

	candidate := parts[0]
	if !strings.Contains(candidate, "://") {
		candidate = "http://" + candidate
	}

	parsedURL, err := url.Parse(candidate)
	if err != nil {
		return ""
	}

	host := strings.TrimSuffix(strings.ToLower(parsedURL.Hostname()), ".")
	if host == "" {
		return ""
	}

	return host
}

func parseWebsiteExportType(raw string) (websiteExportType, error) {
	switch raw {
	case "", string(websiteExportTypeAll):
		return websiteExportTypeAll, nil
	case "review", string(websiteExportTypeToReview):
		return websiteExportTypeToReview, nil
	case "ready", "to_placement", string(websiteExportTypePlacement):
		return websiteExportTypePlacement, nil
	default:
		return "", errors.New("invalid export type")
	}
}

func parsePagination(c *gin.Context) (int, int) {
	page := defaultPage
	perPage := defaultPerPage

	if rawPage := c.Query("page"); rawPage != "" {
		if parsed, err := strconv.Atoi(rawPage); err == nil && parsed > 0 {
			page = parsed
		}
	}

	if rawPerPage := c.Query("per_page"); rawPerPage != "" {
		if parsed, err := strconv.Atoi(rawPerPage); err == nil && parsed > 0 {
			if parsed > maxPerPage {
				perPage = maxPerPage
			} else {
				perPage = parsed
			}
		}
	}

	return page, perPage
}

func parseWebsiteFilters(raw string) (websiteFilters, error) {
	if raw == "" {
		return websiteFilters{}, nil
	}

	var filters websiteFilters
	if err := json.Unmarshal([]byte(raw), &filters); err != nil {
		return websiteFilters{}, errors.New("invalid filters json")
	}

	return filters, nil
}

func applyWebsiteFilters(query *gorm.DB, filters websiteFilters) *gorm.DB {
	if len(filters.CMS) > 0 {
		query = query.Where("cms IN ?", filters.CMS)
	}

	if len(filters.Lang) > 0 {
		query = query.Where("lang IN ?", filters.Lang)
	}

	if len(filters.IsForum) == 1 {
		query = query.Where("is_forum = ?", filters.IsForum[0])
	}

	if len(filters.Accepted) == 1 {
		query = query.Where("accepted = ?", filters.Accepted[0])
	}

	tagSet := map[string]struct{}{}
	if tag := strings.TrimSpace(filters.Tag); tag != "" {
		tagSet[tag] = struct{}{}
	}
	for _, rawTag := range filters.Tags {
		tag := strings.TrimSpace(rawTag)
		if tag == "" {
			continue
		}
		tagSet[tag] = struct{}{}
	}

	if len(tagSet) > 0 {
		tags := make([]string, 0, len(tagSet))
		for tag := range tagSet {
			tags = append(tags, tag)
		}

		query = query.Where(
			"EXISTS (SELECT 1 FROM website_tag_websites wtw JOIN website_tags wt ON wt.id = wtw.website_tag_id WHERE wtw.website_id = websites.id AND wt.tag IN ?)",
			tags,
		)
	}

	hasDetectedTrue := false
	hasDetectedFalse := false
	for _, value := range filters.Detected {
		if value {
			hasDetectedTrue = true
		} else {
			hasDetectedFalse = true
		}
	}

	if hasDetectedTrue != hasDetectedFalse {
		if hasDetectedTrue {
			query = query.Where("cms IS NOT NULL AND cms <> '' AND cms <> 'undefined'")
		} else {
			query = query.Where("cms IS NULL OR cms = '' OR cms = 'undefined'")
		}
	}

	return query
}

func buildWebsiteMeta(db *gorm.DB, filters websiteFilters) (websiteListMeta, error) {
	meta := websiteListMeta{
		CMS:  []valueCountString{},
		Lang: []valueCountString{},
		Tags: []valueCountString{},
		IsForum: []valueCountBool{
			{Value: true, Count: 0},
			{Value: false, Count: 0},
		},
		Detected: []valueCountBool{
			{Value: true, Count: 0},
			{Value: false, Count: 0},
		},
	}

	type stringValue struct {
		Value string `gorm:"column:value"`
	}

	allCMSValues := []stringValue{}
	if err := db.Model(&models.Website{}).
		Select("DISTINCT cms as value").
		Where("cms IS NOT NULL AND cms <> ''").
		Order("cms ASC").
		Scan(&allCMSValues).Error; err != nil {
		return websiteListMeta{}, err
	}

	allLangValues := []stringValue{}
	if err := db.Model(&models.Website{}).
		Select("DISTINCT lang as value").
		Where("lang IS NOT NULL AND lang <> ''").
		Order("lang ASC").
		Scan(&allLangValues).Error; err != nil {
		return websiteListMeta{}, err
	}

	allTagValues := []stringValue{}
	if err := db.Model(&models.WebsiteTag{}).
		Select("DISTINCT tag as value").
		Where("tag IS NOT NULL AND tag <> ''").
		Order("tag ASC").
		Scan(&allTagValues).Error; err != nil {
		return websiteListMeta{}, err
	}

	filtersWithoutCMS := filters
	filtersWithoutCMS.CMS = nil

	filtersWithoutLang := filters
	filtersWithoutLang.Lang = nil

	filtersWithoutIsForum := filters
	filtersWithoutIsForum.IsForum = nil

	filtersWithoutDetected := filters
	filtersWithoutDetected.Detected = nil

	var cms []valueCountString
	if err := applyWebsiteFilters(db.Model(&models.Website{}), filtersWithoutCMS).Session(&gorm.Session{}).
		Select("cms as value, COUNT(*) as count").
		Where("cms IS NOT NULL AND cms <> ''").
		Group("cms").
		Scan(&cms).Error; err != nil {
		return websiteListMeta{}, err
	}

	cmsCountByValue := map[string]int64{}
	for _, item := range cms {
		cmsCountByValue[item.Value] = item.Count
	}

	meta.CMS = make([]valueCountString, 0, len(allCMSValues))
	for _, item := range allCMSValues {
		meta.CMS = append(meta.CMS, valueCountString{
			Value: item.Value,
			Count: cmsCountByValue[item.Value],
		})
	}

	sort.Slice(meta.CMS, func(i, j int) bool {
		if meta.CMS[i].Count == meta.CMS[j].Count {
			return meta.CMS[i].Value < meta.CMS[j].Value
		}
		return meta.CMS[i].Count > meta.CMS[j].Count
	})

	var lang []valueCountString
	if err := applyWebsiteFilters(db.Model(&models.Website{}), filtersWithoutLang).Session(&gorm.Session{}).
		Select("lang as value, COUNT(*) as count").
		Where("lang IS NOT NULL AND lang <> ''").
		Group("lang").
		Scan(&lang).Error; err != nil {
		return websiteListMeta{}, err
	}

	langCountByValue := map[string]int64{}
	for _, item := range lang {
		langCountByValue[item.Value] = item.Count
	}

	meta.Lang = make([]valueCountString, 0, len(allLangValues))
	for _, item := range allLangValues {
		meta.Lang = append(meta.Lang, valueCountString{
			Value: item.Value,
			Count: langCountByValue[item.Value],
		})
	}

	sort.Slice(meta.Lang, func(i, j int) bool {
		if meta.Lang[i].Count == meta.Lang[j].Count {
			return meta.Lang[i].Value < meta.Lang[j].Value
		}
		return meta.Lang[i].Count > meta.Lang[j].Count
	})

	var tags []valueCountString
	if err := applyWebsiteFilters(db.Model(&models.Website{}), filters).Session(&gorm.Session{}).
		Joins("JOIN website_tag_websites ON website_tag_websites.website_id = websites.id").
		Joins("JOIN website_tags ON website_tags.id = website_tag_websites.website_tag_id").
		Select("website_tags.tag as value, COUNT(DISTINCT websites.id) as count").
		Group("website_tags.tag").
		Scan(&tags).Error; err != nil {
		return websiteListMeta{}, err
	}

	tagCountByValue := map[string]int64{}
	for _, item := range tags {
		tagCountByValue[item.Value] = item.Count
	}

	meta.Tags = make([]valueCountString, 0, len(allTagValues))
	for _, item := range allTagValues {
		meta.Tags = append(meta.Tags, valueCountString{
			Value: item.Value,
			Count: tagCountByValue[item.Value],
		})
	}

	sort.Slice(meta.Tags, func(i, j int) bool {
		if meta.Tags[i].Count == meta.Tags[j].Count {
			return meta.Tags[i].Value < meta.Tags[j].Value
		}
		return meta.Tags[i].Count > meta.Tags[j].Count
	})

	var isForum []valueCountBool
	if err := applyWebsiteFilters(db.Model(&models.Website{}), filtersWithoutIsForum).Session(&gorm.Session{}).
		Select("is_forum as value, COUNT(*) as count").
		Group("is_forum").
		Order("is_forum DESC").
		Scan(&isForum).Error; err != nil {
		return websiteListMeta{}, err
	}

	isForumCountByValue := map[bool]int64{}
	for _, item := range isForum {
		isForumCountByValue[item.Value] = item.Count
	}

	meta.IsForum = []valueCountBool{
		{Value: true, Count: isForumCountByValue[true]},
		{Value: false, Count: isForumCountByValue[false]},
	}

	type detectedCountRow struct {
		Value bool  `gorm:"column:value"`
		Count int64 `gorm:"column:count"`
	}

	var detected []detectedCountRow
	if err := applyWebsiteFilters(db.Model(&models.Website{}), filtersWithoutDetected).Session(&gorm.Session{}).
		Select("(cms IS NOT NULL AND cms <> '' AND cms <> 'undefined') as value, COUNT(*) as count").
		Group("(cms IS NOT NULL AND cms <> '' AND cms <> 'undefined')").
		Order("value DESC").
		Scan(&detected).Error; err != nil {
		return websiteListMeta{}, err
	}

	detectedCountByValue := map[bool]int64{}
	for _, item := range detected {
		detectedCountByValue[item.Value] = item.Count
	}

	meta.Detected = []valueCountBool{
		{Value: true, Count: detectedCountByValue[true]},
		{Value: false, Count: detectedCountByValue[false]},
	}

	type placementMetaRow struct {
		ToReview int64 `gorm:"column:to_review"`
		Accepted int64 `gorm:"column:accepted"`
	}

	var placementMeta placementMetaRow
	if err := applyWebsiteFilters(db.Model(&models.Website{}), filters).Session(&gorm.Session{}).
		Select("SUM(CASE WHEN cms IS NOT NULL AND LOWER(cms) <> 'undefined' and accepted is null THEN 1 ELSE 0 END) as to_review, SUM(CASE WHEN accepted = TRUE THEN 1 ELSE 0 END) as accepted").
		Scan(&placementMeta).Error; err != nil {
		return websiteListMeta{}, err
	}

	meta.ToReview = placementMeta.ToReview
	meta.Accepted = placementMeta.Accepted

	return meta, nil
}
