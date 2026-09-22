package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// parseEnvFile reads a `smoke.env` style file into a map. Each non-blank, non-comment
// line is `[export ]KEY=VALUE`; a quoted VALUE is unquoted (Go-string first, then a
// plain surrounding-quote trim). A missing file is not an error (returns an empty map)
// — the process env may supply everything.
func parseEnvFile(path string) (map[string]string, error) {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("smoke: open env file: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = unquoteEnv(strings.TrimSpace(v))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("smoke: read env file: %w", err)
	}
	return out, nil
}

// unquoteEnv strips a quoted value: it prefers Go-string unquoting, falling back to
// trimming a single pair of surrounding quotes.
func unquoteEnv(v string) string {
	if s, err := strconv.Unquote(v); err == nil {
		return s
	}
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}
