package crud

import (
	"api/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type pageListResponse struct {
	Items      []models.Page  `json:"items"`
	Pagination paginationMeta `json:"pagination"`
}

func PageListHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, perPage := parsePagination(c)

		query := db.Model(&models.Page{})

		var total int64
		if err := query.Count(&total).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to count pages"})
			return
		}

		items := make([]models.Page, 0, perPage)
		offset := (page - 1) * perPage
		if err := query.Order("id DESC").Offset(offset).Limit(perPage).Find(&items).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to fetch pages"})
			return
		}

		resp := pageListResponse{
			Items: items,
			Pagination: paginationMeta{
				Page:        page,
				PerPage:     perPage,
				Total:       total,
				HasNextPage: int64(offset+len(items)) < total,
			},
		}

		c.JSON(200, resp)
	}
}
