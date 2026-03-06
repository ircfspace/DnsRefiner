package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	AllowedSchemes []string `yaml:"allowed_schemes"`
	OutputOrder  string   `yaml:"output_order"`
	ExportDir    string   `yaml:"export_dir"`
	Subscriptions  []struct {
		Key string `yaml:"key"`
		URL string `yaml:"url"`
	} `yaml:"subscriptions"`
}

type DecodedConfig struct {
	Scheme string `json:"scheme"`
	SubKey string `json:"subKey"`
	Addr   string `json:"addr"`
	NS     string `json:"ns"`
	PubKey interface{} `json:"pubKey"`
	User   interface{} `json:"user"`
	Pass   interface{} `json:"pass"`
}

func main() {
	// Parse command line flags
	var exportDir string
	flag.StringVar(&exportDir, "dir", "export", "Directory to export files")
	flag.Parse()
	
	// Read config file
	configData, err := os.ReadFile("config.yaml")
	if err != nil {
		fmt.Printf("Error reading config file: %v\n", err)
		return
	}

	var config Config
	err = yaml.Unmarshal(configData, &config)
	if err != nil {
		fmt.Printf("Error parsing config file: %v\n", err)
		return
	}

	fmt.Printf("Allowed schemes: %v\n", config.AllowedSchemes)
	fmt.Printf("Found %d subscriptions\n", len(config.Subscriptions))

	// Process each subscription
	var allResults []DecodedConfig

	for _, sub := range config.Subscriptions {
		fmt.Printf("\nProcessing subscription: %s\n", sub.Key)
		
		// Fetch subscription content
		content, err := fetchURL(sub.URL)
		if err != nil {
			fmt.Printf("Error fetching %s: %v\n", sub.Key, err)
			continue
		}

		if content == "" {
			fmt.Printf("Warning: Empty content for %s\n", sub.Key)
			continue
		}

		// Process lines
		lines := strings.Split(content, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// Check if line starts with allowed scheme
			for _, scheme := range config.AllowedSchemes {
				schemePrefix := scheme + "://"
				if strings.HasPrefix(line, schemePrefix) {
					// Extract Base64 part
					base64Part := strings.TrimPrefix(line, schemePrefix)
					
					// Decode Base64
					decoded, err := decodeBase64(base64Part)
					if err != nil {
						fmt.Printf("Error decoding Base64 for %s: %v\n", sub.Key, err)
						continue
					}

					// Extract addr, ns, pubKey based on scheme
					var addr, ns, user, pass, pubKey string
					if scheme == "dns" {
						if decodedMap, ok := decoded.(map[string]interface{}); ok {
							addr = getString(decodedMap, "addr")
							ns = strings.TrimSuffix(getString(decodedMap, "ns"), ".")
							user = getString(decodedMap, "user")
							pass = getString(decodedMap, "pass")
							pubKey = getString(decodedMap, "pubkey")
						}
					} else if scheme == "slipnet" {
						addr, ns, pubKey = parseSlipnetConfig(decoded.(string))
					}

					result := DecodedConfig{
						Scheme: scheme,
						SubKey: sub.Key,
						Addr:   addr,
						NS:     ns,
						PubKey: getValueOrFalse(pubKey),
						User:   getValueOrFalse(user),
						Pass:   getValueOrFalse(pass),
					}
					allResults = append(allResults, result)
					break
				}
			}
		}
	}

	// Output results as JSON
	sortResults(config.OutputOrder, allResults)
	jsonOutput, err := json.MarshalIndent(allResults, "", "  ")
	if err != nil {
		fmt.Printf("Error marshaling JSON: %v\n", err)
		return
	}

	fmt.Printf("\n=== JSON Output ===\n")
	fmt.Println(string(jsonOutput))

	// Export to files
	exportToFiles(allResults, config.ExportDir)
}

func fetchURL(url string) (string, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func decodeBase64(data string) (interface{}, error) {
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		// Try URL-safe base64
		decoded, err = base64.URLEncoding.DecodeString(data)
		if err != nil {
			return nil, err
		}
	}

	// Try to parse as JSON first
	var jsonData interface{}
	if err := json.Unmarshal(decoded, &jsonData); err == nil {
		return jsonData, nil
	}

	// If not JSON, return as string
	return string(decoded), nil
}

func sortResults(order string, results []DecodedConfig) {
	switch order {
	case "DESC":
		sort.Slice(results, func(i, j int) bool {
			return results[i].SubKey > results[j].SubKey
		})
	case "RAND":
		rand.Seed(time.Now().UnixNano())
		rand.Shuffle(len(results), func(i, j int) {
			results[i], results[j] = results[j], results[i]
		})
	case "ASC":
		fallthrough
	default:
		sort.Slice(results, func(i, j int) bool {
			return results[i].SubKey < results[j].SubKey
		})
	}
}

func exportToFiles(results []DecodedConfig, exportDir string) {
	// Create export directory
	dirName := "export"
	if exportDir != "" {
		dirName = exportDir
	}
	os.MkdirAll(dirName, 0755)
	
	// Group results by subscription key
	subscriptions := make(map[string][]DecodedConfig)
	for _, result := range results {
		subscriptions[result.SubKey] = append(subscriptions[result.SubKey], result)
	}
	
	// Export each subscription to separate directory
	for subKey, configs := range subscriptions {
		// Create subdirectory for this subscription
		subDir := fmt.Sprintf("%s/%s", dirName, subKey)
		os.MkdirAll(subDir, 0755)
		
		// Export JSON
		jsonData, err := json.MarshalIndent(configs, "", "  ")
		if err == nil {
			jsonFile := fmt.Sprintf("%s/sub.json", subDir)
			os.WriteFile(jsonFile, jsonData, 0644)
			fmt.Printf("Exported %d configs to %s/sub.json\n", len(configs), subDir)
		}
		
		// Export base64
		allJsonData, err := json.MarshalIndent(configs, "", "  ")
		if err == nil {
			base64Data := base64.StdEncoding.EncodeToString(allJsonData)
			base64File := fmt.Sprintf("%s/sub.base64", subDir)
			os.WriteFile(base64File, []byte(base64Data), 0644)
			fmt.Printf("Exported all configs to %s/sub.base64\n", subDir)
		}
	}
}

func getValueOrFalse(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func parseSlipnetConfig(decoded string) (addr, ns, pubKey string) {
	parts := strings.Split(decoded, "|")
	if len(parts) < 12 {
		return "", "", ""
	}
	
	ns = parts[3]
	// Remove trailing dot from ns if present
	ns = strings.TrimSuffix(ns, ".")
	
	// part[4] contains comma-separated DNS servers
	dnsServers := parts[4]
	if dnsServers != "" {
		// Take the first DNS server as addr
		firstServer := strings.Split(dnsServers, ",")[0]
		// Remove the :0 suffix if present
		addr = strings.TrimSuffix(firstServer, ":0")
	}
	
	// part[11] contains the pubKey (may be empty for some configs)
	if len(parts) > 11 {
		pubKey = parts[11]
	}
	
	return addr, ns, pubKey
}
