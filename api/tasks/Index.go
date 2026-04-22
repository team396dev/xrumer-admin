package tasks

import (
	"bufio"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	indexDomainsDir = "domains"
	indexBatchSize  = 20000
	indexPollDelay  = 1 * time.Second
)

func Index(db *gorm.DB, logger *slog.Logger) {
	logger.Info("[Index] service stated")

	for {
		entries, err := os.ReadDir(indexDomainsDir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				time.Sleep(indexPollDelay)
				continue
			}

			logger.Error("[Index] failed to read domains directory", "path", indexDomainsDir, "error", err)
			time.Sleep(indexPollDelay)
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".txt" {
				continue
			}

			filePath := filepath.Join(indexDomainsDir, entry.Name())
			logger.Info("[Index] opened file", "file", filePath)

			linesRead, insertedCount, processErr := processDomainFile(db, filePath)
			if processErr != nil {
				logger.Error("[Index] failed to process file", "file", filePath, "error", processErr)
				continue
			}

			if insertedCount == 0 {
				logger.Info("[Index] no new rows", "file", filePath, "lines_read", linesRead)
			} else {
				logger.Info("[Index] file processed", "file", filePath, "lines_read", linesRead, "inserted_unique", insertedCount)
			}

			if err := os.Remove(filePath); err != nil {
				logger.Error("[Index] failed to remove processed file", "file", filePath, "error", err)
				continue
			}

			logger.Info("[Index] file removed", "file", filePath)
		}

		time.Sleep(indexPollDelay)
	}
}

func processDomainFile(db *gorm.DB, filePath string) (int64, int64, error) {
	type domainRow struct {
		Domain string `gorm:"column:domain"`
	}

	file, err := os.Open(filePath)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024), 1024*1024)

	batch := make([]domainRow, 0, indexBatchSize)
	var linesRead int64
	var insertedCount int64

	insertBatch := func(rows []domainRow) error {
		if len(rows) == 0 {
			return nil
		}

		result := db.
			Table("websites").
			Clauses(clause.OnConflict{
				DoNothing: true,
			}).
			CreateInBatches(rows, indexBatchSize)

		if result.Error != nil {
			return result.Error
		}

		insertedCount += result.RowsAffected
		return nil
	}

	for scanner.Scan() {
		linesRead++

		domain := strings.TrimSpace(scanner.Text())
		if domain == "" {
			continue
		}

		batch = append(batch, domainRow{Domain: domain})
		if len(batch) < indexBatchSize {
			continue
		}

		if err := insertBatch(batch); err != nil {
			return linesRead, insertedCount, err
		}

		batch = batch[:0]
	}

	if err := scanner.Err(); err != nil {
		return linesRead, insertedCount, err
	}

	if err := insertBatch(batch); err != nil {
		return linesRead, insertedCount, err
	}

	return linesRead, insertedCount, nil
}
