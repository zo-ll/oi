package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	quantRE = regexp.MustCompile(`[Qq]\d[_.][Kk]?_?[A-Za-z0-9]*|IQ\d[_.][A-Za-z0-9]*`)
	shardRE = regexp.MustCompile(`-\d{5}-of-\d{5}`)
	paramRE = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*[Bb](?:illion)?(?:\b|_)`)

	client = &http.Client{Timeout: 15 * time.Second}

	// Semaphore to limit concurrent requests
	sem = make(chan struct{}, 10)
)

// Model represents a model entry in the output JSON.
type Model struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Repo             string   `json:"repo"`
	FilenameTemplate string   `json:"filename_template"`
	MinVRAMGB        float64  `json:"min_vram_gb"`
	Description      string   `json:"description"`
	Tags             []string `json:"tags"`
	downloads        int      // internal, not serialized
}

// Output is the top-level JSON structure.
type Output struct {
	Models []Model `json:"models"`
}

// apiModel represents the HuggingFace API search result.
type apiModel struct {
	ID        string `json:"id"`
	Downloads int    `json:"downloads"`
	Likes     int    `json:"likes"`
}

// apiModelDetail represents a detailed model response with siblings.
type apiModelDetail struct {
	Siblings []sibling `json:"siblings"`
}

type sibling struct {
	RFilename string `json:"rfilename"`
}

func estimateQ4GB(billions float64) float64 {
	return math.Round((billions*0.6+0.5)*10) / 10
}

func parseParamBillions(name string) float64 {
	m := paramRE.FindStringSubmatch(name)
	if len(m) < 2 {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	return v
}

func sizeTags(totalMemGB float64) []string {
	tags := []string{"1b", "3b"}
	if totalMemGB >= 8 {
		tags = append(tags, "7b", "8b")
	}
	if totalMemGB >= 16 {
		tags = append(tags, "14b")
	}
	if totalMemGB >= 32 {
		tags = append(tags, "30b", "32b", "34b")
	}
	if totalMemGB >= 64 {
		tags = append(tags, "70b", "72b")
	}
	return tags
}

func apiGet(url string) ([]byte, error) {
	sem <- struct{}{}
	defer func() { <-sem }()

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "oi-cli/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func deriveCliID(repoID string) string {
	parts := strings.Split(repoID, "/")
	repoName := parts[len(parts)-1]
	id := strings.ToLower(repoName)
	id = regexp.MustCompile(`-gguf$`).ReplaceAllString(id, "")
	id = regexp.MustCompile(`-instruct`).ReplaceAllString(id, "")
	id = regexp.MustCompile(`[._]`).ReplaceAllString(id, "-")
	id = regexp.MustCompile(`-+`).ReplaceAllString(id, "-")
	id = strings.Trim(id, "-")
	return id
}

func buildFilenameTemplate(filename string) string {
	loc := quantRE.FindStringIndex(filename)
	if loc == nil {
		return filename
	}
	return filename[:loc[0]] + "{quant}" + filename[loc[1]:]
}

func filterSharded(filename string) bool {
	return shardRE.MatchString(filename)
}

func formatNumber(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

func fetchModels(orgs []string, tags []string, maxParamB float64) []Model {
	type result struct {
		model Model
	}

	var wg sync.WaitGroup
	ch := make(chan result, 100)

	for _, org := range orgs {
		for _, size := range tags {
			wg.Add(1)
			go func(org, size string) {
				defer wg.Done()

				url := fmt.Sprintf(
					"https://huggingface.co/api/models?author=%s&search=%s+gguf&sort=downloads&direction=-1&limit=5&filter=text-generation",
					org, size,
				)

				data, err := apiGet(url)
				if err != nil {
					return
				}

				var results []apiModel
				if err := json.Unmarshal(data, &results); err != nil {
					return
				}

				for _, repo := range results {
					repoID := repo.ID
					downloads := repo.Downloads
					likes := repo.Likes

					paramB := parseParamBillions(repoID)
					if paramB > 0 && paramB > maxParamB {
						continue
					}

					detailData, err := apiGet(fmt.Sprintf("https://huggingface.co/api/models/%s", repoID))
					if err != nil {
						continue
					}

					var detail apiModelDetail
					if err := json.Unmarshal(detailData, &detail); err != nil {
						continue
					}

					// Collect single-file GGUFs only
					var ggufFiles []string
					for _, s := range detail.Siblings {
						fn := s.RFilename
						if !strings.HasSuffix(fn, ".gguf") || strings.HasPrefix(fn, ".") {
							continue
						}
						if filterSharded(fn) {
							continue
						}
						ggufFiles = append(ggufFiles, fn)
					}

					if len(ggufFiles) == 0 {
						continue
					}

					// Pick representative file (prefer Q4_K_M)
					rep := ""
					for _, fn := range ggufFiles {
						upper := strings.ToUpper(strings.ReplaceAll(fn, "-", "_"))
						if strings.Contains(upper, "Q4_K_M") {
							rep = fn
							break
						}
					}
					if rep == "" {
						rep = ggufFiles[0]
					}

					template := buildFilenameTemplate(rep)

					var minVRAM float64
					if paramB > 0 {
						minVRAM = estimateQ4GB(paramB)
					} else {
						minVRAM = 3.0
					}

					cliID := deriveCliID(repoID)

					repoName := repoID[strings.LastIndex(repoID, "/")+1:]
					modelName := strings.ReplaceAll(repoName, "-GGUF", "")
					modelName = strings.ReplaceAll(modelName, "-gguf", "")

					ch <- result{
						model: Model{
							ID:               cliID,
							Name:             modelName,
							Repo:             repoID,
							FilenameTemplate: template,
							MinVRAMGB:        minVRAM,
							Description:      fmt.Sprintf("%s downloads, %s likes on HuggingFace", formatNumber(downloads), formatNumber(likes)),
							Tags:             []string{"dynamic", strings.ToLower(org)},
							downloads:        downloads,
						},
					}
				}
			}(org, size)
		}
	}

	// Close channel when all goroutines complete
	go func() {
		wg.Wait()
		close(ch)
	}()

	// Deduplicate: keep highest downloads
	seen := make(map[string]Model)
	for r := range ch {
		m := r.model
		if existing, ok := seen[m.ID]; ok {
			if existing.downloads >= m.downloads {
				continue
			}
		}
		seen[m.ID] = m
	}

	// Sort by downloads descending
	models := make([]Model, 0, len(seen))
	for _, m := range seen {
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].downloads > models[j].downloads
	})

	return models
}

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintf(os.Stderr, "Usage: %s <cache_path> <space_separated_orgs> <total_mem_gb>\n", os.Args[0])
		os.Exit(1)
	}

	cachePath := os.Args[1]
	orgs := strings.Fields(os.Args[2])
	totalMemGB, err := strconv.ParseFloat(os.Args[3], 64)
	if err != nil {
		totalMemGB = 8
	}

	tags := sizeTags(totalMemGB)
	maxParamB := (totalMemGB - 1) / 0.6

	models := fetchModels(orgs, tags, maxParamB)

	output := Output{Models: models}
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing cache file: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Fetched %d models from HuggingFace\n", len(models))
}
