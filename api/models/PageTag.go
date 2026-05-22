package models

import "gorm.io/gorm"

type PageTag struct {
	gorm.Model
	Tag   string `gorm:"not null;uniqueIndex"`
	Pages []Page `gorm:"many2many:page_tag_pages;" json:"-"`
}

type PageTagPage struct {
	PageID    uint `gorm:"not null;uniqueIndex:idx_page_tag_pages_unique"`
	PageTagID uint `gorm:"not null;uniqueIndex:idx_page_tag_pages_unique"`
}

func (PageTagPage) TableName() string {
	return "page_tag_pages"
}
