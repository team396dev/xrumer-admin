package crud

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"api/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultPage    = 1
	defaultPerPage = 20
	maxPerPage     = 200
	domainBatchSize = 500
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

type websiteDomainRow struct {
	ID     uint   `gorm:"column:id"`
	Domain string `gorm:"column:domain"`
}

type websiteTagWebsiteLink struct {
	WebsiteID    uint `gorm:"column:website_id"`
	WebsiteTagID uint `gorm:"column:website_tag_id"`
}

type websiteImportJob struct {
	mu sync.RWMutex

	ID             string             `json:"job_id"`
	Status         string             `json:"status"`
	Phase          string             `json:"phase"`
	Type           string             `json:"type"`
	Accepted       bool               `json:"accepted"`
	Message        string             `json:"message"`
	TotalLines     int                `json:"total_lines"`
	ProcessedLines int                `json:"processed_lines"`
	TotalDomains   int                `json:"total_domains"`
	ProcessedDomains int              `json:"processed_domains"`
	UniqueDomains  int                `json:"unique_domains"`
	Progress       float64            `json:"progress_percent"`
	Error          string             `json:"error,omitempty"`
	Result         map[string]any     `json:"result,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
	StartedAt      *time.Time         `json:"started_at,omitempty"`
	FinishedAt     *time.Time         `json:"finished_at,omitempty"`
}

var websiteImportJobs = struct {
	mu   sync.RWMutex
	jobs map[string]*websiteImportJob
}{
	jobs: map[string]*websiteImportJob{},
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
			tagSet := make(map[string]struct{}, len(website.WebsiteTags))
			for _, websiteTag := range website.WebsiteTags {
				tagSet[websiteTag.Tag] = struct{}{}
			}

			tags := make([]string, 0, len(tagSet))
			for tag := range tagSet {
				tags = append(tags, tag)
			}
			sort.Strings(tags)

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

		fileHeader, err := c.FormFile("file")
		if err != nil {
			c.JSON(400, gin.H{"error": "file is required"})
			return
		}

		sourceFile, err := fileHeader.Open()
		if err != nil {
			c.JSON(400, gin.H{"error": "failed to open uploaded file"})
			return
		}
		defer sourceFile.Close()

		tmpFile, err := os.CreateTemp("", "website-import-*.tsv")
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to prepare import job"})
			return
		}

		if _, err := io.Copy(tmpFile, sourceFile); err != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			c.JSON(500, gin.H{"error": "failed to stage uploaded file"})
			return
		}

		if err := tmpFile.Close(); err != nil {
			os.Remove(tmpFile.Name())
			c.JSON(500, gin.H{"error": "failed to stage uploaded file"})
			return
		}

		acceptedValue := rawType == "good"
		job := newWebsiteImportJob(rawType, acceptedValue)
		storeWebsiteImportJob(job)

		go runWebsiteImportJob(db, job, tmpFile.Name())

		c.JSON(202, gin.H{
			"job_id":     job.ID,
			"status":     job.Status,
			"phase":      job.Phase,
			"message":    job.Message,
			"type":       job.Type,
			"accepted":   job.Accepted,
			"created_at": job.CreatedAt,
		})
	}
}

func WebsiteBulkAcceptedImportStatusHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		jobID := strings.TrimSpace(c.Param("jobID"))
		if jobID == "" {
			c.JSON(400, gin.H{"error": "job id is required"})
			return
		}

		job, ok := getWebsiteImportJob(jobID)
		if !ok {
			c.JSON(404, gin.H{"error": "import job not found"})
			return
		}

		c.JSON(200, snapshotWebsiteImportJob(job))
	}
}

func newWebsiteImportJob(rawType string, accepted bool) *websiteImportJob {
	return &websiteImportJob{
		ID:        newWebsiteImportJobID(),
		Status:    "queued",
		Phase:     "queued",
		Type:      rawType,
		Accepted:  accepted,
		Message:   "Задача создана",
		Progress:  0,
		CreatedAt: time.Now().UTC(),
	}
}

func newWebsiteImportJobID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("import_%d", time.Now().UTC().UnixNano())
	}

	return hex.EncodeToString(b)
}

func storeWebsiteImportJob(job *websiteImportJob) {
	websiteImportJobs.mu.Lock()
	defer websiteImportJobs.mu.Unlock()
	websiteImportJobs.jobs[job.ID] = job
}

func getWebsiteImportJob(jobID string) (*websiteImportJob, bool) {
	websiteImportJobs.mu.RLock()
	defer websiteImportJobs.mu.RUnlock()
	job, ok := websiteImportJobs.jobs[jobID]
	return job, ok
}

func snapshotWebsiteImportJob(job *websiteImportJob) gin.H {
	job.mu.RLock()
	defer job.mu.RUnlock()

	snapshot := gin.H{
		"job_id":          job.ID,
		"status":          job.Status,
		"phase":           job.Phase,
		"type":            job.Type,
		"accepted":        job.Accepted,
		"message":         job.Message,
		"total_lines":     job.TotalLines,
		"processed_lines": job.ProcessedLines,
		"total_domains":   job.TotalDomains,
		"processed_domains": job.ProcessedDomains,
		"unique_domains":  job.UniqueDomains,
		"progress_percent": job.Progress,
		"created_at":      job.CreatedAt,
	}

	if job.StartedAt != nil {
		snapshot["started_at"] = *job.StartedAt
	}

	if job.FinishedAt != nil {
		snapshot["finished_at"] = *job.FinishedAt
	}

	if job.Error != "" {
		snapshot["error"] = job.Error
	}

	if job.Result != nil {
		snapshot["result"] = job.Result
	}

	return snapshot
}

func updateWebsiteImportJob(job *websiteImportJob, mutate func(*websiteImportJob)) {
	job.mu.Lock()
	defer job.mu.Unlock()
	mutate(job)
}

func runWebsiteImportJob(db *gorm.DB, job *websiteImportJob, stagedFilePath string) {
	defer os.Remove(stagedFilePath)

	started := time.Now().UTC()
	updateWebsiteImportJob(job, func(j *websiteImportJob) {
		j.Status = "running"
		j.Phase = "parsing"
		j.Message = "Чтение файла"
		j.StartedAt = &started
	})

	result, err := processWebsiteImport(db, job, stagedFilePath)
	if err != nil {
		finished := time.Now().UTC()
		updateWebsiteImportJob(job, func(j *websiteImportJob) {
			j.Status = "failed"
			j.Phase = "failed"
			j.Message = "Импорт завершился с ошибкой"
			j.Error = err.Error()
			j.FinishedAt = &finished
		})
		return
	}

	finished := time.Now().UTC()
	updateWebsiteImportJob(job, func(j *websiteImportJob) {
		j.Status = "completed"
		j.Phase = "completed"
		j.Message = "Импорт завершен"
		j.Progress = 100
		j.ProcessedDomains = j.TotalDomains
		j.FinishedAt = &finished
		j.Result = result
	})
}

func processWebsiteImport(db *gorm.DB, job *websiteImportJob, filePath string) (map[string]any, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, errors.New("failed to open staged import file")
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
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, errors.New("failed to parse uploaded tsv")
		}

		totalLines++
		if len(record) == 0 {
		if totalLines%1000 == 0 {
			updateWebsiteImportJob(job, func(j *websiteImportJob) {
				j.ProcessedLines = totalLines
			})
		}
			continue
		}

		domain := normalizeWebsiteDomain(record[0])
		if domain == "" {
		if totalLines%1000 == 0 {
			updateWebsiteImportJob(job, func(j *websiteImportJob) {
				j.ProcessedLines = totalLines
			})
		}
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

		if totalLines%1000 == 0 {
			updateWebsiteImportJob(job, func(j *websiteImportJob) {
				j.ProcessedLines = totalLines
				j.UniqueDomains = len(domainTagsMap)
			})
		}
	}

	if len(domainTagsMap) == 0 {
		return nil, errors.New("no valid domains found in file")
	}

	domains := make([]string, 0, len(domainTagsMap))
	for domain := range domainTagsMap {
		domains = append(domains, domain)
	}

	updateWebsiteImportJob(job, func(j *websiteImportJob) {
		j.TotalLines = totalLines
		j.ProcessedLines = totalLines
		j.TotalDomains = len(domains)
		j.ProcessedDomains = 0
		j.UniqueDomains = len(domains)
		j.Phase = "db"
		j.Message = "Обновление базы данных"
		j.Progress = 35
	})

	var matchedDomains int64
	domainChunks := splitStringSliceIntoChunks(domains, domainBatchSize)
	for index, domainChunk := range domainChunks {
		var chunkCount int64
		if err := db.Model(&models.Website{}).
			Where("domain IN ?", domainChunk).
			Count(&chunkCount).Error; err != nil {
			return nil, errors.New("failed to match domains")
		}
		matchedDomains += chunkCount

		if (index+1)%25 == 0 || index+1 == len(domainChunks) {
			progress := 35 + (15*float64(index+1))/float64(max(len(domainChunks), 1))
			processedDomains := domainsFromChunkIndex(domainChunks, index)
			updateWebsiteImportJob(job, func(j *websiteImportJob) {
				j.Progress = progress
				j.ProcessedDomains = processedDomains
			})
		}
	}

	var updatedRows int64
	var createdDomains int
	var tagsCreated int
	var websitesTagged int
	var tagLinksCreated int

		err = db.Transaction(func(tx *gorm.DB) error {
		for index, domainChunk := range domainChunks {
			chunkWebsites := make([]models.Website, 0, len(domainChunk))
			for _, domain := range domainChunk {
				chunkWebsites = append(chunkWebsites, models.Website{
					Domain:   domain,
					Accepted: job.Accepted,
				})
			}

			createResult := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunkWebsites)
			if createResult.Error != nil {
				return createResult.Error
			}

			createdDomains += int(createResult.RowsAffected)

			if (index+1)%25 == 0 || index+1 == len(domainChunks) {
				progress := 50 + (10*float64(index+1))/float64(max(len(domainChunks), 1))
				processedDomains := domainsFromChunkIndex(domainChunks, index)
				updateWebsiteImportJob(job, func(j *websiteImportJob) {
					j.Progress = progress
					j.ProcessedDomains = processedDomains
				})
			}
		}

		for index, domainChunk := range domainChunks {
			updateResult := tx.Model(&models.Website{}).
				Where("domain IN ?", domainChunk).
				Update("accepted", job.Accepted)
			if updateResult.Error != nil {
				return updateResult.Error
			}
			updatedRows += updateResult.RowsAffected

			if (index+1)%25 == 0 || index+1 == len(domainChunks) {
				progress := 60 + (10*float64(index+1))/float64(max(len(domainChunks), 1))
				processedDomains := domainsFromChunkIndex(domainChunks, index)
				updateWebsiteImportJob(job, func(j *websiteImportJob) {
					j.Progress = progress
					j.ProcessedDomains = processedDomains
				})
			}
		}

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
			tagChunks := splitStringSliceIntoChunks(allTags, domainBatchSize)
			for _, tagChunk := range tagChunks {
				newTags := make([]models.WebsiteTag, 0, len(tagChunk))
				for _, tag := range tagChunk {
					newTags = append(newTags, models.WebsiteTag{Tag: tag})
				}

				createTagsResult := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&newTags)
				if createTagsResult.Error != nil {
					return createTagsResult.Error
				}
				tagsCreated += int(createTagsResult.RowsAffected)
			}

			for _, tagChunk := range tagChunks {
				chunkTags := []models.WebsiteTag{}
				if err := tx.Where("tag IN ?", tagChunk).Find(&chunkTags).Error; err != nil {
					return err
				}
				for _, tag := range chunkTags {
					tagsByValue[tag.Tag] = tag
				}
			}
		}

		if len(tagsByValue) == 0 {
			return nil
		}

		websiteRows := []websiteDomainRow{}
		for _, domainChunk := range domainChunks {
			chunkRows := []websiteDomainRow{}
			if err := tx.Model(&models.Website{}).
				Select("id, domain").
				Where("domain IN ?", domainChunk).
				Find(&chunkRows).Error; err != nil {
				return err
			}
			websiteRows = append(websiteRows, chunkRows...)
		}

		websiteIDByDomain := make(map[string]uint, len(websiteRows))
		for _, row := range websiteRows {
			websiteIDByDomain[row.Domain] = row.ID
		}

		links := make([]websiteTagWebsiteLink, 0)
		for domain, tagNames := range domainTagsMap {
			websiteID, exists := websiteIDByDomain[domain]
			if !exists || len(tagNames) == 0 {
				continue
			}

			linksForWebsite := 0
			for _, tagName := range tagNames {
				tag, tagExists := tagsByValue[tagName]
				if !tagExists {
					continue
				}

				links = append(links, websiteTagWebsiteLink{
					WebsiteID:    websiteID,
					WebsiteTagID: tag.ID,
				})
				linksForWebsite++
			}

			if linksForWebsite > 0 {
				websitesTagged++
				tagLinksCreated += linksForWebsite
			}
		}

		if len(links) > 0 {
			for _, linksChunk := range splitWebsiteTagLinksIntoChunks(links, domainBatchSize*4) {
				if err := tx.Table("website_tag_websites").
					Clauses(clause.OnConflict{DoNothing: true}).
					Create(&linksChunk).Error; err != nil {
					return err
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, errors.New("failed to update websites")
	}

	return map[string]any{
		"type":            job.Type,
		"accepted":        job.Accepted,
		"total_lines":     totalLines,
		"valid_domains":   validDomains,
		"unique_domains":  len(domains),
		"matched_domains": matchedDomains,
		"created_domains": createdDomains,
		"updated_rows":    updatedRows,
		"tags_created":    tagsCreated,
		"websites_tagged": websitesTagged,
		"tag_links":       tagLinksCreated,
	}, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}

	return b
}

func domainsFromChunkIndex(chunks [][]string, index int) int {
	if index < 0 {
		return 0
	}

	if index >= len(chunks) {
		index = len(chunks) - 1
	}

	total := 0
	for i := 0; i <= index; i++ {
		total += len(chunks[i])
	}

	return total
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

func splitStringSliceIntoChunks(items []string, chunkSize int) [][]string {
	if len(items) == 0 {
		return nil
	}

	if chunkSize <= 0 {
		chunkSize = len(items)
	}

	chunks := make([][]string, 0, (len(items)+chunkSize-1)/chunkSize)
	for start := 0; start < len(items); start += chunkSize {
		end := start + chunkSize
		if end > len(items) {
			end = len(items)
		}
		chunks = append(chunks, items[start:end])
	}

	return chunks
}

func splitWebsiteTagLinksIntoChunks(items []websiteTagWebsiteLink, chunkSize int) [][]websiteTagWebsiteLink {
	if len(items) == 0 {
		return nil
	}

	if chunkSize <= 0 {
		chunkSize = len(items)
	}

	chunks := make([][]websiteTagWebsiteLink, 0, (len(items)+chunkSize-1)/chunkSize)
	for start := 0; start < len(items); start += chunkSize {
		end := start + chunkSize
		if end > len(items) {
			end = len(items)
		}
		chunks = append(chunks, items[start:end])
	}

	return chunks
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
