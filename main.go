package main

import (
	"encoding/json"
	"log"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
)

var validTokens map[string]string

func main() {
	// Load tokens from environment variable once
	tokensJSON := os.Getenv("VALID_TOKENS_JSON")
	if tokensJSON == "" {
		log.Fatal("FATAL: VALID_TOKENS_JSON is not set or empty")
	}

	if err := json.Unmarshal([]byte(tokensJSON), &validTokens); err != nil {
		log.Fatalf("FATAL: Failed to parse VALID_TOKENS_JSON: %v", err)
	}

	app := fiber.New()

	app.Get("/validate-token", validateTokenHandler)

	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8080"
	}

	addr := ":" + port
	log.Printf("Starting authentication service on %s...", addr)

	if err := app.Listen(addr); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func validateTokenHandler(c *fiber.Ctx) error {
	authHeader := c.Get("Authorization")
	if authHeader == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing Authorization header"})
	}

	// Go 1.20+: CutPrefix is faster & cleaner
	token, ok := strings.CutPrefix(authHeader, "Bearer ")
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid Authorization header"})
	}

	tenantID, found := validTokens[token]
	if !found {
		log.Printf("Invalid token attempt: %s", token)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid token"})
	}

	c.Set("X-Scope-OrgID", tenantID)
	log.Printf("Authenticated tenant: %s", tenantID)

	return c.SendStatus(fiber.StatusOK)
}
