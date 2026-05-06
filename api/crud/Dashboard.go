package crud

import (
	"bufio"
	"os"
	"strings"

	"api/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type cmsTotalRow struct {
	Name  string `gorm:"column:name"`
	Total int64  `gorm:"column:total"`
}

type websiteTagTotalRow struct {
	Name  string `gorm:"column:name"`
	Total int64  `gorm:"column:total"`
}

func DashboardGetHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		dashboard, err := buildDashboard(db)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, dashboard)
	}
}

func buildDashboard(db *gorm.DB) (models.Dashboard, error) {
	var total int64
	if err := db.Model(&models.Website{}).Count(&total).Error; err != nil {
		return models.Dashboard{}, err
	}

	var checked int64
	if err := db.Model(&models.Website{}).
		Where("cms IS NOT NULL").
		Count(&checked).Error; err != nil {
		return models.Dashboard{}, err
	}

	var detected int64
	if err := db.Model(&models.Website{}).
		Where("cms IS NOT NULL AND cms <> '' AND LOWER(cms) <> ?", "undefined").
		Count(&detected).Error; err != nil {
		return models.Dashboard{}, err
	}

	var toPlacement int64
	if err := db.Model(&models.Website{}).
		Where("accepted = ?", true).
		Count(&toPlacement).Error; err != nil {
		return models.Dashboard{}, err
	}

	proxyTotal, err := countProxyTotal(resolveProxyFilePath())
	if err != nil {
		return models.Dashboard{}, err
	}

	rows := make([]cmsTotalRow, 0)
	if err := db.Model(&models.Website{}).
		Select("cms as name, COUNT(*) as total").
		Where("cms IS NOT NULL AND cms <> '' AND LOWER(cms) <> ?", "undefined").
		Group("cms").
		Order("total DESC, cms ASC").
		Scan(&rows).Error; err != nil {
		return models.Dashboard{}, err
	}

	tagRows := make([]websiteTagTotalRow, 0)
	if err := db.Model(&models.WebsiteTag{}).
		Select("website_tags.tag as name, COUNT(website_tag_websites.website_id) as total").
		Joins("LEFT JOIN website_tag_websites ON website_tag_websites.website_tag_id = website_tags.id").
		Group("website_tags.id, website_tags.tag").
		Order("total DESC, website_tags.tag ASC").
		Scan(&tagRows).Error; err != nil {
		return models.Dashboard{}, err
	}

	cmsTable := make([]models.CmsTotal, 0, len(rows))
	for _, row := range rows {
		cmsTable = append(cmsTable, models.CmsTotal{
			Name:  row.Name,
			Total: uint(row.Total),
		})
	}

	tagTable := make([]models.WebsiteTagTotal, 0, len(tagRows))
	for _, row := range tagRows {
		tagTable = append(tagTable, models.WebsiteTagTotal{
			Name:  row.Name,
			Total: uint(row.Total),
		})
	}

	return models.Dashboard{
		Total:       uint(total),
		Checked:     uint(checked),
		Detected:    uint(detected),
		ToPlacement: uint(toPlacement),
		ProxyTotal:  proxyTotal,
		ProxyValid:  0,
		CmsTable:    cmsTable,
		TagTable:    tagTable,
	}, nil
}

func countProxyTotal(filePath string) (uint, error) {
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}

		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var total uint

	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}

		total++
	}

	if err := scanner.Err(); err != nil {
		return 0, err
	}

	return total, nil
}

func resolveProxyFilePath() string {
	candidates := []string{"proxy.txt", "api/proxy.txt", "../api/proxy.txt"}

	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}

		if info.IsDir() {
			continue
		}

		return candidate
	}

	return "proxy.txt"
}
