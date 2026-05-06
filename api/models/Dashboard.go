package models

type CmsTotal struct {
	Name  string
	Total uint
}

type WebsiteTagTotal struct {
	Name  string
	Total uint
}

type Dashboard struct {
	Total       uint
	Checked     uint
	Detected    uint
	ToPlacement uint
	ProxyTotal  uint
	ProxyValid  uint
	CmsTable    []CmsTotal
	TagTable    []WebsiteTagTotal
}
