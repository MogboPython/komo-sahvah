package config

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	MongoClient *mongo.Client
	MongoDB     *mongo.Database
)

func GetEnvOrDefault(key, fallback string) string {
	err := godotenv.Load(".env")
	if err != nil {
		log.Printf("error loading .env file: %v", err)
	}

	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func ConnectMongoDB() error {
	dbName := GetEnvOrDefault("MONGODB_DB_NAME", "komo-sahvah-db")
	mongoURI := fmt.Sprintf("%s/%s", GetEnvOrDefault("MONGODB_URI", "mongodb://localhost:27017"), dbName)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		return err
	}

	MongoClient = client
	MongoDB = client.Database(dbName)

	return nil
}
