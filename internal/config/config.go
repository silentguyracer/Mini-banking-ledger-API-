package config

import (
	"os"
)

type Config struct {
	Port  string
	DBURL string
}

func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		dbURL = "postgres://ledger:ledger@localhost:5432/ledger?sslmode=disable"
	}

	return &Config{
		Port:  port,
		DBURL: dbURL,
	}
}
