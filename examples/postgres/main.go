package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	// Pontus default PostgreSQL proxy address
	connStr := "postgres://postgres:password@localhost:5432/postgres?sslmode=disable"

	// Use database/sql to open a connection
	db, err := sql.Open("pgx", connStr)
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
	fmt.Println("Successfully connected to Pontus (PostgreSQL proxy)!")

	// Execute a simple query
	var version string
	err = db.QueryRowContext(ctx, "SELECT version()").Scan(&version)
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
