package main

import (
	"fmt"
	"log"
	"log/slog"
	"os"

	"api/crud"
	"api/models"
	"api/tasks"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"gopkg.in/natefinch/lumberjack.v2"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}

	return fallback
}

func main() {
	rotator := &lumberjack.Logger{
		Filename:   ".logs/xrumer-admin.log",
		MaxSize:    100,
		MaxBackups: 7,
		MaxAge:     30,
		Compress:   true,
	}

	logger := slog.New(slog.NewJSONHandler(rotator, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	if err := godotenv.Load(".env"); err != nil {
		log.Printf("warning: .env file not loaded: %v", err)
	}

	dsn := fmt.Sprintf(
		"host=%s user=%s password=%s dbname=%s port=%s sslmode=%s TimeZone=%s",
		getEnv("DB_HOST", "localhost"),
		getEnv("DB_USER", "uxrumer"),
		getEnv("DB_PASSWORD", "pwdxrumer"),
		getEnv("DB_NAME", "dbxrumer"),
		getEnv("DB_PORT", "5434"),
		getEnv("DB_SSLMODE", "disable"),
		getEnv("DB_TIMEZONE", "UTC"),
	)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to postgres: %v", err)
	}

	if err := db.AutoMigrate(&models.Website{}, &models.Page{}); err != nil {
		log.Fatalf("failed to run migrations: %v", err)
	}

	router := gin.Default()
	if err := router.SetTrustedProxies(nil); err != nil {
		log.Printf("warning: failed to disable trusted proxies: %v", err)
	}
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})
	router.GET("/websites", crud.WebsiteListHandler(db))
	router.GET("/websites/export", crud.WebsiteExportTSVHandler(db))
	router.POST("/websites/accepted/import", crud.WebsiteBulkAcceptedImportHandler(db))
	router.GET("/pages", crud.PageListHandler(db))
	router.GET("/dashboard", crud.DashboardGetHandler(db))

	// threads, _ := strconv.Atoi(getEnv("THREADS", "10"))
	go tasks.Index(db, logger)
	// go tasks.Detect(db, threads, logger)

	addr := "0.0.0.0:" + getEnv("API_PORT", "8080")
	log.Printf("api server is running on %s", addr)
	if err := router.Run(addr); err != nil {
		log.Fatalf("failed to start api server: %v", err)
	}

}
