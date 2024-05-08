package main

import (
	"context"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

var (
	databaseURL  string = os.Getenv("DB_URL")
	databaseName string = os.Getenv("DB_NAME")
)

// DBConnectionTest checks the async database is accessible on startup
func DBConnectionTest() (string, error) {
	if databaseName == "none" || databaseName == "" {
		return "No database configured, skipping", nil
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return "", err
	}
	client, err := azcosmos.NewClient(databaseURL, cred, nil)
	if err != nil {
		return "", err
	}
	database, err := client.NewDatabase(databaseName)
	if err != nil {
		return "", err
	}
	result, err := database.Read(context.TODO(), nil)
	if err != nil {
		return "", err
	}
	return result.DatabaseProperties.ID, nil
}
