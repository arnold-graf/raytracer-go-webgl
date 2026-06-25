// validate checks a TOML-derived JSON document against a JSON Schema (Draft 2020-12).
//
// Usage:
//
//	tomlv -json scene.toml | go run ./schemas/cmd/validate scene.schema.json
//	go run ./schemas/cmd/validate scene.schema.json document.json
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <schema.json> < document.json\n", os.Args[0])
		os.Exit(2)
	}

	schemaBytes, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "read schema: %v\n", err)
		os.Exit(1)
	}
	dataBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read document: %v\n", err)
		os.Exit(1)
	}

	var schemaDoc any
	if err := json.Unmarshal(schemaBytes, &schemaDoc); err != nil {
		fmt.Fprintf(os.Stderr, "parse schema: %v\n", err)
		os.Exit(1)
	}
	var data any
	if err := json.Unmarshal(dataBytes, &data); err != nil {
		fmt.Fprintf(os.Stderr, "parse document: %v\n", err)
		os.Exit(1)
	}

	c := jsonschema.NewCompiler()
	if err := c.AddResource(os.Args[1], schemaDoc); err != nil {
		fmt.Fprintf(os.Stderr, "compile schema: %v\n", err)
		os.Exit(1)
	}
	sch, err := c.Compile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "compile schema: %v\n", err)
		os.Exit(1)
	}
	if err := sch.Validate(data); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	fmt.Println("valid")
}
