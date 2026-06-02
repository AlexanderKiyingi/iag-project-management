package config

import (
	"os"

	"github.com/joho/godotenv"
)

func loadDotEnv() {
	if _, err := os.Stat(".env"); err == nil {
		_ = godotenv.Load(".env")
	}
}
