package models

import "gorm.io/gorm"

type Page struct {
	gorm.Model
	TargetUri string `gorm:"not null, unique"`
	WebsiteID uint   `gorm:"not null;index"`
	Website   Website
}
