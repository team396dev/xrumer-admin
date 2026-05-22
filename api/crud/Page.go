package crud

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"api/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type pageFilters struct {
	CMS      []string `json:"cms"`
	Lang     []string `json:"lang"`
	Tag      string   `json:"tag"`
	Tags     []string `json:"tags"`
	IsForum  []bool   `json:"is_forum"`
	Accepted []bool   `json:"accepted"`
	Detected []bool   `json:"detected"`
}

type pageListMeta struct {
	CMS      []valueCountString `json:"cms"`
	Lang     []valueCountString `json:"lang"`
	Tags     []valueCountString `json:"tags"`
	IsForum  []valueCountBool   `json:"is_forum"`
	Detected []valueCountBool   `json:"detected"`
	Accepted int64              `json:"accepted"`
	ToReview int64              `json:"to_review"`
}

type pageListItem struct {
	ID        uint      `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	TargetUri string    `json:"target_uri"`
	Domain    string    `json:"domain"`
	CMS       string    `json:"cms"`
	IsForum   bool      `json:"is_forum"`
	Lang      string    `json:"lang"`
	Accepted  *bool     `json:"accepted"`
	Tags      []string  `json:"tags"`
}

type pageListResponse struct {
	Items      []pageListItem `json:"items"`
	Pagination paginationMeta `json:"pagination"`
	Meta       pageListMeta   `json:"meta"`
}

type pageListRow struct {
	ID        uint      `gorm:"column:id"`
	CreatedAt time.Time `gorm:"column:created_at"`
	TargetUri string    `gorm:"column:target_uri"`
	WebsiteID uint      `gorm:"column:website_id"`
	Domain    string    `gorm:"column:domain"`
	CMS       string    `gorm:"column:cms"`
	IsForum   bool      `gorm:"column:is_forum"`
	Lang      string    `gorm:"column:lang"`
	Accepted  *bool     `gorm:"column:accepted"`
}

type pageExportRow struct {
	TargetUri string `gorm:"column:target_uri"`
	Domain    string `gorm:"column:domain"`
	CMS       string `gorm:"column:cms"`
	Lang      string `gorm:"column:lang"`
	IsForum   bool   `gorm:"column:is_forum"`
	Accepted  *bool  `gorm:"column:accepted"`
}

type pageExportType string

const (
	pageExportTypeAll       pageExportType = "all"
	pageExportTypeToReview  pageExportType = "to_review"
	pageExportTypePlacement pageExportType = "placement"
)

func parsePageFilters(raw string) (pageFilters, error) {
	if raw == "" {
		return pageFilters{}, nil
	}
	var filters pageFilters
	if err := json.Unmarshal([]byte(raw), &filters); err != nil {
		return pageFilters{}, errors.New("invalid filters json")
	}
	return filters, nil
}

func applyPageFilters(query *gorm.DB, filters pageFilters) *gorm.DB {
	if len(filters.CMS) > 0 {
		query = query.Where("websites.cms IN ?", filters.CMS)
	}
	if len(filters.Lang) > 0 {
		query = query.Where("websites.lang IN ?", filters.Lang)
	}
	if len(filters.IsForum) == 1 {
		query = query.Where("websites.is_forum = ?", filters.IsForum[0])
	}
	if len(filters.Accepted) == 1 {
		query = query.Where("pages.accepted = ?", filters.Accepted[0])
	}

	tagSet := map[string]struct{}{}
	if tag := strings.TrimSpace(filters.Tag); tag != "" {
		tagSet[tag] = struct{}{}
	}
	for _, t := range filters.Tags {
		if t = strings.TrimSpace(t); t != "" {
			tagSet[t] = struct{}{}
		}
	}
	if len(tagSet) > 0 {
		tags := make([]string, 0, len(tagSet))
		for t := range tagSet {
			tags = append(tags, t)
		}
		query = query.Where(
			`(EXISTS (
				SELECT 1 FROM page_tag_pages ptp
				JOIN page_tags pt ON pt.id = ptp.page_tag_id
				WHERE ptp.page_id = pages.id AND pt.tag IN ?
			) OR EXISTS (
				SELECT 1 FROM website_tag_websites wtw
				JOIN website_tags wt ON wt.id = wtw.website_tag_id
				WHERE wtw.website_id = pages.website_id AND wt.tag IN ?
			))`,
			tags, tags,
		)
	}

	hasDetectedTrue := false
	hasDetectedFalse := false
	for _, v := range filters.Detected {
		if v {
			hasDetectedTrue = true
		} else {
			hasDetectedFalse = true
		}
	}
	if hasDetectedTrue != hasDetectedFalse {
		if hasDetectedTrue {
			query = query.Where("websites.cms IS NOT NULL AND websites.cms <> '' AND websites.cms <> 'undefined'")
		} else {
			query = query.Where("websites.cms IS NULL OR websites.cms = '' OR websites.cms = 'undefined'")
		}
	}

	return query
}

func basePageQuery(db *gorm.DB) *gorm.DB {
	return db.Table("pages").Joins("JOIN websites ON websites.id = pages.website_id")
}

func PageListHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, perPage := parsePagination(c)

		filters, err := parsePageFilters(c.Query("filters"))
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		query := applyPageFilters(basePageQuery(db), filters)

		var total int64
		if err := query.Count(&total).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to count pages"})
			return
		}

		offset := (page - 1) * perPage
		rows := make([]pageListRow, 0, perPage)
		if err := query.
			Select("pages.id, pages.created_at, pages.target_uri, pages.website_id, pages.accepted, websites.domain, websites.cms, websites.is_forum, websites.lang").
			Order("pages.id DESC").
			Offset(offset).
			Limit(perPage).
			Scan(&rows).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to fetch pages"})
			return
		}

		pageIDs := make([]uint, len(rows))
		for i, r := range rows {
			pageIDs[i] = r.ID
		}

		tagsByPageID := make(map[uint][]string, len(pageIDs))
		if len(pageIDs) > 0 {
			type tagRow struct {
				PageID uint   `gorm:"column:page_id"`
				Tag    string `gorm:"column:tag"`
			}

			var pageTags []tagRow
			if err := db.Raw(`
				SELECT ptp.page_id, pt.tag
				FROM page_tag_pages ptp
				JOIN page_tags pt ON pt.id = ptp.page_tag_id
				WHERE ptp.page_id IN ?`, pageIDs).Scan(&pageTags).Error; err == nil {
				for _, t := range pageTags {
					tagsByPageID[t.PageID] = append(tagsByPageID[t.PageID], t.Tag)
				}
			}

			websiteIDByPageID := make(map[uint]uint, len(rows))
			for _, r := range rows {
				websiteIDByPageID[r.ID] = r.WebsiteID
			}

			var websiteTags []tagRow
			if err := db.Raw(`
				SELECT p.id as page_id, wt.tag
				FROM pages p
				JOIN website_tag_websites wtw ON wtw.website_id = p.website_id
				JOIN website_tags wt ON wt.id = wtw.website_tag_id
				WHERE p.id IN ?`, pageIDs).Scan(&websiteTags).Error; err == nil {
				for _, t := range websiteTags {
					tagsByPageID[t.PageID] = append(tagsByPageID[t.PageID], t.Tag)
				}
			}

			for id, tags := range tagsByPageID {
				tagSet := make(map[string]struct{}, len(tags))
				for _, tag := range tags {
					tagSet[tag] = struct{}{}
				}
				deduped := make([]string, 0, len(tagSet))
				for tag := range tagSet {
					deduped = append(deduped, tag)
				}
				sort.Strings(deduped)
				tagsByPageID[id] = deduped
			}
		}

		items := make([]pageListItem, 0, len(rows))
		for _, r := range rows {
			tags := tagsByPageID[r.ID]
			if tags == nil {
				tags = []string{}
			}
			items = append(items, pageListItem{
				ID:        r.ID,
				CreatedAt: r.CreatedAt,
				TargetUri: r.TargetUri,
				Domain:    r.Domain,
				CMS:       r.CMS,
				IsForum:   r.IsForum,
				Lang:      r.Lang,
				Accepted:  r.Accepted,
				Tags:      tags,
			})
		}

		meta, err := buildPageMeta(db, filters)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to build pages meta"})
			return
		}

		c.JSON(200, pageListResponse{
			Items: items,
			Pagination: paginationMeta{
				Page:        page,
				PerPage:     perPage,
				Total:       total,
				HasNextPage: int64(offset+len(rows)) < total,
			},
			Meta: meta,
		})
	}
}

func PageExportTSVHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		exportType := c.Query("type")
		if exportType == "" {
			exportType = c.Query("export")
		}

		parsedExportType, err := parsePageExportType(exportType)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		filters, err := parsePageFilters(c.Query("filters"))
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		query := applyPageFilters(basePageQuery(db), filters).
			Select("pages.target_uri, pages.accepted, websites.domain, websites.cms, websites.lang, websites.is_forum").
			Order("pages.id DESC")

		switch parsedExportType {
		case pageExportTypeToReview:
			query = query.Where("websites.cms IS NOT NULL AND websites.cms <> '' AND LOWER(websites.cms) <> 'undefined' AND pages.accepted IS NULL")
		case pageExportTypePlacement:
			query = query.Where("pages.accepted = ?", true)
		}

		rows := make([]pageExportRow, 0)
		if err := query.Scan(&rows).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to export pages"})
			return
		}

		var buffer bytes.Buffer
		writer := csv.NewWriter(&buffer)
		writer.Comma = '\t'

		if err := writer.Write([]string{"url", "domain", "cms", "lang", "is_forum", "accepted"}); err != nil {
			c.JSON(500, gin.H{"error": "failed to prepare export file"})
			return
		}

		for _, row := range rows {
			acceptedValue := ""
			if row.Accepted != nil {
				acceptedValue = strconv.FormatBool(*row.Accepted)
			}
			if err := writer.Write([]string{
				row.TargetUri,
				row.Domain,
				row.CMS,
				row.Lang,
				strconv.FormatBool(row.IsForum),
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

		filename := fmt.Sprintf("pages_%s_%s.tsv", parsedExportType, time.Now().UTC().Format("20060102_150405"))
		c.Header("Content-Type", "text/tab-separated-values; charset=utf-8")
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		c.Data(200, "text/tab-separated-values; charset=utf-8", buffer.Bytes())
	}
}

func parsePageExportType(raw string) (pageExportType, error) {
	switch raw {
	case "", string(pageExportTypeAll):
		return pageExportTypeAll, nil
	case "review", string(pageExportTypeToReview):
		return pageExportTypeToReview, nil
	case "ready", "to_placement", string(pageExportTypePlacement):
		return pageExportTypePlacement, nil
	default:
		return "", errors.New("invalid export type")
	}
}

func buildPageMeta(db *gorm.DB, filters pageFilters) (pageListMeta, error) {
	meta := pageListMeta{
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

	var allCMSValues []stringValue
	if err := db.Model(&models.Website{}).
		Select("DISTINCT cms as value").
		Where("cms IS NOT NULL AND cms <> ''").
		Joins("JOIN pages ON pages.website_id = websites.id").
		Order("cms ASC").
		Scan(&allCMSValues).Error; err != nil {
		return meta, err
	}

	var allLangValues []stringValue
	if err := db.Model(&models.Website{}).
		Select("DISTINCT lang as value").
		Where("lang IS NOT NULL AND lang <> ''").
		Joins("JOIN pages ON pages.website_id = websites.id").
		Order("lang ASC").
		Scan(&allLangValues).Error; err != nil {
		return meta, err
	}

	var allTagValues []stringValue
	if err := db.Raw(`
		SELECT DISTINCT tag as value FROM (
			SELECT pt.tag
			FROM page_tag_pages ptp
			JOIN page_tags pt ON pt.id = ptp.page_tag_id
			UNION
			SELECT wt.tag
			FROM pages p
			JOIN website_tag_websites wtw ON wtw.website_id = p.website_id
			JOIN website_tags wt ON wt.id = wtw.website_tag_id
		) combined_tags
		WHERE tag IS NOT NULL AND tag <> ''
		ORDER BY value ASC
	`).Scan(&allTagValues).Error; err != nil {
		return meta, err
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
	if err := applyPageFilters(basePageQuery(db), filtersWithoutCMS).Session(&gorm.Session{}).
		Select("websites.cms as value, COUNT(DISTINCT pages.id) as count").
		Where("websites.cms IS NOT NULL AND websites.cms <> ''").
		Group("websites.cms").
		Scan(&cms).Error; err != nil {
		return meta, err
	}
	cmsCountByValue := map[string]int64{}
	for _, item := range cms {
		cmsCountByValue[item.Value] = item.Count
	}
	meta.CMS = make([]valueCountString, 0, len(allCMSValues))
	for _, item := range allCMSValues {
		meta.CMS = append(meta.CMS, valueCountString{Value: item.Value, Count: cmsCountByValue[item.Value]})
	}
	sort.Slice(meta.CMS, func(i, j int) bool {
		if meta.CMS[i].Count == meta.CMS[j].Count {
			return meta.CMS[i].Value < meta.CMS[j].Value
		}
		return meta.CMS[i].Count > meta.CMS[j].Count
	})

	var lang []valueCountString
	if err := applyPageFilters(basePageQuery(db), filtersWithoutLang).Session(&gorm.Session{}).
		Select("websites.lang as value, COUNT(DISTINCT pages.id) as count").
		Where("websites.lang IS NOT NULL AND websites.lang <> ''").
		Group("websites.lang").
		Scan(&lang).Error; err != nil {
		return meta, err
	}
	langCountByValue := map[string]int64{}
	for _, item := range lang {
		langCountByValue[item.Value] = item.Count
	}
	meta.Lang = make([]valueCountString, 0, len(allLangValues))
	for _, item := range allLangValues {
		meta.Lang = append(meta.Lang, valueCountString{Value: item.Value, Count: langCountByValue[item.Value]})
	}
	sort.Slice(meta.Lang, func(i, j int) bool {
		if meta.Lang[i].Count == meta.Lang[j].Count {
			return meta.Lang[i].Value < meta.Lang[j].Value
		}
		return meta.Lang[i].Count > meta.Lang[j].Count
	})

	var tags []valueCountString
	if err := applyPageFilters(basePageQuery(db), filters).Session(&gorm.Session{}).
		Select(`t.tag as value, COUNT(DISTINCT pages.id) as count`).
		Joins(`JOIN LATERAL (
			SELECT pt.tag FROM page_tag_pages ptp JOIN page_tags pt ON pt.id = ptp.page_tag_id WHERE ptp.page_id = pages.id
			UNION
			SELECT wt.tag FROM website_tag_websites wtw JOIN website_tags wt ON wt.id = wtw.website_tag_id WHERE wtw.website_id = pages.website_id
		) t ON TRUE`).
		Group("t.tag").
		Scan(&tags).Error; err != nil {
		return meta, err
	}
	tagCountByValue := map[string]int64{}
	for _, item := range tags {
		tagCountByValue[item.Value] = item.Count
	}
	meta.Tags = make([]valueCountString, 0, len(allTagValues))
	for _, item := range allTagValues {
		meta.Tags = append(meta.Tags, valueCountString{Value: item.Value, Count: tagCountByValue[item.Value]})
	}
	sort.Slice(meta.Tags, func(i, j int) bool {
		if meta.Tags[i].Count == meta.Tags[j].Count {
			return meta.Tags[i].Value < meta.Tags[j].Value
		}
		return meta.Tags[i].Count > meta.Tags[j].Count
	})

	var isForum []valueCountBool
	if err := applyPageFilters(basePageQuery(db), filtersWithoutIsForum).Session(&gorm.Session{}).
		Select("websites.is_forum as value, COUNT(DISTINCT pages.id) as count").
		Group("websites.is_forum").
		Scan(&isForum).Error; err != nil {
		return meta, err
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
	if err := applyPageFilters(basePageQuery(db), filtersWithoutDetected).Session(&gorm.Session{}).
		Select("(websites.cms IS NOT NULL AND websites.cms <> '' AND websites.cms <> 'undefined') as value, COUNT(DISTINCT pages.id) as count").
		Group("(websites.cms IS NOT NULL AND websites.cms <> '' AND websites.cms <> 'undefined')").
		Scan(&detected).Error; err != nil {
		return meta, err
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
	if err := applyPageFilters(basePageQuery(db), filters).Session(&gorm.Session{}).
		Select(`SUM(CASE WHEN websites.cms IS NOT NULL AND LOWER(websites.cms) <> 'undefined' AND pages.accepted IS NULL THEN 1 ELSE 0 END) as to_review,
		        SUM(CASE WHEN pages.accepted = TRUE THEN 1 ELSE 0 END) as accepted`).
		Scan(&placementMeta).Error; err != nil {
		return meta, err
	}
	meta.ToReview = placementMeta.ToReview
	meta.Accepted = placementMeta.Accepted

	return meta, nil
}
