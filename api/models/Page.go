package models

import "gorm.io/gorm"

type Page struct {
	gorm.Model
	TargetUri string    `gorm:"not null;uniqueIndex"`
	WebsiteID uint      `gorm:"not null;index"`
	Accepted  *bool     `gorm:"default:null;index"`
	Website   Website
	PageTags  []PageTag `gorm:"many2many:page_tag_pages;" json:"-"`
}
