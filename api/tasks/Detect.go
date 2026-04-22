package tasks

import (
	"api/detector"
	"api/models"
	"log/slog"
	"strings"
	"time"

	"github.com/alitto/pond/v2"
	"gorm.io/gorm"
)

const detectPollDelay = 2 * time.Second

func Detect(db *gorm.DB, threads int, logger *slog.Logger) {
	pool := pond.NewPool(threads)
	defer pool.StopAndWait()

	logger.Info("[Detect] service stated", "threads", threads)

	for {
		var websites []models.Website
		db.Where("cms is null").Limit(threads * 2).Find(&websites)

		if len(websites) == 0 {
			time.Sleep(detectPollDelay)
			continue
		}

		logger.Info("[Detect] founded new websites for detecting", "count", len(websites))

		futures := make([]pond.Task, 0, len(websites))
		for _, website := range websites {
			w := website
			task := pool.Submit(func() {
				result := detector.Detect(w.Domain)

				updates := map[string]any{
					"cms":      normalizeCMS(result.CMS),
					"lang":     normalizeLang(result.Lang),
					"is_forum": false,
					"status":   result.Status,
				}

				db.Model(&models.Website{}).Where("id = ?", w.ID).Updates(updates)

				logger.Info("[Detect] website processed",
					"domain", w.Domain,
					"status", result.Status,
					"cms", result.CMS,
					"lang", result.Lang,
				)
			})

			futures = append(futures, task)
		}

		for _, future := range futures {
			_ = future.Wait()
		}
	}
}

func normalizeCMS(cms string) any {
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
