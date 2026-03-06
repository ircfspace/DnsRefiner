package main

import (
	"encoding/base64"
	"encoding/json"
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
	exportToFiles(allResults)
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

func exportToFiles(results []DecodedConfig) {
	// Create export directory
	os.MkdirAll("export", 0755)
	
	// Export as JSON
	jsonData, err := json.MarshalIndent(results, "", "  ")
	if err == nil {
		os.WriteFile("export/sub.json", jsonData, 0644)
		fmt.Println("Exported to export/sub.json")
	}
	
	// Export as base64
	base64Data := base64.StdEncoding.EncodeToString(jsonData)
	os.WriteFile("export/sub.base64", []byte(base64Data), 0644)
	fmt.Println("Exported to export/sub.base64")
}

func getValueOrFalse(value string) interface{} {
	if value == "" {
		return false
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
