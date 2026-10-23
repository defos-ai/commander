package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/defos-ai/commander/apps/admin/client"
	"github.com/defos-ai/commander/apps/admin/missions"
)

func runMissions(ctx context.Context, configuration config, stdin io.Reader, stdout io.Writer) error {
	args := configuration.arguments[1:]
	if len(args) == 0 || len(args) == 1 && args[0] == "help" {
		return missionHelp(stdout)
	}
	command := args[0]
	wantArguments := 1
	if command == "export" || command == "validate" || command == "import" {
		wantArguments = 2
	} else if command != "list" && command != "seed" {
		return missionUsage()
	}
	if len(args) != wantArguments || configuration.dryRun && command != "import" && command != "seed" {
		return missionUsage()
	}
	var definitions []missions.Definition
	if command == "validate" || command == "import" {
		var err error
		definitions, err = readMissions(args[1], stdin)
		if err != nil {
			return &UsageError{cause: err}
		}
		if command == "validate" {
			return showValidation(stdout, definitions, configuration.json)
		}
	}
	api, err := client.New(configuration.connection)
	if err != nil {
		return &UsageError{cause: err}
	}
	defer api.Close()
	switch command {
	case "list":
		definitions, err := missions.List(ctx, api)
		if err != nil {
			return err
		}
		return showMissions(stdout, definitions, configuration.json)
	case "export":
		definition, err := missions.Export(ctx, api, args[1])
		if err != nil {
			return err
		}
		return writeJSON(stdout, definition.JSON)
	case "import":
		result, importError := missions.Import(ctx, api, definitions, configuration.dryRun)
		if err := showImport(stdout, result, configuration.json); err != nil {
			return errors.Join(importError, err)
		}
		return importError
	case "seed":
		result, seedError := missions.Seed(ctx, api, configuration.dryRun)
		if err := showImport(stdout, result, configuration.json); err != nil {
			return errors.Join(seedError, err)
		}
		return seedError
	}
	return nil
}

func missionUsage() error {
	return &UsageError{cause: errors.New("invalid mission command or arguments; run commander-admin missions --help")}
}

func readMissions(path string, stdin io.Reader) ([]missions.Definition, error) {
	input := stdin
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open mission file: %w", err)
		}
		defer file.Close()
		input = file
	}
	if input == nil {
		return nil, errors.New("provide mission JSON on stdin when using -")
	}
	data, err := io.ReadAll(io.LimitReader(input, missions.MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read mission definitions: %w", err)
	}
	return missions.Parse(data)
}

func showMissions(output io.Writer, definitions []missions.Definition, asJSON bool) error {
	if asJSON {
		items := make([]json.RawMessage, len(definitions))
		for i, definition := range definitions {
			items[i] = definition.JSON
		}
		return writeJSON(output, struct {
			Items []json.RawMessage `json:"items"`
		}{items})
	}
	if len(definitions) == 0 {
		_, err := fmt.Fprintln(output, "No missions.")
		return err
	}
	for _, definition := range definitions {
		if _, err := fmt.Fprintf(output, "%q\n", definition.Name); err != nil {
			return err
		}
	}
	return nil
}

func showValidation(output io.Writer, definitions []missions.Definition, asJSON bool) error {
	names := make([]string, len(definitions))
	for i, definition := range definitions {
		names[i] = definition.Name
	}
	if asJSON {
		return writeJSON(output, struct {
			Valid    bool     `json:"valid"`
			Missions []string `json:"missions"`
		}{true, names})
	}
	_, err := fmt.Fprintf(output, "%d mission definitions valid.\n", len(definitions))
	return err
}

func showImport(output io.Writer, result missions.ImportResult, asJSON bool) error {
	if asJSON {
		return writeJSON(output, result)
	}
	for _, item := range result.Items {
		if _, err := fmt.Fprintf(output, "%s %q\n", strings.ReplaceAll(item.Status, "_", " "), item.Name); err != nil {
			return err
		}
	}
	var summary []string
	for _, count := range []struct {
		name  string
		value int
	}{
		{"created", result.Counts.Created}, {"skipped", result.Counts.Skipped},
		{"would create", result.Counts.WouldCreate}, {"failed", result.Counts.Failed},
		{"uncertain", result.Counts.Uncertain}, {"not attempted", result.Counts.NotAttempted},
	} {
		if count.value != 0 {
			summary = append(summary, fmt.Sprintf("%d %s", count.value, count.name))
		}
	}
	_, err := fmt.Fprintln(output, strings.Join(summary, " · "))
	return err
}

func writeJSON(output io.Writer, value any) error {
	// RawMessage keeps schema numbers and escapes intact when formatting exports.
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return err
	}
	_, err := output.Write(buffer.Bytes())
	return err
}

func missionHelp(output io.Writer) error {
	_, err := io.WriteString(output, `Usage: commander-admin missions <command> [options]

Commands:
  list                    List mission names; --json returns full definitions
  export NAME             Write one mission definition as JSON
  validate FILE           Validate every definition locally, without API access
  import FILE [--dry-run]  Create missing missions and preserve existing names
  seed [--dry-run]         Add bundled missions, preserving existing names

FILE accepts one mission, an array, or a list result with items. Use - for stdin.
Imports stop on the first failure and report completed and unattempted items.
After an uncertain write, inspect the saved mission before submitting again.
Options: --json, --api-url URL, --api-username NAME, -h, --help.
`)
	return err
}
