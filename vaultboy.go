package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	var inputFiles multiFlag
	output := flag.String("o", "output.json", "Output file (.json, .env, .yaml)")
	reverse := flag.Bool("reverse", false, "Reverse mode: Vault JSON to .env/.yaml")

	flag.Var(&inputFiles, "i", "Input file(s): .env/.yaml or Vault .json")
	flag.Parse()

	if len(inputFiles) == 0 {
		log.Fatal("❌ At least one input file is required using -i")
	}

	if *reverse {
		if len(inputFiles) != 1 {
			log.Fatal("❌ Reverse mode only accepts 1 input JSON file")
		}
		runReverse(inputFiles[0], *output)
	} else {
		runNormal(inputFiles, *output)
	}
	fmt.Printf("✅ Done! Output written to %s\n", *output)
}

func runNormal(inputs []string, output string) {
	flat := make(map[string]interface{})
	for _, path := range inputs {
		parsed, err := detectAndParse(path)
		if err != nil {
			log.Fatalf("❌ Failed to parse %s: %v", path, err)
		}
		for k, v := range parsed {
			flat[k] = v
		}
	}

	jsonData, err := json.MarshalIndent(flat, "", "  ")
	if err != nil {
		log.Fatalf("❌ Failed to marshal JSON: %v", err)
	}
	if err := os.WriteFile(output, jsonData, 0644); err != nil {
		log.Fatalf("❌ Failed to write %s: %v", output, err)
	}
}

func runReverse(inputPath, outputPath string) {
	data, err := parseJSON(inputPath)
	if err != nil {
		log.Fatalf("❌ Failed to read input JSON: %v", err)
	}

	switch strings.ToLower(filepath.Ext(outputPath)) {
	case ".env":
		if err := writeEnv(data, outputPath); err != nil {
			log.Fatalf("❌ Failed to write env: %v", err)
		}
	case ".yaml", ".yml":
		nested := unflatten(data)
		if err := writeYAML(nested, outputPath); err != nil {
			log.Fatalf("❌ Failed to write yaml: %v", err)
		}
	default:
		log.Fatalf("❌ Unsupported output extension: %s", outputPath)
	}
}

type multiFlag []string

func (m *multiFlag) String() string         { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(value string) error { *m = append(*m, value); return nil }
func detectAndParse(path string) (map[string]interface{}, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".env":
		return parseEnvFile(path)
	case ".yaml", ".yml":
		raw, err := parseYAML(path)
		if err != nil {
			return nil, err
		}
		flat := make(map[string]interface{})
		flatten("", raw, flat)
		return flat, nil
	default:
		return nil, errors.New("unsupported input file type: " + ext)
	}
}

func parseEnvFile(path string) (map[string]interface{}, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := make(map[string]interface{})
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		result[key] = value
	}
	return result, scanner.Err()
}

func parseYAML(path string) (map[string]interface{}, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	if err := yaml.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func flatten(prefix string, in map[string]interface{}, out map[string]interface{}) {
	for k, v := range in {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]interface{}:
			flatten(key, val, out)
		case []interface{}:
			for i, item := range val {
				itemKey := fmt.Sprintf("%s[%d]", key, i)
				if m, ok := item.(map[string]interface{}); ok {
					flatten(itemKey, m, out)
				} else {
					out[itemKey] = item
				}
			}
		default:
			out[key] = val
		}
	}
}

func parseJSON(path string) (map[string]interface{}, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var data map[string]interface{}
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, err
	}
	return data, nil
}

func writeEnv(data map[string]interface{}, path string) error {
	var lines []string
	keys := sortedKeys(data)
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf(`%s="%v"`, k, data[k]))
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

func writeYAML(data map[string]interface{}, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	defer enc.Close()

	return enc.Encode(data)
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func unflatten(flat map[string]interface{}) map[string]interface{} {
	root := make(map[string]interface{})

	for flatKey, value := range flat {
		parts := strings.Split(flatKey, ".")
		current := root

		for i := 0; i < len(parts); i++ {
			isLast := i == len(parts)-1
			key, idx, isArray := parseArrayKey(parts[i])

			if isArray {
				var arr []interface{}
				if existing, ok := current[key]; ok {
					arr, _ = existing.([]interface{})
				}
				// Ensure array size
				for len(arr) <= idx {
					arr = append(arr, nil)
				}
				if isLast {
					arr[idx] = value
				} else {
					if arr[idx] == nil {
						arr[idx] = make(map[string]interface{})
					}
				}
				current[key] = arr
				if !isLast {
					current = arr[idx].(map[string]interface{})
				}
			} else {
				if isLast {
					current[key] = value
				} else {
					if _, ok := current[key]; !ok {
						current[key] = make(map[string]interface{})
					}
					if next, ok := current[key].(map[string]interface{}); ok {
						current = next
					}
				}
			}
		}
	}

	return root
}

func parseArrayKey(key string) (string, int, bool) {
	if strings.HasSuffix(key, "]") {
		bracketIdx := strings.LastIndex(key, "[")
		if bracketIdx != -1 {
			arrayKey := key[:bracketIdx]
			idxStr := key[bracketIdx+1 : len(key)-1]
			if idx, err := strconv.Atoi(idxStr); err == nil {
				return arrayKey, idx, true
			}
		}
	}
	return key, -1, false
}
