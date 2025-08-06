package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/invopop/jsonschema"

	"github.com/umputun/cronn/app/crontab"
)

func main() {
	// generate schema for yamlConfig
	schema := jsonschema.Reflect(&crontab.YamlConfig{})

	// set schema metadata
	schema.Title = "Cronn YAML Configuration Schema"
	schema.Description = "Schema for cronn YAML configuration file"
	schema.Version = "1.0.0"

	// marshal to JSON with indentation
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		log.Fatalf("failed to marshal schema: %v", err)
	}

	// write to file
	outputPath := "schema.json"
	if len(os.Args) > 1 {
		outputPath = os.Args[1]
	}

	if err := os.WriteFile(outputPath, data, 0o600); err != nil { //nolint:gosec // schema file is not sensitive
		log.Fatalf("failed to write schema file: %v", err)
	}

	fmt.Printf("Schema generated successfully at %s\n", outputPath)
}
