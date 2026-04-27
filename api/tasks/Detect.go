package tasks

import (
	"api/detector"
	"api/models"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"github.com/alitto/pond/v2"
	"gorm.io/gorm"
)

const detectPollDelay = 2 * time.Second

func Detect(db *gorm.DB, threads int, logger *slog.Logger) {
	if threads < 1 {
		threads = 1
	}

	pool := pond.NewPool(threads)
	defer pool.StopAndWait()

	logger.Info("[Detect] service stated", "threads", threads)

	for {
		batchSize := threads * 2
		websites, err := fetchDetectBatch(db, batchSize)
		if err != nil {
			logger.Error("[Detect] failed to fetch websites", "error", err)
			time.Sleep(detectPollDelay)
			continue
		}

		if len(websites) == 0 {
			time.Sleep(detectPollDelay)
			continue
		}

		logger.Info("[Detect] founded new websites for detecting", "count", len(websites))

		futures := make([]pond.Task, 0, len(websites))
		updatesCh := make(chan websiteUpdate, len(websites))
		for _, website := range websites {
			w := website
			task := pool.Submit(func() {
				result := detector.Detect(w.Domain)
				updatesCh <- websiteUpdate{
					ID:      w.ID,
					CMS:     normalizeCMS(result.CMS),
					Lang:    normalizeLang(result.Lang),
					IsForum: false,
					Status:  result.Status,
				}
			})

			futures = append(futures, task)
		}

		for _, future := range futures {
			_ = future.Wait()
		}

		close(updatesCh)
		updates := make([]websiteUpdate, 0, len(websites))
		for update := range updatesCh {
			updates = append(updates, update)
		}

		if err := applyWebsiteUpdatesBatch(db, updates); err != nil {
			logger.Error("[Detect] failed to update websites in batch", "error", err, "count", len(updates))
			continue
		}

		logger.Info("[Detect] batch processed", "count", len(updates))
	}
}

type websiteUpdate struct {
	ID      uint
	CMS     string
	Lang    any
	IsForum bool
	Status  int
}

func fetchDetectBatch(db *gorm.DB, limit int) ([]models.Website, error) {
	if limit <= 0 {
		return nil, nil
	}

	type idBounds struct {
		MinID uint
		MaxID uint
	}

	var bounds idBounds
	if err := db.Model(&models.Website{}).
		Select("COALESCE(MIN(id), 0) AS min_id, COALESCE(MAX(id), 0) AS max_id").
		Where("cms IS NULL").
		Scan(&bounds).Error; err != nil {
		return nil, err
	}

	if bounds.MaxID == 0 {
		return nil, nil
	}

	randomStart := bounds.MinID
	if bounds.MaxID > bounds.MinID {
		randomStart = bounds.MinID + uint(rand.Int63n(int64(bounds.MaxID-bounds.MinID+1)))
	}

	batch := make([]models.Website, 0, limit)
	if err := db.Where("cms IS NULL AND id >= ?", randomStart).
		Order("id ASC").
		Limit(limit).
		Find(&batch).Error; err != nil {
		return nil, err
	}

	if len(batch) < limit {
		remaining := limit - len(batch)
		var tail []models.Website
		if err := db.Where("cms IS NULL AND id < ?", randomStart).
			Order("id ASC").
			Limit(remaining).
			Find(&tail).Error; err != nil {
			return nil, err
		}

		batch = append(batch, tail...)
	}

	return batch, nil
}

func applyWebsiteUpdatesBatch(db *gorm.DB, updates []websiteUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	args := make([]any, 0, len(updates)*5)
	valueRows := make([]string, 0, len(updates))

	for i, update := range updates {
		base := i*5 + 1
		valueRows = append(valueRows, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d)", base, base+1, base+2, base+3, base+4))
		args = append(args, update.ID, update.CMS, update.Lang, update.IsForum, update.Status)
	}

	query := `
		UPDATE websites AS w
		SET cms = v.cms,
			lang = v.lang,
			is_forum = v.is_forum,
			status = v.status
		FROM (VALUES ` + strings.Join(valueRows, ",") + `) AS v(id, cms, lang, is_forum, status)
		WHERE w.id = v.id
	`

	return db.Exec(query, args...).Error
}

func normalizeCMS(cms string) string {
	value := strings.TrimSpace(cms)
	if value == "" || strings.EqualFold(value, "undefined") {
		return "undefined"
	}

	return value
}

func normalizeLang(lang string) any {
	value := strings.TrimSpace(strings.ToLower(lang))
	if value == "" {
		return nil
	}

	return value
}
