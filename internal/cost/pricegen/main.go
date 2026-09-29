// Command pricegen refreshes internal/cost/prices.json from LiteLLM's price list. The
// weekly prices workflow runs it and opens a pull request with what changed:
//
//	go run ./internal/cost/pricegen            # download and update
//	go run ./internal/cost/pricegen -from f.json
//
// The changes are printed one per line on stdout; nothing is printed when there are none.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/LucasAntunesdeAlmeida/sill/internal/atomicfile"
	"github.com/LucasAntunesdeAlmeida/sill/internal/cost"
)

func main() {
	table := flag.String("table", "internal/cost/prices.json", "price table to update")
	from := flag.String("from", "", "read LiteLLM's list from this file instead of downloading it")
	flag.Parse()
	if err := run(*table, *from); err != nil {
		fmt.Fprintln(os.Stderr, "pricegen:", err)
		os.Exit(1)
	}
}

func run(tablePath, from string) error {
	current, err := os.ReadFile(tablePath)
	if err != nil {
		return err
	}
	rows, err := cost.ParseTable(current)
	if err != nil {
		return fmt.Errorf("%s: %w", tablePath, err)
	}
	list, err := source(from)
	if err != nil {
		return err
	}
	updated, changes, err := cost.Update(rows, list, time.Now().UTC().Format(time.DateOnly))
	if err != nil {
		return err
	}
	for _, c := range changes {
		fmt.Println(c)
	}
	return atomicfile.Write(tablePath, cost.FormatTable(updated), 0o644)
}

func source(from string) ([]byte, error) {
	if from != "" {
		return os.ReadFile(from)
	}
	client := &http.Client{Timeout: time.Minute}
	resp, err := client.Get(cost.LiteLLMURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", cost.LiteLLMURL, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}
