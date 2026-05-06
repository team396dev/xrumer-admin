package models

import "gorm.io/gorm"

type Website struct {
	gorm.Model
	Domain      string `gorm:"not null, unique"`
	CMS         string `gorm:"default null;index:idx_websites_cms"`
	IsForum     bool   `gorm:"default false;index:idx_websites_is_forum"`
	Lang        string `gorm:"default null;index:idx_websites_lang"`
	Status      int    `gorm:"default 0;index:idx_websites_status"`
	Accepted    bool   `gorm:"default null;index:idx_websites_accepted"`
	Pages       []Page
	WebsiteTags []WebsiteTag `gorm:"many2many:website_tag_websites;" json:"-"`
}

type WebsiteTag struct {
	gorm.Model
	Tag      string    `gorm:"not null, unique"`
	Websites []Website `gorm:"many2many:website_tag_websites;" json:"-"`
}

type WebsiteTagWebsite struct {
	WebsiteID    uint `gorm:"not null;uniqueIndex:idx_website_tag_websites_unique"`
	WebsiteTagID uint `gorm:"not null;uniqueIndex:idx_website_tag_websites_unique"`
}

func (WebsiteTagWebsite) TableName() string {
	return "website_tag_websites"
}
