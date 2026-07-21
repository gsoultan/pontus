package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	// Pontus default MySQL proxy address (assumes Pontus is configured for MySQL)
	// Example DSN: user:password@tcp(localhost:3306)/dbname
	dsn := "root:password@tcp(localhost:3306)/mysql"

	// Use database/sql to open a connection
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Create a context with timeout for the ping
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Ping the database through Pontus
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("failed to ping Pontus: %v", err)
	}
	fmt.Println("Successfully connected to Pontus (MySQL proxy)!")

	// Execute a simple query
	var version string
	err = db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version)
	if err != nil {
		log.Fatalf("failed to query version: %v", err)
	}
	fmt.Printf("Backend Database Version: %s\n", version)

	// Example of transaction mode pooling in Pontus
	for i := range 5 {
		runQuery(db, i)
	}
}

func runQuery(db *sql.DB, id int) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var one int
	err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
	if err != nil {
		log.Printf("Query %d failed: %v", id, err)
		return
	}
	fmt.Printf("Query %d: Success\n", id)
}
